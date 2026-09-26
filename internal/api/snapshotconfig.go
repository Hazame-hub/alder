package api

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/hazame-hub/alder/internal/config"
	"github.com/hazame-hub/alder/internal/diff"
	"github.com/hazame-hub/alder/internal/session"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// Configuration snapshots and comparisons.
//
// A configuration snapshot is one server's own configuration, normalised by
// the model that server's software has. There is no portable model: a
// comparison of two providers' configuration reports the mismatch and stops,
// rather than lining up settings that have nothing to do with one another.
//
// Nothing here writes. A difference becomes a change the way every other
// difference does -- an ordinary change request, through the plan -- and only
// for the settings Alder already changes that way.

// The read a configuration side stands for, in the data-shaped fields every
// snapshot summary has.
const (
	configSideScope  = "sub"
	configSideFilter = "(objectClass=*)"
)

// captureConfigSnapshot answers a capture request for kind config.
func (s *Server) captureConfigSnapshot(c *fiber.Ctx, sess *session.Session) error {
	ctx, cancel := reqCtx(c)
	defer cancel()
	snap, err := config.Capture(ctx, sess.Conn, config.Options{})
	if err != nil {
		return configRefusal(c, s, err)
	}
	var buf bytes.Buffer
	if err := snapshot.EncodeConfig(&buf, snap); err != nil {
		return s.fail(c, err)
	}
	name := fmt.Sprintf("alder-config-snapshot-%s-%s.json", snap.Source.Provider,
		time.Now().UTC().Format("2006-01-02T150405Z"))
	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
	c.Set(fiber.HeaderContentDisposition, fmt.Sprintf("attachment; filename=%q", name))
	return c.Send(buf.Bytes())
}

// configRefusal answers a capture that could not be made.
func configRefusal(c *fiber.Ctx, s *Server, err error) error {
	switch {
	case errors.Is(err, config.ErrUnreadable):
		return writeError(c, fiber.StatusBadRequest, ErrorErrorConfigModelUnavailable,
			"This session cannot read the server's configuration.",
			"Connect with an account that may read the configuration tree; on a server that keeps it behind its own identity, that is the configuration bind.")
	case errors.Is(err, config.ErrNoModel):
		return writeError(c, fiber.StatusBadRequest, ErrorErrorConfigModelUnavailable,
			"Alder has no configuration model for this server.",
			"Configuration is provider-specific. Alder models OpenLDAP's cn=config and 389 Directory Server's, and reads no others.")
	}
	var se *snapshot.Error
	if errors.As(err, &se) {
		return snapshotRefusal(c, "", err)
	}
	return s.snapshotFail(c, err)
}

// resolvedConfigSide is one side of a configuration comparison.
type resolvedConfigSide struct {
	side      diff.ConfigSide
	integrity snapshot.Integrity
}

// diffConfig compares two configuration states.
func (s *Server) diffConfig(c *fiber.Ctx, sess *session.Session, body diffBody) error {
	sides := map[string]*resolvedConfigSide{}
	for name, side := range map[string]diffSideBody{"source": body.Source, "target": body.Target} {
		if side.Live != nil {
			l := side.Live
			if l.Base != nil || l.Scope != nil || l.Filter != nil || l.OperationalAttributes != nil || l.SchemaTarget != nil {
				return badRequest(c, "The live configuration is read whole.",
					"base, scope, filter, operationalAttributes and schemaTarget do not apply to a configuration side.")
			}
			continue
		}
		snap, integrity, err := snapshot.DecodeConfig(side.Snapshot)
		if err != nil {
			return snapshotRefusal(c, name, err)
		}
		sides[name] = &resolvedConfigSide{side: diff.ConfigSide{Snapshot: snap}, integrity: integrity}
	}

	ctx, cancel := reqCtx(c)
	defer cancel()
	for name, side := range map[string]diffSideBody{"source": body.Source, "target": body.Target} {
		if side.Live == nil {
			continue
		}
		snap, err := config.Capture(ctx, sess.Conn, config.Options{})
		if err != nil {
			return configRefusal(c, s, err)
		}
		sides[name] = &resolvedConfigSide{side: diff.ConfigSide{Snapshot: snap, Live: true}}
	}

	result := diff.CompareConfig(sides["source"].side, sides["target"].side,
		diff.ConfigOptions{IncludeUnchanged: body.IncludeUnchanged})
	return c.JSON(signedDiff(configDiffView(result, sides["source"], sides["target"]), body))
}

