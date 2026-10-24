package api

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/gofiber/fiber/v2"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/directory/ldapdriver"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/filter"
	"github.com/hazame-hub/alder/internal/schema"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// Members nobody can find (docs/proposals/group-membership-tools.md, Part 2).
//
// A group goes on naming an entry long after the entry was deleted, wherever
// referential integrity is not configured. This reads the groups below a base,
// collects the DNs they name, and reads whether each exists -- and says, for
// every one it could not find, that *this bind* did not find it. A server
// answers "no such object" both for an entry that was deleted and for one the
// bind may not see, so the word "deleted" appears nowhere in what this returns.
//
// It is whole or refused. An existence read that stopped early would turn
// every name after the stop into "not found", which is the one report that
// leads somebody to remove members who are still there.

const (
	// auditDirectReads is the most names read one at a time. Past it, the
	// names are checked by searching under their common ancestors.
	auditDirectReads = 200
	// auditMaxBases bounds those searches.
	auditMaxBases = 50
	// auditMaxRead is what the audit reads in all, summed over every search:
	// the ceiling a data snapshot has, so one number means "too large to read
	// whole" everywhere.
	auditMaxRead = snapshot.MaxEntries
)

// dnMembershipAttrs are the membership attributes whose values are DNs, and so
// can be checked. memberUid holds a login name and memberURL a search.
var dnMembershipAttrs = []string{"member", "uniqueMember"}

// auditRefusal is an audit that was not made, and why.
type auditRefusal struct {
	code    ErrorError
	message string
	detail  string
}

func (e *auditRefusal) Error() string { return e.message }

// auditName is one DN a group names, with everywhere it is named.
type auditName struct {
	dn       dn.DN
	referral string // set once the name is known to be at or below a referral
	found    bool
}

// auditValue is one membership value of one group.
type auditValue struct {
	attribute string
	value     string
	key       string // the named DN's key; empty when the value is not checked
	reason    MemberAuditUncheckedReason
}

type auditGroup struct {
	dn       dn.DN
	requires bool
	values   []auditValue
}

// auditReferral is a referral the server returned, as a DN to compare names
// against, or -- when its URL named no DN Alder could parse -- the base of the
// search it came back from, everything below which is then not checked.
type auditReferral struct {
	at    dn.DN
	label string
}

type memberAuditor struct {
	r        directory.Reader
	sch      *schema.Schema
	contexts []dn.DN

	read      []MemberAuditRead
	referrals []MemberAuditReferral
	below     []auditReferral
	entries   int
}

// auditMembers runs the audit. The bind is the caller's to fill in.
func auditMembers(ctx context.Context, r directory.Reader, base dn.DN, limit int) (*MemberAudit, error) {
	sch, err := r.Schema(ctx)
	if err != nil {
		return nil, err
	}
	a := &memberAuditor{r: r, sch: sch, contexts: dataContexts(r.Capabilities())}

	groups, err := a.readGroups(ctx, base, limit)
	if err != nil {
		return nil, err
	}

	names := map[string]*auditName{}
	var order []string
	for _, g := range groups {
		for i := range g.values {
			v := &g.values[i]
			if v.key == "" {
				continue
			}
			if _, seen := names[v.key]; !seen {
				parsed, _ := dn.Parse(trimUIDSuffix(v.value))
				names[v.key] = &auditName{dn: parsed}
				order = append(order, v.key)
			}
		}
	}
	// The referral objects where the names are. A server need not answer a
	// read below one with a referral -- 389 DS answers no such object -- so
	// they are asked for first, and nothing below one is read at all.
	if err := a.findReferrals(ctx, names, order); err != nil {
		return nil, err
	}
	for _, key := range order {
		a.markBelow(names[key])
	}

	var pending []*auditName
	for _, key := range order {
		if names[key].referral == "" {
			pending = append(pending, names[key])
		}
	}
	if len(pending) <= auditDirectReads {
		err = a.readEach(ctx, pending)
	} else {
		err = a.searchUnder(ctx, pending)
	}
	if err != nil {
		return nil, err
	}

	return a.report(groups, names), nil
}

