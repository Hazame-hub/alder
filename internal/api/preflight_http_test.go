package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hazame-hub/alder/internal/changepkg"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/schema"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// 1.12: migration preflight over HTTP.
//
// The endpoint takes an artifact from anywhere and reads it against the
// session's directory. The property that matters most is the one no single
// report shows: nothing is ever written, whatever the artifact is and however
// the preflight turns out.

const preflightAT = "( 1.3.6.1.4.1.99999.70.1 NAME 'alderPreflightTeam' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )"

func preflightSchemaAttrs() map[string][]string {
	attrs := baseSchemaAttrs([]string{
		"( 2.5.4.4 NAME 'sn' SUP name )",
		"( 2.5.4.11 NAME 'ou' SUP name )",
		"( 2.5.4.31 NAME 'member' EQUALITY distinguishedNameMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.12 )",
	}, []string{
		"( 2.5.6.5 NAME 'organizationalUnit' SUP top STRUCTURAL MUST ou )",
		"( 2.5.6.6 NAME 'person' SUP top STRUCTURAL MUST ( cn $ sn ) )",
		"( 2.5.6.9 NAME 'groupOfNames' SUP top STRUCTURAL MUST ( cn $ member ) )",
	})
	attrs[schema.AttrLDAPSyntaxes] = []string{
		"( 1.3.6.1.4.1.1466.115.121.1.15 DESC 'Directory String' )",
		"( 1.3.6.1.4.1.1466.115.121.1.12 DESC 'DN' )",
		"( 1.3.6.1.4.1.1466.115.121.1.38 DESC 'OID' )",
	}
	attrs[schema.AttrMatchingRules] = []string{
		"( 2.5.13.2 NAME 'caseIgnoreMatch' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
		"( 2.5.13.1 NAME 'distinguishedNameMatch' SYNTAX 1.3.6.1.4.1.1466.115.121.1.12 )",
	}
	return attrs
}

func preflightRig(t *testing.T) *testRig {
	t.Helper()
	rig := schemaRig(t, preflightSchemaAttrs())
	for _, d := range []string{"dc=alder,dc=test", "ou=people,dc=alder,dc=test"} {
		e := directory.NewEntry(mustParse(t, d))
		e.Set("objectClass", [][]byte{[]byte("top")})
		rig.fake.byDN[d] = e
	}
	rig.fake.visibility = map[string]directory.AttributeVisibility{}
	return rig
}

