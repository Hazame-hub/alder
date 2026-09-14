package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func modifiedAlice(candidate map[string]any) map[string]any {
	item := map[string]any{
		"kind": "modified", "sourceDn": aliceDN, "targetDn": aliceDN,
		"attributes": []any{
			map[string]any{"name": "title", "kind": "modified",
				"removed": []any{map[string]any{"text": "Senior Engineer"}},
				"added":   []any{map[string]any{"text": "Engineer"}}},
			map[string]any{"name": "userPassword", "kind": "modified", "sensitive": true, "withheldSource": 1, "withheldTarget": 2},
			map[string]any{"name": "description", "kind": "modified",
				"removed": []any{map[string]any{"text": "evil \x1b]0;owned\x07 \u202egnp.exe"}},
				"added":   []any{map[string]any{"base64": "AAEC"}}},
		},
	}
	if candidate != nil {
		item["candidate"] = candidate
	}
	return item
}

var modifyCandidate = map[string]any{"destructive": false, "changes": []any{
	map[string]any{"dn": aliceDN, "type": "modify", "mods": []any{
		map[string]any{"op": "delete", "name": "title", "values": []any{map[string]any{"text": "Senior Engineer"}}},
		map[string]any{"op": "add", "name": "title", "values": []any{map[string]any{"text": "Engineer"}}},
	}},
}}

func TestDiffExitStatusSaysWhatTheComparisonFound(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   int
	}{
		{"no differences", http.StatusOK, "", ExitOK},
		{"differences", http.StatusOK, "modified", ExitDifferences},
		{"incomplete", http.StatusOK, "incomplete", ExitIncomplete},
		{"unknown items", http.StatusOK, "unknown", ExitIncomplete},
		{"refused", http.StatusUnauthorized, "", ExitFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStub(t)
			var body string
			switch {
			case tc.status != http.StatusOK:
				body = apiErrorJSON("unauthorized", "Not connected to a directory. Connect first.", nil)
			case tc.body == "modified":
				body = diffJSON(t, true, diffCounts{compared: 1, modified: 1}, modifiedAlice(nil))
			case tc.body == "incomplete":
				body = diffJSON(t, false, diffCounts{compared: 1, modified: 1}, modifiedAlice(nil))
			case tc.body == "unknown":
				body = diffJSON(t, true, diffCounts{compared: 1, unknown: 1},
					map[string]any{"kind": "unknown", "sourceDn": aliceDN, "reason": "insufficient_access"})
			default:
				body = diffJSON(t, true, diffCounts{compared: 1, unchanged: 1})
			}
			s.reply("POST /api/v1/diff", tc.status, body)
			snap := writeTemp(t, t.TempDir(), "s.json", snapshotDoc)
			expectCode(t, s.run(t, runOpts{}, "diff", snap, "@live"), tc.want)
		})
	}
}

func TestSnapshotsAreSentAsTheBytesTheyAre(t *testing.T) {
	s := newStub(t)
	s.reply("POST /api/v1/diff", http.StatusOK, diffJSON(t, true, diffCounts{}))
	dir := t.TempDir()
	// Odd formatting, and a field a version 1 reader must refuse: the client
	// passes both on, so that the server is the one to decide.
	a := "{\"format\":\"alder-snapshot\",   \"future\": true,\n\"entries\":[]}"
	b := snapshotDoc
	r := s.run(t, runOpts{}, "diff", writeTemp(t, dir, "a.json", "\n  "+a+"\n"), writeTemp(t, dir, "b.json", b))
	expectCode(t, r, ExitOK)

	var req struct {
		Source struct {
			Snapshot json.RawMessage `json:"snapshot"`
		} `json:"source"`
		Target struct {
			Snapshot json.RawMessage `json:"snapshot"`
		} `json:"target"`
		IncludeUnchanged bool `json:"includeUnchanged"`
	}
	if err := json.Unmarshal(s.called("POST /api/v1/diff")[0].Body, &req); err != nil {
		t.Fatal(err)
	}
	if string(req.Source.Snapshot) != a || string(req.Target.Snapshot) != strings.TrimSpace(b) {
		t.Errorf("the snapshots were not sent as they are:\nsource: %s\ntarget: %s", req.Source.Snapshot, req.Target.Snapshot)
	}
}

