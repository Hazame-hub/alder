// Command alder is the Alder directory engineering tool.
//
// "alder serve" starts the web UI and the API. The other commands -- snapshot,
// diff, plan, apply and version -- are a client of that API, in internal/cli.
// They were left out until 1.8 on the grounds that a second, half-maintained
// interface to the same operations is a liability. They are not a second
// interface to the operations: they call the same endpoints the web interface
// calls, and implement none of what those endpoints do. That is the condition
// under which they exist.
package main

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/spf13/cobra"

	"github.com/hazame-hub/alder/internal/cli"
)

// version is set by the linker in release builds.
var version = "dev"

func main() {
	env := cli.OSEnv(buildVersion())
	os.Exit(cli.Execute(context.Background(), rootWith(env), env, os.Args[1:]))
}

func rootWith(env *cli.Env) *cobra.Command {
	root := &cobra.Command{
		Use:   "alder",
		Short: "A directory engineering tool for OpenLDAP and 389 Directory Server",
		Long: "Alder is a web UI for engineering LDAP directories.\n\n" +
			"Browse the schema, edit entries safely, and export every change as\n" +
			"LDIF or as an Ansible task. Nothing is written to a directory without\n" +
			"showing the exact LDIF change record first.",
		SilenceUsage: true,
		Version:      buildVersion(),
	}
	root.AddCommand(serveCmd())
	root.AddCommand(cli.Commands(env)...)
	return root
}

func buildVersion() string {
	if version != "dev" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return version
	}
	var revision, modified string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if len(s.Value) >= 12 {
				revision = s.Value[:12]
			}
		case "vcs.modified":
			if s.Value == "true" {
				modified = "-dirty"
			}
		}
	}
	if revision == "" {
		return version
	}
	return fmt.Sprintf("%s+%s%s", version, revision, modified)
}
