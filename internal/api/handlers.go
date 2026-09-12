package api

import (
	"bufio"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/hazame-hub/alder/internal/allowlist"
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

	// Checked here: on the server, before anything is dialled. The host and the
	// port arrive in a request body, so a check the browser performs is not one
	// at all -- and after Connect would be too late, since the connection
	// attempt is the thing being restricted.
	//
	// A target that is not a usable host and port is a 400 whether or not a
	// list is configured, so switching the allowlist on changes which
	// destinations are reachable rather than which inputs parse.
	if err := s.cfg.AllowedTargets.Check(cfg.Host, cfg.Port); err != nil {
		if errors.Is(err, allowlist.ErrNotAllowed) {
			// The target and nothing else. The request body carrying it holds a
			// bind password, and no part of this line comes from that body
			// beyond the host and the port.
			s.logger.Info("refused by the target allowlist",
				"host", cfg.Host, "port", cfg.Port)
			return writeError(c, fiber.StatusForbidden, ErrorErrorTargetNotAllowed,
				"This Alder is not permitted to connect to that directory.",
				"The operator has restricted which directories this instance may reach. "+
					"Permitted: "+strings.Join(s.cfg.AllowedTargets.Endpoints(), ", "))
		}
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
	// Where the server counts children for us, say how many of them this
	// session did not get to see.
	if hidden, ok := hiddenChildCount(ctx, sess.Conn, parent, len(page.Nodes), res.Truncated); ok {
		page.HiddenChildren = ptr(hidden)
	}
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
	contexts := append([]string{}, caps.NamingContexts...)
	if caps.Config.Readable && caps.Config.DN != "" {
		contexts = append(contexts, caps.Config.DN)
	}
	// The schema entry, where the schema is that entry rather than a view
	// generated from configuration.
	//
	// A subschema subentry sits outside every naming context, so it appears
	// under none of the roots above. Where the schema lives in configuration
	// that costs nothing — the entries holding it are already reachable under
	// the configuration root — but where the subentry *is* the schema, it was
	// the one part of the directory the tree could not reach, and the only way
	// to it was the schema browser.
	//
	// It is added only when it is writable, which is the same condition as it
	// being the real thing. Offering a generated, read-only view as an editable
	// entry would be a trap: the edit looks available and the server refuses it.
	if caps.SchemaWrite.Style == directory.SchemaStyleSubschema {
		for _, target := range caps.SchemaWrite.Targets {
			if !reachableFrom(target.DN, contexts) {
				contexts = append(contexts, target.DN)
			}
		}
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

// reachableFrom reports whether a DN already sits at or beneath one of the
// roots the tree is about to show, so that nothing is offered twice.
func reachableFrom(target string, roots []string) bool {
	for _, root := range roots {
		if withinConfigTree(target, root) {
			return true
		}
	}
	return false
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
		Requirements:  ptr(requirementsView(req, sch)),
		Ldif:          ptr(directory.EntryLDIF(entry).String()),
	}
	if kinds := candidateKinds(entry, sch, req); len(kinds) > 0 {
		view.CandidateKinds = &kinds
	}
	if members := membershipAttributes(sch, req); len(members) > 0 {
		view.MembershipAttributes = &members
	}
	view.ReferencedByFilter = ptrIfSet(referencedByFilter(sch, entry.DN.String()))
	if browser, canBrowse := sess.Conn.(treeBrowser); canBrowse {
		if hasKids, kidErr := browser.HasChildren(ctx, target); kidErr == nil {
			view.HasChildren = ptr(hasKids)
		}
	}
	return c.JSON(view)
}

// CountEntries counts a subtree, up to a limit.
//
// It exists because "how many users are there" is the first question anybody
// asks of a directory and the last one a browser should answer casually: it is
// a search, and on a large suffix an expensive one. So it is an action somebody
// takes, one naming context at a time, and it says when it stopped rather than
// reporting a number that is merely the limit.
//
// No attributes are requested — 1.1 in RFC 4511 terms — so the cost is the
// search itself rather than shipping every entry back to be counted.
func (s *Server) CountEntries(c *fiber.Ctx, params CountEntriesParams) error {
	sess := s.require(c)
	if sess == nil {
		return nil
	}
	base, ok := parseDNParam(c, params.Dn)
	if !ok {
		return nil
	}
	limit := clamp(deref(params.Limit), 10000, 1, 100000)

	ctx, cancel := reqCtx(c)
	defer cancel()

	started := time.Now()
	count := 0
	var cookie []byte
	for {
		res, err := sess.Conn.Search(ctx, directory.SearchRequest{
			BaseDN:     base,
			Scope:      directory.ScopeSubtree,
			Filter:     filter.Present("objectClass"),
			Attributes: []string{"1.1"},
			Limit:      limit - count,
			PageSize:   directory.MaxPageSize,
			Cookie:     cookie,
		})
		if err != nil {
			return s.fail(c, err)
		}
		count += len(res.Entries)
		cookie = res.Cookie
		if count >= limit {
			return c.JSON(CountResult{
				Count: limit, Truncated: true,
				Took: ptr(time.Since(started).Round(time.Millisecond).String()),
			})
		}
		// A server that cannot page returns no cookie, and Truncated is then
		// the only thing that says the answer is short.
		if len(cookie) == 0 {
			return c.JSON(CountResult{
				Count: count, Truncated: res.Truncated,
				Took: ptr(time.Since(started).Round(time.Millisecond).String()),
			})
		}
	}
}

// --- search -----------------------------------------------------------------

// searchTail is everything in a search response except the entries: the fields
// that are only known once the search has finished.
//
// It exists because the response is streamed, so the entries are already on
// their way out by the time any of this is decided. The tags match
// SearchResponse exactly -- the document a client receives is the same
// document, only written in a different order, and JSON says the order of an
// object's members carries no meaning.
type searchTail struct {
	Truncated bool      `json:"truncated"`
	Took      *string   `json:"took,omitempty"`
	Cookie    *string   `json:"cookie,omitempty"`
	Referrals *[]string `json:"referrals,omitempty"`
	Command   *string   `json:"command,omitempty"`
}

// Search runs a bounded, paged search and streams the result as it arrives.
//
// Streamed for the reason the LDIF export is: building the response first meant
// holding every entry the directory returned, every wire type derived from it
// and the marshalled bytes of the whole document, all at the same instant. At
// the documented maximum of 10,000 entries that was 120 MB of live heap for a
// 15 MB answer. A page at a time it is 24 MB, and the document a client reads
// is the same document -- the entries are written first, and the fields that
// are only known at the end are written at the end, which JSON does not mind.
//
// The paging loop moved up here from the driver for the same reason. The driver
// will happily fill a limit of ten thousand in one call, but it can only do
// that by accumulating ten thousand entries, which is the thing being avoided.
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

	// Deliberately not deferred. The body stream writer below runs after this
	// function has returned, so a deferred cancel would cut the search off at
	// its first page -- the trap the LDIF export fell into and the reason the
	// unit-test fake honours the context it is handed. Every path out of here
	// cancels exactly once instead.
	ctx, cancel := reqCtx(c)

	// Asked for one page at a time. The driver loops pages internally to fill
	// whatever Limit it is given, so asking it for the whole limit at once
	// would put every entry back in memory a layer below.
	//
	// want is what is still owed, not the page size: a search for seven entries
	// must ask the directory for seven, or a server without the paging control
	// gets a size limit of a hundred and sends back ninety-three nobody wanted.
	page := func(cookie []byte, want int) (*directory.SearchResult, error) {
		one := req
		one.Limit = min(want, req.PageSize)
		one.Cookie = cookie
		return sess.Conn.Search(ctx, one)
	}

	started := time.Now()

	// Both of these happen before a byte is sent, because once the first byte
	// is out the status code is settled. A directory that refuses the search,
	// or a schema that cannot be read, is still a proper HTTP answer.
	first, err := page(req.Cookie, req.Limit)
	if err != nil {
		cancel()
		return s.fail(c, err)
	}
	sch, err := sess.Conn.Schema(ctx)
	if err != nil {
		cancel()
		return s.fail(c, err)
	}

	// Built from req rather than from body: the filter in it is the parsed one,
	// which is what the directory was actually asked.
	command := ptrIfSet(searchCommand(commandTarget{
		Host:       sess.Host(),
		Port:       sess.Port(),
		TLS:        sess.TLS(),
		BindDN:     sess.BindDN(),
		SkipVerify: sess.SkipsVerification(),
		CustomCA:   sess.HasCustomCA(),
	}, req))

	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	c.Context().SetBodyStreamWriter(func(conn *bufio.Writer) {
		// The searches that feed this run here, so the context lives until the
		// last entry is written.
		defer cancel()

		// A wider buffer over the 4 KB one the server hands out. Every time it
		// fills, the bytes cross a pipe to the connection goroutine and come
		// back as a chunk of their own, and a fifteen-megabyte answer crosses
		// it nearly four thousand times. Measured at ten thousand entries:
		// 387 ms through the 4 KB buffer, 328 ms through this one.
		bw := bufio.NewWriterSize(conn, 64<<10)
		defer func() { _ = bw.Flush() }()

		enc := json.NewEncoder(bw)
		written := 0
		var referrals []string

		if _, err := bw.WriteString(`{"entries":[`); err != nil {
			return
		}
		emit := func(entries []*directory.Entry) error {
			for _, e := range entries {
				if written > 0 {
					if _, err := bw.WriteString(","); err != nil {
						return err
					}
				}
				// Encode writes a trailing newline, which is insignificant
				// whitespace between two array elements. It is used rather than
				// Marshal because it reuses one buffer across ten thousand
				// entries instead of allocating a document-sized one per entry.
				if err := enc.Encode(SearchResultEntry{
					Dn:         e.DN.String(),
					Rdn:        ptr(rdnLabel(e.DN)),
					Attributes: ptr(entryAttributes(e, sch, sch.Requirements(e.ObjectClasses()))),
				}); err != nil {
					return err
				}
				written++
			}
			return nil
		}

		res := first
		cookie, truncated := first.Cookie, first.Truncated
		for {
			// Collected page by page. Only the last page's would be reported by
			// a handler that assigned here, and most of them would be lost.
			referrals = append(referrals, res.Referrals...)
			if err := emit(res.Entries); err != nil {
				s.abandon(c, bw, written, err)
				return
			}
			if len(cookie) == 0 || written >= req.Limit {
				break
			}
			next, pErr := page(cookie, req.Limit-written)
			if pErr != nil {
				s.abandon(c, bw, written, pErr)
				return
			}
			res, cookie, truncated = next, next.Cookie, next.Truncated
			if len(next.Entries) == 0 {
				// A cookie answered with no entries: take what it said and
				// stop. The driver keeps asking in this case, and a server that
				// kept answering the same way would spin there forever.
				referrals = append(referrals, next.Referrals...)
				break
			}
		}

		// A cookie that outlives the limit is the only honest way to say the
		// answer is short, and the client sends it back to continue.
		tail := searchTail{
			Truncated: truncated || len(cookie) > 0,
			Took:      ptr(time.Since(started).Round(time.Millisecond).String()),
			Command:   command,
		}
		if len(cookie) > 0 {
			tail.Cookie = ptr(string(cookie))
		}
		if len(referrals) > 0 {
			tail.Referrals = ptr(referrals)
		}
		rest, err := json.Marshal(tail)
		if err != nil {
			s.abandon(c, bw, written, err)
			return
		}
		if _, err := bw.WriteString("],"); err != nil {
			return
		}
		// rest is an object and searchTail always has truncated in it, so
		// dropping the opening brace splices its members into the one already
		// open without leaving a stray comma.
		_, _ = bw.Write(rest[1:])
	})
	return nil
}

// abandon ends a search response that failed after it had begun.
//
// It leaves the JSON document unterminated on purpose. Once the first byte is
// out the status code is settled, and the response has no field for "this went
// wrong" -- so the alternatives were to close the object and hand back a short
// answer no client could tell from a complete one, or to make the document fail
// to parse. A parse error is the loud one, and the comment says why to whoever
// reads the body to find out.
func (s *Server) abandon(c *fiber.Ctx, bw *bufio.Writer, written int, err error) {
	s.logger.Error("the search failed partway through streaming its response",
		"written", written, "error", err, "path", c.Path())

	// Folded onto one line and stripped of anything that would end the comment,
	// so the trailer cannot be closed early by whatever the directory said.
	reason := strings.NewReplacer("*/", "* /", "\n", " ", "\r", " ").Replace(err.Error())
	_, _ = fmt.Fprintf(bw, "\n/* Alder: this search failed after %d entries: %s.\n"+
		"   The response is left unterminated on purpose: a short answer that\n"+
		"   parsed would be indistinguishable from a complete one. */\n", written, reason)
}

// --- schema -----------------------------------------------------------------

// GetSchema returns the whole schema, indexed for browsing.
// ListObjectViews answers what "users" and "groups" mean on this directory.
//
// The schema is already read and cached by the session, so this costs nothing
// beyond the derivation, and it is a read of published schema rather than of
// any entry: a session that cannot see a single user still gets the views, and
// finds out what it can read by running one.
func (s *Server) ListObjectViews(c *fiber.Ctx) error {
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
	return c.JSON(ObjectViewList{Views: objectViews(sch)})
}

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
	// Where each definition came from, which the connection worked out once at
	// connect time.
	write := sess.Conn.Capabilities().SchemaWrite

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
		classes = append(classes, objectClassSummary(oc, write))
	}
	sort.Slice(classes, func(i, j int) bool {
		return strings.ToLower(classes[i].Name) < strings.ToLower(classes[j].Name)
	})
	view.ObjectClasses = ptr(classes)

	attrs := make([]AttributeTypeSummary, 0, len(sch.AttributeTypes))
	for _, at := range sch.AttributeTypes {
		attrs = append(attrs, attributeTypeSummary(sch, at, write))
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

func objectClassSummary(oc *schema.ObjectClass, origins directory.SchemaWrite) ObjectClassSummary {
	return ObjectClassSummary{
		Name:      oc.Name(),
		Names:     ptr(oc.Names),
		Oid:       oc.OID,
		Desc:      ptr(oc.Desc),
		Kind:      ObjectClassSummaryKind(oc.Kind.String()),
		Obsolete:  ptr(oc.Obsolete),
		Superiors: ptr(oc.SuperNames),
		Origin:    ptrIfSet(provenance(oc.OID, oc.Extensions, origins)),
	}
}

func attributeTypeSummary(sch *schema.Schema, at *schema.AttributeType, origins directory.SchemaWrite) AttributeTypeSummary {
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
		Origin:      ptrIfSet(provenance(at.OID, at.Extensions, origins)),
	}
}