func TestTheLiveSideIsNamedWhereItStands(t *testing.T) {
	s := newStub(t)
	s.reply("POST /api/v1/diff", http.StatusOK, diffJSON(t, true, diffCounts{}))
	snap := writeTemp(t, t.TempDir(), "s.json", snapshotDoc)
	expectCode(t, s.run(t, runOpts{}, "diff", "@live", snap, "--base", "ou=staff,dc=example,dc=test", "--scope", "one", "--include-unchanged"), ExitOK)
	var req map[string]any
	_ = json.Unmarshal(s.called("POST /api/v1/diff")[0].Body, &req)
	source := req["source"].(map[string]any)
	live, ok := source["live"].(map[string]any)
	if !ok || live["base"] != "ou=staff,dc=example,dc=test" || live["scope"] != "one" || req["includeUnchanged"] != true {
		t.Fatalf("source = %v, request = %v", source, req)
	}
	if _, isSnapshot := req["target"].(map[string]any)["snapshot"]; !isSnapshot {
		t.Error("the snapshot is not the target")
	}
}

func TestASnapshotOnStandardInput(t *testing.T) {
	s := newStub(t)
	s.reply("POST /api/v1/diff", http.StatusOK, diffJSON(t, true, diffCounts{}))
	expectCode(t, s.run(t, runOpts{stdin: snapshotDoc}, "diff", "-", "@live"), ExitOK)
	if !bytes.Contains(s.called("POST /api/v1/diff")[0].Body, []byte(`"entryCount": 1`)) {
		t.Error("the snapshot from standard input was not sent")
	}
}

func TestDiffJSONIsAlderAnswerAndNothingElse(t *testing.T) {
	s := newStub(t)
	body := diffJSON(t, true, diffCounts{compared: 1, modified: 1}, modifiedAlice(nil))
	s.reply("POST /api/v1/diff", http.StatusOK, body)
	snap := writeTemp(t, t.TempDir(), "s.json", snapshotDoc)
	r := s.run(t, runOpts{}, "diff", snap, "@live", "--json")
	expectCode(t, r, ExitDifferences)
	oneJSONDocument(t, r.stdout)
	if strings.TrimSpace(r.stdout) != body {
		t.Fatalf("stdout is not Alder's comparison:\n%s", r.stdout)
	}
	if strings.Contains(r.stdout, "\x1b") {
		t.Error("an escape character reached JSON output unescaped")
	}
}

func TestAFailureInJSONModeIsAlderErrorAsAJSONDocument(t *testing.T) {
	s := newStub(t)
	refusal := apiErrorJSON("snapshot_checksum_mismatch", "The snapshot does not match its checksum.", nil)
	s.reply("POST /api/v1/diff", http.StatusBadRequest, refusal)
	snap := writeTemp(t, t.TempDir(), "s.json", snapshotDoc)
	r := s.run(t, runOpts{}, "diff", snap, "@live", "--json")
	expectCode(t, r, ExitFailed)
	doc := oneJSONDocument(t, r.stdout)
	if doc["error"].(map[string]any)["error"] != "snapshot_checksum_mismatch" {
		t.Errorf("stdout = %s", r.stdout)
	}

	r = s.run(t, runOpts{}, "diff", filepath.Join(t.TempDir(), "missing.json"), "@live", "--json")
	expectCode(t, r, ExitFailed)
	doc = oneJSONDocument(t, r.stdout)
	if e := doc["error"].(map[string]any); e["origin"] != "cli" || e["error"] != "input" {
		t.Errorf("a client-side failure = %v", doc)
	}
}

func TestHumanDiffShowsEachDifferenceSafely(t *testing.T) {
	s := newStub(t)
	s.reply("POST /api/v1/diff", http.StatusOK, diffJSON(t, true, diffCounts{compared: 2, modified: 1, removed: 1},
		modifiedAlice(modifyCandidate),
		map[string]any{"kind": "removed", "sourceDn": "uid=dave,ou=people,dc=example,dc=test",
			"candidate": map[string]any{"destructive": true, "changes": []any{map[string]any{"dn": "uid=dave,ou=people,dc=example,dc=test", "type": "delete"}}}}))
	snap := writeTemp(t, t.TempDir(), "s.json", snapshotDoc)
	r := s.run(t, runOpts{}, "diff", "@live", snap)
	expectCode(t, r, ExitDifferences)
	for _, want := range []string{
		"2 entries compared", "1 modified", "1 removed",
		"modified  " + aliceDN, "    - Senior Engineer", "    + Engineer",
		"sensitive, values never shown: 1 value -> 2 values",
		`\x1b]0;owned\x07`, `\u202e`, "(binary value, 3 bytes)",
		"change offered: 1 request (select with --stage)",
		"change offered: deletes the entry (select with --stage-deletion)",
	} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("missing %q in:\n%s", want, r.stdout)
		}
	}
	if strings.ContainsAny(r.stdout, "\x1b\x07\u202e") {
		t.Error("a control character from the directory reached the terminal")
	}

	r = s.run(t, runOpts{}, "diff", "@live", snap, "--summary")
	if strings.Contains(r.stdout, aliceDN) || !strings.Contains(r.stdout, "1 modified") {
		t.Errorf("--summary printed:\n%s", r.stdout)
	}
}

