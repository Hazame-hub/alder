package ldapdriver

import (
	"context"
	"strings"

	"github.com/go-ldap/ldap/v3"

	"github.com/hazame-hub/alder/internal/directory"
)

// Reaching a server's own configuration tree.
//
// A directory keeps its configuration in the directory, in a tree beside the
// data. Being able to read it is what makes the schema browser able to say
// where the schema comes from, and being able to write it is what makes schema
// editing work on a server that stores its schema there.
//
// Two things stand between a session and that tree, and they are different
// problems:
//
//  1. Knowing the DN. Some servers publish it in the RootDSE as configContext.
//     Others do not publish it at all while still serving it at the same
//     conventional DN.
//  2. Being allowed in. The account that administers a suffix usually has no
//     rights in the configuration tree, and vice versa — they are separate
//     administrative domains, deliberately.
//
// The first is solved by preferring what the server announces and otherwise
// trying the convention and believing only what the server answers. That is
// still observation rather than a vendor check: the candidate DN is tried
// against every server, the result decides, and a server that does not serve it
// simply has no configuration tree as far as Alder is concerned.
//
// The second is solved by letting a connection carry a second identity for the
// configuration tree. Operations are routed by DN, so one session browses data
// as one account and the configuration as another, instead of forcing a choice
// between the two.

// configCandidates are the DNs tried when a server announces none.
//
// This is a convention, not a guess about who the server is. Each is tried and
// used only if the server returns a configuration entry for it.
var configCandidates = []string{"cn=config"}

// resolveConfigContext returns the DN of the configuration tree this session can
// reach, or "" if there is none.
func (s *session) resolveConfigContext(ctx context.Context, announced string) string {
	if announced != "" {
		// Announced, but not necessarily readable by this session. Saying it
		// exists is still worth doing: the schema browser explains that the
		// schema lives there and that another account would be needed, which is
		// more use than silence.
		return announced
	}
	for _, candidate := range configCandidates {
		if s.readsAsConfigRoot(ctx, candidate) {
			return candidate
		}
	}
	return ""
}

// readsAsConfigRoot reports whether a base read of the candidate returns
// something that is a configuration root rather than ordinary data.
//
// The check that it is not inside a naming context is what keeps this honest:
// a directory could hold an entry actually called cn=config as part of its
// data, and treating that as the server's configuration would be wrong.
func (s *session) readsAsConfigRoot(ctx context.Context, candidate string) bool {
	for _, nc := range s.caps.NamingContexts {
		if withinTree(candidate, nc) {
			return false
		}
	}
	req := ldap.NewSearchRequest(candidate, ldap.ScopeBaseObject, ldap.NeverDerefAliases,
		1, int(s.timeout.Seconds()), false, "(objectClass=*)", []string{"objectClass"}, nil)
	res, err := s.searchLocked(ctx, req)
	return err == nil && len(res.Entries) > 0
}

// withinTree reports whether target is base or sits beneath it.
//
// DNs compare case-insensitively on their component names, and this comparison
// only ever decides which connection to use, so folding the whole string is
// enough — a false negative routes to the primary connection, which is what
// would have happened anyway.
func withinTree(target, base string) bool {
	if base == "" {
		return false
	}
	t, b := strings.ToLower(strings.TrimSpace(target)), strings.ToLower(strings.TrimSpace(base))
	return t == b || strings.HasSuffix(t, ","+b)
}

// connFor picks the connection an operation on this DN belongs on.
//
// Routing by DN rather than by operation is what lets a single session read
// people as the directory administrator and write schema as the configuration
// administrator, with neither borrowing the other's rights.
func (s *session) connFor(target string) *ldap.Conn {
	if s.configConn != nil && withinTree(target, s.configTreeDN) {
		return s.configConn
	}
	return s.conn
}

// configAccess describes what this session can do in the configuration tree,
// for the UI to report rather than for anything to branch on.
func (s *session) configAccess(ctx context.Context, treeDN string) directory.ConfigAccess {
	// Recorded before the read, because routing has to know which DNs belong to
	// the second connection even while working out whether it can read them.
	s.configTreeDN = treeDN
	if treeDN == "" {
		return directory.ConfigAccess{
			Reason: "This server does not publish a configuration tree, and none was found.",
		}
	}
	req := ldap.NewSearchRequest(treeDN, ldap.ScopeBaseObject, ldap.NeverDerefAliases,
		1, int(s.timeout.Seconds()), false, "(objectClass=*)", []string{"objectClass"}, nil)
	if _, err := s.searchLocked(ctx, req); err != nil {
		return directory.ConfigAccess{
			DN: treeDN,
			Reason: "This session cannot read " + treeDN + " (" + err.Error() +
				"). Connect again with configuration credentials to reach it.",
		}
	}
	return directory.ConfigAccess{
		DN:           treeDN,
		Readable:     true,
		SeparateBind: s.configConn != nil,
		BoundAs:      s.configBindDN(),
	}
}

func (s *session) configBindDN() string {
	if s.configConn != nil {
		return s.cfg.ConfigBindDN
	}
	return s.cfg.BindDN
}