// provenance says where a definition came from, in the server's own words.
//
// Two servers answer this in two different ways and neither is named here. One
// keeps its schema in configuration entries and discards X-ORIGIN when it loads
// a schema file, so the collection holding the definition is the only thing
// left that distinguishes them — and it is the more useful answer anyway, since
// "which file is this in" is the question an administrator actually asks. The
// other keeps X-ORIGIN and has a single schema entry, where the collection
// would say nothing.
//
// So: the collection where there is one, the extension otherwise, and nothing
// at all where the server published neither. What this deliberately does not do
// is decide whether a definition was "shipped" or "added" — on the first server
// that is not knowable, and a column that guesses on one of two supported
// servers is worse than a column that is absent.
func provenance(oid string, ext schema.Extensions, origins directory.SchemaWrite) string {
	if collection, ok := origins.Origin[oid]; ok && collection != "" {
		return collection
	}
	if values, ok := ext["X-ORIGIN"]; ok && len(values) > 0 {
		return strings.Join(values, ", ")
	}
	return ""
}

// GetObjectClass returns one object class with its cross-links resolved.
// GetRequirements answers what an entry of the named classes would look like.
//
// The creation form is built from this, so it offers the same controls the
// editor does — a Boolean gets a Boolean, a DN gets the entry picker, and every
// field carries the schema's own description. Building it from attribute names
// alone is what left creation with a text box for everything.
func (s *Server) GetRequirements(c *fiber.Ctx, params GetRequirementsParams) error {
	sess := s.require(c)
	if sess == nil {
		return nil
	}
	if len(params.Class) == 0 {
		return badRequest(c, "Name at least one object class.",
			"The class parameter is required, and may be repeated.")
	}
	ctx, cancel := reqCtx(c)
	defer cancel()

	sch, err := sess.Conn.Schema(ctx)
	if err != nil {
		return s.fail(c, err)
	}

	req := sch.Requirements(params.Class)
	names := append(append([]string{}, req.Must...), req.May...)
	kinds := make([]AttributeKind, 0, len(names))
	for _, name := range names {
		kinds = append(kinds, attributeKind(sch.KindOf(name)))
	}
	return c.JSON(RequirementsView{
		Requirements: requirementsView(req, sch),
		Kinds:        kinds,
	})
}

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
		Summary:       objectClassSummary(oc, sess.Conn.Capabilities().SchemaWrite),
		Must:          ptr(canonicalAll(sch, oc.Must)),
		May:           ptr(canonicalAll(sch, oc.May)),
		InheritedMust: ptr(sortedKeys(inheritedMust)),
		InheritedMay:  ptr(sortedKeys(inheritedMay)),
		SuperiorChain: ptr(chain),
		Subclasses:    ptr(subNames),
		Raw:           ptr(oc.Raw),
		Edit:          ptr(objectClassEditForm(oc)),
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
		Summary:       attributeTypeSummary(sch, at, sess.Conn.Capabilities().SchemaWrite),
		Kind:          ptr(attributeKind(sch.KindOf(at.Name()))),
		SuperiorChain: ptr(chain),
		RequiredBy:    ptr(classNames(must)),
		OptionalIn:    ptr(classNames(may)),
		Raw:           ptr(at.Raw),
		Edit:          ptr(attributeTypeEditForm(at)),
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

	preview, err := s.renderPreview(record, sch, sess.Conn.Capabilities())
	if err != nil {
		return s.fail(c, err)
	}
	return c.JSON(preview)
}

