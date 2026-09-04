package api

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/hazame-hub/alder/internal/ansible"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/filter"
	"github.com/hazame-hub/alder/internal/ldif"
	"github.com/hazame-hub/alder/internal/schema"
	"github.com/hazame-hub/alder/internal/session"
)

// requestTimeout bounds one directory operation performed on behalf of a
// request. It is shorter than the driver's own timeout so a slow directory
// surfaces as a request that fails rather than a browser that hangs.
const requestTimeout = 30 * time.Second

func reqCtx(c *fiber.Ctx) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.UserContext(), requestTimeout)
}

// --- session ----------------------------------------------------------------

// CreateSession connects to a directory and binds.
func (s *Server) CreateSession(c *fiber.Ctx) error {
	var body ConnectRequest
	if err := c.BodyParser(&body); err != nil {
		return badRequest(c, "The request body is not valid JSON.", err.Error())
	}

	cfg := directory.ConnConfig{
		Host:       strings.TrimSpace(body.Host),
		Port:       body.Port,
		TLS:        directory.TLSMode(body.Tls),
		BindDN:     strings.TrimSpace(deref(body.BindDn)),
		ServerName: strings.TrimSpace(deref(body.ServerName)),
		Timeout:    requestTimeout,

		ConfigBindDN: strings.TrimSpace(deref(body.ConfigBindDn)),
	}
	if body.BindPassword != nil {
		cfg.BindPassword = *body.BindPassword
	}
	if body.ConfigBindPassword != nil {
		cfg.ConfigBindPassword = *body.ConfigBindPassword
	}
	if body.InsecureSkipVerify != nil {
		cfg.InsecureSkipVerify = *body.InsecureSkipVerify
	}
	if body.CaCertificate != nil && strings.TrimSpace(*body.CaCertificate) != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(*body.CaCertificate)) {
			return badRequest(c, "The CA certificate is not a PEM bundle.", "")
		}
		cfg.CACertificates = pool
	}

	if err := cfg.Validate(); err != nil {
		return badRequest(c, "The connection settings are not usable.", err.Error())
	}

	ctx, cancel := reqCtx(c)
	defer cancel()

	conn, err := s.driver.Connect(ctx, cfg)
	if err != nil {
		// A failed connect is reported as an upstream failure, not a 500: the
		// fault is with the directory or the settings, not with Alder.
		s.logger.Info("connection refused",
			"host", cfg.Host, "port", cfg.Port, "tls", cfg.TLS, "error", err)
		return writeError(c, fiber.StatusBadGateway, ErrorErrorUpstream,
			"Could not connect to the directory.", err.Error())
	}

	sess, err := s.sessions.Add(conn, cfg, s.cfg.ReadOnly)
	if err != nil {
		_ = conn.Close()
		return s.fail(c, err)
	}
	s.setSessionCookie(c, sess.ID)
	return c.Status(fiber.StatusCreated).JSON(s.sessionInfo(sess))
}

// GetSession describes the current session.
func (s *Server) GetSession(c *fiber.Ctx) error {
	sess, err := s.current(c)
	if err != nil {
		// Not being connected is a normal state for this endpoint: the UI calls
		// it on load to decide whether to show the connection screen.
		return c.JSON(SessionInfo{Connected: false, ReadOnly: ptr(s.cfg.ReadOnly)})
	}
	return c.JSON(s.sessionInfo(sess))
}

// DeleteSession disconnects.
func (s *Server) DeleteSession(c *fiber.Ctx) error {
	if id := c.Cookies(s.cookieName()); id != "" {
		s.sessions.Remove(id)
	}
	s.clearSessionCookie(c)
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *Server) sessionInfo(sess *session.Session) SessionInfo {
	caps := sess.Conn.Capabilities()
	info := SessionInfo{
		Connected:    true,
		Host:         ptr(sess.Host()),
		Port:         ptr(sess.Port()),
		Tls:          ptr(sess.TLS()),
		Verified:     ptr(sess.Verified()),
		BindDn:       ptr(sess.BindDN()),
		ReadOnly:     ptr(sess.ReadOnly),
		Capabilities: ptr(capabilitiesView(caps)),
	}
	if caps.VendorName != "" {
		info.VendorName = ptr(caps.VendorName)
	}
	if caps.VendorVersion != "" {
		info.VendorVersion = ptr(caps.VendorVersion)
	}
	return info
}