// dataContexts is the naming contexts the server announced that hold data:
// never its configuration or schema tree, which the audit does not read.
func dataContexts(caps directory.Capabilities) []dn.DN {
	var out []dn.DN
	for _, raw := range caps.NamingContexts {
		parsed, err := dn.Parse(raw)
		if err != nil || parsed.IsEmpty() || targetKind(caps, parsed) != PlanTargetData {
			continue
		}
		out = append(out, parsed)
	}
	return out
}

func (a *memberAuditor) contextOf(d dn.DN) (dn.DN, bool) {
	var best dn.DN
	found := false
	for _, c := range a.contexts {
		if d.HasSuffix(c) && (!found || len(c) > len(best)) {
			best, found = c, true
		}
	}
	return best, found
}

// readGroups reads every group below the base with its membership values,
// refusing when there are more than the limit.
func (a *memberAuditor) readGroups(ctx context.Context, base dn.DN, limit int) ([]auditGroup, error) {
	anchors := resolveClasses(a.sch, viewSpecs[1].anchors)
	if len(anchors) == 0 {
		return nil, nil
	}
	subs := make([]filter.Filter, 0, len(anchors))
	for _, oc := range anchors {
		subs = append(subs, filter.Equal("objectClass", oc.Name()))
	}
	groupFilter := filter.Or(subs...)
	if len(subs) == 1 {
		groupFilter = subs[0]
	}

	attrs := append([]string{"objectClass"}, membershipAttrs...)
	entries, err := a.search(ctx, base, directory.ScopeSubtree, groupFilter, attrs, limit+1)
	if err != nil {
		return nil, err
	}
	if len(entries) > limit {
		return nil, &auditRefusal{code: ErrorErrorAuditIncomplete,
			message: fmt.Sprintf("There are more than %d groups below %s, so the audit was not made.", limit, base),
			detail:  "An audit of some of the groups would be read as an audit of all of them. Narrow the base, or raise the limit (at most 2000)."}
	}

	out := make([]auditGroup, 0, len(entries))
	for _, e := range entries {
		g := auditGroup{dn: e.DN, requires: a.requiresMember(e)}
		for _, attr := range membershipAttrs {
			isDN := false
			for _, d := range dnMembershipAttrs {
				isDN = isDN || strings.EqualFold(d, attr)
			}
			for _, raw := range e.Get(attr) {
				value := string(raw)
				if value == "" {
					continue
				}
				g.values = append(g.values, a.classify(attr, value, isDN))
			}
		}
		out = append(out, g)
	}
	return out, nil
}

func (a *memberAuditor) classify(attr, value string, isDN bool) auditValue {
	v := auditValue{attribute: attr, value: value}
	if !isDN {
		v.reason = UncheckedNotADn
		return v
	}
	parsed, err := dn.Parse(trimUIDSuffix(value))
	if err != nil {
		v.reason = UncheckedNotADn
		return v
	}
	if _, ok := a.contextOf(parsed); !ok {
		v.reason = UncheckedOutsideContexts
		return v
	}
	v.key = parsed.Key()
	return v
}

// requiresMember reports whether the group's classes require a membership
// attribute, which is what makes the plan refuse the removal of its last value.
func (a *memberAuditor) requiresMember(e *directory.Entry) bool {
	for _, must := range a.sch.Requirements(e.ObjectClasses()).Must {
		for _, attr := range membershipAttrs {
			if strings.EqualFold(schema.BaseName(must), attr) {
				return true
			}
		}
	}
	return false
}

