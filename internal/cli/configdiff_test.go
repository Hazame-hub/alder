package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 1.13: configuration snapshots and configuration comparisons on the command
// line. Alder does the deciding; what is tested here is that the command shows
// what it decided, refuses what it was told is not changeable, and never puts
// a secret or a control character on a terminal.

const configSnapshotDoc = `{
  "format": "alder-snapshot",
  "version": 1,
  "kind": "config",
  "createdAt": "2026-09-17T10:00:00Z",
  "source": {"provider": "openldap", "vendor": "OpenLDAP", "root": "cn=config"},
  "completeness": "complete",
  "incomplete": [],
  "coverage": {"sections": ["limits", "server"], "excluded": ["runtime-state"]},
  "counts": {"resources": 1, "settings": 2, "withheld": 1, "operational": 1, "readOnly": 1},
  "resources": [],
  "settings": [],
  "checksum": "sha256:abc"
}`

func configItems() []map[string]any {
	idle := map[string]any{
		"id": "limits//olcidletimeout", "kind": "modified", "section": "limits", "key": "olcIdleTimeout",
		"source": []any{"0"}, "target": []any{"1800"}, "type": "int", "comparison": "normalised",
		"mutability": "writable", "actionable": "writable", "dn": "cn=config",
		"candidate": map[string]any{"changes": []any{map[string]any{
			"dn": "cn=config", "type": "modify", "mods": []any{map[string]any{
				"op": "replace", "name": "olcIdleTimeout", "values": []any{map[string]any{"text": "1800"}}}},
		}}},
	}
	rootpw := map[string]any{
		"id": "backend/database:dc=alder,dc=test/olcrootpw", "kind": "modified", "section": "backend",
		"resource": "database:dc=alder,dc=test", "resourceLabel": "{1}mdb", "key": "olcRootPW",
		"type": "string", "comparison": "normalised", "mutability": "writable", "actionable": "read_only",
		"sensitive": true, "dn": "olcDatabase={1}mdb,cn=config",
		"problems":  []any{"sensitive_withheld"},
		"candidate": map[string]any{"changes": []any{}, "blocked": "sensitive"},
	}
	args := map[string]any{
		// A value carrying a terminal escape and a bidi override, as a
		// configuration read from a server Alder does not own may.
		"id": "server//olcargsfile", "kind": "modified", "section": "server", "key": "olcArgsFile",
		"source": []any{"/run/slapd/slapd.args"}, "target": []any{"/run/\x1b]0;owned\x07slapd\u202egnp.exe"},
		"type": "path", "comparison": "normalised", "mutability": "read_only", "actionable": "read_only",
		"operational": true, "dn": "cn=config", "problems": []any{"no_write_path"},
		"candidate": map[string]any{"changes": []any{}, "blocked": "read_only"},
	}
	access := map[string]any{
		"id": "access_control//olcaccess", "kind": "modified", "section": "access_control", "key": "olcAccess",
		"source": []any{"to * by users read"}, "target": []any{"to * by users write"}, "ordered": true,
		"type": "structured", "comparison": "raw", "mutability": "unknown", "actionable": "unknown",
		"dn": "cn=config", "problems": []any{"not_comparable"},
		"candidate": map[string]any{"changes": []any{}, "blocked": "read_only"},
	}
	return []map[string]any{access, rootpw, idle, args}
}