// --- tree -------------------------------------------------------------------

// treeBrowser is the optional interface the LDAP driver provides for cheap
// child tests. A driver that does not implement it still works; the tree just
// reports every node as expandable.
type treeBrowser interface {
	HasChildren(context.Context, dn.DN) (bool, error)
	Children(context.Context, dn.DN, []string, int, []byte) (*directory.SearchResult, error)
}

// ListChildren returns the naming contexts, or one entry's children.
func (s *Server) ListChildren(c *fiber.Ctx, params ListChildrenParams) error {
	sess := s.require(c)
	if sess == nil {
		return nil
	}
	ctx, cancel := reqCtx(c)
	defer cancel()

	// The schema is best-effort here: a tree that cannot name a node's
	// structural class is still a usable tree.
	sch, _ := sess.Conn.Schema(ctx)

	if params.Dn == nil || strings.TrimSpace(*params.Dn) == "" {
		return s.namingContextNodes(c, ctx, sess, sch)
	}

	parent, ok := parseDNParam(c, *params.Dn)
	if !ok {
		return nil
	}
	limit := clamp(deref(params.Limit), 100, 1, 1000)

	browser, ok := sess.Conn.(treeBrowser)
	if !ok {
		return s.fail(c, errors.New("the directory driver does not support tree browsing"))
	}
	res, err := browser.Children(ctx, parent, []string{"objectClass"}, limit, []byte(deref(params.Cookie)))
	if err != nil {
		return s.fail(c, err)
	}

	page := TreePage{Nodes: make([]TreeNode, 0, len(res.Entries))}
	for _, e := range res.Entries {
		hasKids, kidErr := browser.HasChildren(ctx, e.DN)
		if kidErr != nil {
			// Not being allowed to look below a node is not a reason to fail
			// the whole listing; it means the node is drawn without an
			// expander, which is what the user's access actually permits.
			hasKids = false
		}
		page.Nodes = append(page.Nodes, treeNode(e, sch, hasKids, false))
	}
	sortNodes(page.Nodes)
	if len(res.Cookie) > 0 {
		page.Cookie = ptr(string(res.Cookie))
	}
	page.Truncated = ptr(res.Truncated)
	return c.JSON(page)
}

// namingContextNodes returns the tree's roots.
//
// The data suffixes come from the RootDSE. The server's own configuration tree
// is a root too, when this session can read it: it is where the schema, the
// databases and the access rules actually live, and leaving it out of the tree
// meant the one part of the directory an engineer most often needs to look at
// was the one part Alder would not show them.
func (s *Server) namingContextNodes(c *fiber.Ctx, ctx context.Context, sess *session.Session, sch *schema.Schema) error {
	browser, _ := sess.Conn.(treeBrowser)
	caps := sess.Conn.Capabilities()
	contexts := caps.NamingContexts
	if caps.Config.Readable && caps.Config.DN != "" {
		contexts = append(append([]string{}, contexts...), caps.Config.DN)
	}

	page := TreePage{Nodes: make([]TreeNode, 0, len(contexts))}
	for _, raw := range contexts {
		root, err := dn.Parse(raw)
		if err != nil {
			s.logger.Warn("the server published a naming context that does not parse",
				"namingContext", raw, "error", err)
			continue
		}
		// Reading the root gives it object classes, so the tree can pick an
		// icon. A root the bind cannot read is still shown: it is where the
		// user will want to look, and the error belongs at the click.
		entry, readErr := sess.Conn.Read(ctx, root, []string{"objectClass"})
		if readErr != nil {
			page.Nodes = append(page.Nodes, TreeNode{
				Dn: root.String(), Rdn: rdnLabel(root),
				HasChildren: true, IsNamingContext: ptr(true),
			})
			continue
		}
		hasKids := true
		if browser != nil {
			if got, kidErr := browser.HasChildren(ctx, root); kidErr == nil {
				hasKids = got
			}
		}
		page.Nodes = append(page.Nodes, treeNode(entry, sch, hasKids, true))
	}
	sortNodes(page.Nodes)
	return c.JSON(page)
}