// configDiffView renders a configuration comparison, with a candidate change
// for each difference Alder already knows how to make.
func configDiffView(r *diff.ConfigResult, source, target *resolvedConfigSide) Diff {
	out := Diff{
		Kind:        StateKindConfig,
		Source:      configSideSummary(source),
		Target:      configSideSummary(target),
		Complete:    r.Complete,
		CrossVendor: r.CrossVendor,
		Counts: DiffCounts{
			Compared: r.Counts.Compared, Added: r.Counts.Added, Removed: r.Counts.Removed,
			Modified: r.Counts.Modified, Unchanged: r.Counts.Unchanged, Unknown: r.Counts.Unknown,
		},
		Items: []DiffItem{},
		Config: &ConfigDiff{
			ProviderMismatch: r.ProviderMismatch,
			Complete:         r.Complete,
			CrossVendor:      r.CrossVendor,
			Counts:           configCounts(r.Counts),
			Sections:         make([]ConfigSectionCounts, 0, len(r.Sections)),
			Items:            make([]ConfigDiffItem, 0, len(r.Items)),
			Objects:          configObjects(r, source),
			Source:           configProviderSummary(r.Source),
			Target:           configProviderSummary(r.Target),
		},
	}
	if r.Provider != "" {
		out.Config.Provider = ptr(ConfigProvider(r.Provider))
	}
	if len(r.Reasons) > 0 {
		reasons := make([]DiffReason, 0, len(r.Reasons))
		for _, reason := range r.Reasons {
			reasons = append(reasons, DiffReason{Code: DiffReasonCode(reason.Code), Detail: reason.Detail})
		}
		out.Reasons = &reasons
	}
	for _, section := range r.Sections {
		out.Config.Sections = append(out.Config.Sections, ConfigSectionCounts{
			Section: section.Section, Counts: configCounts(section.Counts)})
	}
	for _, item := range r.Items {
		view := ConfigDiffItem{
			Id: item.ID, Kind: DiffKind(item.Kind), Section: item.Section, Key: item.Key,
			Actionable: ConfigActionable(item.Actionable),
		}
		optional(&view.Resource, item.Resource)
		optional(&view.ResourceLabel, item.ResourceLabel)
		optional(&view.Type, item.Type)
		optional(&view.Comparison, item.Comparison)
		optional(&view.Dn, item.DN)
		if item.Mutability != "" {
			view.Mutability = ptr(ConfigMutability(item.Mutability))
		}
		if len(item.Source) > 0 {
			view.Source = ptr(item.Source)
		}
		if len(item.Target) > 0 {
			view.Target = ptr(item.Target)
		}
		if item.Ordered {
			view.Ordered = ptr(true)
		}
		if item.Sensitive {
			view.Sensitive = ptr(true)
		}
		if item.Operational {
			view.Operational = ptr(true)
		}
		if len(item.Problems) > 0 {
			view.Problems = ptr(item.Problems)
		}
		if candidate := diff.DeriveConfig(r, item, source.side); len(candidate.Records) > 0 || candidate.Blocked != "" {
			view.Candidate = candidateView(diff.Candidate{Records: candidate.Records, Blocked: candidate.Blocked})
		}
		out.Config.Items = append(out.Config.Items, view)
	}
	return out
}

// configObjects is what a comparison found about each configuration object,
// with the change Alder would send where it has one. A removal carries its
// change too, marked destructive: nothing selects one for the operator, and
// the client that asks for it by name needs something to send.
func configObjects(r *diff.ConfigResult, source *resolvedConfigSide) []ConfigDiffObject {
	out := make([]ConfigDiffObject, 0, len(r.Objects))
	for _, object := range r.Objects {
		view := ConfigDiffObject{
			Id: object.ID, Kind: DiffKind(object.Kind), Section: object.Section, Object: object.Object,
			Name: object.Name, Settings: object.Settings, Actionable: ConfigActionable(object.Actionable),
		}
		optional(&view.Label, object.Label)
		optional(&view.Dn, object.DN)
		optional(&view.Refusal, object.Refusal)
		if object.Destructive {
			view.Destructive = ptr(true)
		}
		if source != nil && object.Actionable == diff.ActionableWritable {
			candidate := diff.DeriveConfigObject(r, object, source.side, object.Destructive)
			if len(candidate.Records) > 0 || candidate.Blocked != "" {
				view.Candidate = candidateView(diff.Candidate{Records: candidate.Records,
					Blocked: candidate.Blocked, Destructive: object.Destructive})
			}
		}
		out = append(out, view)
	}
	return out
}

