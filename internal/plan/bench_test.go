package plan

import (
	"context"
	"fmt"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
)

// Planning against a large group, which is the path the planner gained most
// work on in 1.5: every modification of a membership attribute is checked
// against the values the entry holds, and now also replayed to report what it
// gains and loses.
//
// The directory here is a single in-memory entry, so these measure the
// planner and nothing else.

type benchDirectory struct{ entry *directory.Entry }

func (b benchDirectory) Read(context.Context, dn.DN, []string) (*directory.Entry, error) {
	return b.entry, nil
}

func largeGroup(b *testing.B, members int) (*directory.Entry, [][]byte) {
	b.Helper()
	target, err := dn.Parse("cn=everyone,ou=groups,dc=alder,dc=test")
	if err != nil {
		b.Fatal(err)
	}
	e := directory.NewEntry(target)
	e.Set("objectClass", [][]byte{[]byte("top"), []byte("groupOfNames")})
	e.Set("cn", [][]byte{[]byte("everyone")})
	values := make([][]byte, 0, members)
	for i := 0; i < members; i++ {
		values = append(values, []byte(fmt.Sprintf("uid=bulk%07d,ou=bulk,dc=alder,dc=test", i)))
	}
	e.Set("member", values)
	return e, values
}

func newMembers(n int) [][]byte {
	out := make([][]byte, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, []byte(fmt.Sprintf("uid=new%07d,ou=bulk,dc=alder,dc=test", i)))
	}
	return out
}

func benchPlan(b *testing.B, entry *directory.Entry, record directory.ChangeRecord, opts Options) {
	b.Helper()
	pl, err := NewPlanner(func(error) bool { return false })
	if err != nil {
		b.Fatal(err)
	}
	r := benchDirectory{entry: entry}
	records := []directory.ChangeRecord{record}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := pl.Compute(context.Background(), r, nil, records, opts); err != nil {
			b.Fatal(err)
		}
	}
}

// Adding a hundred members to a group of a hundred thousand. The question the
// planner asks is "are these already members", once per added value.
func BenchmarkPlanAddMembersToALargeGroup(b *testing.B) {
	entry, _ := largeGroup(b, 100000)
	record := directory.ChangeRecord{DN: entry.DN, Type: directory.ChangeModify,
		Mods: []directory.Mod{{Op: directory.ModAdd, Name: "member", Values: newMembers(100)}}}
	b.Run("classification", func(b *testing.B) { benchPlan(b, entry, record, Options{}) })
}

// Replacing a group of a hundred thousand with the same list less one member.
func BenchmarkPlanReplaceALargeGroup(b *testing.B) {
	entry, values := largeGroup(b, 100000)
	record := directory.ChangeRecord{DN: entry.DN, Type: directory.ChangeModify,
		Mods: []directory.Mod{{Op: directory.ModReplace, Name: "member", Values: values[1:]}}}
	b.Run("classification", func(b *testing.B) { benchPlan(b, entry, record, Options{}) })
}