// renderPreview builds the LDIF, the Ansible task and the warnings for one
// change record.
//
// It takes the capabilities as well as the schema because two different things
// are worth warning about: what the schema says the entry may hold, and whether
// the entry is part of the server's own configuration.
func (s *Server) renderPreview(record directory.ChangeRecord, sch *schema.Schema, caps directory.Capabilities) (ChangePreview, error) {
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
	// Configuration warnings come first: "this is the server's own
	// configuration" is the more urgent thing to read, and a warning list is
	// read from the top.
	warnings := append(configWarnings(record, caps), schemaWarnings(record, sch)...)
	if len(warnings) > 0 {
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

	caps := sess.Conn.Capabilities()
	if err := sess.Conn.Apply(ctx, record); err != nil {
		return s.failChange(c, err, record, caps)
	}
	target, err := record.Target()
	if err != nil {
		return s.fail(c, err)
	}
	result := ApplyResult{
		Applied: true,
		Dn:      target.String(),
		Summary: ptr(record.Summary()),
		Ldif:    ptr(record.LDIF()),
	}
	// An added entry is not always stored under the name it was given, so where
	// it actually went is looked up rather than assumed.
	if stored, moved, note := storedLocation(ctx, sess, record); moved {
		result.StoredDn = ptr(stored)
	} else if note != "" {
		result.Note = ptr(note)
	}
	return c.JSON(result)
}

// --- transfer ---------------------------------------------------------------

// ExportLdif exports an entry or a subtree.
// maxExportEntries bounds a streamed export.
//
// It is a bound on how long an export runs, not on how much it holds: entries
// are rendered a page at a time and never accumulate. The old ceiling was
// directory.MaxResults, which exists because a *search* result is a JSON array
// held in memory -- a reason that stopped applying the moment the export stopped
// building its document before sending it.
const maxExportEntries = 250000

// ExportLdif streams an entry or a subtree as LDIF.
//
// Streamed rather than assembled because "the output is code" is a promise
// about a whole directory, and the interesting directories are the large ones.
// Building the document first meant holding every entry, every record and the
// rendered bytes at once, which is why the export could not go past ten
// thousand entries -- on a hundred-thousand-entry directory, the one Alder most
// needs to be able to hand you, it simply refused.
//
// Entries go out in the order the server returned them, which is what the
// non-streaming version did too. Both target servers return a subtree parent
// first, and the conformance suite pins that, because an export whose children
// precede their parents cannot be applied back.
//
// What streaming costs is the header: the entry count and whether the search
// truncated are only known once it has finished, so they are written at the end
// instead. That is worth having rather than merely tolerable -- a file that
// ends with its own summary proves it arrived whole, where a count at the top
// of a download that died halfway is a lie the reader cannot detect.
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

	// Parsed into a tree, never pasted into one: this value comes from a URL.
	exportFilter := filter.Present("objectClass")
	if raw := strings.TrimSpace(deref(params.Filter)); raw != "" {
		parsed, parseErr := filter.Parse(raw)
		if parseErr != nil {
			return badRequest(c, "The export filter is not a valid RFC 4515 filter.", parseErr.Error())
		}
		exportFilter = parsed
	}

	attrs := []string{"*"}
	if params.IncludeOperational != nil && *params.IncludeOperational {
		attrs = append(attrs, "+")
	}
	withSecrets := params.IncludeSensitive != nil && *params.IncludeSensitive
	limit := clamp(deref(params.Limit), 1000, 1, maxExportEntries)

	// Deliberately not deferred. The body stream writer below runs after this
	// function has returned, so a deferred cancel would cut the export off at
	// its first page -- which is exactly what it did, and the file said so:
	// "this export stopped after 1000 entries: context canceled". Every path
	// out of here cancels exactly once instead.
	ctx, cancel := reqCtx(c)

	page := func(cookie []byte, want int) (*directory.SearchResult, error) {
		if want > directory.MaxPageSize {
			want = directory.MaxPageSize
		}
		return sess.Conn.Search(ctx, directory.SearchRequest{
			BaseDN:     base,
			Scope:      scope,
			Filter:     exportFilter,
			Attributes: attrs,
			Limit:      want,
			PageSize:   want,
			Cookie:     cookie,
		})
	}

	// The first page is fetched before anything is sent, so that "nothing
	// matched" is still an HTTP status rather than a comment inside a file. Once
	// the first byte is out the status is fixed, and every later failure has to
	// be reported in the document itself.
	first, err := page(nil, limit)
	if err != nil {
		cancel()
		return s.fail(c, err)
	}
	if len(first.Entries) == 0 {
		cancel()
		// Without a filter this means the base is not there. With one it means
		// the base holds nothing matching, which is a different thing to be
		// told -- and not a 404, because the entry the caller named does exist.
		if strings.TrimSpace(deref(params.Filter)) == "" {
			return writeError(c, fiber.StatusNotFound, ErrorErrorNotFound, "No such entry.", "")
		}
		return badRequest(c, "Nothing matched, so there is nothing to export.",
			"The filter is valid and the base exists; no entry under it satisfies the filter.")
	}

	c.Set(fiber.HeaderContentType, "text/plain; charset=utf-8")
	c.Set(fiber.HeaderContentDisposition,
		fmt.Sprintf("attachment; filename=%q", exportFilename(base, scope)))

	c.Context().SetBodyStreamWriter(func(bw *bufio.Writer) {
		// The search that feeds this runs here, so the context lives until the
		// last record is written.
		defer cancel()

		// Everything goes through the LDIF writer, including the comments: it
		// folds them at the same column as the records and it remembers the
		// first write error, so a client that hangs up halfway stops the export
		// rather than being written at for another ninety thousand entries.
		w := ldif.NewWriter(bw)
		written := 0

		blank := func() bool {
			if w.Err() != nil {
				return false
			}
			_, err := bw.WriteString("\n")
			return err == nil
		}

		w.WriteComment("Exported by Alder")
		w.WriteComment("base:  " + base.String())
		w.WriteComment("scope: " + scope.String())
		if rendered, rErr := exportFilter.Render(); rErr == nil {
			w.WriteComment("filter: " + rendered)
		}
		if !withSecrets {
			w.WriteComment("Sensitive attributes such as userPassword were omitted.")
		}
		w.WriteComment("The entry count and any truncation warning are at the end of this")
		w.WriteComment("file: they are not known until the export finishes, and a file that")
		w.WriteComment("ends with them is one you can tell arrived whole.")
		if !blank() {
			return
		}
		w.WriteVersion()

		// stop writes why the file is not to be trusted, into the file. Once
		// the first byte is out the status code is settled, so this is the only
		// place left to say it.
		stop := func(reason string) {
			_ = blank()
			w.WriteComment("ERROR: " + reason)
			w.WriteComment("This export is incomplete. Do not restore from it.")
		}

		emit := func(entries []*directory.Entry) bool {
			for _, e := range entries {
				rec := directory.EntryLDIF(e)
				if withSecrets {
					rec = directory.EntryLDIFWithSecrets(e)
				}
				if wErr := w.WriteRecord(rec); wErr != nil {
					stop(fmt.Sprintf("this export stopped while writing %s: %v", e.DN, wErr))
					return false
				}
				written++
			}
			return true
		}

		res, cookie := first, first.Cookie
		for {
			if !emit(res.Entries) {
				return
			}
			if len(cookie) == 0 || written >= limit {
				break
			}
			next, pErr := page(cookie, limit-written)
			if pErr != nil {
				stop(fmt.Sprintf("this export stopped after %d entries: %v", written, pErr))
				return
			}
			if len(next.Entries) == 0 {
				break
			}
			res, cookie = next, next.Cookie
		}

		if !blank() {
			return
		}
		w.WriteComment(fmt.Sprintf("%d entries", written))
		if len(cookie) > 0 && written >= limit {
			// A truncated export that does not say so is a file someone will
			// restore from and discover the gap much later.
			w.WriteComment(fmt.Sprintf("WARNING: the result was truncated at the export limit of %d.", limit))
			w.WriteComment("This file does not contain the whole subtree.")
		} else {
			w.WriteComment("This export is complete.")
		}
	})
	return nil
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

	wantReconcile := body.Reconcile != nil && *body.Reconcile

	result := ImportResult{Changes: make([]ChangePreview, 0, len(records))}
	requests := make([]ChangeRequest, 0, len(records))
	var unchanged, skippedAttrs []string
	reconciled := 0

	for i, rec := range records {
		change, convErr := recordToChange(rec)
		if convErr != nil {
			return badRequest(c, fmt.Sprintf("Record %d (%s) cannot be applied.", i+1, rec.DN), convErr.Error())
		}
		if err := change.Validate(); err != nil {
			return badRequest(c, fmt.Sprintf("Record %d (%s) is not usable.", i+1, rec.DN), err.Error())
		}

		// Only a content record can be reconciled. A changetype record already
		// says what it wants done, and second-guessing it would be inventing an
		// intent the document does not carry.
		if wantReconcile && change.Type == directory.ChangeAdd {
			live, readErr := sess.Conn.Read(ctx, change.DN, []string{"*", "+"})
			switch {
			case readErr != nil && !isNoSuchObject(readErr):
				// An entry that is absent is the ordinary case — the record
				// stays an add. Anything else is a real failure and saying so
				// beats silently importing half a document.
				return s.fail(c, readErr)
			case readErr == nil && live != nil:
				outcome := reconcile(change, live, sch)
				skippedAttrs = appendNew(skippedAttrs, outcome.Skipped)
				if !outcome.Changed {
					// Nothing to confirm, so nothing is offered to confirm.
					unchanged = append(unchanged, change.DN.String())
					continue
				}
				change = outcome.Change
				reconciled++
			}
		}

		preview, prevErr := s.renderPreview(change, sch, sess.Conn.Capabilities())
		if prevErr != nil {
			return s.fail(c, prevErr)
		}
		result.Changes = append(result.Changes, preview)
		requests = append(requests, changeRequest(change))
	}

	result.Requests = ptr(requests)
	if wantReconcile {
		result.Reconciled = ptr(reconciled)
		if len(unchanged) > 0 {
			result.Unchanged = ptr(unchanged)
		}
		if len(skippedAttrs) > 0 {
			result.SkippedAttributes = ptr(skippedAttrs)
		}
	}
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

// hiddenChildCount reports how many children a parent has that this session
// could not see.
//
// Some servers publish numSubordinates and some do not. That is a capability,
// read from the entry, and never inferred from which server is answering --
// 389 DS publishes it and OpenLDAP does not, but the code has no business
// knowing that. Where it is published the server computes it and the access
// rules do not filter it, so the gap between it and the children that came back
// is exactly what this bind may not see.
//
// This is the only place in Alder where a hidden entry is directly countable.
// Everywhere else a directory the session may not fully read simply looks like
// a smaller directory.
func hiddenChildCount(
	ctx context.Context,
	sess directory.Session,
	parent dn.DN,
	shown int,
	truncated bool,
) (int, bool) {
	if truncated {
		// The listing stopped at its limit, so the difference is paging and
		// says nothing about access.
		return 0, false
	}
	e, err := sess.Read(ctx, parent, []string{"numSubordinates"})
	if err != nil {
		return 0, false
	}
	raw := e.GetOne("numSubordinates")
	if raw == "" {
		return 0, false
	}
	total, convErr := strconv.Atoi(raw)
	if convErr != nil || total <= shown {
		return 0, false
	}
	return total - shown, true
}
