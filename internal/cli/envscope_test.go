package cli

import (
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/hazame-hub/alder/internal/envflags"
)

// connectingFlags are the flags docs/CLI.md's "Connecting" table lists with a
// variable. They, and only they, read ALDER_* on a client command: anything
// that confirms or widens a write, chooses what a command operates on, or
// says where its output goes is typed or not set at all.
var connectingFlags = map[string]bool{
	"api-url": true, "api-ca-file": true, "timeout": true,
	"host": true, "port": true, "tls": true, "ca-file": true, "server-name": true,
	"bind-dn": true, "bind-password-file": true,
	"config-bind-dn": true, "config-bind-password-file": true,
}

// Every client command, walked, so a command added later cannot forget
// envflags.Exclude the way the signing commands once did -- where an
// inherited ALDER_FORCE=true overwrote a private key.
func TestOnlyTheConnectionFlagsReadTheEnvironment(t *testing.T) {
	var offenders []string
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		c.Flags().VisitAll(func(f *pflag.Flag) {
			if envflags.Reads(f) && !connectingFlags[f.Name] && f.Name != "help" {
				offenders = append(offenders, c.CommandPath()+" --"+f.Name)
			}
		})
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	root := &cobra.Command{Use: "alder"}
	root.AddCommand(Commands(&Env{})...)
	walk(root)
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Errorf("these flags read ALDER_* but are not connection settings; exclude them with envflags.Exclude:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}
