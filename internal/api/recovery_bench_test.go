package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
)

// What asking for a recovery bundle costs an apply.
//
// Each benchmark plans once, then applies the same planned set over and over
// against a directory reset before every run, with and without a bundle. The
// difference is the whole cost of recovery: the wider verification read, any
// read after the first change, the derivation, and the bundle's encoding into
// the response. reads/op is how many entry reads the apply made, which is the
// number that matters against a real server.

func benchRig(b *testing.B, entries func() []*directory.Entry) (*testRig, *memDirectory) {
	b.Helper()
	byDN := map[string]*directory.Entry{}
	for _, e := range entries() {
		byDN[memKey(e.DN)] = e
	}
	mem := &memDirectory{fakeSession: &fakeSession{caps: defaultCaps(), sch: testSchema(b), byDN: byDN}}
	rig := newRig(b, Config{}, mem.fakeSession)
	// Re-register the session against the stateful directory.
	sess, err := rig.server.sessions.Add(mem, directory.ConnConfig{
		Host: "ldap.example.test", Port: 636, TLS: directory.TLSModeLDAPS,
		BindDN: "cn=admin,dc=alder,dc=test", BindPassword: sentinelPassword,
	}, false)
	if err != nil {
		b.Fatal(err)
	}
	rig.cookie = sess.ID
	return rig, mem
}

func benchPerson(b *testing.B, d string, extra ...[]string) *directory.Entry {
	b.Helper()
	e := directory.NewEntry(mustParseTB(b, d))
	e.Set("objectClass", [][]byte{[]byte("top"), []byte("person"), []byte("inetOrgPerson")})
	e.Set("cn", [][]byte{[]byte("Bench")})
	e.Set("sn", [][]byte{[]byte("Mark")})
	for _, p := range extra {
		values := make([][]byte, 0, len(p)-1)
		for _, v := range p[1:] {
			values = append(values, []byte(v))
		}
		e.Set(p[0], values)
	}
	return e
}

func runApplyBench(b *testing.B, entries func() []*directory.Entry, changes []ChangeRequest) {
	for _, withRecovery := range []bool{false, true} {
		b.Run(fmt.Sprintf("recovery=%v", withRecovery), func(b *testing.B) {
			rig, mem := benchRig(b, entries)
			planBody, _ := json.Marshal(map[string]any{"changes": changes})
			res := rig.do(b, http.MethodPost, "/api/v1/plan", bytes.NewReader(planBody))
			if res.Status != http.StatusOK {
				b.Fatalf("plan: %d %s", res.Status, res.Body)
			}
			var p Plan
			_ = json.Unmarshal([]byte(res.Body), &p)
			send := make([]ChangeRequest, 0, len(p.Items))
			for _, item := range p.Items {
				if item.Baseline == nil {
					b.Fatalf("item %d does not apply: %s", item.Index, item.Action)
				}
				c := changes[item.Index]
				c.Baseline = item.Baseline
				send = append(send, c)
			}
			applyBody, _ := json.Marshal(map[string]any{"changes": send, "recovery": withRecovery})

			reads, bundleBytes := 0, 0
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				fresh := map[string]*directory.Entry{}
				for _, e := range entries() {
					fresh[memKey(e.DN)] = e
				}
				mem.byDN = fresh
				mem.readDNs = nil
				mem.applied = nil
				b.StartTimer()
				res := rig.do(b, http.MethodPost, "/api/v1/changeset/apply", bytes.NewReader(applyBody))
				b.StopTimer()
				if res.Status != http.StatusOK || strings.Contains(res.Body, `"failedIndex"`) {
					b.Fatalf("apply: %d %s", res.Status, res.Body)
				}
				reads += len(mem.readDNs)
				if withRecovery {
					var r struct {
						Recovery json.RawMessage `json:"recovery"`
					}
					_ = json.Unmarshal([]byte(res.Body), &r)
					bundleBytes += len(r.Recovery)
				}
				b.StartTimer()
			}
			b.ReportMetric(float64(reads)/float64(b.N), "reads/op")
			b.ReportMetric(float64(bundleBytes)/float64(b.N), "bundle-bytes/op")
		})
	}
}

func BenchmarkRecoveryOneModification(b *testing.B) {
	const target = "uid=bench,ou=people,dc=alder,dc=test"
	entries := func() []*directory.Entry {
		return []*directory.Entry{benchPerson(b, target, []string{"description", "before"})}
	}
	runApplyBench(b, entries, []ChangeRequest{{
		Dn: target, Type: ChangeRequestTypeModify,
		Mods: &[]ChangeMod{{Op: ChangeModOpReplace, Name: "description", Values: &[]AttributeValue{{Text: ptr("after")}}}},
	}})
}

func BenchmarkRecoveryHundredChanges(b *testing.B) {
	entries := func() []*directory.Entry {
		out := make([]*directory.Entry, 0, 100)
		for i := 0; i < 100; i++ {
			out = append(out, benchPerson(b, fmt.Sprintf("uid=bench%03d,ou=people,dc=alder,dc=test", i),
				[]string{"description", "before"}, []string{"title", "t"}))
		}
		return out
	}
	changes := make([]ChangeRequest, 0, 100)
	for i := 0; i < 100; i++ {
		changes = append(changes, ChangeRequest{
			Dn: fmt.Sprintf("uid=bench%03d,ou=people,dc=alder,dc=test", i), Type: ChangeRequestTypeModify,
			Mods: &[]ChangeMod{
				{Op: ChangeModOpReplace, Name: "description", Values: &[]AttributeValue{{Text: ptr("after")}}},
				{Op: ChangeModOpAdd, Name: "mail", Values: &[]AttributeValue{{Text: ptr(fmt.Sprintf("b%d@alder.test", i))}}},
			},
		})
	}
	runApplyBench(b, entries, changes)
}

func BenchmarkRecoveryLargeGroup(b *testing.B) {
	const group = "cn=everyone,ou=groups,dc=alder,dc=test"
	const members = 5000
	entries := func() []*directory.Entry {
		g := directory.NewEntry(mustParseTB(b, group))
		g.Set("objectClass", [][]byte{[]byte("top"), []byte("groupOfNames")})
		g.Set("cn", [][]byte{[]byte("everyone")})
		values := make([][]byte, 0, members)
		for i := 0; i < members; i++ {
			values = append(values, []byte(fmt.Sprintf("uid=member%05d,ou=people,dc=alder,dc=test", i)))
		}
		g.Set("member", values)
		return []*directory.Entry{g}
	}
	runApplyBench(b, entries, []ChangeRequest{{
		Dn: group, Type: ChangeRequestTypeModify,
		Mods: &[]ChangeMod{
			{Op: ChangeModOpAdd, Name: "member", Values: &[]AttributeValue{{Text: ptr("uid=newcomer,ou=people,dc=alder,dc=test")}}},
			{Op: ChangeModOpDelete, Name: "member", Values: &[]AttributeValue{{Text: ptr("uid=member00042,ou=people,dc=alder,dc=test")}}},
		},
	}})
}
