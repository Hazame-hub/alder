// Command alder is the Alder directory engineering tool.
//
// "alder serve" starts the web UI and the API. There is deliberately nothing
// else: Alder is a browser tool, and a second, half-maintained command-line
// interface to the same operations is a liability rather than a feature.
package main

import (
	"fmt"
	"os"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// version is set by the linker in release builds.
var version = "dev"

func main() {
	if err := rootCmd().Execute(); err != nil {
		// Cobra has already printed the error.
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
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
