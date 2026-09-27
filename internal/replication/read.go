package replication

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/filter"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// Finding replication.
//
// One search of the configuration tree, for the objects either server uses to
// describe a link, and then one read per suffix for how far along this server
// is. Nothing branches on a vendor name: the search asks for the shapes, and
// whichever shape comes back is what this server uses.
//
//	OpenLDAP    olcSyncrepl on a database entry   -- an incoming link
//	            an olcSyncProvConfig overlay      -- it can serve outgoing ones
//	389 DS      an nsds5Replica entry             -- its role for a suffix
//	            an nsds5ReplicationAgreement      -- an outgoing link
//
// The attributes asked for are the union of both servers'. A server returns
// the ones it has, which is the same way every other capability question in
// Alder is settled.

// Reader is what a replication report needs: a paged search and a read.
// Nothing here writes.
type Reader interface {
	Capabilities() directory.Capabilities
	Read(ctx context.Context, target dn.DN, attrs []string) (*directory.Entry, error)
	Search(ctx context.Context, req directory.SearchRequest) (*directory.SearchResult, error)
}

// ErrUnreadable is returned when the configuration tree cannot be read at all,
// which is a fact about the bind rather than about replication.
var ErrUnreadable = errors.New("replication: this session cannot read the server's configuration tree")

// The attributes a link is described by, on either server. Asked for by name
// rather than with "*" so that a credential is never even fetched: the value
// of olcSyncrepl carries one, so that attribute is read and the credential
// inside it is stripped before the value goes anywhere.
var linkAttributes = []string{
	// OpenLDAP
	"olcSyncrepl", "olcSuffix", "olcUpdateRef", "olcMirrorMode", "olcMultiProvider",
	"olcOverlay", "olcDatabase",
	// 389 Directory Server
	"nsDS5ReplicaRoot", "nsDS5ReplicaId", "nsDS5ReplicaType", "nsDS5Flags",
	"nsDS5ReplicaHost", "nsDS5ReplicaPort", "nsDS5ReplicaBindDN", "nsDS5ReplicaBindMethod",
	"nsDS5ReplicaTransportInfo", "nsDS5ReplicatedAttributeList",
	"nsds5replicaLastUpdateStart", "nsds5replicaLastUpdateEnd", "nsds5replicaLastUpdateStatus",
	"nsds5replicaLastUpdateStatusJSON", "nsds5replicaUpdateInProgress",
	"nsds5replicaLastInitStart", "nsds5replicaLastInitEnd", "nsds5replicaLastInitStatus",
	"nsds5replicaChangeCount", "nsds50ruv",
	"objectClass", "cn", "description",
}

// Options steer a report.
type Options struct {
	// PageSize is how many entries one search page asks for.
	PageSize int
}

