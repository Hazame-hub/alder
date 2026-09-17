package preflight

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/hazame-hub/alder/internal/diff"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/schema"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// maxFallbackReads bounds the entry reads a data snapshot preflight makes when
// the target's subtree could not be read in one capture.
const maxFallbackReads = 5000

// SchemaSnapshot preflights a schema snapshot against a target: which of its
// definitions the target already has, which it could take, which it holds with
// another meaning, and which it cannot represent.
//
// The target's own definitions that the snapshot does not hold are not
// findings. A preflight asks what would carry across, not what the target
// would lose.
func SchemaSnapshot(ctx context.Context, s *snapshot.SchemaSnapshot, integrity string, t Target, opts Options) (*Report, error) {
	caps := t.Capabilities()
	b := newBuilder(SourceInfo{Type: SourceSchemaSnapshot, Format: s.Format, Version: s.Version, Kind: s.Kind, Checksum: s.Checksum,
		Integrity: integrity, Vendor: s.Source.Vendor, VendorVersion: s.Source.VendorVersion,
		Objects: len(s.AttributeTypes) + len(s.ObjectClasses)}, targetInfo(caps, s.Source.Vendor), opts.now())

	if s.Completeness != snapshot.SchemaComplete {
		b.incomplete(CodeSourcePartial)
		b.add(Finding{ID: "artifact:partial", Code: CodeSourcePartial, Classification: Unknown, Category: CategoryArtifact, Scope: ScopeArtifact,
			Count:       len(s.Unparsed),
			Explanation: strconv.Itoa(len(s.Unparsed)) + " definition(s) in the snapshot could not be parsed when it was captured, so what they define is not known."})
	}
	// A target schema that cannot be read is a finding, not a failure: the
	// report says nothing could be judged, and is incomplete.
	target := readableSchema(ctx, t)
	if target == nil {
		b.incomplete("target_schema_unreadable")
		b.add(Finding{ID: "artifact:target-schema", Code: CodeDefinitionUnknown, Classification: Unknown, Category: CategoryArtifact,
			Scope: ScopeArtifact, Target: &TargetFact{Fact: "schema_unreadable"},
			Explanation: "The target's schema could not be read, so no definition could be judged."})
		return b.finish(), nil
	}
	live, err := liveSchemaSnapshot(caps, target)
	if err != nil {
		return nil, err
	}
	compared := diff.CompareSchema(diff.SchemaSide{Snapshot: live, Live: true}, diff.SchemaSide{Snapshot: s}, diff.SchemaOptions{IncludeUnchanged: true, WithoutOrder: true})
	if live.Completeness != snapshot.SchemaComplete {
		b.incomplete("target_schema_partial")
	}

	defs := definitionsOfSnapshot(s)
	eval := newSchemaEval(b, target, caps, compared, defs)
	order := make([]string, 0, len(defs))
	for _, d := range defs {
		order = append(order, d.key())
	}
	sort.Strings(order)
	eval.all(order)
	return b.finish(), nil
}

// readableSchema is the target's schema, or nil when it cannot be read; the
// caller reports which.
func readableSchema(ctx context.Context, t Target) *schema.Schema {
	sch, err := t.RefreshSchema(ctx)
	if err != nil {
		return nil
	}
	return sch
}

