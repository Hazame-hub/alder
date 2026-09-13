package plan

import (
	"bytes"
	"sort"
	"strings"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/schema"
)

// What a plan does beyond the entries it names.
//
// Two facts that can be worked out from the plan and the entries it already
// read, with no further searching: which group memberships change, and which
// deletions together remove a branch. Inbound references need a search of the
// directory and are the API layer's to find; this package does not search.

// membershipChanges is the net effect of one planned item on the membership
// attributes of the entry it touches.
//
// Computed by replaying the item's modifications over the live values, in
// order, so that an add followed by a delete of the same member reads as the
// nothing it is. Values are keyed as DNs where they parse as DNs -- member and
// uniqueMember hold DNs, and two spellings of one name are one member -- and
// byte for byte where they do not, which is memberUid.
func membershipChanges(item Item, live *directory.Entry, members map[string]bool) []MembershipChange {
	isMembership := func(name string) bool {
		return members[strings.ToLower(schema.BaseName(name))]
	}

	type attribute struct {
		name   string
		before [][]byte
		after  [][]byte
	}
	var order []string
	byKey := map[string]*attribute{}
	touch := func(name string, before [][]byte) *attribute {
		key := strings.ToLower(schema.BaseName(name))
		if a, ok := byKey[key]; ok {
			return a
		}
		a := &attribute{name: name, before: before, after: before}
		byKey[key] = a
		order = append(order, key)
		return a
	}

	switch item.Action {
	case ActionAdd:
		for _, attr := range item.Record.Attrs {
			if isMembership(attr.Name) {
				a := touch(attr.Name, nil)
				a.after = append(a.after, attr.Values...)
			}
		}
	case ActionModify:
		for _, m := range item.Record.Mods {
			if !isMembership(m.Name) {
				continue
			}
			var before [][]byte
			if live != nil {
				before = live.Get(m.Name)
			}
			a := touch(m.Name, before)
			a.after = applyMod(a.after, m)
		}
	case ActionDelete:
		if live == nil {
			return nil
		}
		for name := range members {
			if values := live.Get(name); len(values) > 0 {
				a := touch(name, values)
				a.after = nil
			}
		}
	default:
		return nil
	}

	var out []MembershipChange
	for _, key := range order {
		a := byKey[key]
		gained, removed := difference(a.before, a.after)
		if len(gained) == 0 && len(removed) == 0 {
			continue
		}
		out = append(out, MembershipChange{Attribute: a.name, Gained: gained, Removed: removed})
	}
	return out
}

// difference is what is in after and not before, and in before and not after,
// keyed as membership values are compared. Linear in both.
//
// A value present byte for byte on both sides is the same member however it is
// spelled, so only the values that are not are keyed as DNs, and each side is
// keyed only when the other has a value to look up in it. Adding a hundred
// members to a group of a hundred thousand keys the hundred thousand once;
// removing one keys nothing it does not have to.
func difference(before, after [][]byte) (gained, removed []string) {
	var maybeGained, maybeRemoved [][]byte
	if extends(after, before) {
		// The shape an add of members produces: the values that were there,
		// then the new ones. Nothing can have been removed, and the candidates
		// are the tail, with no set built to find either.
		maybeGained = after[len(before):]
	} else {
		beforeRaw := make(map[string]struct{}, len(before))
		for _, v := range before {
			beforeRaw[string(v)] = struct{}{}
		}
		afterRaw := make(map[string]struct{}, len(after))
		for _, v := range after {
			afterRaw[string(v)] = struct{}{}
		}
		for _, v := range after {
			if _, ok := beforeRaw[string(v)]; !ok {
				maybeGained = append(maybeGained, v)
			}
		}
		for _, v := range before {
			if _, ok := afterRaw[string(v)]; !ok {
				maybeRemoved = append(maybeRemoved, v)
			}
		}
	}

	keys := func(values [][]byte) map[string]bool {
		out := make(map[string]bool, len(values))
		for _, v := range values {
			out[memberKey(v)] = true
		}
		return out
	}
	if len(maybeGained) > 0 {
		beforeKeys := keys(before)
		for _, v := range maybeGained {
			if k := memberKey(v); !beforeKeys[k] {
				gained = append(gained, string(v))
				beforeKeys[k] = true // one report per member, however often repeated
			}
		}
	}
	if len(maybeRemoved) > 0 {
		afterKeys := keys(after)
		for _, v := range maybeRemoved {
			if k := memberKey(v); !afterKeys[k] {
				removed = append(removed, string(v))
				afterKeys[k] = true
			}
		}
	}
	return gained, removed
}

