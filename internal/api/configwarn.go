package api

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hazame-hub/alder/internal/directory"
)

// Warnings for changes addressed into the server's own configuration.
//
// Editing an entry and editing the server that serves it are not the same act,
// and until now Alder presented them identically: the same dialog, the same
// LDIF, the same confirm button. A change that replaces an access control list
// with one that excludes you is applied as readily as a change to someone's
// telephone number, and afterwards there is nothing left to log in with.
//
// None of these block. The directory is the authority, an operator with
// configuration credentials is doing this on purpose, and a tool that refuses
// the dangerous half of the job is one people leave for ldapmodify. What they
// do is make sure nobody discovers afterwards that they were in the
// configuration tree.
//
// The attribute lists below name attributes, not vendors. That is the same
// approach the value deny-list already takes for userPassword and
// nsslapd-rootpw: what matters is the meaning of the attribute, and one server
// having no attribute of that name simply means the entry never matches.

// lockoutClass groups attributes by what going wrong with them costs.
type lockoutClass struct {
	attrs   []string
	explain string
}

var lockoutClasses = []lockoutClass{
	{
		attrs: []string{"olcAccess", "aci"},
		explain: "This is the access control list. A replace removes every rule that is not " +
			"listed here, including the one granting you access. Check that a rule still " +
			"grants your bind DN write access before applying.",
	},
	{
		attrs: []string{"olcRootDN", "olcRootPW", "nsslapd-rootdn", "nsslapd-rootpw"},
		explain: "This is the administrative identity for the database. Changing it can leave " +
			"nobody able to administer it, and the account you are connected as may be the " +
			"one you are changing.",
	},
	{
		attrs: []string{
			"nsslapd-port", "nsslapd-securePort", "nsslapd-listenhost", "nsslapd-securelistenhost",
			"nsslapd-security", "olcTLSCertificateFile", "olcTLSCertificateKeyFile",
		},
		explain: "This governs how the server listens for connections. A value it cannot use " +
			"will stop it accepting them, and the fix is then on the server's console rather " +
			"than in Alder.",
	},
	{
		attrs: []string{"olcSuffix", "olcDbDirectory", "nsslapd-suffix", "nsslapd-directory"},
		explain: "This defines what the database holds and where. Changing it on a populated " +
			"database can leave every entry in it unreachable.",
	},
}

// configWarnings reports that a change lands in the configuration tree, and
// names any attribute in it that is known to be able to lock the operator out.
//
// Schema entries are exempt from the general warning. Schema editing reaches
// the configuration tree too, but it does so through a dedicated form that
// already says what it is doing, and repeating the warning on every schema edit
// would train people to click past it.
func configWarnings(record directory.ChangeRecord, caps directory.Capabilities) []string {
	tree := caps.Config.DN
	if tree == "" || !withinConfigTree(record.DN.String(), tree) {
		return nil
	}

	var warnings []string
	if _, isSchema := caps.SchemaWrite.Target(record.DN.String()); !isSchema {
		warnings = append(warnings, fmt.Sprintf(
			"%s is this server's own configuration, not directory data. Changes here take "+
				"effect immediately, there is no undo, and a mistake can stop the directory "+
				"serving or lock you out of it.", tree))
	}

	touched := map[string]bool{}
	for _, a := range record.AffectedAttributes() {
		touched[strings.ToLower(a)] = true
	}
	for _, class := range lockoutClasses {
		var matched []string
		for _, attr := range class.attrs {
			if touched[strings.ToLower(attr)] {
				matched = append(matched, attr)
			}
		}
		if len(matched) == 0 {
			continue
		}
		sort.Strings(matched)
		warnings = append(warnings, fmt.Sprintf("%s: %s", strings.Join(matched, ", "), class.explain))
	}
	return warnings
}

// withinConfigTree reports whether a DN is at or below the configuration root.
// It folds case because DNs compare that way, and this only ever decides
// whether to say something.
func withinConfigTree(target, tree string) bool {
	t := strings.ToLower(strings.TrimSpace(target))
	b := strings.ToLower(strings.TrimSpace(tree))
	return t == b || strings.HasSuffix(t, ","+b)
}
