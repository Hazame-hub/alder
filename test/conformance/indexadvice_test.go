//go:build conformance

package conformance

import (
	"net/http"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/api"
	"github.com/hazame-hub/alder/internal/directory"
)

// 1.41: the index advisor against both servers' live index lists.
//
// The log is written here, in each server's own format, rather than taken
// from the container: what is proved is that the advisor reads the live
// index list the same way on both servers, that an attribute the backend
// indexes is never suggested, that one it does not is, and that the
// suggestion stages through the 1.32 candidate path unchanged.

const adviceAttribute = "carLicense"

func TestTheAdvisorSuggestsOnlyWhatTheBackendLacks(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		if !sess.Capabilities().Config.Readable {
			t.Skip("the configuration tree is not readable")
		}
		client, base := alderSession(t, s, true)
		backend := dataBackend(t, indexReport(t, client, base))
		if _, indexed := indexNamed(backend, adviceAttribute); indexed {
			t.Skipf("%s: the harness indexes %s", s.name, adviceAttribute)
		}
		var indexed string
		for _, i := range backend.Indexes {
			if i.Types == nil {
				continue
			}
			for _, typ := range *i.Types {
				if typ == "eq" && !strings.EqualFold(i.Attribute, "objectClass") {
					indexed = i.Attribute
				}
			}
		}
		if indexed == "" {
			t.Fatalf("%s: no attribute indexed for equality to compare against", s.name)
		}

		logs := map[string]string{
			"389-access": `[30/Oct/2026:14:02:11.200000000 +0000] conn=12 op=1 SRCH base="` + suffix + `" scope=2 filter="(` + adviceAttribute + `=ABC-123)" attrs=ALL
[30/Oct/2026:14:02:11.700000000 +0000] conn=12 op=1 RESULT err=0 tag=101 nentries=1 wtime=0.0001 optime=0.5 etime=0.5001 notes=A
[30/Oct/2026:14:02:12.000000000 +0000] conn=12 op=2 SRCH base="` + suffix + `" scope=2 filter="(` + indexed + `=x)" attrs=ALL
[30/Oct/2026:14:02:12.900000000 +0000] conn=12 op=2 RESULT err=0 tag=101 nentries=0 wtime=0.0001 optime=0.9 etime=0.9
`,
			"openldap-stats": `Oct 30 14:02:11 ldap slapd[42]: conn=1000 op=1 SRCH base="` + suffix + `" scope=2 deref=0 filter="(` + adviceAttribute + `=ABC-123)"
Oct 30 14:02:11 ldap slapd[42]: conn=1000 op=1 SEARCH RESULT tag=101 err=0 qtime=0.000012 etime=0.5 nentries=1 text=
Oct 30 14:02:12 ldap slapd[42]: conn=1000 op=2 SRCH base="` + suffix + `" scope=2 deref=0 filter="(` + indexed + `=x)"
Oct 30 14:02:12 ldap slapd[42]: conn=1000 op=2 SEARCH RESULT tag=101 err=0 qtime=0.000012 etime=0.9 nentries=0 text=
`,
		}
		for format, log := range logs {
			res := post(t, client, base+"/config/indexes/advice", mustEncode(t, map[string]any{"log": log}))
			if res.status != http.StatusOK {
				t.Fatalf("%s %s: POST /config/indexes/advice: %d\n%s", s.name, format, res.status, res.body)
			}
			if strings.Contains(res.body, "ABC-123") {
				t.Fatalf("%s %s: a value from the log came back: %s", s.name, format, res.body)
			}
			advice := decodeInto[api.IndexAdvice](t, res)
			if advice.Format == nil || string(*advice.Format) != format || advice.Counted != 2 {
				t.Errorf("%s %s: read as %s", s.name, format, mustEncode(t, advice))
			}
			if len(advice.Suggestions) != 1 {
				t.Fatalf("%s %s: want one suggestion, got %s", s.name, format, mustEncode(t, advice.Suggestions))
			}
			got := advice.Suggestions[0]
			if !strings.EqualFold(got.Attribute, adviceAttribute) || got.Type != "eq" ||
				got.Status != api.IndexSuggestionCandidate || !strings.EqualFold(got.Backend, backend.Name) {
				t.Errorf("%s %s: %s", s.name, format, mustEncode(t, got))
			}
		}

		// Staged through the candidate path, unchanged: a change is derived
		// and nothing is applied.
		res := post(t, client, base+"/config/indexes/candidate",
			mustEncode(t, map[string]any{"backend": backend.Name, "attribute": adviceAttribute, "types": []string{"eq"}}))
		candidate := decodeInto[api.IndexCandidate](t, res)
		if res.status != http.StatusOK || candidate.Change == nil {
			t.Errorf("%s: staging the suggestion: %d %s", s.name, res.status, res.body)
		}
		if _, now := indexNamed(dataBackend(t, indexReport(t, client, base)), adviceAttribute); now {
			t.Errorf("%s: asking for advice or a candidate added an index", s.name)
		}
	})
}