// For reports what this server says about its own replication.
func For(ctx context.Context, r Reader, opts Options) (*Report, error) {
	caps := r.Capabilities()
	report := &Report{Disclaimer: Disclaimer, Role: RoleNone}

	root := caps.Config.DN
	if root == "" {
		root = caps.ConfigContext
	}
	if root == "" {
		return nil, ErrUnreadable
	}
	base, err := dn.Parse(root)
	if err != nil {
		return nil, ErrUnreadable
	}

	entries, truncated, err := search(ctx, r, base, opts)
	if err != nil {
		return nil, errors.Join(ErrUnreadable, err)
	}
	if truncated {
		report.Unread = append(report.Unread, Unread{Where: base.String(),
			Reason: "the configuration tree holds more replication entries than one read returns"})
	}
	if !caps.Config.Readable {
		report.Unread = append(report.Unread, Unread{Where: base.String(), Reason: caps.Config.Reason})
	}

	report.Provider = providerOf(entries, caps)

	// A suffix is built up as its pieces are found, in whatever order the
	// server returned them.
	byDN := map[string]*directory.Entry{}
	for _, entry := range entries {
		byDN[strings.ToLower(entry.DN.String())] = entry
	}
	suffixes := map[string]*Suffix{}
	at := func(name string) *Suffix {
		key := strings.ToLower(name)
		if s, ok := suffixes[key]; ok {
			return s
		}
		s := &Suffix{DN: name, Role: RoleNone}
		suffixes[key] = s
		return s
	}

	supplies := map[string]bool{}
	consumes := map[string]bool{}

	for _, entry := range entries {
		switch {
		case len(entry.GetStrings("olcSyncrepl")) > 0:
			name := firstValue(entry, "olcSuffix")
			if name == "" {
				// OpenLDAP's own configuration database has no olcSuffix, and
				// it is the one most often replicated in a multi-provider
				// pair. Dropping the link made the report say the server
				// replicates nothing while it was receiving configuration
				// changes from a peer, so it is named after the tree it
				// actually holds.
				name = configRootName(caps, entry.DN)
			}
			s := at(name)
			for _, value := range entry.GetStrings("olcSyncrepl") {
				s.Links = append(s.Links, openldapLink(value, entry.DN.String()))
				consumes[strings.ToLower(name)] = true
			}
			if ref := firstValue(entry, "olcUpdateRef"); ref != "" {
				s.Notes = append(s.Notes,
					"A write sent to this server is referred to "+ref+" rather than applied here.")
			}
			if isTrue(firstValue(entry, "olcMirrorMode")) || isTrue(firstValue(entry, "olcMultiProvider")) {
				s.Notes = append(s.Notes,
					"This server accepts writes of its own as well as receiving them, so two servers may change the same entry.")
				supplies[strings.ToLower(name)] = true
			}

		case hasClass(entry, "olcSyncProvConfig"):
			// The provider overlay. Its own entry says nothing about which
			// suffix it serves; the database above it does.
			parent := byDN[strings.ToLower(entry.DN.Parent().String())]
			name := ""
			if parent != nil {
				name = firstValue(parent, "olcSuffix")
			}
			if name == "" {
				if parent, err := readSuffixOwner(ctx, r, entry.DN.Parent()); err == nil {
					name = parent
				}
			}
			if name == "" {
				// The database above it either has no suffix -- the
				// configuration database does not -- or could not be read.
				// The first is ordinary and has a name; only the second is
				// worth reporting as unread.
				if parent != nil {
					name = configRootName(caps, entry.DN.Parent())
				} else {
					report.Unread = append(report.Unread, Unread{Where: entry.DN.String(),
						Reason: "this server serves changes from here and the database above it could not be read"})
					continue
				}
			}
			s := at(name)
			supplies[strings.ToLower(name)] = true
			s.Notes = append(s.Notes,
				"This server serves changes to any consumer that asks. OpenLDAP does not record who has asked, "+
					"so the consumers are not listed here; each one knows its own provider.")

		case hasClass(entry, "nsds5Replica"):
			name := firstValue(entry, "nsDS5ReplicaRoot")
			if name == "" {
				continue
			}
			s := at(name)
			s.ServerID = firstValue(entry, "nsDS5ReplicaId")
			s.Cursors = append(s.Cursors, ruvCursors(entry)...)
			// nsDS5Flags bit 0 means this server writes a changelog, which is
			// what a server that sends changes has to do. The type says what
			// it was set up as; the flag says what it can actually do, so the
			// flag decides and the type only adds a word.
			flags, _ := strconv.Atoi(firstValue(entry, "nsDS5Flags"))
			if flags&1 == 1 {
				supplies[strings.ToLower(name)] = true
			}
			// A replica that holds changes originating anywhere but here has
			// received them from somebody, whatever its configured type says.
			// This is the server's own record rather than an inference from
			// how it was set up, and it is what makes a multi-supplier pair
			// report itself as both.
			for _, cursor := range s.Cursors {
				if cursor.Origin != "" && cursor.Origin != s.ServerID {
					consumes[strings.ToLower(name)] = true
					break
				}
			}
			switch firstValue(entry, "nsDS5ReplicaType") {
			case "2":
				consumes[strings.ToLower(name)] = true
				if flags&1 == 0 {
					s.Notes = append(s.Notes,
						"This server was set up to receive changes only. Writes sent here are referred to a supplier.")
				}
				// 389 DS gives every server that originates no changes the
				// same reserved id. Reported because it is what the server
				// holds, and explained because otherwise it reads as a
				// suspiciously round number somebody typed.
				if s.ServerID == "65535" {
					s.Notes = append(s.Notes,
						"The replica id 65535 is the one 389 Directory Server gives a server that originates "+
							"no changes of its own, rather than an identity anybody chose.")
				}
			case "3":
				supplies[strings.ToLower(name)] = true
			}

		case hasClass(entry, "nsds5ReplicationAgreement"):
			name := firstValue(entry, "nsDS5ReplicaRoot")
			if name == "" {
				continue
			}
			s := at(name)
			s.Links = append(s.Links, ds389Link(entry))
			supplies[strings.ToLower(name)] = true
		}
	}

	// How far along this server is, for each suffix. 389 DS wrote it into the
	// replica entry above; OpenLDAP writes it on the suffix itself, so that
	// one has to be read.
	for _, s := range suffixes {
		if len(s.Cursors) > 0 {
			continue
		}
		target, err := dn.Parse(s.DN)
		if err != nil {
			continue
		}
		entry, err := r.Read(ctx, target, []string{"contextCSN"})
		if err != nil {
			report.Unread = append(report.Unread, Unread{Where: s.DN,
				Reason: "how far along this server is could not be read"})
			continue
		}
		for _, value := range entry.GetStrings("contextCSN") {
			if cursor, ok := parseOpenLDAPCSN(value); ok {
				s.Cursors = append(s.Cursors, cursor)
			}
		}
	}

	for key, s := range suffixes {
		s.Role = roleOf(supplies[key], consumes[key])
		sort.Slice(s.Cursors, func(i, j int) bool { return s.Cursors[i].Origin < s.Cursors[j].Origin })
		sort.SliceStable(s.Links, func(i, j int) bool { return s.Links[i].Name < s.Links[j].Name })
		report.Suffixes = append(report.Suffixes, *s)
	}
	sort.Slice(report.Suffixes, func(i, j int) bool { return report.Suffixes[i].DN < report.Suffixes[j].DN })

	// OpenLDAP keeps the server's own identity on the global entry rather
	// than per suffix, so it is filled in afterwards for the suffixes that
	// have none of their own.
	if id := serverID(ctx, r, base); id != "" {
		for i := range report.Suffixes {
			if report.Suffixes[i].ServerID == "" {
				report.Suffixes[i].ServerID = id
			}
		}
	}

	report.Role = overallRole(report.Suffixes)
	report.Why = whyRole(report.Role, report.Suffixes)
	return report, nil
}

