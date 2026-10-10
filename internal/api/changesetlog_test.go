package api

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
)

// The server's log must say what the response says. A run stopped by a change
// whose outcome is unknown was logged as "stopped at a failure" while the
// response and the UI said it may already have been applied -- the claim the
// UI was changed to stop making.
func TestTheLogNamesAnUnknownOutcomeAsUnknown(t *testing.T) {
	for _, tc := range []struct {
		name     string
		applyErr error
		want     string
		notWant  string
	}{
		{
			name:     "interrupted",
			applyErr: fmt.Errorf("%w (modify: connection lost)", directory.ErrWriteOutcomeUnknown),
			want:     "changeset stopped at a change whose outcome is unknown",
			notWant:  "failure",
		},
		{
			name:     "refused",
			applyErr: errors.New("constraint violation"),
			want:     "changeset stopped at a failure",
			notWant:  "unknown",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			alice := personAt(t, planAlice, []string{"cn", "Alice"}, []string{"sn", "L"})
			bob := personAt(t, "uid=bob,ou=people,dc=alder,dc=test", []string{"cn", "Bob"}, []string{"sn", "B"})
			rig := planRig(t, alice, bob)
			rig.fake.applyErr = tc.applyErr
			var buf bytes.Buffer
			rig.server.logger = slog.New(slog.NewTextHandler(&buf, nil))

			// Two changes, so the one that stops the run is not the last
			// outcome: the second follows it as not attempted.
			var changes []string
			for _, d := range []string{planAlice, "uid=bob,ou=people,dc=alder,dc=test"} {
				changes = append(changes, fmt.Sprintf(
					`{"dn":%q,"type":"modify","mods":[{"op":"replace","name":"sn","values":[{"text":"X"}]}]}`, d))
			}
			res := rig.do(t, http.MethodPost, "/api/v1/changeset/apply",
				strings.NewReader(`{"changes":[`+strings.Join(changes, ",")+`]}`))
			if res.Status != http.StatusOK && res.Status != http.StatusConflict && res.Status != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d\n%s", res.Status, res.Body)
			}

			var line string
			for _, l := range strings.Split(buf.String(), "\n") {
				if strings.Contains(l, "changeset stopped") {
					line = l
				}
			}
			if !strings.Contains(line, tc.want) {
				t.Fatalf("log line %q, want it to contain %q\nfull log:\n%s", line, tc.want, buf.String())
			}
			if strings.Contains(line, tc.notWant) {
				t.Errorf("log line %q should not contain %q", line, tc.notWant)
			}
		})
	}
}