// sortNodes orders siblings by their RDN, case-insensitively. Directories
// return children in whatever order suits their index, and a tree that
// reshuffles between refreshes is unusable.
func sortNodes(nodes []TreeNode) {
	sort.SliceStable(nodes, func(i, j int) bool {
		return strings.ToLower(nodes[i].Rdn) < strings.ToLower(nodes[j].Rdn)
	})
}

// --- entry ------------------------------------------------------------------

// GetEntry reads one entry, annotated from the schema.
func (s *Server) GetEntry(c *fiber.Ctx, params GetEntryParams) error {
	sess := s.require(c)
	if sess == nil {
		return nil
	}
	target, ok := parseDNParam(c, params.Dn)
	if !ok {
		return nil
	}
	ctx, cancel := reqCtx(c)
	defer cancel()

	attrs := []string{"*"}
	if params.IncludeOperational == nil || *params.IncludeOperational {
		attrs = append(attrs, "+")
	}
	entry, err := sess.Conn.Read(ctx, target, attrs)
	if err != nil {
		return s.fail(c, err)
	}
	sch, err := sess.Conn.Schema(ctx)
	if err != nil {
		return s.fail(c, err)
	}

	classes := entry.ObjectClasses()
	req := sch.Requirements(classes)

	view := EntryView{
		Dn:            entry.DN.String(),
		Rdn:           ptr(rdnLabel(entry.DN)),
		ParentDn:      ptr(entry.DN.Parent().String()),
		ObjectClasses: ptr(classes),
		Attributes:    entryAttributes(entry, sch, req),
		Requirements:  ptr(requirementsView(req)),
		Ldif:          ptr(directory.EntryLDIF(entry).String()),
	}
	if browser, canBrowse := sess.Conn.(treeBrowser); canBrowse {
		if hasKids, kidErr := browser.HasChildren(ctx, target); kidErr == nil {
			view.HasChildren = ptr(hasKids)
		}
	}
	return c.JSON(view)
}

// --- search -----------------------------------------------------------------

// Search runs a bounded, paged search.
func (s *Server) Search(c *fiber.Ctx) error {
	sess := s.require(c)
	if sess == nil {
		return nil
	}
	var body SearchRequest
	if err := c.BodyParser(&body); err != nil {
		return badRequest(c, "The request body is not valid JSON.", err.Error())
	}
	base, ok := parseDNParam(c, body.BaseDn)
	if !ok {
		return nil
	}
	scope, err := searchScope(body.Scope)
	if err != nil {
		return badRequest(c, "Unknown search scope.", err.Error())
	}

	// The filter is parsed into a tree, never pasted into one. A value holding
	// filter metacharacters becomes an escaped assertion value, not structure.
	parsed, err := filter.Parse(strings.TrimSpace(body.Filter))
	if err != nil {
		return badRequest(c, "The search filter is not a valid RFC 4515 filter.", err.Error())
	}

	req := directory.SearchRequest{
		BaseDN:   base,
		Scope:    scope,
		Filter:   parsed,
		Limit:    clamp(deref(body.Limit), 100, 1, directory.MaxResults),
		PageSize: clamp(deref(body.PageSize), 100, 1, directory.MaxPageSize),
		Cookie:   []byte(deref(body.Cookie)),
	}
	if body.Attributes != nil && len(*body.Attributes) > 0 {
		req.Attributes = *body.Attributes
	}

	ctx, cancel := reqCtx(c)
	defer cancel()

	started := time.Now()
	res, err := sess.Conn.Search(ctx, req)
	if err != nil {
		return s.fail(c, err)
	}
	sch, err := sess.Conn.Schema(ctx)
	if err != nil {
		return s.fail(c, err)
	}

	out := SearchResponse{
		Entries:   make([]SearchResultEntry, 0, len(res.Entries)),
		Truncated: res.Truncated,
		Took:      ptr(time.Since(started).Round(time.Millisecond).String()),
	}
	for _, e := range res.Entries {
		out.Entries = append(out.Entries, SearchResultEntry{
			Dn:         e.DN.String(),
			Rdn:        ptr(rdnLabel(e.DN)),
			Attributes: ptr(entryAttributes(e, sch, sch.Requirements(e.ObjectClasses()))),
		})
	}
	if len(res.Cookie) > 0 {
		out.Cookie = ptr(string(res.Cookie))
	}
	if len(res.Referrals) > 0 {
		out.Referrals = ptr(res.Referrals)
	}
	return c.JSON(out)
}