// search reads the replication objects out of the configuration tree.
func search(ctx context.Context, r Reader, base dn.DN, opts Options) ([]*directory.Entry, bool, error) {
	page := opts.PageSize
	if page <= 0 || page > directory.MaxPageSize {
		page = directory.MaxPageSize
	}
	// The shapes, not the names. A server that has none of them answers with
	// nothing, which is the honest answer for a server that replicates
	// nothing.
	shapes := filter.Or(
		filter.Present("olcSyncrepl"),
		filter.Equal("objectClass", "olcSyncProvConfig"),
		filter.Equal("objectClass", "nsds5Replica"),
		filter.Equal("objectClass", "nsds5ReplicationAgreement"),
	)
	var entries []*directory.Entry
	var cookie []byte
	for {
		res, err := r.Search(ctx, directory.SearchRequest{
			BaseDN: base, Scope: directory.ScopeSubtree, Filter: shapes,
			Attributes: linkAttributes, Limit: page, PageSize: page, Cookie: cookie,
		})
		if err != nil {
			if len(entries) == 0 {
				return nil, false, err
			}
			return entries, true, nil
		}
		entries = append(entries, res.Entries...)
		if len(entries) >= maxEntries {
			return entries[:maxEntries], true, nil
		}
		if len(res.Cookie) == 0 {
			return entries, res.Truncated, nil
		}
		cookie = res.Cookie
	}
}

