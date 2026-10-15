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
	"strings"
	"time"

	"github.com/hazame-hub/alder/internal/apiclient"
	"github.com/hazame-hub/alder/internal/project"
)

// A saved plan is the plan alder project plan showed, kept in a file so that a
// later run can apply exactly that plan or refuse (docs/proposals/saved-plans.md).
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
	AlderVersion string                    `json:"alderVersion"`
	Project      savedPlanProject          `json:"project"`
	Environment  savedPlanEnvironment      `json:"environment"`
	Changes      []apiclient.ChangeRequest `json:"changes"`
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

type savedPlanEnvironment struct {
	Name   string           `json:"name"`
	Host   string           `json:"host"`
	BindDN string           `json:"bindDn"`
	Rebase *savedPlanRebase `json:"rebase,omitempty"`
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
	e := savedPlanEnvironment{Name: envName, Host: conn.label(), BindDN: conn.bindDN}
	if from, to, ok := p.Rebasing(envName); ok {
		e.Rebase = &savedPlanRebase{From: from.String(), To: to.String()}
	}
	return e
}

// savePlan turns the plan of a project into a saved plan and writes it. The
// caller has checked that every change in the plan can be applied as written.
//
// Each change is the plan's own record. A modification also carries an
// expectation: the attributes it replaces, with the values the directory holds
// for them now. The saved changes are then planned once, exactly as an apply
// will plan them, and the file is written only if that plan is the one shown:
// every change applies, none is a conflict. That closes the gap between the
// plan and the reads behind the expectations, and proves the file applies.
func savePlan(ctx context.Context, env *Env, r *remote, p *project.Project, envName string, conn *connection,
	plan apiclient.Plan, path string, force bool) error {
	digest, err := projectDigest(p)
	if err != nil {
		return err
	}
	saved := savedPlan{
		Format:       savedPlanFormat,
		Version:      savedPlanVersion,
		CreatedAt:    time.Now().UTC().Truncate(time.Second),
		AlderVersion: env.Version,
		Project:      savedPlanProject{Digest: digest},
		Environment:  environmentOf(p, envName, conn),
		Changes:      []apiclient.ChangeRequest{},
		Unchanged:    plan.Counts.Unchanged,
	}
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
			expect, err := expectationFor(ctx, r, change)
			if err != nil {
				return err
			}
			change.Expect = expect
		default:
			// A project plans adds and modifications only. Anything else is
			// not something this file knows how to make safe to keep.
			return failf("plan_not_saved", "the plan was not saved to %s: %s of %s is not a change a saved plan holds",
				safe(path), item.Action, safe(item.Dn))
		}
		if err := withheldValue(change); err != nil {
			return failf("plan_not_saved", "the plan was not saved to %s: %s", safe(path), err)
		}
		saved.Changes = append(saved.Changes, change)
	}
	if err := checkSaved(ctx, r, saved.Changes); err != nil {
		return &ExitError{Code: ExitStale, Local: "plan_not_saved",
			Message: fmt.Sprintf("the plan was not saved to %s: %s. Plan again", safe(path), err)}
	}

	doc, err := json.MarshalIndent(saved, "", "  ")
	if err != nil {
		return failf("output", "cannot encode the saved plan: %v", err)
	}
	doc = append(doc, '\n')
	if err := writeFile(path, force, func(w io.Writer) error {
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
	}); err != nil {
		return err
	}
	writef(env.Stderr, "alder: plan for environment %s saved to %s: %s\n", safe(envName), safe(path),
		plural(len(saved.Changes), "change", "changes"))
	return nil
}

// expectationFor reads the entry a modification is about and returns what it
// holds for every attribute the modification touches. An attribute it does
// not hold is expected absent.
func expectationFor(ctx context.Context, r *remote, change apiclient.ChangeRequest) (*apiclient.ChangeExpectation, error) {
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
			key := strings.ToLower(m.Name)
			if seen[key] {
				continue
			}
			seen[key] = true
			held := apiclient.ChangeAttribute{Name: m.Name, Values: []apiclient.AttributeValue{}}
			for _, a := range res.JSON200.Attributes {
				if !strings.EqualFold(a.Name, m.Name) {
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

// withheldValue finds a value the server withheld from the plan: only its size
// is there, and a file holding it could not be applied.
func withheldValue(change apiclient.ChangeRequest) error {
	check := func(name string, values []apiclient.AttributeValue) error {
		for _, v := range values {
			if v.Text == nil && v.Base64 == nil {
				return fmt.Errorf("a value of %s on %s was withheld from the plan; apply this one with alder project apply",
					safe(name), safe(change.Dn))
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

// checkSaved says why a plan of the saved changes is not the plan they were
// saved from, or nil when every one of them applies.
func checkSaved(ctx context.Context, r *remote, changes []apiclient.ChangeRequest) error {
	if len(changes) == 0 {
		return nil
	}
	p, _, err := planSaved(ctx, r, changes)
	if err != nil {
		return err
	}
	return holds(p, len(changes))
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
	case s.Environment.Name == "" || s.Environment.Host == "":
		return nil, fmt.Errorf("it does not name the environment it was made for")
	case !strings.HasPrefix(s.Project.Digest, "sha256:"):
		return nil, fmt.Errorf("it does not name the project it was made from")
	}
	for i, c := range s.Changes {
		switch {
		case c.Baseline != nil:
			return nil, fmt.Errorf("change %d carries a baseline, which no saved plan holds", i+1)
		case c.Type != apiclient.ChangeRequestTypeAdd && c.Type != apiclient.ChangeRequestTypeModify:
			return nil, fmt.Errorf("change %d is a %s; a saved plan holds adds and modifications", i+1, c.Type)
		case c.Type == apiclient.ChangeRequestTypeModify && c.Expect == nil:
			return nil, fmt.Errorf("change %d is a modification with no expectation", i+1)
		}
		if err := withheldValue(c); err != nil {
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
	got := s.Environment
	switch {
	case got.Name != want.Name:
		return refuse("was made for environment %s, not %s", safe(got.Name), safe(want.Name))
	case got.Host != want.Host:
		return refuse("was made against %s; environment %s is now %s", safe(got.Host), safe(want.Name), safe(want.Host))
	case !strings.EqualFold(got.BindDN, want.BindDN):
		return refuse("was made binding as %s; environment %s now binds as %s", safe(got.BindDN), safe(want.Name), safe(want.BindDN))
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