// --- schema -----------------------------------------------------------------

// GetSchema returns the whole schema, indexed for browsing.
func (s *Server) GetSchema(c *fiber.Ctx) error {
	sess := s.require(c)
	if sess == nil {
		return nil
	}
	ctx, cancel := reqCtx(c)
	defer cancel()

	sch, err := sess.Conn.Schema(ctx)
	if err != nil {
		return s.fail(c, err)
	}

	counts := sch.Counts()
	view := SchemaView{
		SubschemaDn: sch.DN,
		Counts: SchemaCounts{
			ObjectClasses:    ptr(counts.ObjectClasses),
			AttributeTypes:   ptr(counts.AttributeTypes),
			Syntaxes:         ptr(counts.Syntaxes),
			MatchingRules:    ptr(counts.MatchingRules),
			MatchingRuleUses: ptr(counts.MatchingRuleUses),
			DitContentRules:  ptr(counts.DITContentRules),
			NameForms:        ptr(counts.NameForms),
			Errors:           ptr(counts.Errors),
		},
	}

	classes := make([]ObjectClassSummary, 0, len(sch.ObjectClasses))
	for _, oc := range sch.ObjectClasses {
		classes = append(classes, objectClassSummary(oc))
	}
	sort.Slice(classes, func(i, j int) bool {
		return strings.ToLower(classes[i].Name) < strings.ToLower(classes[j].Name)
	})
	view.ObjectClasses = ptr(classes)

	attrs := make([]AttributeTypeSummary, 0, len(sch.AttributeTypes))
	for _, at := range sch.AttributeTypes {
		attrs = append(attrs, attributeTypeSummary(sch, at))
	}
	sort.Slice(attrs, func(i, j int) bool {
		return strings.ToLower(attrs[i].Name) < strings.ToLower(attrs[j].Name)
	})
	view.AttributeTypes = ptr(attrs)

	syntaxes := make([]SyntaxSummary, 0, len(sch.Syntaxes))
	for _, sy := range sch.Syntaxes {
		syntaxes = append(syntaxes, SyntaxSummary{
			Oid:         sy.OID,
			Desc:        ptr(sch.SyntaxLabel(sy.OID)),
			Kind:        ptr(string(schema.SyntaxKind(sy.OID))),
			UsedByCount: ptr(len(sch.AttributesWithSyntax(sy.OID))),
		})
	}
	sort.Slice(syntaxes, func(i, j int) bool {
		return strings.ToLower(deref(syntaxes[i].Desc)) < strings.ToLower(deref(syntaxes[j].Desc))
	})
	view.Syntaxes = ptr(syntaxes)

	rules := make([]MatchingRuleSummary, 0, len(sch.MatchingRules))
	for _, mr := range sch.MatchingRules {
		rules = append(rules, MatchingRuleSummary{
			Name:     mr.Name(),
			Oid:      mr.OID,
			Desc:     ptr(mr.Desc),
			Syntax:   ptr(mr.Syntax),
			Obsolete: ptr(mr.Obsolete),
		})
	}
	sort.Slice(rules, func(i, j int) bool {
		return strings.ToLower(rules[i].Name) < strings.ToLower(rules[j].Name)
	})
	view.MatchingRules = ptr(rules)

	// Parse failures are reported rather than hidden. A schema browser that
	// silently omits what it could not read is lying about the directory.
	if len(sch.Errors) > 0 {
		errs := make([]SchemaParseError, 0, len(sch.Errors))
		for _, e := range sch.Errors {
			errs = append(errs, SchemaParseError{
				Attribute:  ptr(e.Attribute),
				Definition: ptr(e.Definition),
				Message:    ptr(e.Err.Error()),
			})
		}
		view.Errors = ptr(errs)
	}
	return c.JSON(view)
}

func objectClassSummary(oc *schema.ObjectClass) ObjectClassSummary {
	return ObjectClassSummary{
		Name:      oc.Name(),
		Names:     ptr(oc.Names),
		Oid:       oc.OID,
		Desc:      ptr(oc.Desc),
		Kind:      ObjectClassSummaryKind(oc.Kind.String()),
		Obsolete:  ptr(oc.Obsolete),
		Superiors: ptr(oc.SuperNames),
	}
}

