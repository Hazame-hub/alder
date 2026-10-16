package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// examples/project is documentation people copy, so it is held to working:
// the project validates with this build, and the CI files beside it parse
// and run the commands their comments say they run.
func TestTheExampleProjectValidates(t *testing.T) {
	dir := filepath.Join("..", "..", "examples", "project", "directory")
	r := newStub(t).run(t, runOpts{noConnection: true}, "project", "validate", "--project", dir)
	expectCode(t, r, ExitOK)
	// And is already in the form fmt writes, so copying it and running fmt
	// produces no diff to puzzle over.
	r = newStub(t).run(t, runOpts{noConnection: true}, "project", "fmt", "--check", "--project", dir)
	expectCode(t, r, ExitOK)
}

func TestTheExampleCIFilesParseAndRunTheProject(t *testing.T) {
	for _, name := range []string{"github-actions.yml", "gitlab-ci.yml"} {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("..", "..", "examples", "project", "ci", name))
			if err != nil {
				t.Fatal(err)
			}
			var doc map[string]any
			if err := yaml.Unmarshal(raw, &doc); err != nil {
				t.Fatalf("not YAML: %v", err)
			}
			text := string(raw)
			for _, want := range []string{
				"alder project validate --project",
				"--detailed-exitcode",
				"sha256sum -c",
				"ALDER_STAGING_BIND_PASSWORD",
				"ALDER_PRODUCTION_BIND_PASSWORD",
			} {
				if !strings.Contains(text, want) {
					t.Errorf("%s does not contain %q", name, want)
				}
			}
			if strings.Contains(text, "--yes") {
				t.Errorf("%s applies; the example plans only, and says why", name)
			}
		})
	}
}

// The apply examples write to a directory, so what makes them safe is held
// here: a person starts or approves the apply, the apply takes the saved
// file and nothing else, and neither runs on a pull or merge request.
func TestTheExampleApplyFilesApplyOnlyTheReviewedPlan(t *testing.T) {
	read := func(t *testing.T, name string) (string, map[string]any) {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join("..", "..", "examples", "project", "ci", name))
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("not YAML: %v", err)
		}
		text := string(raw)
		for _, want := range []string{
			"sha256sum -c",
			"--out ",
			"--plan ",
			"--yes",
			"--recovery-out",
			"ALDER_STAGING_BIND_PASSWORD",
			"ALDER_PRODUCTION_BIND_PASSWORD",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("%s does not contain %q", name, want)
			}
		}
		// `cond && secrets.A || secrets.B` is B whenever A is empty, so a
		// missing production secret would send the staging password to the
		// production directory. The secret is picked by name instead.
		if strings.Contains(text, "&& secrets.") {
			t.Errorf("%s picks a secret with `&& secrets.`, which falls back to the other one when it is empty", name)
		}
		// --yes without --plan would plan again and apply what nobody saw.
		for _, line := range strings.Split(text, "\n") {
			if strings.Contains(line, "--yes") && !strings.Contains(line, "--plan ") {
				t.Errorf("%s: --yes outside the line that applies the saved plan: %q", name, strings.TrimSpace(line))
			}
		}
		return text, doc
	}

	t.Run("github-actions-apply.yml", func(t *testing.T) {
		_, doc := read(t, "github-actions-apply.yml")
		// yaml.v3 reads the key "on" as the string "on".
		on, _ := doc["on"].(map[string]any)
		if len(on) != 1 || on["workflow_dispatch"] == nil {
			t.Errorf("runs on %v; it must run only when someone starts it", on)
		}
		jobs, _ := doc["jobs"].(map[string]any)
		apply, _ := jobs["apply"].(map[string]any)
		if apply == nil {
			t.Fatal("no apply job")
		}
		if apply["needs"] != "plan" {
			t.Errorf("apply needs %v, not the plan job", apply["needs"])
		}
		if env, _ := apply["environment"].(string); !strings.Contains(env, "inputs.environment") {
			t.Errorf("apply's environment is %q; the approval gate must be the chosen environment", env)
		}
		plan, _ := jobs["plan"].(map[string]any)
		if _, gated := plan["environment"]; gated {
			t.Error("plan is behind the environment gate: reviewers would approve before seeing the plan")
		}
	})

	t.Run("gitlab-ci-apply.yml", func(t *testing.T) {
		_, doc := read(t, "gitlab-ci-apply.yml")
		if _, ok := doc["stages"]; ok {
			t.Error("an included file's stages are replaced by the including file's; say which to add instead")
		}
		tmpl, _ := doc[".apply"].(map[string]any)
		rules, _ := tmpl["rules"].([]any)
		if len(rules) == 0 {
			t.Fatal(".apply has no rules")
		}
		for _, r := range rules {
			rule, _ := r.(map[string]any)
			if rule["when"] != "manual" {
				t.Errorf(".apply rule %v is not manual", rule)
			}
			if cond, _ := rule["if"].(string); strings.Contains(cond, `== "merge_request_event"`) {
				t.Errorf(".apply runs on merge requests: %q", cond)
			}
		}
		for _, env := range []string{"staging", "production"} {
			job, _ := doc["apply:"+env].(map[string]any)
			e, _ := job["environment"].(map[string]any)
			if e["name"] != env {
				t.Errorf("apply:%s is not in the %s environment, so it is not behind its protection", env, env)
			}
			needs, _ := job["needs"].([]any)
			if len(needs) != 1 || needs[0] != "save-plan:"+env {
				t.Errorf("apply:%s needs %v, not save-plan:%s", env, needs, env)
			}
		}
	})
}
