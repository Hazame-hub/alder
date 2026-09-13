package plan

import (
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
)

// The membership report, which did not exist before 1.5 and so has no "before".
// Kept apart from bench_test.go so that file still compiles against 1.4 for the
// before-and-after comparison of classification alone.

var membershipOptions = Options{MembershipAttributes: []string{"member", "uniqueMember", "memberUid"}}

func BenchmarkPlanAddMembersToALargeGroupWithMembership(b *testing.B) {
	entry, _ := largeGroup(b, 100000)
	record := directory.ChangeRecord{DN: entry.DN, Type: directory.ChangeModify,
		Mods: []directory.Mod{{Op: directory.ModAdd, Name: "member", Values: newMembers(100)}}}
	benchPlan(b, entry, record, membershipOptions)
}

func BenchmarkPlanReplaceALargeGroupWithMembership(b *testing.B) {
	entry, values := largeGroup(b, 100000)
	record := directory.ChangeRecord{DN: entry.DN, Type: directory.ChangeModify,
		Mods: []directory.Mod{{Op: directory.ModReplace, Name: "member", Values: values[1:]}}}
	benchPlan(b, entry, record, membershipOptions)
}