// search runs one paged search to its end, and refuses on anything that
// stopped it early: a server limit, a truncation however the driver reports
// it, or more entries than the audit reads in all.
func (a *memberAuditor) search(
	ctx context.Context, base dn.DN, scope directory.Scope, f filter.Filter, attrs []string, most int,
) ([]*directory.Entry, error) {
	var entries []*directory.Entry
	var cookie []byte
	for {
		want := min(most-len(entries), directory.MaxPageSize, auditMaxRead+1-a.entries)
		res, err := a.r.Search(ctx, directory.SearchRequest{
			BaseDN: base, Scope: scope, Filter: f, Attributes: attrs,
			Limit: want, PageSize: want, Cookie: cookie,
		})
		if err != nil {
			if limit, ok := auditLimit(err); ok {
				return nil, incomplete(base, scope, limit)
			}
			return nil, err
		}
		entries = append(entries, res.Entries...)
		a.entries += len(res.Entries)
		for _, ref := range res.Referrals {
			a.referral(ref, "", base)
		}
		if a.entries > auditMaxRead {
			return nil, tooMuchToRead()
		}
		if len(entries) >= most {
			break
		}
		// Truncated with a cookie is only "there is another page". Without
		// one, the server stopped the search: the driver reports a size limit
		// on an unpaged search this way.
		if res.Truncated && len(res.Cookie) == 0 {
			return nil, incomplete(base, scope, "sizeLimitExceeded, or a truncated result")
		}
		if len(res.Cookie) == 0 {
			break
		}
		cookie = res.Cookie
	}
	a.read = append(a.read, MemberAuditRead{Base: base.String(), Scope: auditScope(scope), Entries: len(entries)})
	return entries, nil
}

// auditLimit names the server limit an error is, when it is one.
func auditLimit(err error) (string, bool) {
	var le *ldapdriver.Error
	if !errors.As(err, &le) {
		return "", false
	}
	switch le.Code {
	case ldap.LDAPResultSizeLimitExceeded:
		return "sizeLimitExceeded", true
	case ldap.LDAPResultTimeLimitExceeded:
		return "timeLimitExceeded", true
	case ldap.LDAPResultAdminLimitExceeded:
		return "adminLimitExceeded", true
	}
	return "", false
}

func incomplete(base dn.DN, scope directory.Scope, limit string) *auditRefusal {
	return &auditRefusal{code: ErrorErrorAuditIncomplete,
		message: "A search stopped before its end, so the audit was not made.",
		detail: fmt.Sprintf("The %s search of %s ended in %s. A read that stops early would report every name "+
			"after the stop as not found. Narrow the base, or ask for this bind's limit to be raised.",
			auditScope(scope), base, limit)}
}

func tooMuchToRead() *auditRefusal {
	return &auditRefusal{code: ErrorErrorSnapshotTooLarge,
		message: fmt.Sprintf("Checking these members means reading more than %d entries, so the audit was not made.", auditMaxRead),
		detail: fmt.Sprintf("The audit stopped counting at %d entries read under the members' common ancestors, "+
			"which is the most a data snapshot holds too. Narrow the base, so fewer groups and members are checked.", auditMaxRead)}
}

func auditScope(scope directory.Scope) MemberAuditReadScope {
	switch scope {
	case directory.ScopeBase:
		return MemberAuditReadScopeBase
	case directory.ScopeOneLevel:
		return MemberAuditReadScopeOne
	}
	return MemberAuditReadScopeSub
}

// referral records a referral the server returned. Its URL names the DN it
// stands for; one that names none Alder can parse makes everything below the
// search's base not checked.
func (a *memberAuditor) referral(raw, matched string, base dn.DN) {
	entry := MemberAuditReferral{Url: raw}
	at, ok := referralDN(raw)
	if !ok && matched != "" {
		at, ok = parsedDN(matched)
	}
	label := raw
	if label == "" {
		label = matched
	}
	if ok {
		entry.Dn = ptr(at.String())
		a.below = append(a.below, auditReferral{at: at, label: label})
	} else {
		a.below = append(a.below, auditReferral{at: base, label: label})
	}
	a.referrals = append(a.referrals, entry)
}

func referralDN(raw string) (dn.DN, bool) {
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, "ldap") && !strings.EqualFold(u.Scheme, "ldaps") {
		return nil, false
	}
	path := strings.TrimPrefix(u.Path, "/")
	if u.RawPath != "" {
		if unescaped, err := url.PathUnescape(strings.TrimPrefix(u.RawPath, "/")); err == nil {
			path = unescaped
		}
	}
	return parsedDN(path)
}

func parsedDN(s string) (dn.DN, bool) {
	d, err := dn.Parse(s)
	if err != nil || d.IsEmpty() {
		return nil, false
	}
	return d, true
}