func attributeTypeSummary(sch *schema.Schema, at *schema.AttributeType) AttributeTypeSummary {
	syn := sch.EffectiveSyntax(at)
	return AttributeTypeSummary{
		Name:        at.Name(),
		Names:       ptr(at.Names),
		Oid:         at.OID,
		Desc:        ptr(at.Desc),
		Obsolete:    ptr(at.Obsolete),
		Superior:    ptr(at.SuperName),
		Syntax:      ptr(syn),
		SyntaxLabel: ptr(sch.SyntaxLabel(syn)),
		Equality:    ptr(sch.EffectiveEquality(at)),
		SingleValue: ptr(sch.EffectiveSingleValue(at)),
		Operational: ptr(sch.EffectiveUsage(at).Operational()),
	}
}

// GetObjectClass returns one object class with its cross-links resolved.
func (s *Server) GetObjectClass(c *fiber.Ctx, name string) error {
	sess := s.require(c)
	if sess == nil {
		return nil
	}
	ctx, cancel := reqCtx(c)
	defer cancel()

	sch, err := sess.Conn.Schema(ctx)
	if err != nil {
		return s.fail(c, err)
	}
	oc := sch.ObjectClass(name)
	if oc == nil {
		return writeError(c, fiber.StatusNotFound, ErrorErrorNotFound,
			fmt.Sprintf("No object class named %q in this schema.", name), "")
	}

	supers := sch.Supers(oc)
	chain := make([]string, 0, len(supers))
	inheritedMust := map[string]bool{}
	inheritedMay := map[string]bool{}
	for _, sup := range supers {
		chain = append(chain, sup.Name())
		for _, m := range sup.Must {
			inheritedMust[sch.CanonicalAttrName(m)] = true
		}
		for _, m := range sup.May {
			inheritedMay[sch.CanonicalAttrName(m)] = true
		}
	}
	subs := sch.SubclassesOf(oc)
	subNames := make([]string, 0, len(subs))
	for _, sub := range subs {
		subNames = append(subNames, sub.Name())
	}

	return c.JSON(ObjectClassDetail{
		Summary:       objectClassSummary(oc),
		Must:          ptr(canonicalAll(sch, oc.Must)),
		May:           ptr(canonicalAll(sch, oc.May)),
		InheritedMust: ptr(sortedKeys(inheritedMust)),
		InheritedMay:  ptr(sortedKeys(inheritedMay)),
		SuperiorChain: ptr(chain),
		Subclasses:    ptr(subNames),
		Raw:           ptr(oc.Raw),
	})
}

// GetAttributeType returns one attribute type with its cross-links resolved.
func (s *Server) GetAttributeType(c *fiber.Ctx, name string) error {
	sess := s.require(c)
	if sess == nil {
		return nil
	}
	ctx, cancel := reqCtx(c)
	defer cancel()

	sch, err := sess.Conn.Schema(ctx)
	if err != nil {
		return s.fail(c, err)
	}
	at := sch.AttributeType(name)
	if at == nil {
		return writeError(c, fiber.StatusNotFound, ErrorErrorNotFound,
			fmt.Sprintf("No attribute type named %q in this schema.", name), "")
	}

	supers := sch.SuperTypes(at)
	chain := make([]string, 0, len(supers))
	for _, sup := range supers {
		chain = append(chain, sup.Name())
	}
	must, may := sch.UsedBy(at.Name())
	return c.JSON(AttributeTypeDetail{
		Summary:       attributeTypeSummary(sch, at),
		Kind:          ptr(attributeKind(sch.KindOf(at.Name()))),
		SuperiorChain: ptr(chain),
		RequiredBy:    ptr(classNames(must)),
		OptionalIn:    ptr(classNames(may)),
		Raw:           ptr(at.Raw),
	})
}

func classNames(list []*schema.ObjectClass) []string {
	out := make([]string, 0, len(list))
	for _, c := range list {
		out = append(out, c.Name())
	}
	return out
}

func canonicalAll(sch *schema.Schema, names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, sch.CanonicalAttrName(n))
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

// --- changes ----------------------------------------------------------------

