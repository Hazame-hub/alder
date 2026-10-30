package session

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/hazame-hub/alder/internal/reqlog"
)

// "session opened" names the bind DN, so it is the line an operator matches
// to the proxy's access log to find who connected. It carries the request's
// header value when the request had one.
func TestSessionOpenedCarriesTheRequestHeader(t *testing.T) {
	var buf bytes.Buffer
	store := NewStore(slog.New(reqlog.Handler(slog.NewTextHandler(&buf, nil))), time.Minute, time.Hour)
	defer store.Close()

	sess, err := store.AddReplacing(reqlog.With(context.Background(), "req-9"), &fakeSession{}, testConfig(), false, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AttachReference(reqlog.With(context.Background(), "req-10"), sess, &fakeSession{}, testConfig()); err != nil {
		t.Fatal(err)
	}
	var opened []string
	for _, l := range strings.Split(buf.String(), "\n") {
		if strings.Contains(l, "session opened") {
			opened = append(opened, l)
		}
	}
	if len(opened) != 2 || !strings.Contains(opened[0], "request_header=req-9") || !strings.Contains(opened[1], "request_header=req-10") {
		t.Errorf("session opened lines lack the request header:\n%s", buf.String())
	}
}