// findReferrals searches each naming context the names are in for referral
// objects, for names only. A referral object comes back as a search result
// reference, which is recorded like any other; an entry that comes back
// instead (a server that returns the object itself) says nothing about what is
// below it, and is not counted.
func (a *memberAuditor) findReferrals(ctx context.Context, names map[string]*auditName, order []string) error {
	if a.sch.ObjectClass("referral") == nil {
		return nil
	}
	var contexts []dn.DN
	for _, key := range order {
		c, ok := a.contextOf(names[key].dn)
		if ok && !slices.ContainsFunc(contexts, c.Equal) {
			contexts = append(contexts, c)
		}
	}
	for _, c := range contexts {
		var cookie []byte
		for {
			res, err := a.r.Search(ctx, directory.SearchRequest{
				BaseDN: c, Scope: directory.ScopeSubtree, Filter: filter.Equal("objectClass", "referral"),
				Attributes: []string{"1.1"}, Limit: directory.MaxPageSize, PageSize: directory.MaxPageSize, Cookie: cookie,
			})
			if err != nil {
				if limit, ok := auditLimit(err); ok {
					return incomplete(c, directory.ScopeSubtree, limit)
				}
				return err
			}
			for _, ref := range res.Referrals {
				a.referral(ref, "", c)
			}
			if res.Truncated && len(res.Cookie) == 0 {
				return incomplete(c, directory.ScopeSubtree, "sizeLimitExceeded, or a truncated result")
			}
			if len(res.Cookie) == 0 {
				break
			}
			cookie = res.Cookie
		}
		a.read = append(a.read, MemberAuditRead{Base: c.String(), Scope: MemberAuditReadScopeSub, Entries: 0})
	}
	return nil
}

func (a *memberAuditor) markBelow(n *auditName) {
	if n.referral != "" {
		return
	}
	for _, ref := range a.below {
		if n.dn.HasSuffix(ref.at) {
			n.referral = ref.label
			return
		}
	}
}

// readEach reads every name on its own, as a base search for no attributes.
func (a *memberAuditor) readEach(ctx context.Context, names []*auditName) error {
	for _, n := range names {
		_, err := a.r.Read(ctx, n.dn, []string{"1.1"})
		got := 0
		var le *ldapdriver.Error
		switch {
		case err == nil:
			n.found, got = true, 1
		case isNoSuchObject(err):
		case errors.As(err, &le) && le.Code == ldap.LDAPResultReferral:
			if len(le.Referrals) == 0 {
				a.referral("", le.ReferralDN, n.dn)
			}
			for _, ref := range le.Referrals {
				a.referral(ref, le.ReferralDN, n.dn)
			}
			a.markBelow(n)
		default:
			if limit, ok := auditLimit(err); ok {
				return incomplete(n.dn, directory.ScopeBase, limit)
			}
			return err
		}
		a.entries += got
		a.read = append(a.read, MemberAuditRead{Base: n.dn.String(), Scope: MemberAuditReadScopeBase, Entries: got})
	}
	return nil
}

// searchUnder finds which names exist by searching under their common
// ancestors, for names only.
func (a *memberAuditor) searchUnder(ctx context.Context, names []*auditName) error {
	dns := make([]dn.DN, len(names))
	for i, n := range names {
		dns[i] = n.dn
	}
	found := map[string]bool{}
	for _, b := range narrowBases(dns, a.contexts) {
		entries, err := a.search(ctx, b.dn, b.scope, filter.Present("objectClass"), []string{"1.1"}, auditMaxRead+1)
		var le *ldapdriver.Error
		switch {
		case err == nil:
		case isNoSuchObject(err):
			// The ancestor itself is gone, and with it everything below.
			a.read = append(a.read, MemberAuditRead{Base: b.dn.String(), Scope: auditScope(b.scope), Entries: 0})
		case errors.As(err, &le) && le.Code == ldap.LDAPResultReferral:
			a.referral(firstOr(le.Referrals, ""), le.ReferralDN, b.dn)
		default:
			return err
		}
		for _, e := range entries {
			found[e.DN.Key()] = true
		}
	}
	for _, n := range names {
		a.markBelow(n)
		n.found = found[n.dn.Key()]
	}
	return nil
}