func configDiffJSON(t *testing.T, items []map[string]any, mismatch bool) string {
	t.Helper()
	side := func(kind string) map[string]any {
		return map[string]any{"kind": kind, "base": "cn=config", "scope": "sub", "filter": "(objectClass=*)",
			"operationalAttributes": false, "entryCount": 12, "vendor": "OpenLDAP"}
	}
	summary := func(provider string, settings, resources int) map[string]any {
		return map[string]any{"provider": provider, "completeness": "complete", "settings": settings,
			"resources": resources, "sections": []any{
				map[string]any{"section": "backend", "settings": settings / 2},
				map[string]any{"section": "limits", "settings": settings - settings/2},
			}}
	}
	counts := map[string]any{"compared": len(items), "added": 0, "removed": 0, "modified": len(items),
		"unchanged": 0, "unknown": 0, "actionable": 1}
	config := map[string]any{
		"providerMismatch": mismatch, "complete": true, "crossVendor": mismatch,
		"counts": counts, "items": items,
		"sections": []any{
			map[string]any{"section": "limits", "counts": map[string]any{"compared": 1, "added": 0, "removed": 0,
				"modified": 1, "unchanged": 0, "unknown": 0, "actionable": 1}},
		},
		"source": summary("openldap", 88, 6), "target": summary("openldap", 88, 6),
	}
	if mismatch {
		config["items"] = []any{}
		config["counts"] = map[string]any{"compared": 0, "added": 0, "removed": 0, "modified": 0,
			"unchanged": 0, "unknown": 0, "actionable": 0}
		config["sections"] = []any{}
		config["complete"] = false
		config["target"] = summary("389ds", 1431, 182)
	} else {
		config["provider"] = "openldap"
	}
	doc := map[string]any{
		"kind": "config", "source": side("live"), "target": side("snapshot"),
		"complete": !mismatch, "crossVendor": mismatch, "operationalIgnored": false, "items": []any{},
		"counts": map[string]any{"compared": len(items), "added": 0, "removed": 0,
			"modified": len(items), "renamed": 0, "unchanged": 0, "unknown": 0},
		"config": config,
	}
	if mismatch {
		doc["reasons"] = []any{map[string]any{"code": "provider_mismatch",
			"detail": "these are openldap and 389ds configurations"}}
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAConfigCaptureAsksForTheConfigurationAndNothingElse(t *testing.T) {
	s := newStub(t)
	var sent map[string]any
	s.on("POST /api/v1/snapshots/capture", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &sent)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, configSnapshotDoc)
	})
	out := filepath.Join(t.TempDir(), "config.json")
	r := s.run(t, runOpts{}, "snapshot", "--kind", "config", "--output", out)
	expectCode(t, r, ExitOK)
	if sent["kind"] != "config" || sent["base"] != nil || sent["scope"] != nil || sent["filter"] != nil {
		t.Errorf("request: %v", sent)
	}
	if data, _ := os.ReadFile(out); string(data) != configSnapshotDoc {
		t.Errorf("the file is not what Alder sent:\n%s", data)
	}
	if !strings.Contains(r.stderr, "openldap") || !strings.Contains(r.stderr, "2 settings") {
		t.Errorf("stderr: %s", r.stderr)
	}
	if !strings.Contains(r.stderr, "withheld") {
		t.Errorf("the capture does not say a secret was withheld: %s", r.stderr)
	}
}

func TestAConfigCaptureTakesNoSubtreeOptions(t *testing.T) {
	// A configuration is not a subtree of the directory the user chose; asking
	// for one with a base or a filter is a mistake rather than a narrowing.
	for name, args := range map[string][]string{
		"a base":                   {"snapshot", "--kind", "config", "--base", "dc=alder,dc=test"},
		"a scope":                  {"snapshot", "--kind", "config", "--scope", "one"},
		"a filter":                 {"snapshot", "--kind", "config", "--filter", "(objectClass=*)"},
		"operational attributes":   {"snapshot", "--kind", "config", "--operational"},
		"a kind nothing has heard": {"snapshot", "--kind", "configuration"},
	} {
		t.Run(name, func(t *testing.T) {
			expectCode(t, newStub(t).run(t, runOpts{}, append(args, "--output", "x.json")...), ExitUsage)
		})
	}
}