// PreviewChange renders a change without applying it.
func (s *Server) PreviewChange(c *fiber.Ctx) error {
	sess := s.require(c)
	if sess == nil {
		return nil
	}
	var body ChangeRequest
	if err := c.BodyParser(&body); err != nil {
		return badRequest(c, "The request body is not valid JSON.", err.Error())
	}
	record, err := changeRecord(body)
	if err != nil {
		return badRequest(c, "The change is not usable.", err.Error())
	}
	if err := record.Validate(); err != nil {
		return badRequest(c, "The change is not usable.", err.Error())
	}

	ctx, cancel := reqCtx(c)
	defer cancel()
	sch, _ := sess.Conn.Schema(ctx)

	preview, err := s.renderPreview(record, sch)
	if err != nil {
		return s.fail(c, err)
	}
	return c.JSON(preview)
}

// renderPreview builds the LDIF, the Ansible task and the schema warnings for
// one change record.
func (s *Server) renderPreview(record directory.ChangeRecord, sch *schema.Schema) (ChangePreview, error) {
	task, err := ansible.Task(record)
	if err != nil {
		return ChangePreview{}, err
	}
	preview := ChangePreview{
		Ldif:               record.LDIF(),
		LdifFolded:         ptr(record.LDIFFolded()),
		Ansible:            task,
		AnsiblePlaybook:    ptr(ansible.Playbook(task)),
		Summary:            record.Summary(),
		AffectedAttributes: ptr(record.AffectedAttributes()),
	}
	if warnings := schemaWarnings(record, sch); len(warnings) > 0 {
		preview.Warnings = ptr(warnings)
	}
	return preview, nil
}

// schemaWarnings reports what the schema says about a change without blocking
// it.
//
// The server is the authority on whether a change is legal, and Alder does not
// duplicate that judgement. What it can usefully do is say "no object class on
// this entry permits that attribute" before the round trip, so the user finds
// out from the editor rather than from a result code.
func schemaWarnings(record directory.ChangeRecord, sch *schema.Schema) []string {
	if sch == nil {
		return nil
	}
	var warnings []string

	switch record.Type {
	case directory.ChangeAdd:
		var classes []string
		for _, a := range record.Attrs {
			if strings.EqualFold(schema.BaseName(a.Name), "objectClass") {
				for _, v := range a.Values {
					classes = append(classes, string(v))
				}
			}
		}
		req := sch.Requirements(classes)
		for _, unknown := range req.Unknown {
			warnings = append(warnings, fmt.Sprintf(
				"The schema does not define the object class %q.", unknown))
		}
		if req.Structural == nil && len(req.Unknown) == 0 {
			warnings = append(warnings,
				"These object classes do not resolve to exactly one structural class; "+
					"the directory requires exactly one.")
		}
		present := map[string]bool{}
		for _, a := range record.Attrs {
			present[foldName(a.Name)] = true
		}
		for _, m := range req.Must {
			if !present[foldName(m)] {
				warnings = append(warnings, fmt.Sprintf(
					"%s is required by these object classes but is not set.", m))
			}
		}
		for _, a := range record.Attrs {
			warnings = appendUnknownAttrWarning(warnings, sch, a.Name, req)
		}
	case directory.ChangeModify:
		for _, m := range record.Mods {
			kind := sch.KindOf(m.Name)
			if !kind.Known {
				warnings = append(warnings, fmt.Sprintf(
					"The schema does not define the attribute %q.", m.Name))
				continue
			}
			if kind.ReadOnly {
				warnings = append(warnings, fmt.Sprintf(
					"%s is NO-USER-MODIFICATION; the directory owns it and will refuse this.", kind.Name))
			}
			if kind.SingleValue && len(m.Values) > 1 {
				warnings = append(warnings, fmt.Sprintf(
					"%s is single-valued but the change supplies %d values.", kind.Name, len(m.Values)))
			}
		}
	}
	return warnings
}

func appendUnknownAttrWarning(warnings []string, sch *schema.Schema, name string, req schema.AttributeRequirements) []string {
	if strings.EqualFold(schema.BaseName(name), "objectClass") {
		return warnings
	}
	if !sch.KindOf(name).Known {
		return append(warnings, fmt.Sprintf("The schema does not define the attribute %q.", name))
	}
	folded := foldName(name)
	for _, m := range req.Must {
		if foldName(m) == folded {
			return warnings
		}
	}
	for _, m := range req.May {
		if foldName(m) == folded {
			return warnings
		}
	}
	if len(req.Unknown) > 0 {
		// With an unrecognised class in play the MUST/MAY sets are incomplete,
		// so this check would produce noise rather than information.
		return warnings
	}
	return append(warnings, fmt.Sprintf(
		"No object class on this entry permits %s; add an auxiliary class that does.", name))
}

