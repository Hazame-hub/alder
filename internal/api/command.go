package api

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/hazame-hub/alder/internal/directory"
)

// The search you just ran, as the command you would have typed.
//
// This is the positioning line taken literally: a directory tool whose output
// is code should be able to hand back the `ldapsearch` for what it just did, so
// the search can go in a runbook, a ticket or a shell script without being
// reconstructed from memory.
//
// It is rendered here rather than in the browser for the same reason the LDIF
// preview is. The browser holds the filter the operator typed; the server holds
// the filter it parsed and would actually send, and those are not always the
// same string — normalising it is the entire point of parsing it. A command
// built from the typed text would quietly differ from the search whose results
// are on screen, which is the one thing this product cannot do.
//
// The password is never in it. `-W` makes ldapsearch prompt, which is what an
// operator would have typed anyway, and it keeps this on the right side of the
// rule that bind credentials do not leave the session.

// commandTarget is the connection, reduced to what the command needs.
//
// A struct rather than the session itself, so the renderer is a pure function
// of its inputs and can be tested without a directory.
type commandTarget struct {
	Host       string
	Port       int
	TLS        string
	BindDN     string
	SkipVerify bool
	// CustomCA reports that the session was given a CA bundle. The bytes cannot
	// go in a command line, so their existence is reported instead.
	CustomCA bool
}

// searchCommand renders the ldapsearch that would run this search.
func searchCommand(t commandTarget, req directory.SearchRequest) string {
	var out strings.Builder

	// A private CA cannot be inlined, and a command that omits it fails with a
	// certificate error the reader then has to diagnose. Saying so costs a line.
	if t.CustomCA && !t.SkipVerify {
		out.WriteString("# This directory is behind a private CA. Point LDAPTLS_CACERT\n")
		out.WriteString("# at the bundle, or install it in the system trust store.\n")
	}

	// Reproduce what the session actually did rather than what it should have
	// done. A command that silently verifies where Alder did not would fail for
	// a reason the reader cannot see from the command.
	if t.SkipVerify {
		out.WriteString("LDAPTLS_REQCERT=never \\\n")
	}

	// -x is simple bind, which is the only bind Alder does.
	out.WriteString("ldapsearch -x -H " + shellQuote(ldapURI(t)))

	if t.TLS == string(directory.TLSModeStartTLS) {
		// -ZZ rather than -Z: the single form carries on unencrypted if the
		// upgrade fails, which is not what Alder did.
		out.WriteString(" -ZZ")
	}

	if t.BindDN != "" {
		// -W prompts. The password is not here and will not be.
		out.WriteString(" \\\n  -D " + shellQuote(t.BindDN) + " -W")
	}

	out.WriteString(" \\\n  -b " + shellQuote(req.BaseDN.String()))
	out.WriteString(" -s " + scopeFlag(req.Scope))
	if req.Limit > 0 {
		fmt.Fprintf(&out, " -z %d", req.Limit)
	}

	// The filter as the server would send it, not as it was typed.
	rendered, err := req.Filter.Render()
	if err != nil {
		// A filter that reached a search renders; if it somehow does not, no
		// command is better than one asserting something else.
		return ""
	}
	out.WriteString(" \\\n  " + shellQuote(rendered))

	if len(req.Attributes) > 0 {
		quoted := make([]string, 0, len(req.Attributes))
		for _, a := range req.Attributes {
			quoted = append(quoted, shellQuote(a))
		}
		out.WriteString(" \\\n  " + strings.Join(quoted, " "))
	}

	return out.String()
}

func ldapURI(t commandTarget) string {
	scheme := "ldap"
	if t.TLS == string(directory.TLSModeLDAPS) {
		scheme = "ldaps"
	}
	return fmt.Sprintf("%s://%s:%d", scheme, t.Host, t.Port)
}

func scopeFlag(s directory.Scope) string {
	switch s {
	case directory.ScopeBase:
		return "base"
	case directory.ScopeOneLevel:
		return "one"
	default:
		return "sub"
	}
}

// safeUnquoted is the set a POSIX shell passes through untouched. Anything
// outside it is quoted rather than escaped character by character, which is
// both easier to get right and easier to read.
var safeUnquoted = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// shellQuote makes one argument safe for a POSIX shell.
//
// A DN holds commas, equals signs and often spaces; a filter holds parentheses,
// asterisks and ampersands. Pasting either unquoted produces a command that is
// either wrong or, with the right value, does something other than it appears
// to — which is the same injection this codebase escapes against everywhere
// else, aimed at a shell instead of a directory.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if safeUnquoted.MatchString(s) {
		return s
	}
	// Inside single quotes a shell honours nothing at all, so the only case to
	// handle is a single quote itself: close, emit an escaped one, reopen.
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