// maxEntries bounds the report the way every other read in Alder is bounded.
// A topology with more links than this is not one a page can show.
const maxEntries = 500

// providerOf names the configuration model this was read through, for display.
// It reuses the configuration package's rule rather than inventing a second
// one, so the two cannot disagree about what a server is.
func providerOf(entries []*directory.Entry, caps directory.Capabilities) string {
	for _, entry := range entries {
		for name := range entry.Attributes {
			switch {
			case strings.HasPrefix(strings.ToLower(name), "olc"):
				return snapshot.ProviderOpenLDAP
			case strings.HasPrefix(strings.ToLower(name), "nsds5"):
				return snapshot.Provider389DS
			}
		}
	}
	vendor := strings.ToLower(caps.VendorName)
	switch {
	case strings.Contains(vendor, "389"), strings.Contains(vendor, "red hat"), strings.Contains(vendor, "netscape"):
		return snapshot.Provider389DS
	case strings.Contains(vendor, "openldap"):
		return snapshot.ProviderOpenLDAP
	}
	return ""
}

// openldapLink reads one olcSyncrepl value.
//
// The value is a list of key=value pairs, and one of them is the password
// this server binds to its provider with. It is dropped here, before the
// value reaches anything that could render or log it: nothing downstream has
// to remember that this string is different from every other configuration
// value.
func openldapLink(value, where string) Link {
	link := Link{Direction: DirectionIncoming, State: StateUnknown, DN: where}
	// slapd writes an ordered attribute's values with the position in front:
	// "{0}rid=001 provider=...". The position is not part of the link and
	// changes when a different one is removed; leaving it on made the first
	// field parse as a key called "{0}rid" and lost the rid entirely.
	value = stripPosition(value)
	for _, field := range splitSyncrepl(value) {
		key, v, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"`)
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "rid":
			link.Name = "rid=" + v
		case "provider":
			link.Peer = v
		case "binddn":
			link.BindDN = v
		case "starttls":
			if isTrue(v) || strings.EqualFold(v, "critical") {
				link.Transport = "StartTLS"
			}
		case "type":
			link.Notes = append(link.Notes, "mode: "+v)
		case "retry":
			link.Notes = append(link.Notes, "retry: "+v)
		}
	}
	if link.Transport == "" && strings.HasPrefix(strings.ToLower(link.Peer), "ldaps://") {
		link.Transport = "LDAPS"
	}
	if link.Transport == "" {
		link.Transport = "LDAP"
	}
	if link.Name == "" {
		link.Name = "syncrepl"
	}
	// OpenLDAP records no outcome for a syncrepl link anywhere a client can
	// read: whether it is working is in the server's log and in how far along
	// the suffix is, which is above. Saying so is better than an empty column
	// that reads as "nothing wrong".
	link.Status = "This server records no status for a syncrepl link. " +
		"How far along it is, above, is the measure it does keep."
	return link
}

// stripPosition removes slapd's leading "{n}" from an ordered value.
func stripPosition(value string) string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "{") {
		return value
	}
	_, rest, ok := strings.Cut(value, "}")
	if !ok {
		return value
	}
	return strings.TrimSpace(rest)
}

// splitSyncrepl splits an olcSyncrepl value into its fields, respecting the
// quoting: a filter or a bind DN contains spaces and is quoted, and splitting
// on every space would cut one in half.
func splitSyncrepl(value string) []string {
	var out []string
	var field strings.Builder
	quoted := false
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c == '"':
			quoted = !quoted
			field.WriteByte(c)
		case c == ' ' && !quoted:
			if field.Len() > 0 {
				out = append(out, field.String())
				field.Reset()
			}
		default:
			field.WriteByte(c)
		}
	}
	if field.Len() > 0 {
		out = append(out, field.String())
	}
	return out
}