// ApplyChange applies a change. It is the only endpoint that writes.
func (s *Server) ApplyChange(c *fiber.Ctx) error {
	sess := s.requireWritable(c)
	if sess == nil {
		return nil
	}
	var body ChangeRequest
	if err := c.BodyParser(&body); err != nil {
		return badRequest(c, "The request body is not valid JSON.", err.Error())
	}
	record, err := changeRecord(body)
	if err != nil {
		return badRequest(c, "The change is not usable.", err.Error())
	}

	ctx, cancel := reqCtx(c)
	defer cancel()

	if err := sess.Conn.Apply(ctx, record); err != nil {
		return s.fail(c, err)
	}
	target, err := record.Target()
	if err != nil {
		return s.fail(c, err)
	}
	return c.JSON(ApplyResult{
		Applied: true,
		Dn:      target.String(),
		Summary: ptr(record.Summary()),
		Ldif:    ptr(record.LDIF()),
	})
}

// --- transfer ---------------------------------------------------------------

// ExportLdif exports an entry or a subtree.
func (s *Server) ExportLdif(c *fiber.Ctx, params ExportLdifParams) error {
	sess := s.require(c)
	if sess == nil {
		return nil
	}
	base, ok := parseDNParam(c, params.Dn)
	if !ok {
		return nil
	}
	scopeName := "base"
	if params.Scope != nil {
		scopeName = string(*params.Scope)
	}
	scope, err := directory.ParseScope(scopeName)
	if err != nil {
		return badRequest(c, "Unknown export scope.", err.Error())
	}

	attrs := []string{"*"}
	if params.IncludeOperational != nil && *params.IncludeOperational {
		attrs = append(attrs, "+")
	}

	ctx, cancel := reqCtx(c)
	defer cancel()

	res, err := sess.Conn.Search(ctx, directory.SearchRequest{
		BaseDN:     base,
		Scope:      scope,
		Filter:     filter.Present("objectClass"),
		Attributes: attrs,
		Limit:      clamp(deref(params.Limit), 1000, 1, directory.MaxResults),
	})
	if err != nil {
		return s.fail(c, err)
	}
	if len(res.Entries) == 0 {
		return writeError(c, fiber.StatusNotFound, ErrorErrorNotFound, "No such entry.", "")
	}

	withSecrets := params.IncludeSensitive != nil && *params.IncludeSensitive
	records := make([]*ldif.Record, 0, len(res.Entries))
	for _, e := range res.Entries {
		if withSecrets {
			records = append(records, directory.EntryLDIFWithSecrets(e))
			continue
		}
		records = append(records, directory.EntryLDIF(e))
	}

	doc, err := ldif.Marshal(records)
	if err != nil {
		return s.fail(c, err)
	}

	var header strings.Builder
	header.WriteString("# Exported by Alder\n")
	fmt.Fprintf(&header, "# base:  %s\n", base)
	fmt.Fprintf(&header, "# scope: %s\n", scope)
	fmt.Fprintf(&header, "# %d entries\n", len(res.Entries))
	if res.Truncated {
		// A truncated export that does not say so is a file someone will
		// restore from and discover the gap much later.
		header.WriteString("#\n# WARNING: the result was truncated at the export limit.\n")
		header.WriteString("# This file does not contain the whole subtree.\n")
	}
	if !withSecrets {
		header.WriteString("#\n# Sensitive attributes such as userPassword were omitted.\n")
	}
	header.WriteString("\n")

	c.Set(fiber.HeaderContentType, "text/plain; charset=utf-8")
	c.Set(fiber.HeaderContentDisposition,
		fmt.Sprintf("attachment; filename=%q", exportFilename(base, scope)))
	return c.SendString(header.String() + string(doc))
}