func TestAConfigDiffIsShownBySettingAndSafely(t *testing.T) {
	s := newStub(t)
	s.reply("POST /api/v1/diff", http.StatusOK, configDiffJSON(t, configItems(), false))
	snap := writeTemp(t, t.TempDir(), "config.json", configSnapshotDoc)
	r := s.run(t, runOpts{}, "diff", "@live", snap)
	expectCode(t, r, ExitDifferences)

	for _, want := range []string{
		"Configuration of openldap",
		"4 settings compared",
		"1 that Alder can change through a plan",
		"modified   limits             olcIdleTimeout",
		"-        0",
		"+        1800",
		"can change this setting through a plan (--stage limits//olcidletimeout)",
		"has no proven way to change this setting",
		"does not know whether this setting can be changed",
		"resource database:dc=alder,dc=test ({1}mdb)",
		"withheld: this setting is a secret",
		"note     not_comparable",
	} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, r.stdout)
		}
	}
	if strings.Contains(r.stdout, "alder-admin") {
		t.Error("a secret reached the terminal")
	}
	if strings.ContainsAny(r.stdout, "\x1b\x07\u202e") {
		t.Errorf("a control or bidi character reached the terminal:\n%q", r.stdout)
	}
}

func TestTwoProvidersConfigurationIsNotComparedOnTheCommandLine(t *testing.T) {
	s := newStub(t)
	s.reply("POST /api/v1/diff", http.StatusOK, configDiffJSON(t, configItems(), true))
	snap := writeTemp(t, t.TempDir(), "config.json", configSnapshotDoc)
	r := s.run(t, runOpts{}, "diff", "@live", snap)

	for _, want := range []string{
		"different servers' software",
		"does not translate configuration between",
		"openldap, 88 settings in 6 resources",
		"389ds, 1431 settings in 182 resources",
	} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, r.stdout)
		}
	}
	for _, unwanted := range []string{"olcIdleTimeout", "added", "--stage"} {
		if strings.Contains(r.stdout, unwanted) {
			t.Errorf("a cross-provider comparison printed %q as though it were a difference:\n%s", unwanted, r.stdout)
		}
	}
}

func TestOnlyAConfigSettingAlderCanChangeIsStaged(t *testing.T) {
	body := configDiffJSON(t, configItems(), false)
	snap := writeTemp(t, t.TempDir(), "config.json", configSnapshotDoc)
	run := func(t *testing.T, args ...string) (result, string) {
		s := newStub(t)
		s.reply("POST /api/v1/diff", http.StatusOK, body)
		out := filepath.Join(t.TempDir(), "changes.json")
		return s.run(t, runOpts{}, append([]string{"diff", "@live", snap, "--changes-out", out}, args...)...), out
	}

	t.Run("a writable setting, by identity and by key", func(t *testing.T) {
		for _, name := range []string{"limits//olcidletimeout", "olcIdleTimeout"} {
			r, out := run(t, "--stage", name)
			expectCode(t, r, ExitDifferences)
			data, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			var changes []struct {
				DN   string `json:"dn"`
				Type string `json:"type"`
				Mods []struct {
					Op     string `json:"op"`
					Name   string `json:"name"`
					Values []struct {
						Text string `json:"text"`
					} `json:"values"`
				} `json:"mods"`
			}
			if err := json.Unmarshal(data, &changes); err != nil {
				t.Fatalf("%s: %v", data, err)
			}
			if len(changes) != 1 || changes[0].DN != "cn=config" || changes[0].Type != "modify" {
				t.Fatalf("staged: %s", data)
			}
			if changes[0].Mods[0].Op != "replace" || changes[0].Mods[0].Values[0].Text != "1800" {
				t.Fatalf("staged: %s", data)
			}
		}
	})

	for name, args := range map[string][]string{
		"a secret":                    {"--stage", "olcRootPW"},
		"a setting the server owns":   {"--stage", "olcArgsFile"},
		"a setting nothing parses":    {"--stage", "olcAccess"},
		"a setting that is not there": {"--stage", "olcNonsense"},
		"a deletion":                  {"--stage-deletion", "olcIdleTimeout"},
	} {
		t.Run(name, func(t *testing.T) {
			r, out := run(t, args...)
			expectCode(t, r, ExitNotApplicable)
			if _, err := os.Stat(out); err == nil {
				t.Error("a refused selection still wrote change requests")
			}
		})
	}
}