func firstOr(values []string, fallback string) string {
	if len(values) > 0 {
		return values[0]
	}
	return fallback
}

// auditBase is one search the existence check makes.
type auditBase struct {
	dn    dn.DN
	scope directory.Scope
}

// narrowBases is where to search for a set of names: their parents, with a
// parent at or below another dropped, and -- while more than auditMaxBases
// remain -- the deepest replaced by their own parents, merging those that
// meet. A base is never above the naming context it is in. Each is searched
// one level down when every name below it is its child, and as a subtree
// otherwise.
func narrowBases(names []dn.DN, contexts []dn.DN) []auditBase {
	contextOf := func(d dn.DN) dn.DN {
		var best dn.DN
		for _, c := range contexts {
			if d.HasSuffix(c) && len(c) > len(best) {
				best = c
			}
		}
		return best
	}
	up := func(d dn.DN) dn.DN {
		ctx := contextOf(d)
		if len(d) <= len(ctx) {
			return d
		}
		return d.Parent()
	}

	bases := make([]dn.DN, 0, len(names))
	for _, n := range names {
		bases = append(bases, up(n))
	}
	bases = outermost(bases)
	for len(bases) > auditMaxBases {
		deepest := 0
		for _, b := range bases {
			if len(b) > len(contextOf(b)) && len(b) > deepest {
				deepest = len(b)
			}
		}
		if deepest == 0 {
			break // every base is a naming context already
		}
		for i, b := range bases {
			if len(b) == deepest {
				bases[i] = up(b)
			}
		}
		bases = outermost(bases)
	}

	out := make([]auditBase, 0, len(bases))
	for _, b := range bases {
		scope := directory.ScopeOneLevel
		for _, n := range names {
			if n.HasSuffix(b) && !n.Parent().Equal(b) {
				scope = directory.ScopeSubtree
				break
			}
		}
		out = append(out, auditBase{dn: b, scope: scope})
	}
	return out
}

// outermost drops every DN at or below another in the list, keeping the order
// of first appearance.
func outermost(dns []dn.DN) []dn.DN {
	out := make([]dn.DN, 0, len(dns))
	for i, d := range dns {
		covered := false
		for j, other := range dns {
			if i == j {
				continue
			}
			if d.HasSuffix(other) && (len(other) < len(d) || j < i) {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, d)
		}
	}
	return out
}

// report builds the answer from what was read.
func (a *memberAuditor) report(groups []auditGroup, names map[string]*auditName) *MemberAudit {
	out := &MemberAudit{
		ReadAt:    time.Now().UTC(),
		Read:      a.read,
		Referrals: a.referrals,
		Groups:    make([]MemberAuditGroup, 0, len(groups)),
		Cycles:    []string{},
	}
	if out.Read == nil {
		out.Read = []MemberAuditRead{}
	}
	if out.Referrals == nil {
		out.Referrals = []MemberAuditReferral{}
	}

	index := map[string]int{}
	for i, g := range groups {
		index[g.dn.Key()] = i
	}

	for _, g := range groups {
		row := MemberAuditGroup{
			Dn: g.dn.String(), ClassesRequireMember: g.requires,
			NotFound: []MemberAuditValue{}, NotChecked: []MemberAuditUnchecked{},
		}
		for _, v := range g.values {
			switch {
			case v.key == "":
				row.NotChecked = append(row.NotChecked, MemberAuditUnchecked{Attribute: v.attribute, Value: v.value, Reason: v.reason})
			case names[v.key].referral != "":
				row.NotChecked = append(row.NotChecked, MemberAuditUnchecked{Attribute: v.attribute, Value: v.value,
					Reason: UncheckedBelowReferral, Referral: ptr(names[v.key].referral)})
			case !names[v.key].found:
				row.NotFound = append(row.NotFound, MemberAuditValue{Attribute: v.attribute, Value: v.value})
			}
		}
		row.Remaining = len(g.values) - len(row.NotFound)
		out.Groups = append(out.Groups, row)
	}

	// The groups each group still names, among those read, once the values
	// not found are gone.
	edges := make([][]int, len(groups))
	onlyGroups := make([]bool, len(groups))
	for i, g := range groups {
		onlyGroups[i] = out.Groups[i].Remaining > 0
		for _, v := range g.values {
			if v.key != "" && names[v.key].referral == "" && !names[v.key].found {
				continue // not found, and so not remaining
			}
			j, isGroup := index[v.key]
			if v.key == "" || !isGroup {
				onlyGroups[i] = false
				continue
			}
			edges[i] = append(edges[i], j)
		}
	}

	// The greatest set of groups whose remaining members are all groups that
	// are empty or in the set: a ring of groups naming only each other holds
	// nobody either.
	noPerson := append([]bool(nil), onlyGroups...)
	for changed := true; changed; {
		changed = false
		for i := range groups {
			if !noPerson[i] {
				continue
			}
			for _, j := range edges[i] {
				if out.Groups[j].Remaining > 0 && !noPerson[j] {
					noPerson[i], changed = false, true
					break
				}
			}
		}
	}
	for i := range groups {
		out.Groups[i].NoPersonThroughNesting = noPerson[i]
	}

	for _, i := range onCycles(edges) {
		out.Cycles = append(out.Cycles, out.Groups[i].Dn)
	}
	return out
}