// exportFilename builds a filename from the RDN, keeping only characters that
// are safe in one on every platform.
func exportFilename(base dn.DN, scope directory.Scope) string {
	label := "export"
	if len(base) > 0 && len(base.RDN()) > 0 {
		label = base.RDN()[0].Value
	}
	var b strings.Builder
	for _, r := range label {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	trimmed := strings.Trim(b.String(), "-")
	if trimmed == "" {
		trimmed = "export"
	}
	return fmt.Sprintf("%s-%s.ldif", trimmed, scope)
}

// maxImportBytes bounds an uploaded LDIF document. The whole document is parsed
// in memory, so the bound is what stops one request from exhausting it.
const maxImportBytes = 8 << 20

// ParseLdif parses an LDIF document into reviewable change records.
func (s *Server) ParseLdif(c *fiber.Ctx) error {
	sess := s.require(c)
	if sess == nil {
		return nil
	}
	var body ImportRequest
	if err := c.BodyParser(&body); err != nil {
		return badRequest(c, "The request body is not valid JSON.", err.Error())
	}
	if len(body.Ldif) > maxImportBytes {
		return badRequest(c, fmt.Sprintf(
			"The LDIF is larger than the %d MB import limit.", maxImportBytes>>20), "")
	}

	records, err := ldif.Unmarshal([]byte(body.Ldif))
	if err != nil {
		return s.fail(c, err)
	}
	if len(records) == 0 {
		return badRequest(c, "The LDIF contains no records.", "")
	}

	ctx, cancel := reqCtx(c)
	defer cancel()
	sch, _ := sess.Conn.Schema(ctx)

	result := ImportResult{Changes: make([]ChangePreview, 0, len(records))}
	requests := make([]ChangeRequest, 0, len(records))
	for i, rec := range records {
		change, convErr := recordToChange(rec)
		if convErr != nil {
			return badRequest(c, fmt.Sprintf("Record %d (%s) cannot be applied.", i+1, rec.DN), convErr.Error())
		}
		if err := change.Validate(); err != nil {
			return badRequest(c, fmt.Sprintf("Record %d (%s) is not usable.", i+1, rec.DN), err.Error())
		}
		preview, prevErr := s.renderPreview(change, sch)
		if prevErr != nil {
			return s.fail(c, prevErr)
		}
		result.Changes = append(result.Changes, preview)
		requests = append(requests, changeRequest(change))
	}
	result.Requests = ptr(requests)
	return c.JSON(result)
}

// recordToChange maps a parsed LDIF record onto a ChangeRecord.
//
// A content record, which is what an export produces and what most hand-written
// LDIF contains, is treated as an add. That is what "import this LDIF" means to
// the person who wrote it.
func recordToChange(rec *ldif.Record) (directory.ChangeRecord, error) {
	out := directory.ChangeRecord{DN: rec.DN}
	switch rec.Change {
	case ldif.ChangeNone, ldif.ChangeAdd:
		out.Type = directory.ChangeAdd
		for _, a := range rec.Attrs {
			out.Attrs = append(out.Attrs, directory.Attribute{Name: a.Name, Values: a.Values})
		}
	case ldif.ChangeModify:
		out.Type = directory.ChangeModify
		for _, m := range rec.Mods {
			op, err := ldifModOp(m.Op)
			if err != nil {
				return out, err
			}
			out.Mods = append(out.Mods, directory.Mod{Op: op, Name: m.Name, Values: m.Values})
		}
	case ldif.ChangeDelete:
		out.Type = directory.ChangeDelete
	case ldif.ChangeModRDN:
		out.Type = directory.ChangeModRDN
		out.NewRDN = rec.NewRDN
		out.DeleteOldRDN = rec.DeleteOldRDN
		out.NewSuperior = rec.NewSuperior
	}
	return out, nil
}

func ldifModOp(op ldif.ModOp) (directory.ModOp, error) {
	switch op {
	case ldif.ModAdd:
		return directory.ModAdd, nil
	case ldif.ModDelete:
		return directory.ModDelete, nil
	case ldif.ModReplace:
		return directory.ModReplace, nil
	default:
		// RFC 4525 increment has no ChangeRecord representation, because there
		// is nothing for the preview to show the user that they could check.
		return "", fmt.Errorf("the %s modification is not supported", op)
	}
}

// --- small helpers ----------------------------------------------------------

func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

func clamp(v, fallback, lo, hi int) int {
	if v <= 0 {
		v = fallback
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
