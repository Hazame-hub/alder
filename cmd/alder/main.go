// Command alder is the Alder directory engineering tool.
//
// M0 placeholder. The real entry point, with the cobra command tree and
// "alder serve", arrives with the HTTP server in M2.
package main

import (
	"fmt"
	"os"
	"runtime/debug"
)

// version is set by the linker in release builds.
var version = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version") {
		fmt.Println("alder", buildVersion())
		return
	}
	fmt.Fprintln(os.Stderr, "alder", buildVersion())
	fmt.Fprintln(os.Stderr, "no commands yet: the server lands in M2")
	os.Exit(1)
}

func buildVersion() string {
	if version != "dev" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return version
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 12 {
			return version + "+" + s.Value[:12]
		}
	}
	return version
}
