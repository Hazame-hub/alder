package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/spf13/pflag"

	"github.com/hazame-hub/alder/internal/apiclient"
	"github.com/hazame-hub/alder/internal/project"
)

// A saved plan is the plan alder project plan or alder plan showed, kept in a
// file so that a later run can apply exactly that plan or refuse
// (docs/proposals/saved-plans.md).
//
// It carries no baseline. A baseline is a MAC under a key that exists only in
// the Alder process that issued it, so a file of them would stop applying at
// the first restart, and Alder keeps no key that outlives a process. What it
// carries instead is what a recovery bundle carries: exact operations, each
// with the state it expects to find, which any Alder can check. Applying it
// plans those operations as they are written and refuses if any no longer
// holds; what is applied then is applied with the baselines of that plan.

const (
	savedPlanFormat  = "alder-plan"
	savedPlanVersion = 1
)

type savedPlan struct {
	Format    string    `json:"format"`
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"createdAt"`
	// AlderVersion is the client that made it. Informational: the format
	// version is what a reader checks.
	AlderVersion string `json:"alderVersion"`
	// Project is set on a plan alder project plan saved, and Input on one
	// alder plan saved: exactly one of them. Each is applied only by its own
	// command, which is how a project's plan is never applied without the
	// project's checks.
	Project     *savedPlanProject         `json:"project,omitempty"`
	Input       *savedPlanInput           `json:"input,omitempty"`
	Environment savedPlanEnvironment      `json:"environment"`
	Changes     []apiclient.ChangeRequest `json:"changes"`
	// Unchanged is how many entries the plan found as the project describes
	// them, for the summary. Nothing is checked against it.
	Unchanged int `json:"unchanged"`
}

type savedPlanProject struct {
	// Digest is the SHA-256 of the project's entries as its files describe
	// them, before any environment's suffix is applied. An apply refuses a
	// project that no longer renders to it.
	Digest string `json:"digest"`
}

// savedPlanInput is what alder plan was given. Informational: the changes are
// what is applied, and an apply needs nothing but the file.
type savedPlanInput struct {
	// Kind is ldif or changes.
	Kind string `json:"kind"`
	// Mode is how an LDIF document was read: changes or desired.
	Mode string `json:"mode,omitempty"`
	// Digest is the SHA-256 of the input as it was read, so a reviewer can
	// tell which file the plan came from.
	Digest string `json:"digest"`
}

type savedPlanEnvironment struct {
	// Name is the project's environment. A plan alder plan saved has none:
	// its directory is the host and bind below.
	Name   string `json:"name,omitempty"`
	Host   string `json:"host"`
	BindDN string `json:"bindDn"`
	// ConfigBindDN is the identity the configuration tree (cn=config, the
	// schema) was read and would be written as, absent when the plan was
	// made with none. An apply under another one, or with one where there
	// was none, or with none where there was one, is refused: the plan's
	// configuration changes were reviewed as that identity's.
	ConfigBindDN string           `json:"configBindDn,omitempty"`
	Rebase       *savedPlanRebase `json:"rebase,omitempty"`
}

type savedPlanRebase struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// projectDigest is what a saved plan binds the project by: its entries, as
// the files describe them.
func projectDigest(p *project.Project) (string, error) {
	document, err := projectDocument(p)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(document))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// environmentOf is what a saved plan records about where it was made, and
// what an apply compares.
func environmentOf(p *project.Project, envName string, conn *connection) savedPlanEnvironment {
	e := savedPlanEnvironment{Name: envName, Host: conn.label(), BindDN: conn.bindDN, ConfigBindDN: conn.configBindDN}
	if from, to, ok := p.Rebasing(envName); ok {
		e.Rebase = &savedPlanRebase{From: from.String(), To: to.String()}
	}
	return e
}

// savePlan turns the plan of a project into a saved plan and writes it. The
// caller has checked that every change in the plan can be applied as written.
func savePlan(ctx context.Context, env *Env, r *remote, p *project.Project, envName string, conn *connection,
	plan apiclient.Plan, path string, force bool) error {
	digest, err := projectDigest(p)
	if err != nil {
		return err
	}
	saved := newSavedPlan(env, plan)
	saved.Project = &savedPlanProject{Digest: digest}
	saved.Environment = environmentOf(p, envName, conn)
	if err := writeSaved(ctx, env, r, saved, plan, path, force, "alder project apply"); err != nil {
		return err
	}
	writef(env.Stderr, "alder: plan for environment %s saved to %s: %s\n", safe(envName), safe(path),
		plural(countSaved(plan), "change", "changes"))
	return nil
}

// saveInputPlan is alder plan --out: the plan of an LDIF document or of
// --changes, saved for alder apply --plan.
func saveInputPlan(ctx context.Context, env *Env, r *remote, in *planInput, conn *connection,
	plan apiclient.Plan, path string, force bool) error {
	// The saved change is the plan's record, which carries no expectation of
	// the input's own; saving it would drop the guard the plan showed.
	for i, c := range in.staged {
		if c.Expect != nil {
			return failf("plan_not_saved", "the plan was not saved to %s: change %d carries its own expect, which a saved plan does not keep; apply it with alder apply",
				safe(path), i+1)
		}
	}
	saved := newSavedPlan(env, plan)
	saved.Input = in.saved()
	saved.Environment = savedPlanEnvironment{Host: conn.label(), BindDN: conn.bindDN, ConfigBindDN: conn.configBindDN}
	if err := writeSaved(ctx, env, r, saved, plan, path, force, "alder apply"); err != nil {
		return err
	}
	writef(env.Stderr, "alder: plan for %s saved to %s: %s\n", safe(conn.label()), safe(path),
		plural(countSaved(plan), "change", "changes"))
	return nil
}

func newSavedPlan(env *Env, plan apiclient.Plan) savedPlan {
	return savedPlan{
		Format:       savedPlanFormat,
		Version:      savedPlanVersion,
		CreatedAt:    time.Now().UTC().Truncate(time.Second),
		AlderVersion: env.Version,
		Changes:      []apiclient.ChangeRequest{},
		Unchanged:    plan.Counts.Unchanged,
	}
}

// countSaved is how many of a plan's items a saved plan holds.
func countSaved(plan apiclient.Plan) int {
	n := 0
	for _, item := range plan.Items {
		if item.Record != nil && item.Baseline != nil {
			n++
		}
	}
	return n
}

// writeSaved fills in a saved plan's changes from the plan and writes it.
//
// Each change is the plan's own record. A modification also carries an
// expectation: the attributes it replaces, with the values the directory holds
// for them now. A deletion expects the entry exactly as it is now, and nothing
// else. The saved changes are then planned once, exactly as an apply will plan
// them, and the file is written only if that plan is the one shown: every
// change applies, none is a conflict. That closes the gap between the plan and
// the reads behind the expectations, and proves the file applies.
//
// applyWith names the command a change that cannot be saved is applied with
// instead.
func writeSaved(ctx context.Context, env *Env, r *remote, saved savedPlan, plan apiclient.Plan,
	path string, force bool, applyWith string) error {
	var names attributeNames
	var shown []apiclient.PlanItem
	for _, item := range plan.Items {
		if item.Record == nil || item.Baseline == nil {
			continue
		}
		change := *item.Record
		change.Baseline = nil
		switch item.Action {
		case apiclient.PlanActionAdd:
			// An add expects nothing: planning it where the entry now exists
			// is already a conflict.
		case apiclient.PlanActionModify:
			if names == nil {
				var err error
				if names, err = readAttributeNames(ctx, r); err != nil {
					return err
				}
			}
			expect, err := expectationFor(ctx, r, change, names)
			if err != nil {
				return err
			}
			change.Expect = expect
		case apiclient.PlanActionDelete:
			expect, err := expectationForDelete(ctx, r, change, applyWith)
			if err != nil {
				return err
			}
			change.Expect = expect
		default:
			// Anything else is not something this file knows how to make
			// safe to keep: a rename has nothing an expectation can bind, and
			// a password's value is never written down.
			return failf("plan_not_saved", "the plan was not saved to %s: %s of %s is not a change a saved plan holds; apply this one with %s",
				safe(path), item.Action, safe(item.Dn), applyWith)
		}
		if err := withheldValue(change, applyWith); err != nil {
			return failf("plan_not_saved", "the plan was not saved to %s: %s", safe(path), err)
		}
		saved.Changes = append(saved.Changes, change)
		shown = append(shown, item)
	}
	if len(saved.Changes) > 0 {
		// Only a plan that no longer holds is stale (exit 4): a failure to
		// ask is reported as itself, so a script retrying on 4 does not
		// retry a network failure.
		p, _, err := planSaved(ctx, r, saved.Changes)
		if err != nil {
			return err
		}
		if err := holds(p, len(saved.Changes)); err != nil {
			return &ExitError{Code: ExitStale, Local: "plan_not_saved",
				Message: fmt.Sprintf("the plan was not saved to %s: %s. Plan again", safe(path), err)}
		}
		// The expectations were read after the plan was shown, so they hold
		// whatever the directory held then. The saved changes' plan is of
		// that state; if it is not what was shown, someone changed the
		// directory in between, and saving would bind the reviewed plan to
		// a state nobody reviewed.
		if err := sameAsShown(shown, p); err != nil {
			return &ExitError{Code: ExitStale, Local: "plan_not_saved",
				Message: fmt.Sprintf("the plan was not saved to %s: %s. Plan again", safe(path), err)}
		}
	}

	doc, err := json.MarshalIndent(saved, "", "  ")
	if err != nil {
		return failf("output", "cannot encode the saved plan: %v", err)
	}
	doc = append(doc, '\n')
	return writeFile(path, force, func(w io.Writer) error {
		if _, err := w.Write(doc); err != nil {
			return failf("output", "cannot write %s: %v", safe(path), err)
		}
		return nil
	}, func(f *os.File) error {
		data, err := io.ReadAll(io.LimitReader(f, int64(maxRequestBytes)+1))
		if err != nil {
			return failf("output", "cannot read back %s: %v", safe(path), err)
		}
		if _, err := decodeSavedPlan(data); err != nil {
			return failf("output", "the saved plan does not read back, so %s was not written: %v", safe(path), err)
		}
		return nil
	})
}

// attributeNames maps every name and OID of an attribute type, lowercased, to
// the name the directory returns it under.
type attributeNames map[string]string

// canonical is the name an attribute is returned under, for any of its names
// or its OID. A name the schema does not know is returned as it is.
func (n attributeNames) canonical(name string) string {
	if c, ok := n[strings.ToLower(name)]; ok {
		return c
	}
	return name
}

// readAttributeNames reads the directory's schema for the names of its
// attribute types. An expectation is checked by the name the entry returns
// (cn, never commonName or 2.5.4.3), so one written under another name would
// find nothing and expect the attribute absent, which always holds.
func readAttributeNames(ctx context.Context, r *remote) (attributeNames, error) {
	const what = "reading the directory's schema"
	res, err := r.api.GetSchemaWithResponse(ctx, nil)
	if err != nil {
		return nil, transportFailure(ctx, what, err)
	}
	if res.StatusCode() != http.StatusOK {
		return nil, r.refusal(ctx, what, res.HTTPResponse, res.Body)
	}
	if res.JSON200 == nil || res.JSON200.AttributeTypes == nil {
		return nil, failf("unexpected_response", "Alder answered the schema request with no attribute types")
	}
	names := attributeNames{}
	for _, at := range *res.JSON200.AttributeTypes {
		all := []string{at.Name, at.Oid}
		if at.Names != nil {
			all = append(all, *at.Names...)
		}
		for _, n := range all {
			if n != "" {
				names[strings.ToLower(n)] = at.Name
			}
		}
	}
	return names, nil
}

// expectationFor reads the entry a modification is about and returns what it
// holds for every attribute the modification touches, under the name the
// entry returns it by. An attribute it does not hold is expected absent.
func expectationFor(ctx context.Context, r *remote, change apiclient.ChangeRequest, names attributeNames) (*apiclient.ChangeExpectation, error) {
	res, err := r.api.GetEntryWithResponse(ctx, &apiclient.GetEntryParams{Dn: change.Dn, IncludeOperational: ptr(false)})
	if err != nil {
		return nil, transportFailure(ctx, "reading "+safe(change.Dn), err)
	}
	if res.StatusCode() == http.StatusNotFound {
		return nil, &ExitError{Code: ExitStale, Local: "plan_not_saved",
			Message: fmt.Sprintf("the plan was not saved: %s was removed while the plan was being saved. Plan again", safe(change.Dn))}
	}
	if res.StatusCode() != http.StatusOK || res.JSON200 == nil {
		return nil, r.refusal(ctx, "reading "+safe(change.Dn), res.HTTPResponse, res.Body)
	}
	expect := &apiclient.ChangeExpectation{Attributes: []apiclient.ChangeAttribute{}}
	seen := map[string]bool{}
	if change.Mods != nil {
		for _, m := range *change.Mods {
			name := names.canonical(m.Name)
			key := strings.ToLower(name)
			if seen[key] {
				continue
			}
			seen[key] = true
			held := apiclient.ChangeAttribute{Name: name, Values: []apiclient.AttributeValue{}}
			for _, a := range res.JSON200.Attributes {
				if !strings.EqualFold(names.canonical(a.Name), name) {
					continue
				}
				if a.Withheld != nil && *a.Withheld {
					return nil, failf("plan_not_saved", "the plan was not saved: %s of %s is sensitive, and its value cannot be expected",
						safe(m.Name), safe(change.Dn))
				}
				held.Values = append(held.Values, a.Values...)
			}
			expect.Attributes = append(expect.Attributes, held)
		}
	}
	return expect, nil
}

// expectationForDelete reads the entry a deletion is about and returns it as it
// is now, exhaustively: deleting it later is right only while it still holds
// exactly what the reviewer saw deleted. A sensitive attribute cannot be
// expected, and an exhaustive expectation does not count one, so an entry
// holding one is still saved; what it cannot be is restored by anyone.
func expectationForDelete(ctx context.Context, r *remote, change apiclient.ChangeRequest, applyWith string) (*apiclient.ChangeExpectation, error) {
	res, err := r.api.GetEntryWithResponse(ctx, &apiclient.GetEntryParams{Dn: change.Dn, IncludeOperational: ptr(false)})
	if err != nil {
		return nil, transportFailure(ctx, "reading "+safe(change.Dn), err)
	}
	if res.StatusCode() == http.StatusNotFound {
		return nil, &ExitError{Code: ExitStale, Local: "plan_not_saved",
			Message: fmt.Sprintf("the plan was not saved: %s was removed while the plan was being saved. Plan again", safe(change.Dn))}
	}
	if res.StatusCode() != http.StatusOK || res.JSON200 == nil {
		return nil, r.refusal(ctx, "reading "+safe(change.Dn), res.HTTPResponse, res.Body)
	}
	expect := &apiclient.ChangeExpectation{Attributes: []apiclient.ChangeAttribute{}, Exhaustive: ptr(true)}
	for _, a := range res.JSON200.Attributes {
		if a.Withheld != nil && *a.Withheld {
			continue
		}
		for _, v := range a.Values {
			if v.Text == nil && v.Base64 == nil {
				return nil, failf("plan_not_saved", "the plan was not saved: a value of %s on %s was not read whole; apply this one with %s",
					safe(a.Name), safe(change.Dn), applyWith)
			}
		}
		expect.Attributes = append(expect.Attributes, apiclient.ChangeAttribute{Name: a.Name, Values: a.Values})
	}
	return expect, nil
}

// withheldValue finds a value the server withheld from the plan: only its size
// is there, and a file holding it could not be applied. applyWith names the
// command that applies such a change instead.
func withheldValue(change apiclient.ChangeRequest, applyWith string) error {
	check := func(name string, values []apiclient.AttributeValue) error {
		for _, v := range values {
			if v.Text == nil && v.Base64 == nil {
				return fmt.Errorf("a value of %s on %s was withheld from the plan; apply this one with %s",
					safe(name), safe(change.Dn), applyWith)
			}
		}
		return nil
	}
	if change.Attributes != nil {
		for _, a := range *change.Attributes {
			if err := check(a.Name, a.Values); err != nil {
				return err
			}
		}
	}
	if change.Mods != nil {
		for _, m := range *change.Mods {
			if m.Values != nil {
				if err := check(m.Name, *m.Values); err != nil {
					return err
				}
			}
		}
	}
	if change.Expect != nil {
		for _, a := range change.Expect.Attributes {
			if err := check(a.Name, a.Values); err != nil {
				return err
			}
		}
	}
	return nil
}

// planSaved plans a saved plan's changes as they are written.
func planSaved(ctx context.Context, r *remote, changes []apiclient.ChangeRequest) (apiclient.Plan, []byte, error) {
	sent := append([]apiclient.ChangeRequest{}, changes...)
	return requestPlan(ctx, r, apiclient.PlanRequest{Changes: &sent, Reconcile: ptr(false)})
}

// sameAsShown compares the plan of the saved changes, made from the state
// their expectations were read from, with the plan that was shown, item by
// item: each change must still be the same action with the same effect on
// group membership and the same references found to it. Those are what a
// plan says about the directory as it found it; the rest of an item is the
// change itself, which is the same record in both.
func sameAsShown(shown []apiclient.PlanItem, fresh apiclient.Plan) error {
	byIndex := make(map[int]apiclient.PlanItem, len(fresh.Items))
	for _, item := range fresh.Items {
		byIndex[item.Index] = item
	}
	for i, was := range shown {
		now, ok := byIndex[i]
		moved := func(what string) error {
			return fmt.Errorf("%s changed after the plan was shown: %s", safe(was.Dn), what)
		}
		switch {
		case !ok:
			return moved("it is not in the plan of the saved changes")
		case now.Action != was.Action:
			return moved(fmt.Sprintf("it is now a %s, and was shown as a %s", now.Action, was.Action))
		case membershipKey(now.Membership) != membershipKey(was.Membership):
			return moved("what it does to group membership is not what was shown")
		case now.References != nil && was.References != nil &&
			(now.References.Count != was.References.Count || now.References.Dangling != was.References.Dangling):
			return moved("the references found to it are not the ones shown")
		}
	}
	return nil
}

// membershipKey is a plan item's membership changes as one comparable string,
// whatever order the attributes and values are listed in.
func membershipKey(changes *[]apiclient.PlanMembershipChange) string {
	if changes == nil {
		return ""
	}
	var parts []string
	for _, c := range *changes {
		if len(c.Gained) == 0 && len(c.Removed) == 0 {
			continue
		}
		gained := append([]string{}, c.Gained...)
		removed := append([]string{}, c.Removed...)
		sort.Strings(gained)
		sort.Strings(removed)
		parts = append(parts, strings.ToLower(c.Attribute)+"\x00+"+strings.Join(gained, "\x00")+"\x00-"+strings.Join(removed, "\x00"))
	}
	sort.Strings(parts)
	return strings.Join(parts, "\x01")
}

// holds reports whether a plan of n saved changes applies each of them.
func holds(p apiclient.Plan, n int) error {
	if b := blocked(p); b > 0 {
		return fmt.Errorf("%d change(s) no longer hold: the directory changed after the plan was made", b)
	}
	if a := applicable(p); a != n {
		return fmt.Errorf("%d of %s would apply: the directory changed after the plan was made", a, plural(n, "change", "changes"))
	}
	return nil
}

// readSavedPlan reads a saved plan file, strictly.
func readSavedPlan(env *Env, path string) (*savedPlan, error) {
	data, _, err := env.readInput(path, "the saved plan", int64(maxRequestBytes))
	if err != nil {
		return nil, err
	}
	saved, err := decodeSavedPlan(data)
	if err != nil {
		return nil, &ExitError{Code: ExitNotApplicable, Local: "saved_plan_invalid",
			Message: fmt.Sprintf("%s is not a saved plan this release reads: %s", safe(path), safe(err.Error()))}
	}
	return saved, nil
}

// decodeSavedPlan refuses anything it does not know rather than half-reading
// it, as the recovery bundle's reader does.
func decodeSavedPlan(data []byte) (*savedPlan, error) {
	if len(data) > maxRequestBytes {
		return nil, fmt.Errorf("it is larger than %d bytes", maxRequestBytes)
	}
	// encoding/json matches a key in any case and keeps the last of two, so a
	// file could say one thing to a person and another to this reader.
	if err := exactKeys(data, reflect.TypeOf(savedPlan{})); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var s savedPlan
	if err := dec.Decode(&s); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, fmt.Errorf("it holds more than one JSON document")
	}
	switch {
	case s.Format != savedPlanFormat:
		return nil, fmt.Errorf("its format is %q, not %q", s.Format, savedPlanFormat)
	case s.Version != savedPlanVersion:
		return nil, fmt.Errorf("it is version %d; this release reads version %d", s.Version, savedPlanVersion)
	case s.Changes == nil:
		return nil, fmt.Errorf("it has no changes list")
	case (s.Project == nil) == (s.Input == nil):
		return nil, fmt.Errorf("it must name either the project or the input it was made from")
	case s.Environment.Host == "":
		return nil, fmt.Errorf("it does not name the directory it was made against")
	case s.Project != nil && s.Environment.Name == "":
		return nil, fmt.Errorf("it does not name the environment it was made for")
	case s.Project != nil && !strings.HasPrefix(s.Project.Digest, "sha256:"):
		return nil, fmt.Errorf("it does not name the project it was made from")
	case s.Input != nil && s.Environment.Name != "":
		return nil, fmt.Errorf("it names an environment, which only a project's plan has")
	case s.Input != nil && (s.Input.Kind != "ldif" && s.Input.Kind != "changes" || !strings.HasPrefix(s.Input.Digest, "sha256:")):
		return nil, fmt.Errorf("it does not say which input it was made from")
	}
	for i, c := range s.Changes {
		switch {
		case c.Baseline != nil:
			return nil, fmt.Errorf("change %d carries a baseline, which no saved plan holds", i+1)
		case c.Type == apiclient.ChangeRequestTypeAdd:
		case c.Type == apiclient.ChangeRequestTypeModify:
			// An expectation that names nothing always holds. That every
			// attribute changed is expected is checked once the schema can
			// say which names are one attribute (expectsWhatItChanges).
			if c.Expect == nil || len(c.Expect.Attributes) == 0 {
				return nil, fmt.Errorf("change %d is a modification with no expectation", i+1)
			}
		case c.Type == apiclient.ChangeRequestTypeDelete && s.Input != nil:
			// A project never deletes, so its saved plan never holds one.
			if c.Expect == nil || c.Expect.Exhaustive == nil || !*c.Expect.Exhaustive {
				return nil, fmt.Errorf("change %d is a deletion that does not expect the entry exactly", i+1)
			}
		default:
			return nil, fmt.Errorf("change %d is a %s, which this saved plan does not hold", i+1, c.Type)
		}
		if err := withheldValue(c, "alder apply"); err != nil {
			return nil, err
		}
	}
	return &s, nil
}

// matchSaved refuses a saved plan made for another environment, directory,
// bind or project, before anything is sent.
func matchSaved(path string, s *savedPlan, p *project.Project, envName string, conn *connection) error {
	want := environmentOf(p, envName, conn)
	refuse := func(format string, a ...any) error {
		return &ExitError{Code: ExitNotApplicable, Local: "saved_plan_mismatch",
			Message: fmt.Sprintf("not applied: %s ", safe(path)) + fmt.Sprintf(format, a...)}
	}
	if s.Project == nil {
		return refuse("was saved by alder plan, not from a project: apply it with alder apply --plan")
	}
	got := s.Environment
	switch {
	case got.Name != want.Name:
		return refuse("was made for environment %s, not %s", safe(got.Name), safe(want.Name))
	case got.Host != want.Host:
		return refuse("was made against %s; environment %s is now %s", safe(got.Host), safe(want.Name), safe(want.Host))
	case !strings.EqualFold(got.BindDN, want.BindDN):
		return refuse("was made binding as %s; environment %s now binds as %s", safe(got.BindDN), safe(want.Name), safe(want.BindDN))
	case !strings.EqualFold(got.ConfigBindDN, want.ConfigBindDN):
		return refuse("was made %s; environment %s now binds for configuration %s",
			configBindMade(got.ConfigBindDN), safe(want.Name), configBindNow(want.ConfigBindDN))
	case (got.Rebase == nil) != (want.Rebase == nil) ||
		(got.Rebase != nil && (got.Rebase.From != want.Rebase.From || got.Rebase.To != want.Rebase.To)):
		return refuse("was made with another suffix for environment %s than alder.yaml now gives it", safe(want.Name))
	}
	digest, err := projectDigest(p)
	if err != nil {
		return err
	}
	if digest != s.Project.Digest {
		return refuse("was made from other project files than %s now describes: plan this version and review it", safe(p.File))
	}
	return nil
}

// matchSavedInput refuses, before anything is sent, a saved plan that alder
// apply --plan is not the command for, or that was made against another
// directory or as someone else.
func matchSavedInput(path string, s *savedPlan, conn *connection) error {
	refuse := func(format string, a ...any) error {
		return &ExitError{Code: ExitNotApplicable, Local: "saved_plan_mismatch",
			Message: fmt.Sprintf("not applied: %s ", safe(path)) + fmt.Sprintf(format, a...)}
	}
	got := s.Environment
	switch {
	case s.Input == nil:
		return refuse("is a project's plan, for environment %s: apply it with alder project apply --env %s --plan",
			safe(got.Name), safe(got.Name))
	case got.Host != conn.label():
		return refuse("was made against %s, not %s", safe(got.Host), safe(conn.label()))
	case !strings.EqualFold(got.BindDN, conn.bindDN):
		return refuse("was made binding as %s, not %s", safe(got.BindDN), safe(conn.bindDN))
	case !strings.EqualFold(got.ConfigBindDN, conn.configBindDN):
		now := "without one"
		if conn.configBindDN != "" {
			now = safe(conn.configBindDN)
		}
		if got.ConfigBindDN == "" {
			return refuse("was made without --config-bind-dn, not with %s", now)
		}
		return refuse("was made with --config-bind-dn %s, not %s", safe(got.ConfigBindDN), now)
	}
	return nil
}

// configBindMade and configBindNow say which configuration identity a
// project's saved plan was made with, and which its environment now has.
func configBindMade(dn string) string {
	if dn == "" {
		return "with no config-bind-dn"
	}
	return "binding for configuration as " + safe(dn)
}

func configBindNow(dn string) string {
	if dn == "" {
		return "without config-bind-dn"
	}
	return "as " + safe(dn)
}

// applySavedInput is alder apply --plan: a plan alder plan --out saved, its
// changes planned as they are written, shown, confirmed and applied with that
// plan's baselines. The file is the whole input.
func applySavedInput(ctx context.Context, env *Env, conn *connection, o planOptions, flags *pflag.FlagSet,
	args []string, envelope *applyEnvelope) error {
	switch {
	case len(args) > 0 || o.changes != "" || o.recovery != "" || o.pkg != "":
		return usagef("--plan is the input: give no LDIF document, no --changes, no --recovery and no --package with it")
	case flags.Changed("mode") || flags.Changed("schema-target") || o.allowOriginMismatch:
		return usagef("--mode, --schema-target and --allow-origin-mismatch describe an input, and --plan is already planned")
	case o.plan == "-":
		return usagef("--plan needs a file: standard input may be needed to confirm")
	case o.recoveryOut == "-":
		return usagef("--recovery-out needs a file: standard output carries the plan and the result")
	case o.force && o.recoveryOut == "":
		return usagef("--force replaces the --recovery-out file, and none was given")
	}
	if err := refuseExisting(o.recoveryOut, o.force); err != nil {
		return err
	}
	if err := conn.check(false); err != nil {
		return err
	}
	// The file is read and matched first, so a file that is not this
	// directory's plan is refused as such (exit 3) whether or not there is
	// anyone to confirm it.
	saved, err := readSavedPlan(env, o.plan)
	if err != nil {
		return err
	}
	if err := matchSavedInput(o.plan, saved, conn); err != nil {
		return err
	}
	if !o.yes && (conn.bindPasswordStdin || !env.Interactive()) {
		return &ExitError{Code: ExitNotConfirmed, Local: "confirmation_required",
			Message: "not applied: there is no terminal to ask for confirmation, and --yes was not given. " +
				"Review the saved plan, then run apply with --yes"}
	}

	writef(env.Stderr, "alder: applying the plan saved in %s at %s to %s through %s\n",
		safe(o.plan), saved.CreatedAt.Format(time.RFC3339), safe(conn.label()), safe(conn.apiURL))
	out := env.Stdout
	if o.json {
		out = env.Stderr
	}
	if len(saved.Changes) == 0 {
		writeln(out, "Nothing to apply: the saved plan holds no change.")
		return nil
	}

	ctx, cancel := conn.bound(ctx)
	defer cancel()
	r, err := conn.open(ctx, env)
	if err != nil {
		return err
	}
	defer r.close()

	names, err := readAttributeNames(ctx, r)
	if err != nil {
		return err
	}
	if err := expectsWhatItChanges(o.plan, saved.Changes, names); err != nil {
		return err
	}
	plan, raw, err := planSaved(ctx, r, saved.Changes)
	if err != nil {
		return err
	}
	if why := holds(plan, len(saved.Changes)); why != nil {
		envelope.Plan = compactJSON(raw)
		renderPlan(out, plan, !o.summary)
		return &ExitError{Code: ExitStale, Local: "saved_plan_stale",
			Message: fmt.Sprintf("not applied: the plan saved in %s no longer holds: %s. Nothing was written; plan again and review",
				safe(o.plan), why)}
	}
	return applyReviewed(ctx, env, r, plan, raw, saved.Changes, applySettings{
		target:       conn.label(),
		summary:      o.summary,
		yes:          o.yes,
		allowDeletes: o.allowDeletes,
		recoveryOut:  o.recoveryOut,
		force:        o.force,
	}, out, envelope)
}
