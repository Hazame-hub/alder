package main

import (
	"bytes"
	"context"
	"net"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/cli"
)

func runAlder(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	env := &cli.Env{
		Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr,
		Getenv:      func(string) (string, bool) { return "", false },
		Interactive: func() bool { return false },
		Version:     "test",
	}
	return cli.Execute(context.Background(), rootWith(env), env, args), stderr.String()
}

// An address that is already taken is not a mistake on the command line, so
// it is exit 8, not the 7 every other non-exit error now earns.
func TestServeOnATakenAddressExitsEight(t *testing.T) {
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	code, stderr := runAlder(t, "serve", "--addr", ln.Addr().String(), "--allow-http")
	if code != cli.ExitFailed {
		t.Errorf("exit %d, want %d\n%s", code, cli.ExitFailed, stderr)
	}
}

// Starting without TLS is how the command was started, so it is a usage
// error: exit 7.
func TestServeWithoutTLSExitsSeven(t *testing.T) {
	code, stderr := runAlder(t, "serve", "--addr", "127.0.0.1:0")
	if code != cli.ExitUsage {
		t.Errorf("exit %d, want %d\n%s", code, cli.ExitUsage, stderr)
	}
	if !strings.Contains(stderr, "refusing to start without TLS") {
		t.Errorf("stderr:\n%s", stderr)
	}
}

func TestAnUnknownCommandExitsSevenFromTheRealRoot(t *testing.T) {
	if code, stderr := runAlder(t, "nosuchcommand"); code != cli.ExitUsage {
		t.Errorf("exit %d, want %d\n%s", code, cli.ExitUsage, stderr)
	}
}

// A header that carries credentials is refused before anything listens.
func TestServeRefusesToLogACredentialHeader(t *testing.T) {
	for _, name := range []string{"Authorization", "Cookie", "X-Api-Key"} {
		err := runServe(t.Context(), serveOptions{logLevel: "info", logFormat: "text", logHeader: name})
		if err == nil || !strings.Contains(err.Error(), "--log-request-header") {
			t.Errorf("%s: err = %v, want a --log-request-header refusal", name, err)
		}
	}
}
