package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/hazame-hub/alder/internal/changepkg"
)

// 1.11: change packages over HTTP.
//
// A package leaves with the intent and nothing else: no baseline, no
// expectation, no password, and no trace of where this server keeps its
// schema. It comes back to be validated against whatever directory is on the
// other end.

const packProofAT = "( 1.3.6.1.4.1.99999.8.1 NAME 'alderPackTeam' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )"

// postWithoutSession posts to a server nobody has connected, which is how a
// pipeline reads a package: with a document and no credentials.
func postWithoutSession(t *testing.T, app *fiber.App, path, body string) response {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	return response{Status: res.StatusCode, Body: string(raw), Header: res.Header}
}

// buildBody is the changeset a package is made from: an ordinary edit, a
// schema change as this server expresses it, and a password change that cannot
// travel.
func buildBody(t *testing.T, withPassword bool) string {
	t.Helper()
	changes := []map[string]any{
		{"dn": "uid=alice,ou=people,dc=alder,dc=test", "type": "modify",
			// A baseline from some earlier plan, which must not survive.
			"baseline": "token-from-another-process",
			"mods": []any{map[string]any{"op": "replace", "name": "title",
				"values": []any{map[string]any{"text": "Senior Engineer"}}}}},
		{"dn": "cn=schema", "type": "modify",
			"mods": []any{map[string]any{"op": "add", "name": "attributeTypes",
				"values": []any{map[string]any{"text": packProofAT}}}}},
	}
	if withPassword {
		changes = append(changes, map[string]any{"dn": "uid=alice,ou=people,dc=alder,dc=test",
			"type": "setpassword", "newPassword": "hunter2"})
	}
	body, err := json.Marshal(map[string]any{"title": "promote alice", "changes": changes})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func buildPackage(t *testing.T, rig *testRig, body string) (string, *changepkg.Package) {
	t.Helper()
	res := post(t, rig, "/api/v1/packages/build", body)
	if res.Status != http.StatusOK {
		t.Fatalf("build: %d %s", res.Status, res.Body)
	}
	p, _, err := changepkg.Decode([]byte(res.Body))
	if err != nil {
		t.Fatalf("the package Alder built does not decode: %v\n%s", err, res.Body)
	}
	return res.Body, p
}

func TestAPackageCarriesIntentAndNothingElse(t *testing.T) {
	rig := schemaRig(t, baseSchemaAttrs(nil, nil))
	doc, p := buildPackage(t, rig, buildBody(t, true))

	if p.Format != changepkg.Format || p.Version != 1 || len(p.ID) != 36 {
		t.Fatalf("head: %s %d %q", p.Format, p.Version, p.ID)
	}
	if p.Counts.Changes != 2 || p.Counts.Data != 1 || p.Counts.Schema != 1 || p.Counts.Omitted != 1 {
		t.Fatalf("counts %+v\nchanges %+v", p.Counts, p.Changes)
	}
	// The password change is refused, and said so: not dropped, and not
	// carried with a placeholder that could replay it.
	if p.Omitted[0].Reason != changepkg.OmittedSecret {
		t.Errorf("omission %+v", p.Omitted[0])
	}
	for _, forbidden := range []string{"hunter2", "token-from-another-process", "baseline", "expect", "newPassword"} {
		if strings.Contains(doc, forbidden) {
			t.Errorf("the package carries %q:\n%s", forbidden, doc)
		}
	}

	// The schema change travels as what it means, not as this server's
	// modification of its own schema entry.
	var schemaItem *changepkg.Item
	for i := range p.Changes {
		if p.Changes[i].Kind == changepkg.KindSchema {
			schemaItem = &p.Changes[i]
		}
	}
	if schemaItem == nil || schemaItem.Schema == nil {
		t.Fatalf("changes %+v", p.Changes)
	}
	if schemaItem.Schema.Element != changepkg.ElementAttributeType || schemaItem.Schema.Op != changepkg.SchemaAdd ||
		schemaItem.Schema.OID != "1.3.6.1.4.1.99999.8.1" {
		t.Errorf("schema intent %+v", schemaItem.Schema)
	}
	if strings.Contains(doc, "cn=schema") || strings.Contains(doc, "attributeTypes") {
		t.Errorf("the package names this server's schema entry:\n%s", doc)
	}
	// Built twice, the same intent gives the same changes. The identity is its
	// own thing -- a new package is a new package -- and it is part of what the
	// checksum covers, so the checksums differ with it.
	got := post(t, rig, "/api/v1/packages/build", buildBody(t, true))
	if got.Status != http.StatusOK {
		t.Fatalf("build: %d %s", got.Status, got.Body)
	}
	second, _, err := changepkg.Decode([]byte(got.Body))
	if err != nil {
		t.Fatal(err)
	}
	first, again := mustJSON(t, p.Changes), mustJSON(t, second.Changes)
	if first != again {
		t.Errorf("the same intent packaged differently: %s versus %s", first, again)
	}
	if second.ID == p.ID {
		t.Error("two packages share an identity")
	}
	if second.Checksum == p.Checksum {
		t.Error("two packages with different identities share a checksum")
	}
}

func TestAPackageIsInspectedWithoutADirectory(t *testing.T) {
	rig := schemaRig(t, baseSchemaAttrs(nil, nil))
	doc, p := buildPackage(t, rig, buildBody(t, false))

	// No session at all: this reads a document.
	app, driver := directorylessServer(t)
	res := postWithoutSession(t, app, "/api/v1/packages/inspect", doc)
	if res.Status != http.StatusOK {
		t.Fatalf("inspect: %d %s", res.Status, res.Body)
	}
	in := decode[PackageInspection](t, res)
	if in.PackageId != p.ID || in.Integrity != SnapshotIntegrityVerified || in.Counts.Changes != 2 {
		t.Fatalf("inspection %+v", in)
	}
	if len(in.Order) != 2 {
		t.Errorf("order %+v", in.Order)
	}
	if n := driver.connects.Load(); n != 0 {
		t.Errorf("inspecting a package connected %d times", n)
	}

	for name, body := range map[string]string{
		"an edited change": strings.Replace(doc, "Senior Engineer", "Staff Engineer", 1),
		"an unknown field": strings.Replace(doc, `"version": 1`, `"version": 1, "baseline": "smuggled"`, 1),
		"a later version":  strings.Replace(doc, `"version": 1`, `"version": 2`, 1),
		"not a package":    `{"format":"alder-snapshot","version":1}`,
		"trailing content": doc + "{}",
	} {
		t.Run(name, func(t *testing.T) {
			res := postWithoutSession(t, app, "/api/v1/packages/inspect", body)
			if res.Status != http.StatusBadRequest {
				t.Fatalf("status %d", res.Status)
			}
			if code := errorCode(t, res); !strings.HasPrefix(string(code), "package_") {
				t.Errorf("code %s", code)
			}
		})
	}
}

func TestValidationAnswersForThisTargetOnly(t *testing.T) {
	rig := schemaRig(t, baseSchemaAttrs(nil, nil))
	doc, _ := buildPackage(t, rig, buildBody(t, false))

	res := post(t, rig, "/api/v1/packages/validate", `{"package":`+doc+`}`)
	if res.Status != http.StatusOK {
		t.Fatalf("validate: %d %s", res.Status, res.Body)
	}
	v := decode[PackageValidation](t, res)
	if v.Integrity != SnapshotIntegrityVerified {
		t.Errorf("integrity %s", v.Integrity)
	}
	byID := map[string]PackageValidationItem{}
	for _, item := range v.Items {
		byID[item.Id] = item
	}
	// The schema change is ready here, and its prepared change is a
	// modification of this server's schema entry -- built now, not carried.
	var schemaItem PackageValidationItem
	for _, item := range v.Items {
		if item.Kind == "schema" {
			schemaItem = item
		}
	}
	if schemaItem.Status != PackageStatusReady || schemaItem.Changes == nil || len(*schemaItem.Changes) != 1 {
		t.Fatalf("schema item %+v", schemaItem)
	}
	change := (*schemaItem.Changes)[0]
	if change.Dn != "cn=schema" || change.Type != ChangeRequestTypeModify {
		t.Errorf("prepared change %+v", change)
	}
	if change.Baseline != nil {
		t.Error("a prepared change carries a baseline, which only a plan issues")
	}
	// The entry the data change is about does not exist on this fake, so the
	// intent cannot be carried out here -- and says so rather than inventing
	// an add.
	for _, item := range v.Items {
		if item.Kind == "data" && item.Status != PackageStatusConflict {
			t.Errorf("data item %+v", item)
		}
	}
	if v.Counts.Ready != 1 || v.Counts.Conflict != 1 {
		t.Errorf("counts %+v", v.Counts)
	}
	if len(v.Order) != 1 || v.Order[0] != schemaItem.Id {
		t.Errorf("order %+v", v.Order)
	}
}

func TestValidationRefusesAPackageItCannotRead(t *testing.T) {
	rig := schemaRig(t, baseSchemaAttrs(nil, nil))
	doc, _ := buildPackage(t, rig, buildBody(t, false))
	for name, body := range map[string]string{
		"no package":       `{}`,
		"an edited change": `{"package":` + strings.Replace(doc, "Senior Engineer", "Staff Engineer", 1) + `}`,
		"a bad target":     `{"package":` + doc + `,"schemaTarget":"not a dn"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if res := post(t, rig, "/api/v1/packages/validate", body); res.Status != http.StatusBadRequest {
				t.Errorf("status %d: %s", res.Status, res.Body)
			}
		})
	}
}