// extends reports whether after begins with exactly the values of before.
func extends(after, before [][]byte) bool {
	if len(after) < len(before) {
		return false
	}
	for i, v := range before {
		if !bytes.Equal(after[i], v) {
			return false
		}
	}
	return true
}

// memberKey compares a membership value the way the directory would.
func memberKey(v []byte) string {
	s := string(v)
	if plainDN(s) {
		return strings.ToLower(s)
	}
	// uniqueMember may carry an optional "#uid" that is not part of the DN.
	if hash := strings.LastIndexByte(s, '#'); hash > 0 && !strings.ContainsRune(s[hash:], ',') {
		if parsed, err := dn.Parse(s[:hash]); err == nil {
			return strings.ToLower(parsed.String())
		}
	}
	if strings.ContainsRune(s, '=') {
		if parsed, err := dn.Parse(s); err == nil {
			return strings.ToLower(parsed.String())
		}
	}
	return s
}

// plainDN reports whether s is a DN that parsing and rendering again would give
// back unchanged apart from case: every RDN a single type=value, the type a
// descriptor, and nothing in the value that RFC 4514 escapes or unescapes.
//
// Nearly every member value in a real directory is one, and recognising that is
// a scan where parsing is an allocation per RDN. TestPlainDNKeysAsParsingDoes
// holds the two to the same answer.
func plainDN(s string) bool {
	if s == "" {
		return false
	}
	start := 0
	for start <= len(s) {
		end := strings.IndexByte(s[start:], ',')
		if end < 0 {
			end = len(s)
		} else {
			end += start
		}
		rdn := s[start:end]
		eq := strings.IndexByte(rdn, '=')
		if eq < 1 || eq == len(rdn)-1 {
			return false
		}
		for i := 0; i < len(rdn); i++ {
			c := rdn[i]
			letter := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
			digit := c >= '0' && c <= '9'
			switch {
			case i == 0:
				if !letter {
					return false
				}
			case i < eq:
				if !letter && !digit && c != '-' {
					return false
				}
			case i == eq:
			default:
				if !letter && !digit && c != '.' && c != '-' && c != '_' && c != '@' {
					return false
				}
			}
		}
		if end == len(s) {
			return true
		}
		start = end + 1
	}
	return false
}

// deletedSubtrees groups planned deletions under the topmost deleted ancestor.
//
// Walks each deletion up through its parents looking for one that is also being
// deleted, so the cost is the number of deletions times the depth of the tree
// rather than the number of deletions squared. Only branches of more than one
// entry are reported: a single leaf is not a subtree, and saying so would make
// every ordinary deletion look like more than it is.
func deletedSubtrees(items []Item) []Subtree {
	deleted := map[string]dn.DN{}
	for _, item := range items {
		if item.Action == ActionDelete {
			deleted[strings.ToLower(item.DN.String())] = item.DN
		}
	}
	if len(deleted) < 2 {
		return nil
	}

	counts := map[string]int{}
	for _, target := range deleted {
		root := target
		for parent := target.Parent(); !parent.IsEmpty(); parent = parent.Parent() {
			if _, ok := deleted[strings.ToLower(parent.String())]; ok {
				root = parent
			}
		}
		counts[strings.ToLower(root.String())]++
	}

	var out []Subtree
	for key, n := range counts {
		if n < 2 {
			continue
		}
		out = append(out, Subtree{Root: deleted[key], Entries: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Entries != out[j].Entries {
			return out[i].Entries > out[j].Entries
		}
		return out[i].Root.String() < out[j].Root.String()
	})
	return out
}

func foldSet(names []string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[strings.ToLower(schema.BaseName(n))] = true
	}
	return out
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
