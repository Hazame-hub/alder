//go:build conformance

package conformance

import (
	"net/http"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/api"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// 1.14: a configuration snapshot preflighted against both servers.
//
// Against the server it came from, a preflight names each setting that no
// longer matches and says what doing something about it takes. Against the
// other server it evaluates nothing, and says so. Either way the target's
// configuration is the same afterwards.

func preflightConfig(t *testing.T, client *http.Client, base, document string) api.PreflightReport {
	t.Helper()
	res := post(t, client, base+"/preflight", `{"artifact":`+document+`}`)
	if res.status != http.StatusOK {
		t.Fatalf("preflight: status %d\n%s", res.status, res.body)
	}
	return decodeInto[api.PreflightReport](t, res)
}

func configChecksum(t *testing.T, client *http.Client, base string) string {
	t.Helper()
	snap, _, err := snapshot.DecodeConfig([]byte(captureConfig(t, client, base)))
	if err != nil {
		t.Fatal(err)
	}
	return snap.Checksum
}

func TestAConfigurationSnapshotIsPreflightedAgainstItsOwnServer(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		if !sess.Capabilities().Config.Readable || s.configWriteDN == "" {
			t.Skip("no readable configuration, or no setting nominated for this server")
		}
		client, base := alderSession(t, s, true)

		// A capture, preflighted at once: everything already matches.
		doc := captureConfig(t, client, base)
		same := preflightConfig(t, client, base, doc)
		if same.Overall != api.PreflightCompatible || !same.Complete {
			t.Fatalf("a capture preflighted against the server it came from is %s (%v)", same.Overall, same.Reasons)
		}
		for _, n := range same.NotEvaluated {
			if n.Area == "server_configuration" {
				t.Fatal("configuration was evaluated and listed as not evaluated")
			}
		}

		// One setting changed outside Alder: the snapshot now wants the old
		// value back, and the preflight says Alder can do that through a plan.
		target := mustDN(t, s.configWriteDN)
		entry, err := sess.Read(ctx(t), target, []string{s.configWriteAttr})
		if err != nil {
			t.Fatal(err)
		}
		before := entry.GetStrings(s.configWriteAttr)
		t.Cleanup(func() {
			vals := make([][]byte, 0, len(before))
			for _, v := range before {
				vals = append(vals, []byte(v))
			}
			_ = sess.Apply(ctx(t), directory.ChangeRecord{DN: target, Type: directory.ChangeModify,
				Mods: []directory.Mod{{Op: directory.ModReplace, Name: s.configWriteAttr, Values: vals}}})
		})
		mustApply(t, sess, directory.ChangeRecord{DN: target, Type: directory.ChangeModify,
			Mods: []directory.Mod{{Op: directory.ModReplace, Name: s.configWriteAttr, Values: [][]byte{[]byte(s.configWriteValue)}}}})

		checksum := configChecksum(t, client, base)
		drifted := preflightConfig(t, client, base, doc)
		if after := configChecksum(t, client, base); after != checksum {
			t.Fatal("a preflight changed the target's configuration")
		}
		if drifted.Overall != api.PreflightCompatibleWithPrerequisites {
			t.Fatalf("overall %s (%v)", drifted.Overall, drifted.Reasons)
		}
		found := 0
		for _, f := range drifted.Findings {
			if f.Code != "config_setting_changeable" {
				continue
			}
			found++
			if f.Source.Attribute == nil || !strings.EqualFold(*f.Source.Attribute, s.configWriteAttr) {
				t.Errorf("an unexpected setting is reported as changed: %+v", f.Source)
			}
			if f.ManualAction {
				t.Errorf("%s is one Alder changes, and was reported as manual work", s.configWriteAttr)
			}
		}
		if found != 1 {
			t.Fatalf("want one changeable setting, got %d: %s", found, mustEncode(t, drifted.Findings))
		}
		if strings.Contains(mustEncode(t, drifted), s.bindPW) || (s.schemaBindPW != "" && strings.Contains(mustEncode(t, drifted), s.schemaBindPW)) {
			t.Fatal("a preflight report holds a password")
		}
	})
}

func TestAConfigurationSnapshotOfTheOtherServerIsNotEvaluated(t *testing.T) {
	if len(servers) < 2 {
		t.Skip("this proof needs two servers")
	}
	documents := map[string]string{}
	for _, s := range servers {
		sess := connectForSchema(t, s)
		if !sess.Capabilities().Config.Readable {
			t.Skipf("%s: the configuration tree is not readable", s.name)
		}
		client, base := alderSession(t, s, true)
		documents[s.name] = captureConfig(t, client, base)
	}
	for i, s := range servers {
		other := servers[(i+1)%len(servers)]
		t.Run(other.name+" snapshot against "+s.name, func(t *testing.T) {
			client, base := alderSession(t, s, true)
			r := preflightConfig(t, client, base, documents[other.name])
			if r.Overall != api.PreflightIncomplete || r.Complete {
				t.Fatalf("overall %s complete %v", r.Overall, r.Complete)
			}
			if len(r.Findings) != 1 || r.Findings[0].Code != "config_provider_mismatch" {
				t.Fatalf("findings: %s", mustEncode(t, r.Findings))
			}
			listed := false
			for _, n := range r.NotEvaluated {
				listed = listed || n.Area == "server_configuration"
			}
			if !listed {
				t.Fatal("configuration across providers is not listed as not evaluated")
			}
		})
	}
}