func configCounts(c diff.ConfigCounts) ConfigDiffCounts {
	return ConfigDiffCounts{Compared: c.Compared, Added: c.Added, Removed: c.Removed,
		Modified: c.Modified, Unchanged: c.Unchanged, Unknown: c.Unknown, Actionable: c.Actionable}
}

func configProviderSummary(s diff.ConfigProviderSummary) ConfigProviderSummary {
	out := ConfigProviderSummary{Provider: ConfigProvider(s.Provider), Completeness: s.Completeness,
		Settings: s.Settings, Resources: s.Resources, Sections: make([]ConfigSectionTotals, 0, len(s.Sections))}
	optional(&out.Vendor, s.Vendor)
	optional(&out.VendorVersion, s.VendorVersion)
	optional(&out.Root, s.Root)
	for _, section := range s.Sections {
		out.Sections = append(out.Sections, ConfigSectionTotals{Section: section.Section, Settings: section.Settings})
	}
	return out
}

// configSideSummary describes a configuration side in the data-shaped fields
// every comparison has: how many settings it holds, and where they were read
// from.
func configSideSummary(side *resolvedConfigSide) DiffSideSummary {
	snap := side.side.Snapshot
	out := DiffSideSummary{
		Kind: DiffSideKindSnapshot, Base: snap.Source.Root, Scope: SnapshotScope(configSideScope),
		Filter: configSideFilter, OperationalAttributes: false, EntryCount: snap.Counts.Settings,
		DefinitionCount: ptr(snap.Counts.Resources), Vendor: ptrIfSet(snap.Source.Vendor),
	}
	if side.side.Live {
		out.Kind = DiffSideKindLive
		return out
	}
	out.CreatedAt = ptr(snap.CreatedAt)
	out.Checksum = ptr(snap.Checksum)
	out.Integrity = ptr(SnapshotIntegrity(side.integrity))
	return out
}

func optional(field **string, value string) {
	if strings.TrimSpace(value) != "" {
		*field = ptr(value)
	}
}

// GetConfigEntryModel answers what the provider's configuration model says
// about one entry of the server's own configuration.
//
// The editor asks this when the entry it has open is in the configuration
// tree. It is the same model a capture and a comparison read, so the two
// cannot disagree about a setting -- and it reads the tree for the same reason
// a capture does: which settings need a restart, and which plugins the server
// can run without, are facts about this server rather than a list in Alder.
func (s *Server) GetConfigEntryModel(c *fiber.Ctx, params GetConfigEntryModelParams) error {
	sess := s.require(c)
	if sess == nil {
		return nil
	}
	target, ok := parseDNParam(c, params.Dn)
	if !ok {
		return nil
	}
	ctx, cancel := reqCtx(c)
	defer cancel()

	model, err := config.Describe(ctx, sess.Conn, target)
	if errors.Is(err, config.ErrNotConfiguration) {
		return writeError(c, fiber.StatusNotFound, ErrorErrorNotFound,
			"That entry is not part of this server's configuration model.",
			"A configuration model covers the server's own configuration tree. The schema, the task queue and a monitor live there too, and are not configuration.")
	}
	if err != nil {
		return configRefusal(c, s, err)
	}

	out := ConfigEntryModel{
		Dn: model.DN, Provider: ConfigProvider(model.Provider),
		Resource:   configResource(model.Resource),
		Attributes: make([]ConfigAttributeModel, 0, len(model.Attributes)),
	}
	out.Incomplete = ptrIfTrue(model.Incomplete)
	for _, a := range model.Attributes {
		out.Attributes = append(out.Attributes, ConfigAttributeModel{
			Name: a.Name, Section: a.Section, Mutability: ConfigMutability(a.Mutability),
			RestartRequired: ptrIfTrue(a.RestartRequired), Sensitive: ptrIfTrue(a.Sensitive),
			Excluded: ptrIfTrue(a.Excluded),
		})
	}
	return c.JSON(out)
}

// configResource is one resource on the wire.
func configResource(r snapshot.ConfigResource) ConfigResource {
	out := ConfigResource{Section: r.Section, Kind: r.Kind, Name: r.Name}
	out.Dn = ptrIfSet(r.DN)
	out.Label = ptrIfSet(r.Label)
	return out
}