func TestAFileThatIsNotOneJSONObjectIsNeverSent(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"not JSON":      "dn: uid=a\n",
		"not an object": "[1,2,3]",
		// Closes the object early and writes the other side of the request.
		"injection": `{"format":"alder-snapshot"}},"target":{"live":{}},"x":{"snapshot":{}`,
	} {
		t.Run(name, func(t *testing.T) {
			s := newStub(t)
			r := s.run(t, runOpts{}, "diff", writeTemp(t, dir, "x.json", content), "@live")
			expectCode(t, r, ExitFailed)
			if len(s.called("POST /api/v1/diff")) != 0 || len(s.called("POST /api/v1/session")) != 0 {
				t.Error("the file was sent")
			}
		})
	}
}

func TestAComparisonTooLargeForAlderIsNotSent(t *testing.T) {
	dir := t.TempDir()
	big := `{"pad":"` + strings.Repeat("x", 9<<20) + `"}`
	a, b := writeTemp(t, dir, "a.json", big), writeTemp(t, dir, "b.json", big)
	s := newStub(t)
	r := s.run(t, runOpts{}, "diff", a, b)
	expectCode(t, r, ExitFailed)
	if !strings.Contains(r.stderr, "16 MB") || len(s.called("POST /api/v1/session")) != 0 {
		t.Errorf("stderr = %s", r.stderr)
	}
}

func TestSelectingDifferencesIsExplicitAndDeletionsAreNamedAsSuch(t *testing.T) {
	dave := "uid=dave,ou=people,dc=example,dc=test"
	eve := "uid=eve,ou=people,dc=example,dc=test"
	body := diffJSON(t, true, diffCounts{compared: 3, modified: 2, removed: 1},
		modifiedAlice(modifyCandidate),
		map[string]any{"kind": "removed", "sourceDn": dave,
			"candidate": map[string]any{"destructive": true, "changes": []any{map[string]any{"dn": dave, "type": "delete"}}}},
		map[string]any{"kind": "modified", "sourceDn": eve, "targetDn": eve,
			"candidate": map[string]any{"destructive": false, "changes": []any{}, "blocked": "only_unchangeable_attributes"}},
	)
	snap := writeTemp(t, t.TempDir(), "s.json", snapshotDoc)

	run := func(t *testing.T, args ...string) (result, string, *stub) {
		s := newStub(t)
		s.reply("POST /api/v1/diff", http.StatusOK, body)
		out := filepath.Join(t.TempDir(), "changes.json")
		r := s.run(t, runOpts{}, append([]string{"diff", "@live", snap, "--changes-out", out}, args...)...)
		return r, out, s
	}

	t.Run("a modification", func(t *testing.T) {
		r, out, _ := run(t, "--stage", strings.ToUpper(aliceDN))
		expectCode(t, r, ExitDifferences)
		var changes []map[string]any
		data, _ := os.ReadFile(out)
		if err := json.Unmarshal(data, &changes); err != nil || len(changes) != 1 || changes[0]["type"] != "modify" {
			t.Fatalf("changes = %s (%v)", data, err)
		}
	})
	t.Run("a deletion and a modification, in the comparison's order", func(t *testing.T) {
		r, out, _ := run(t, "--stage-deletion", dave, "--stage", aliceDN)
		expectCode(t, r, ExitDifferences)
		var changes []map[string]any
		data, _ := os.ReadFile(out)
		_ = json.Unmarshal(data, &changes)
		if len(changes) != 2 || changes[0]["type"] != "modify" || changes[1]["type"] != "delete" {
			t.Fatalf("changes = %s", data)
		}
	})
	for name, args := range map[string][]string{
		"a deletion selected as a change": {"--stage", dave},
		"a change selected as a deletion": {"--stage-deletion", aliceDN},
		"a difference offering no change": {"--stage", eve},
		"a DN no difference is about":     {"--stage", "uid=nobody,dc=example,dc=test"},
	} {
		t.Run(name, func(t *testing.T) {
			r, out, _ := run(t, args...)
			expectCode(t, r, ExitNotApplicable)
			assertAbsent(t, out)
		})
	}
}
