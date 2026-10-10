package cli

import (
	"os/exec"
	"strings"
	"testing"
)

// The client has no LDAP code (docs/CLI.md): it reads files, asks a running
// Alder server, and prints what comes back. That was a sentence until the
// client was found linking the whole directory stack, through the one import
// it needed for request and response types -- internal/api, which also holds
// the HTTP server, which builds the driver. The types now come from
// internal/apiclient, and this keeps the sentence true.
//
// internal/directory itself is allowed. It is Alder's own driver-independent
// model, the ChangeRecord and the interfaces, with no protocol code; recovery
// bundles are expressed in it. What the client must never reach is the
// protocol: go-ldap, the driver built on it, and the server that builds the
// driver.
func TestTheClientLinksNoLDAPCode(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("this test needs the go command to list the package's dependencies: %v", err)
	}
	out, err := exec.CommandContext(t.Context(), goBin, "list", "-deps", "github.com/hazame-hub/alder/internal/cli").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}
	forbidden := []string{
		"github.com/go-ldap/ldap/v3",
		"github.com/hazame-hub/alder/internal/directory/ldapdriver",
		"github.com/hazame-hub/alder/internal/api",
	}
	deps := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		deps[strings.TrimSpace(line)] = true
	}
	if !deps["github.com/hazame-hub/alder/internal/cli"] {
		t.Fatalf("go list did not list the package itself, so its output cannot be trusted:\n%s", out)
	}
	for _, f := range forbidden {
		if deps[f] {
			t.Errorf("internal/cli depends on %s: the client must not link LDAP code. "+
				"Request and response types come from internal/apiclient.", f)
		}
	}
}