// onCycles is every node on a cycle, in index order: Tarjan's strongly
// connected components, keeping those of more than one node or with a loop.
func onCycles(edges [][]int) []int {
	n := len(edges)
	idx, low := make([]int, n), make([]int, n)
	for i := range idx {
		idx[i] = -1
	}
	onStack := make([]bool, n)
	var stack []int
	counter := 0
	on := make([]bool, n)

	var connect func(v int)
	connect = func(v int) {
		idx[v], low[v] = counter, counter
		counter++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range edges[v] {
			if idx[w] < 0 {
				connect(w)
				low[v] = min(low[v], low[w])
			} else if onStack[w] {
				low[v] = min(low[v], idx[w])
			}
		}
		if low[v] != idx[v] {
			return
		}
		var component []int
		for {
			w := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[w] = false
			component = append(component, w)
			if w == v {
				break
			}
		}
		loop := false
		for _, w := range edges[v] {
			loop = loop || w == v
		}
		if len(component) > 1 || loop {
			for _, w := range component {
				on[w] = true
			}
		}
	}
	for v := range n {
		if idx[v] < 0 {
			connect(v)
		}
	}
	var out []int
	for v, yes := range on {
		if yes {
			out = append(out, v)
		}
	}
	return out
}

// AuditMembers reports the membership values below a base that name no entry
// this bind can find.
func (s *Server) AuditMembers(c *fiber.Ctx) error {
	sess := s.require(c)
	if sess == nil {
		return nil
	}
	var body MemberAuditRequest
	if err := c.BodyParser(&body); err != nil {
		return badRequest(c, "The request body is not valid JSON.", err.Error())
	}
	base, err := dn.Parse(strings.TrimSpace(body.Base))
	if err != nil || base.IsEmpty() {
		return badRequest(c, "The base is not a valid DN.", "Name the subtree that holds the groups.")
	}
	if kind := targetKind(sess.Conn.Capabilities(), base); kind != PlanTargetData {
		return badRequest(c, fmt.Sprintf("A membership audit does not read the %s.", kind),
			"Its base must be directory data.")
	}
	limit := clamp(deref(body.Limit), 500, 1, 2000)

	ctx, cancel := reqCtx(c)
	defer cancel()
	// The base itself first: an audit of a subtree that is not there is a 404,
	// not an empty report.
	if _, err := sess.Conn.Read(ctx, base, []string{"1.1"}); err != nil {
		return s.fail(c, err)
	}
	audit, err := auditMembers(ctx, sess.Conn, base, limit)
	var refusal *auditRefusal
	if errors.As(err, &refusal) {
		return writeError(c, fiber.StatusBadRequest, refusal.code, refusal.message, refusal.detail)
	}
	if err != nil {
		return s.fail(c, err)
	}
	audit.Bind = sess.BindDN()
	return c.JSON(audit)
}
