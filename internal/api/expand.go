package api

import (
	"context"
	"errors"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/directory/ldapdriver"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/plan"
	"github.com/hazame-hub/alder/internal/schema"
)

// Who is actually in this group.
//
// The entry view already lists a group's `member` values, and for a flat group
// that is the whole answer. It stops being the answer the moment a group holds
// another group: `cn=everyone` in the harness lists five members and contains
// nobody, because every one of those five is itself a group.
//
// Expanding walks that structure. What makes it worth doing rather than
// obvious is everything that goes wrong on the way — a group that contains
// itself, a member DN pointing at an entry that was deleted, a membership
// attribute that holds a login name rather than a DN — and each of those is
// reported rather than quietly dropped, because each is something a directory
// owner wants to know about -- and a member that could not be read for some
// other reason is reported as that, not as one of these.

const (
	// maxExpandDepth bounds the nesting walked. Ten levels is far past any
	// real directory and short enough that a pathological one terminates.
	maxExpandDepth = 10
	// maxExpandReads bounds the reads one expansion costs. Determining whether
	// a member is itself a group means reading it, so a group of a thousand
	// people costs a thousand reads; this is where that stops.
	maxExpandReads = 1000
)

// expandAttrs is what a member has to be read with.
//
// The membership attributes are the load-bearing half: a nested group read
// without them looks like an empty group, and the walk stops one level short
// while reporting success. That is precisely what happened the first time this
// ran against the harness — five nested groups found and nobody inside them.
var expandAttrs = append([]string{"objectClass", "cn", "uid"}, membershipAttrs...)

// entryReader reads one entry. The traversal takes this rather than a Session
// so it can be tested without a directory.
type entryReader func(ctx context.Context, target dn.DN, attrs []string) (*directory.Entry, error)

// expansion is everything one walk found, including what it could not resolve.
type expansion struct {
	Members []ExpandedMember
	// Cycles names groups that contain themselves, directly or through others.
	// A cycle is a configuration error somebody should hear about rather than
	// a condition to silently survive.
	Cycles []string
	// Dangling names member values the directory answered "no such object"
	// for.
	//
	// Either the entry was deleted and the reference outlived it, or it is
	// there and this bind may not see it. Nothing tells those apart: a server
	// answers "no such object" for both, on purpose, so that refusing access
	// does not disclose what exists. So this is "status unknown", and anything
	// built on it must not say "deleted" -- an operator asking who can reach
	// something would be told a member is gone while they are still in the
	// group.
	//
	// That answer and no other. A read refused, timed out or cut off says
	// nothing about the entry at all, and listing it here would make the
	// list mean "the directory said it is not there" only some of the time.
	Dangling []string
	// Errors names member values whose read failed some other way.
	Errors []MemberReadError
	// Unresolvable names membership values that are not DNs at all, and so
	// cannot be followed: memberUid holds a login name, memberURL a search.
	Unresolvable []string
	Truncated    bool

	reads int
}

// expandGroup resolves a group's membership, following nested groups.
func expandGroup(
	ctx context.Context,
	read entryReader,
	sch *schema.Schema,
	group *directory.Entry,
	limit int,
) *expansion {
	out := &expansion{Members: []ExpandedMember{}}
	// seen dedupes: somebody in two teams is one member, not two.
	seen := map[string]bool{foldDN(group.DN): true}
	// path is the groups from the root to here. A cycle is reaching one of
	// those again — which is not the same as reaching a DN already recorded,
	// and the difference is the whole of the distinction between a person in
	// two teams and a group that contains itself.
	path := map[string]bool{foldDN(group.DN): true}
	out.walk(ctx, read, sch, group, nil, seen, path, 1, limit)
	return out
}