func packageDocument(t *testing.T, items ...changepkg.Item) string {
	t.Helper()
	p, err := changepkg.Build(&changepkg.Package{ID: "3b9a6c1e-6c2f-4a57-9a0d-5c9e1f2a3b4c", Title: "preflight",
		Changes: changepkg.Derive(items)}, time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := changepkg.Encode(&buf, p); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func dataDocument(t *testing.T, sch *schema.Schema, base string, entries ...*directory.Entry) string {
	t.Helper()
	s, err := snapshot.Build(snapshot.Capture{Base: mustParse(t, base), Scope: "sub", Filter: "(objectClass=*)", Vendor: "Source",
		CreatedAt: time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)}, sch, entries)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := snapshot.Encode(&buf, s); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func preflightBodyFor(artifact string) string { return `{"artifact":` + artifact + `}` }

// strictReport decodes a report into the generated type, refusing any field the
// spec does not describe, so the handler and the spec cannot drift apart.
func strictReport(t *testing.T, body string) PreflightReport {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(body))
	dec.DisallowUnknownFields()
	var r PreflightReport
	if err := dec.Decode(&r); err != nil {
		t.Fatalf("the report does not match the spec: %v\n%s", err, body)
	}
	return r
}

func personEntry(t *testing.T, d string) *directory.Entry {
	e := directory.NewEntry(mustParse(t, d))
	e.Set("objectClass", [][]byte{[]byte("top"), []byte("person")})
	e.Set("cn", [][]byte{[]byte("x")})
	e.Set("sn", [][]byte{[]byte("y")})
	return e
}

// Every mode, every outcome, and a refusal: none writes.
func TestAPreflightNeverWrites(t *testing.T) {
	sch := schema.Load("cn=schema", preflightSchemaAttrs())
	cases := map[string]struct {
		body    string
		setup   func(*fakeSession)
		status  int
		overall PreflightOverall
	}{
		"portable package": {
			body: preflightBodyFor(packageDocument(t,
				changepkg.Item{ID: "c1", Kind: changepkg.KindSchema, Schema: &changepkg.SchemaChange{Element: changepkg.ElementAttributeType,
					Op: changepkg.SchemaAdd, OID: "1.3.6.1.4.1.99999.70.1", Definition: preflightAT}},
			)),
			status: http.StatusOK, overall: PreflightCompatible,
		},
		"incompatible package": {
			body: preflightBodyFor(packageDocument(t,
				changepkg.Item{ID: "c1", Kind: changepkg.KindData, Data: &changepkg.DataChange{DN: "uid=a,dc=elsewhere,dc=example", Type: changepkg.OpAdd,
					Attributes: []changepkg.Attribute{{Name: "objectClass", Values: []changepkg.Value{{Text: "top"}, {Text: "person"}}},
						{Name: "cn", Values: []changepkg.Value{{Text: "a"}}}, {Name: "sn", Values: []changepkg.Value{{Text: "b"}}}}}},
			)),
			status: http.StatusOK, overall: PreflightNotCompatible,
		},
		"package with a missing parent": {
			body: preflightBodyFor(packageDocument(t,
				changepkg.Item{ID: "c1", Kind: changepkg.KindData, Data: &changepkg.DataChange{DN: "uid=a,ou=gone,dc=alder,dc=test", Type: changepkg.OpAdd,
					Attributes: []changepkg.Attribute{{Name: "objectClass", Values: []changepkg.Value{{Text: "top"}, {Text: "person"}}},
						{Name: "cn", Values: []changepkg.Value{{Text: "a"}}}, {Name: "sn", Values: []changepkg.Value{{Text: "b"}}}}}},
			)),
			status: http.StatusOK, overall: PreflightIncomplete,
		},
		"schema snapshot": {
			body:   preflightBodyFor(schemaDocument(t, "Source", baseSchemaAttrs([]string{preflightAT}, nil))),
			status: http.StatusOK,
		},
		"data snapshot": {
			body: preflightBodyFor(dataDocument(t, sch, "ou=people,dc=alder,dc=test",
				personEntry(t, "uid=new,ou=people,dc=alder,dc=test"), personEntry(t, "uid=orphan,ou=nowhere,ou=people,dc=alder,dc=test"))),
			status: http.StatusOK,
		},
		"data snapshot when the target cannot be read": {
			body: preflightBodyFor(dataDocument(t, sch, "ou=people,dc=alder,dc=test", personEntry(t, "uid=new,ou=people,dc=alder,dc=test"))),
			setup: func(f *fakeSession) {
				f.searchErr = errors.New("size limit exceeded")
			},
			status: http.StatusOK, overall: PreflightIncomplete,
		},
		"malformed artifact": {
			body: preflightBodyFor(`{"format":"alder-change-package","version":1,"id":"x","planToken":"smuggled"}`), status: http.StatusBadRequest,
		},
		"empty package": {
			body: preflightBodyFor(`{"format":"alder-change-package","version":1,"id":"x"}`), status: http.StatusOK, overall: PreflightIncomplete,
		},
		"not an artifact": {
			body: preflightBodyFor(`{"format":"alder-recovery","version":1}`), status: http.StatusBadRequest,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rig := preflightRig(t)
			if tc.setup != nil {
				tc.setup(rig.fake)
			}
			res := post(t, rig, "/api/v1/preflight", tc.body)
			if res.Status != tc.status {
				t.Fatalf("status %d, want %d\n%s", res.Status, tc.status, res.Body)
			}
			if len(rig.fake.applied) != 0 {
				t.Fatalf("a preflight wrote %d change(s): %+v", len(rig.fake.applied), rig.fake.applied)
			}
			if strings.Contains(res.Body, sentinelPassword) || strings.Contains(res.Body, rig.cookie) {
				t.Fatal("the response carries a credential or the session id")
			}
			if tc.status != http.StatusOK {
				return
			}
			// A report is analysis: nothing in it is plan input.
			for _, forbidden := range []string{"baseline", "planToken", "changes"} {
				if strings.Contains(res.Body, `"`+forbidden+`"`) {
					t.Fatalf("the report carries %q", forbidden)
				}
			}
			r := strictReport(t, res.Body)
			if tc.overall != "" && r.Overall != tc.overall {
				t.Errorf("overall %s, want %s\n%s", r.Overall, tc.overall, res.Body)
			}
			areas := map[string]bool{}
			for _, n := range r.NotEvaluated {
				areas[n.Area] = true
			}
			if !areas["access_control"] || !areas["server_configuration"] {
				t.Errorf("not evaluated: %+v", r.NotEvaluated)
			}
		})
	}
}

func TestAPreflightRefusalUsesTheArtifactsOwnCode(t *testing.T) {
	rig := preflightRig(t)
	doc := packageDocument(t, changepkg.Item{ID: "c1", Kind: changepkg.KindSchema, Schema: &changepkg.SchemaChange{
		Element: changepkg.ElementAttributeType, Op: changepkg.SchemaAdd, OID: "1.3.6.1.4.1.99999.70.1", Definition: preflightAT}})
	forged := strings.Replace(doc, `"sha256:`, `"sha256:ff`, 1)
	res := post(t, rig, "/api/v1/preflight", preflightBodyFor(forged))
	if res.Status != http.StatusBadRequest || !strings.Contains(res.Body, string(ErrorErrorPackageChecksumMismatch)) {
		t.Fatalf("forged package: %d %s", res.Status, res.Body)
	}
	res = post(t, rig, "/api/v1/preflight", preflightBodyFor(`{"format":"alder-snapshot","version":7,"kind":"data"}`))
	if res.Status != http.StatusBadRequest || !strings.Contains(res.Body, string(ErrorErrorSnapshotUnsupportedVersion)) {
		t.Fatalf("future snapshot: %d %s", res.Status, res.Body)
	}
	res = post(t, rig, "/api/v1/preflight", preflightBodyFor(`{"format":"something-else"}`))
	if res.Status != http.StatusBadRequest || !strings.Contains(res.Body, string(ErrorErrorPreflightArtifactUnsupported)) {
		t.Fatalf("not an artifact: %d %s", res.Status, res.Body)
	}
	res = post(t, rig, "/api/v1/preflight", `{"artifact":"a string"}`)
	if res.Status != http.StatusBadRequest {
		t.Fatalf("a string: %d %s", res.Status, res.Body)
	}
	if len(rig.fake.applied) != 0 {
		t.Fatal("a refusal wrote")
	}
}

func TestAPreflightNeedsASession(t *testing.T) {
	rig := preflightRig(t)
	res := postWithoutSession(t, rig.app, "/api/v1/preflight", preflightBodyFor(`{"format":"alder-change-package"}`))
	if res.Status != http.StatusUnauthorized {
		t.Fatalf("status %d", res.Status)
	}
}