// ds389Link reads one replication agreement entry.
func ds389Link(entry *directory.Entry) Link {
	status := firstValue(entry, "nsds5replicaLastUpdateStatus")
	statusJSON := firstValue(entry, "nsds5replicaLastUpdateStatusJSON")
	inProgress := isTrue(firstValue(entry, "nsds5replicaUpdateInProgress"))

	peer := firstValue(entry, "nsDS5ReplicaHost")
	if port := firstValue(entry, "nsDS5ReplicaPort"); port != "" {
		peer += ":" + port
	}
	link := Link{
		Name:       rdnValue(entry),
		Direction:  DirectionOutgoing,
		Peer:       peer,
		BindDN:     firstValue(entry, "nsDS5ReplicaBindDN"),
		Transport:  firstValue(entry, "nsDS5ReplicaTransportInfo"),
		Status:     status,
		State:      stateFrom(status, statusJSON, inProgress),
		LastUpdate: parseGeneralizedTime(firstValue(entry, "nsds5replicaLastUpdateEnd")),
		LastInit:   parseGeneralizedTime(firstValue(entry, "nsds5replicaLastInitEnd")),
		InProgress: inProgress,
		DN:         entry.DN.String(),
	}
	if init := firstValue(entry, "nsds5replicaLastInitStatus"); init != "" {
		link.Notes = append(link.Notes, "last full copy: "+init)
	}
	if method := firstValue(entry, "nsDS5ReplicaBindMethod"); method != "" {
		link.Notes = append(link.Notes, "bind method: "+method)
	}
	return link
}

// ruvCursors reads the replica update vector off a 389 DS replica entry.
func ruvCursors(entry *directory.Entry) []Cursor {
	var out []Cursor
	for _, value := range entry.GetStrings("nsds50ruv") {
		if cursor, ok := parseRUV(value); ok {
			out = append(out, cursor)
		}
	}
	return out
}

// readSuffixOwner reads the database entry an overlay sits on, for the suffix
// it serves.
func readSuffixOwner(ctx context.Context, r Reader, parent dn.DN) (string, error) {
	entry, err := r.Read(ctx, parent, []string{"olcSuffix"})
	if err != nil {
		return "", err
	}
	return firstValue(entry, "olcSuffix"), nil
}

// serverID is this server's own identity, where it keeps one globally.
func serverID(ctx context.Context, r Reader, base dn.DN) string {
	entry, err := r.Read(ctx, base, []string{"olcServerID"})
	if err != nil {
		return ""
	}
	// olcServerID may be written as a bare number or as "1 ldap://host",
	// which names the server the number belongs to. The number is the part
	// that appears inside a change sequence.
	value := firstValue(entry, "olcServerID")
	if value == "" {
		return ""
	}
	id, _, _ := strings.Cut(strings.TrimSpace(value), " ")
	id = strings.TrimLeft(id, "0")
	if id == "" {
		// Server id 0 is legal, and is what a provider with no olcServerID
		// stamps. Trimming it to nothing made the card say "from server :".
		return "0"
	}
	return id
}

// configRootName names the tree a database with no suffix holds. On OpenLDAP
// that is the configuration database, whose contents are the configuration
// tree itself, so it is named after the root the server announced rather than
// after the {n} in its own DN.
func configRootName(caps directory.Capabilities, where dn.DN) string {
	if caps.Config.DN != "" {
		return caps.Config.DN
	}
	if caps.ConfigContext != "" {
		return caps.ConfigContext
	}
	// Last resort: the top of the tree this entry sits in.
	if len(where) > 0 {
		return where[len(where)-1:].String()
	}
	return "cn=config"
}

func hasClass(entry *directory.Entry, class string) bool {
	for _, value := range entry.GetStrings("objectClass") {
		if strings.EqualFold(value, class) {
			return true
		}
	}
	return false
}

func firstValue(entry *directory.Entry, name string) string {
	values := entry.GetStrings(name)
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(values[0])
}

func rdnValue(entry *directory.Entry) string {
	rdn := entry.DN.RDN()
	if len(rdn) == 0 {
		return ""
	}
	return rdn[0].Value
}

func isTrue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "yes", "on", "1":
		return true
	}
	return false
}
