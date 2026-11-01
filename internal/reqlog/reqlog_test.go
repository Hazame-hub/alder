package reqlog

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestClean(t *testing.T) {
	for in, want := range map[string]string{
		"abc-123":                "abc-123",
		"  padded  ":             "padded",
		"line\nbreak\r\x00":      "linebreak",
		"café":                   "caf",
		strings.Repeat("x", 300): strings.Repeat("x", MaxLen),
		"\n\t":                   "",
	} {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCheckName(t *testing.T) {
	for _, ok := range []string{"X-Request-Id", "traceparent", "X-Amzn-Trace-Id", "X-Correlation-ID"} {
		if err := CheckName(ok); err != nil {
			t.Errorf("CheckName(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{
		"", "X Request", "X-Id:", "Authorization", "proxy-authorization", "COOKIE", "Set-Cookie",
		"X-Auth-Token", "X-Api-Key", "X-Client-Secret", "X-Password",
		// Credential headers under names of a deployment's own choosing.
		"X-Authorization", "X-Forwarded-Authorization", "X-Session-Id", "X-Original-Cookie",
		"Cf-Access-Jwt-Assertion", "X-Amzn-Oidc-Data", "X-Amzn-Oidc-Identity", "X-Saml-Response",
		"X-Credential", "X-Ldap-Passwd",
	} {
		if err := CheckName(bad); err == nil {
			t.Errorf("CheckName(%q) = nil, want a refusal", bad)
		}
	}
}

func TestHandlerAddsTheValueOnlyWhenTheContextCarriesOne(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(Handler(slog.NewJSONHandler(&buf, nil))).With("component", "test")

	log.InfoContext(With(context.Background(), "req-42"), "change applied")
	log.InfoContext(context.Background(), "session closed")
	log.InfoContext(With(context.Background(), "\n"), "nothing left after cleaning")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3:\n%s", len(lines), buf.String())
	}
	if !strings.Contains(lines[0], `"request_header":"req-42"`) || !strings.Contains(lines[0], `"component":"test"`) {
		t.Errorf("first line lacks the value or the logger's attributes: %s", lines[0])
	}
	for _, l := range lines[1:] {
		if strings.Contains(l, Attribute) {
			t.Errorf("a line without a value carries %s: %s", Attribute, l)
		}
	}
}