func (e *expansion) walk(
	ctx context.Context,
	read entryReader,
	sch *schema.Schema,
	group *directory.Entry,
	via []string,
	seen map[string]bool,
	path map[string]bool,
	depth int,
	limit int,
) {
	if depth > maxExpandDepth {
		e.Truncated = true
		return
	}

	for _, attr := range membershipAttrs {
		for _, raw := range group.Get(attr) {
			value := string(raw)
			if value == "" {
				continue
			}
			if len(e.Members) >= limit || e.reads >= maxExpandReads {
				e.Truncated = true
				return
			}

			// memberUid holds a login name and memberURL a search; neither is a
			// DN, so neither can be followed to an entry.
			target, err := dn.Parse(trimUIDSuffix(value))
			if err != nil {
				e.Unresolvable = plan.AppendNew(e.Unresolvable, []string{attr + ": " + value})
				continue
			}

			key := foldDN(target)
			if path[key] {
				// A group on the branch that led here: following it would loop.
				// Worth reporting rather than merely surviving — it is a
				// configuration error somebody has to go and fix.
				e.Cycles = plan.AppendNew(e.Cycles, []string{target.String()})
				continue
			}
			if seen[key] {
				// Reached before down some other branch. Somebody in two teams
				// is one member, and that is ordinary.
				continue
			}
			seen[key] = true

			e.reads++
			member, readErr := read(ctx, target, expandAttrs)
			if readErr == nil && member == nil {
				// The driver answers a missing entry with an error; a reader
				// that returns neither has found nothing.
				e.Dangling = plan.AppendNew(e.Dangling, []string{target.String()})
				continue
			}
			if readErr != nil {
				if isNoSuchObject(readErr) {
					e.Dangling = plan.AppendNew(e.Dangling, []string{target.String()})
					continue
				}
				e.Errors = append(e.Errors, memberReadError(target, readErr))
				// A walk whose request has ended would record every member
				// after this one as an error of its own, each saying the same
				// thing. It stops, and says it stopped.
				if ctx.Err() != nil {
					e.Truncated = true
					return
				}
				continue
			}

			nested := isGroup(sch, member)
			e.Members = append(e.Members, ExpandedMember{
				Dn:      target.String(),
				Rdn:     ptr(rdnLabel(target)),
				Group:   nested,
				Direct:  depth == 1,
				Via:     ptrIfAny(via),
				Through: ptrIfSet(attr),
			})

			if nested {
				path[key] = true
				e.walk(ctx, read, sch, member,
					append(via, target.String()), seen, path, depth+1, limit)
				// Off the branch again: a sibling group may legitimately hold
				// this one without that being a loop.
				delete(path, key)
			}
		}
	}
}

// memberReadError describes a read that failed without saying the entry is
// absent, in the words fail() uses for the same result.
func memberReadError(target dn.DN, err error) MemberReadError {
	out := MemberReadError{Dn: target.String(), Message: "The read did not complete."}
	var ldapErr *ldapdriver.Error
	if !errors.As(err, &ldapErr) {
		return out
	}
	switch {
	case ldapErr.IsClientSide():
		out.Message = "The directory did not answer."
		return out
	case ldapErr.IsInsufficientAccess():
		out.Message = "The directory refused this read for the account you are bound as."
	default:
		out.Message = "The directory returned an error."
	}
	code := int(ldapErr.Code)
	out.LdapCode = &code
	return out
}

func readErrorsIfAny(v []MemberReadError) *[]MemberReadError {
	if len(v) == 0 {
		return nil
	}
	return &v
}

// isGroup reports whether this entry is one whose membership can be expanded.
//
// Decided from the object classes the entry actually carries, not from where it
// sits or what it is called: a group is a group because it holds members.
func isGroup(sch *schema.Schema, e *directory.Entry) bool {
	for _, raw := range e.Get("objectClass") {
		name := string(raw)
		for _, attr := range membershipAttrs {
			if classPermits(sch, name, attr) {
				return true
			}
		}
	}
	return false
}

// classPermits reports whether an object class allows a membership attribute.
func classPermits(sch *schema.Schema, class, attr string) bool {
	if sch == nil {
		return false
	}
	oc := sch.ObjectClass(class)
	if oc == nil {
		return false
	}
	req := sch.Requirements([]string{class})
	for _, name := range append(append([]string{}, req.Must...), req.May...) {
		if strings.EqualFold(schema.BaseName(name), attr) {
			return true
		}
	}
	return false
}

// trimUIDSuffix drops uniqueMember's optional "#uid", which is not part of the
// DN — the same trimming the reference matcher does, for the same reason.
func trimUIDSuffix(value string) string {
	if hash := strings.LastIndexByte(value, '#'); hash > 0 &&
		!strings.ContainsRune(value[hash:], ',') {
		return value[:hash]
	}
	return value
}

// ExpandMembers resolves a group's membership.
func (s *Server) ExpandMembers(c *fiber.Ctx, params ExpandMembersParams) error {
	sess := s.require(c)
	if sess == nil {
		return nil
	}
	target, ok := parseDNParam(c, params.Dn)
	if !ok {
		return nil
	}
	limit := clamp(deref(params.Limit), 500, 1, 2000)

	ctx, cancel := reqCtx(c)
	defer cancel()

	group, err := sess.Conn.Read(ctx, target, []string{"*"})
	if err != nil {
		return s.fail(c, err)
	}
	sch, err := sess.Conn.Schema(ctx)
	if err != nil {
		return s.fail(c, err)
	}

	got := expandGroup(ctx, sess.Conn.Read, sch, group, limit)

	return c.JSON(MemberList{
		Members:      got.Members,
		Truncated:    got.Truncated,
		Cycles:       ptrIfAny(got.Cycles),
		Dangling:     ptrIfAny(got.Dangling),
		Errors:       readErrorsIfAny(got.Errors),
		Unresolvable: ptrIfAny(got.Unresolvable),
	})
}
