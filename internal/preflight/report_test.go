package preflight

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/changepkg"
	"github.com/hazame-hub/alder/internal/snapshot"
)

func TestTheOverallResultIsDerivedFromTheFindings(t *testing.T) {
	f := func(class Classification, blocks, manual bool) Finding {
		return Finding{Classification: class, BlocksPortability: blocks, ManualAction: manual}
	}
	cases := []struct {
		name     string
		findings []Finding
		want     Overall
	}{
		{"nothing to say", nil, Compatible},
		{"portable and satisfied", []Finding{f(Portable, false, false), f(AlreadySatisfied, false, false), f(Excluded, false, false)}, Compatible},
		{"a prerequisite", []Finding{f(Portable, false, false), f(PrerequisiteRequired, true, true)}, CompatibleWithPrerequisite},
		{"manual work", []Finding{f(Excluded, false, true)}, CompatibleWithPrerequisite},
		{"an unknown", []Finding{f(PrerequisiteRequired, true, true), f(Unknown, false, false)}, Incomplete},
		{"incompatible beats unknown", []Finding{f(Unknown, false, false), f(Incompatible, true, false)}, NotCompatible},
		{"unsupported", []Finding{f(Unsupported, true, false)}, NotCompatible},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := conclude(tc.findings); got != tc.want {
				t.Errorf("conclude = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestAnUnknownKeepsTheReportFromBeingComplete(t *testing.T) {
	b := newBuilder(SourceInfo{Type: SourceChangePackage, Objects: 2}, TargetInfo{}, "2026-09-17T00:00:00Z")
	b.add(Finding{ID: "a", Code: CodeDefinitionPortable, Classification: Portable, Category: CategorySchema})
	b.add(Finding{ID: "b", Code: CodeReferenceUnknown, Classification: Unknown, Category: CategoryReferences})
	r := b.finish()
	if r.Complete || r.Overall != Incomplete || len(r.Reasons) != 1 || r.Reasons[0] != CodeReferenceUnknown {
		t.Fatalf("complete %v overall %s reasons %v", r.Complete, r.Overall, r.Reasons)
	}
}

func TestNotEvaluatedAreasAreAlwaysListed(t *testing.T) {
	r := newBuilder(SourceInfo{Objects: 1}, TargetInfo{}, "").finish()
	areas := map[string]bool{}
	for _, n := range r.NotEvaluated {
		areas[n.Area] = true
	}
	for _, want := range []string{"access_control", "server_configuration", "secret_values"} {
		if !areas[want] {
			t.Errorf("%s is not listed as not evaluated", want)
		}
	}
	if r.Overall != Compatible || !r.Complete {
		t.Errorf("an empty report: overall %s complete %v", r.Overall, r.Complete)
	}
}

func TestFindingsAreOrderedAndIdentifiedByContent(t *testing.T) {
	target := withManager(t)
	first := preflightPackage(t, mixed(t), target)
	second := preflightPackage(t, mixed(t), withManager(t))
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if !bytes.Equal(a, b) {
		t.Fatalf("the same source against the same target gave different reports\n%s\n%s", a, b)
	}
	for i, f := range first.Findings {
		if f.ID != "f"+strconv.Itoa(i+1) {
			t.Fatalf("finding %d has id %s", i, f.ID)
		}
		for _, c := range f.Causes {
			if byID(first, c) == nil {
				t.Fatalf("finding %s names a cause %s that is not in the report", f.ID, c)
			}
		}
	}
}

func TestAReportIsBoundedAndSaysSo(t *testing.T) {
	b := newBuilder(SourceInfo{Objects: MaxFindings + 10}, TargetInfo{}, "")
	long := strings.Repeat("x", MaxExplanationRune*4)
	for i := 0; i < MaxFindings+10; i++ {
		b.add(Finding{ID: strconv.Itoa(i), Code: CodeEntryPortable, Classification: Portable, Category: CategoryEntries, Explanation: long,
			Causes: make([]string, MaxCauses*3)})
	}
	r := b.finish()
	if len(r.Findings) != MaxFindings || !r.Truncated || r.Complete || r.Overall != Incomplete {
		t.Fatalf("findings %d truncated %v complete %v overall %s", len(r.Findings), r.Truncated, r.Complete, r.Overall)
	}
	if n := len([]rune(r.Findings[0].Explanation)); n > MaxExplanationRune+1 {
		t.Errorf("an explanation of %d runes survived", n)
	}
}

func TestArtifactsAreRecognisedByWhatTheySayTheyAre(t *testing.T) {
	cases := map[string]struct {
		doc  string
		want string
		err  error
	}{
		"package":        {`{"format":"alder-change-package","version":1}`, SourceChangePackage, nil},
		"schema":         {`{"format":"alder-snapshot","version":1,"kind":"schema"}`, SourceSchemaSnapshot, nil},
		"data":           {`{"format":"alder-snapshot","version":1,"kind":"data"}`, SourceDataSnapshot, nil},
		"config":         {`{"format":"alder-snapshot","version":1,"kind":"config"}`, SourceConfigSnapshot, nil},
		"unknown kind":   {`{"format":"alder-snapshot","version":1,"kind":"acl"}`, "", ErrUnsupportedKind},
		"recovery":       {`{"format":"alder-recovery","version":1}`, "", ErrNotArtifact},
		"not json":       {`uid=alice,dc=example`, "", ErrNotArtifact},
		"ldif-ish":       {"dn: cn=x\nchangetype: add\n", "", ErrNotArtifact},
		"array":          {`[{"format":"alder-change-package"}]`, "", ErrNotArtifact},
		"format missing": {`{"version":1}`, "", ErrNotArtifact},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := Detect([]byte(tc.doc))
			if got != tc.want || !errors.Is(err, tc.err) {
				t.Errorf("Detect = %q, %v; want %q, %v", got, err, tc.want, tc.err)
			}
		})
	}
}

func TestRunRefusesMalformedAndForgedArtifactsWithTheirOwnCodes(t *testing.T) {
	target := withManager(t)
	var buf bytes.Buffer
	if err := changepkg.Encode(&buf, mixed(t)); err != nil {
		t.Fatal(err)
	}
	good := buf.String()

	// A forged checksum.
	forged := strings.Replace(good, `"sha256:`, `"sha256:00`, 1)
	if _, err := Run(context.Background(), []byte(forged), target, Options{NotFound: notFound}); err == nil {
		t.Fatal("a package with a forged checksum was preflighted")
	} else {
		var pkgErr *changepkg.Error
		if !errors.As(err, &pkgErr) || pkgErr.Code != changepkg.CodeChecksumMismatch {
			t.Errorf("forged: %v", err)
		}
	}
	// A smuggled plan token.
	smuggled := strings.Replace(good, `"title":`, `"baseline":"token","title":`, 1)
	if _, err := Run(context.Background(), []byte(smuggled), target, Options{NotFound: notFound}); err == nil {
		t.Fatal("a package carrying a field it does not define was preflighted")
	}
	// A snapshot with a version from the future.
	future := `{"format":"alder-snapshot","version":99,"kind":"data"}`
	if _, err := Run(context.Background(), []byte(future), target, Options{NotFound: notFound}); err == nil {
		t.Fatal("a snapshot version from the future was preflighted")
	} else {
		var snapErr *snapshot.Error
		if !errors.As(err, &snapErr) || snapErr.Code != snapshot.CodeUnsupportedVersion {
			t.Errorf("future: %v", err)
		}
	}
	if target.writes != 0 {
		t.Fatal("a refused artifact caused a write")
	}

	// And the real one runs, and leaves the bytes it was given alone.
	data := []byte(good)
	copyOf := append([]byte(nil), data...)
	r, err := Run(context.Background(), data, target, Options{NotFound: notFound})
	if err != nil || r.Source.Type != SourceChangePackage || r.Source.Integrity != "verified" {
		t.Fatalf("run: %v %+v", err, r)
	}
	if !bytes.Equal(data, copyOf) {
		t.Fatal("the artifact's bytes changed during a preflight")
	}
}

func TestAnEmptyArtifactIsNotAPass(t *testing.T) {
	r := newBuilder(SourceInfo{Type: SourceChangePackage}, TargetInfo{}, "").finish()
	if r.Overall != Incomplete || r.Complete || len(find(r, CodeSourceEmpty, nil)) != 1 {
		t.Fatalf("overall %s complete %v\n%s", r.Overall, r.Complete, describe(r))
	}
}

func TestHostileTextIsBoundedAndCarriedAsData(t *testing.T) {
	target := withManager(t)
	evil := "cn=\u202egnp.exe\u0007" + strings.Repeat("A", 2000) + ",ou=people,dc=alder,dc=test"
	p := buildPackage(t, addEntry("c1", evil, attr("objectClass", "top", "person"), attr("cn", "x"), attr("sn", "y")))
	r := preflightPackage(t, p, target)
	for _, f := range r.Findings {
		if len([]rune(f.Explanation)) > MaxExplanationRune+1 {
			t.Fatalf("an explanation grew past its bound: %d runes", len([]rune(f.Explanation)))
		}
		if f.Target != nil && len([]rune(f.Target.Detail)) > MaxFactRunes+1 {
			t.Fatalf("a target fact grew past its bound")
		}
	}
	if _, err := json.Marshal(r); err != nil {
		t.Fatal(err)
	}
}

func TestAConcealingServerCannotBeToldApartAndTheReportSaysHowItKnows(t *testing.T) {
	// The server denies the parent exists exactly as it would a missing one.
	// Nothing can tell those apart; the finding says it is the server's account,
	// to this bind.
	target := newTarget(t, targetSchema(t, nil, nil), baseEntries(t)[0])
	target.concealed[strings.ToLower(peopleDN)] = true
	r := preflightPackage(t, buildPackage(t, addEntry("c1", "uid=kim,ou=people,dc=alder,dc=test",
		attr("objectClass", "top", "person"), attr("cn", "Kim"), attr("sn", "K"))), target)
	f := one(t, r, CodeParentMissing, byItem("c1"))
	if !strings.Contains(f.Explanation, "as the server reports it to this bind") {
		t.Errorf("the explanation claims more than the server said: %q", f.Explanation)
	}
}