// DataSnapshot preflights a data snapshot against a target: whether each entry
// it holds could be represented there.
//
// A snapshot is state, not intent. An entry it does not hold is not one to
// remove, and an entry only the target has is not a finding; nothing here
// reconciles anything.
func DataSnapshot(ctx context.Context, s *snapshot.Snapshot, integrity string, t Target, opts Options) (*Report, error) {
	caps := t.Capabilities()
	b := newBuilder(SourceInfo{Type: SourceDataSnapshot, Format: s.Format, Version: s.Version, Kind: s.Kind, Checksum: s.Checksum,
		Integrity: integrity, Vendor: s.Source.Vendor, VendorVersion: s.Source.VendorVersion, Objects: len(s.Entries)},
		targetInfo(caps, s.Source.Vendor), opts.now())

	target := readableSchema(ctx, t)
	if target == nil {
		b.incomplete("target_schema_unreadable")
	}
	eval := newSchemaEval(b, target, caps, nil, nil)
	check := newEntryCheck(b, caps, target, eval, newPresence(t, opts.NotFound))

	// The target's entries under the same base, read once, so that most
	// questions -- is it there, does it differ, is its parent there -- are
	// answered without a read per entry.
	kinds := map[string]diff.Item{}
	captured := false
	base, baseErr := dn.Parse(s.Source.Base)
	if baseErr == nil && opts.Capture != nil {
		if inside, known := inNamingContexts(caps, base); !known || inside {
			b.require("paged_results", caps.Paging, "reading the target's entries under "+s.Source.Base,
				"A data snapshot is compared with the target's entries under the same base, read in pages.")
			live, truncated, err := opts.Capture(ctx, base, s.Source.Scope, s.Source.Filter)
			if err == nil {
				compared, err := diff.Compare(ctx, diff.Side{Snapshot: live, Live: true, Truncated: truncated}, diff.Side{Snapshot: s},
					diff.Options{IncludeUnchanged: true, MaxValues: 20})
				if err == nil {
					captured = true
					check.targetDNs = map[string]bool{}
					for i := range live.Entries {
						check.targetDNs[snapshot.DNKey(live.DNAt(i))] = true
					}
					for _, it := range compared.Items {
						if it.TargetDN == "" {
							continue
						}
						if parsed, err := dn.Parse(it.TargetDN); err == nil {
							kinds[snapshot.DNKey(parsed)] = it
						}
					}
					if truncated {
						b.incomplete("target_read_truncated")
					}
				}
			}
			if !captured {
				b.incomplete("target_capture_failed")
				b.add(Finding{ID: "artifact:capture", Code: CodeEntryUnknown, Classification: Unknown, Category: CategoryArtifact,
					Scope: ScopeArtifact, Target: &TargetFact{Fact: "capture_failed"},
					Explanation: "The target's entries under " + s.Source.Base + " could not be read in one capture; each entry is read on its own, within a bound."})
			}
		}
	}

	identities := 0
	fallbackReads := 0
	var inputs []*entryInput
	for i := range s.Entries {
		e := &s.Entries[i]
		parsed := s.DNAt(i)
		in := &entryInput{dn: e.DN, parsed: parsed, valid: true, mode: "snapshot"}
		for _, a := range e.Attributes {
			info, _ := s.Info(a.Name)
			values := make([][]byte, 0, len(a.Values))
			for _, v := range a.Values {
				if raw, err := v.Bytes(); err == nil {
					values = append(values, raw)
				}
			}
			in.attrs = append(in.attrs, contentAttribute{name: a.Name, values: values, withheld: a.Withheld, operational: info.Operational})
		}
		if e.ID != "" {
			identities++
		}
		key := snapshot.DNKey(parsed)
		id := "entry:" + key
		main := Finding{ID: id, Category: CategoryEntries, Scope: ScopeEntry, Source: SourceRef{DN: e.DN}}

		var worst Classification
		var causes []string
		block := func(class Classification, found string) {
			if found == "" {
				return
			}
			worst = worse(worst, class)
			causes = append(causes, found)
		}
		block(check.naming(in))

		state := "absent"
		if worst == "" {
			if captured {
				if it, ok := kinds[key]; ok {
					switch it.Kind {
					case diff.Unchanged:
						state = "equivalent"
					case diff.Modified, diff.Renamed:
						state = "differs"
						main.Target = &TargetFact{Fact: "present_different", Detail: changedAttributes(it)}
					case diff.Unknown:
						state = "unknown"
						main.Target = &TargetFact{Fact: "not_comparable", Detail: it.Reason}
					}
				}
			} else if fallbackReads < maxFallbackReads {
				fallbackReads++
				switch check.presence.of(ctx, parsed).state {
				case PresencePresent:
					// There, and not compared: whether it matches is not known.
					state = "unknown"
					main.Target = &TargetFact{Fact: "present_not_compared"}
				case PresenceHidden, PresenceUnknown:
					state = "unknown"
				}
			} else {
				state = "unknown"
				b.incomplete("entry_read_budget")
			}
		}

		switch {
		case worst != "":
		case state == "equivalent":
			check.notes(in)
			main.Code, main.Classification = CodeEntryPresent, AlreadySatisfied
			main.Target = &TargetFact{Fact: "present_equivalent"}
			main.Explanation = "The target already holds this entry with the same content, compared by each attribute's matching rule."
			check.provided[key], check.outcome[key] = b.add(main), AlreadySatisfied
			continue
		case state == "unknown":
			main.Code, main.Classification = CodeEntryUnknown, Unknown
			main.Explanation = "Whether the target holds this entry could not be decided."
			check.provided[key], check.outcome[key] = b.add(main), Unknown
			continue
		case state == "absent":
			block(check.parent(ctx, in))
			ws, cs := check.content(in, nil, directory.ChangeAdd, nil)
			for _, c := range cs {
				block(ws, c)
			}
		default:
			ws, cs := check.content(in, nil, directory.ChangeAdd, nil)
			for _, c := range cs {
				block(ws, c)
			}
		}

		main.Causes = causes
		switch {
		case worst != "":
			main.Code, main.Classification = CodeEntryBlocked, worst
			main.BlocksPortability = worst != Unknown
			main.Explanation = "This entry cannot be carried to this target as it stands; the findings it links to say why."
		case state == "absent":
			main.Code, main.Classification = CodeEntryPortable, Portable
			main.Target = &TargetFact{Fact: "absent"}
			main.Explanation = "The target does not hold this entry, and could."
		default:
			main.Code, main.Classification, main.ManualAction = CodeEntryDiffers, PrerequisiteRequired, true
			if main.Target == nil {
				main.Target = &TargetFact{Fact: "present_different"}
			}
			main.Explanation = "The target already has an entry at this DN with different content. Whether to change it is a decision for a plan, not for a preflight."
		}
		check.provided[key], check.outcome[key] = b.add(main), main.Classification
		if main.Classification == Portable || main.Code == CodeEntryDiffers {
			inputs = append(inputs, in)
		}
	}
	for _, in := range inputs {
		check.references(ctx, in)
	}
	check.flush()

	if identities > 0 {
		b.add(Finding{ID: "operational:identity", Code: CodeTargetGenerated, Classification: Excluded, Category: CategoryOperational,
			Scope: ScopeArtifact, Count: identities,
			Explanation: strconv.Itoa(identities) + " entries carry an identity their server assigned. The target assigns its own, so an identity cannot be used to recognise a migrated entry."})
	}
	if s.Source.Filter != "" && !strings.EqualFold(strings.TrimSpace(s.Source.Filter), "(objectClass=*)") {
		b.add(Finding{ID: "artifact:filter", Code: CodeSourcePartial, Classification: Excluded, Category: CategoryArtifact, Scope: ScopeArtifact,
			Explanation: "The snapshot was captured with the filter " + s.Source.Filter + ", so parents and referenced entries it did not select are looked for on the target, not in the snapshot."})
	}
	return b.finish(), nil
}

// changedAttributes names the attributes a comparison found different.
func changedAttributes(it diff.Item) string {
	names := make([]string, 0, len(it.Attributes))
	for _, a := range it.Attributes {
		if a.Kind == diff.Unchanged {
			continue
		}
		names = append(names, a.Name)
	}
	if it.Kind == diff.Renamed {
		names = append(names, "renamed from "+it.SourceDN)
	}
	return bound(strings.Join(names, ", "), MaxFactRunes)
}
