//go:build conformance

package conformance

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
)

// 1.22: when the directory refuses a change, the refusal says where to look.
//
// The hint has said what a result code usually means for a while. Since 1.19
// and 1.21 Alder can answer the question that raises -- which rule refuses
// this, which policy is in force -- and this is the thread between the two.
//
// It is proved against a real refusal from a real server: the interesting part
// is that the result code and the change together pick the right screen, and
// only a server that actually refuses something produces that pair. The change
// used is one both servers deny -- another account's password -- so a run that
// passes changes nothing, and so does one that fails.

func get(t *testing.T, client *http.Client, target string) httpResult {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("building a request for %s: %v", target, err)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("getting %s: %v", target, err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading %s: %v", target, err)
	}
	return httpResult{status: res.StatusCode, body: string(raw)}
}

// restrictedAPISession connects the API as the delegated account, which both
// servers deliberately hold short of writing.
func restrictedAPISession(t *testing.T, s server) (*http.Client, string) {
	t.Helper()
	base := startAlder(t)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}

	caPEM, err := os.ReadFile(filepath.Join("..", "compose", "certs", "ca.crt"))
	if err != nil {
		t.Fatalf("reading the harness CA: %v", err)
	}
	connect, err := json.Marshal(map[string]any{
		"host": s.host, "port": s.port, "tls": "ldaps",
		"caCertificate": string(caPEM), "serverName": "localhost",
		"bindDn": s.restrictedDN, "bindPassword": s.restrictedPW,
	})
	if err != nil {
		t.Fatalf("encoding connect: %v", err)
	}
	if res := post(t, client, base+"/session", string(connect)); res.status != http.StatusCreated {
		t.Fatalf("connecting as %s: %d\n%s", s.restrictedDN, res.status, res.body)
	}
	return client, base
}

type refusal struct {
	Error    string `json:"error"`
	Message  string `json:"message"`
	Hint     string `json:"hint"`
	LdapCode int    `json:"ldapCode"`
	Remedy   *struct {
		Kind      string `json:"kind"`
		Dn        string `json:"dn"`
		Attribute string `json:"attribute"`
		Label     string `json:"label"`
	} `json:"remedy"`
}

// refusalFrom reads a refusal whether the API reported it as the whole
// response or as one change's outcome inside a changeset run. Both shapes
// exist, and the remedy has to reach an operator through either.
func refusalFrom(t *testing.T, res httpResult) refusal {
	t.Helper()
	var direct refusal
	if err := json.Unmarshal([]byte(res.body), &direct); err == nil && direct.LdapCode != 0 {
		return direct
	}
	var run struct {
		Outcomes []struct {
			Error *refusal `json:"error"`
		} `json:"outcomes"`
	}
	if err := json.Unmarshal([]byte(res.body), &run); err != nil {
		t.Fatalf("decoding the response: %v\n%s", err, res.body)
	}
	for _, o := range run.Outcomes {
		if o.Error != nil && o.Error.LdapCode != 0 {
			return *o.Error
		}
	}
	return refusal{}
}

func TestRefusedForRightsPointsAtTheAccessRules(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, _ directory.Session) {
		client, base := restrictedAPISession(t, s)

		target := "uid=user0005,ou=people," + suffix
		body, err := json.Marshal(map[string]any{
			"changes": []map[string]any{{
				"dn": target, "type": "modify",
				"mods": []map[string]any{{
					"op": "replace", "name": "userPassword",
					"values": []map[string]any{{"text": "not-going-to-happen"}},
				}},
			}},
		})
		if err != nil {
			t.Fatalf("encoding the change: %v", err)
		}

		out := refusalFrom(t, post(t, client, base+"/changeset/apply", string(body)))
		if out.LdapCode == 0 {
			t.Fatalf("%s: the delegated account was allowed to set another account's password", s.name)
		}
		if out.LdapCode != 50 {
			t.Skipf("%s refused with code %d rather than insufficient access", s.name, out.LdapCode)
		}
		if out.Hint == "" {
			t.Errorf("%s: a refusal with no explanation", s.name)
		}
		if out.Remedy == nil {
			t.Fatalf("%s: refused for rights and the refusal says nowhere to look", s.name)
		}
		if out.Remedy.Kind != "access" {
			t.Errorf("%s: pointed at %q, want the access rules", s.name, out.Remedy.Kind)
		}
		if !strings.EqualFold(out.Remedy.Dn, target) {
			t.Errorf("%s: pointed at %q, want the entry that was refused", s.name, out.Remedy.Dn)
		}
		if out.Remedy.Label == "" {
			t.Errorf("%s: a way in with no words on it", s.name)
		}

		// And the screen it points at answers. A remedy that leads somewhere
		// broken is worse than none: it costs the reader the minute they have.
		rules := get(t, client, base+"/access?dn="+url.QueryEscape(target))
		if rules.status != http.StatusOK {
			t.Fatalf("%s: the remedy points at a screen that fails: %d\n%s", s.name, rules.status, rules.body)
		}
		if !strings.Contains(rules.body, `"rules"`) {
			t.Errorf("%s: the access report holds no rules: %s", s.name, rules.body)
		}
		t.Logf("%s: code %d -> %s on %s", s.name, out.LdapCode, out.Remedy.Kind, out.Remedy.Dn)
	})
}
