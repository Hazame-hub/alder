package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A saved plan records the configuration identity it was made with, and an
// apply under another one (or none, or one where there was none) is refused
// before anything is sent, as another bind DN is.

const savedConfigDN = "cn=Directory Manager"

var withConfigPassword = runOpts{env: map[string]string{"ALDER_CONFIG_BIND_PASSWORD": testSecret}}

// saveInputAs saves inputLDIF's plan against s, with these extra flags.
func saveInputAs(t *testing.T, s *stub, flags ...string) string {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "change.plan.json")
	args := append([]string{"plan", writeTemp(t, dir, "change.ldif", inputLDIF), "--out", out}, flags...)
	r := s.run(t, withConfigPassword, args...)
	expectCode(t, r, ExitOK)
	return out
}

func TestASavedInputPlanRecordsTheConfigBindDN(t *testing.T) {
	s := inputPlanStub(t, true)

	environment, _ := readSaved(t, saveInputAs(t, s, "--config-bind-dn", savedConfigDN))["environment"].(map[string]any)
	if environment["configBindDn"] != savedConfigDN {
		t.Errorf("environment = %v, want configBindDn %q", environment, savedConfigDN)
	}

	environment, _ = readSaved(t, saveInputAs(t, s))["environment"].(map[string]any)
	if _, ok := environment["configBindDn"]; ok {
		t.Errorf("environment = %v: a plan made with no configuration identity records none", environment)
	}
}

func TestApplyPlanRefusesAnotherConfigBindDNBeforeConnecting(t *testing.T) {
	for _, tc := range []struct {
		name        string
		saved, used []string
		want        string
	}{
		{"saved with one, applied with none", []string{"--config-bind-dn", savedConfigDN}, nil,
			"was made with --config-bind-dn " + savedConfigDN + ", not without one"},
		{"saved with none, applied with one", nil, []string{"--config-bind-dn", savedConfigDN},
			"was made without --config-bind-dn, not with " + savedConfigDN},
		{"saved with one, applied with another", []string{"--config-bind-dn", savedConfigDN},
			[]string{"--config-bind-dn", "cn=config-admin,dc=example,dc=test"},
			"was made with --config-bind-dn " + savedConfigDN + ", not cn=config-admin,dc=example,dc=test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := inputPlanStub(t, true)
			saved := saveInputAs(t, s, tc.saved...)
			before := len(s.calls)
			r := s.run(t, withConfigPassword, append([]string{"apply", "--plan", saved, "--yes", "--allow-deletes"}, tc.used...)...)
			expectCode(t, r, ExitNotApplicable)
			if !strings.Contains(r.stderr, tc.want) {
				t.Errorf("stderr does not say %q:\n%s", tc.want, r.stderr)
			}
			if len(s.calls) != before {
				t.Errorf("%d request(s) sent for a saved plan made as another configuration identity", len(s.calls)-before)
			}
		})
	}
}

func TestApplyPlanUnderTheSameConfigBindDNApplies(t *testing.T) {
	s := inputPlanStub(t, true)
	saved := saveInputAs(t, s, "--config-bind-dn", savedConfigDN)
	// Compared as the bind DN is: without regard to case.
	r := s.run(t, withConfigPassword, "apply", "--plan", saved, "--yes", "--allow-deletes",
		"--config-bind-dn", strings.ToUpper(savedConfigDN))
	expectCode(t, r, ExitOK)
	if len(s.called("POST /api/v1/changeset/apply")) != 1 {
		t.Error("the saved plan was not applied")
	}
}

// A file written before the field existed, with no configuration identity,
// reads and applies as it did.
func TestASavedPlanWithoutTheConfigBindDNStillApplies(t *testing.T) {
	s := inputPlanStub(t, true)
	saved := saveInputAs(t, s)
	data, err := os.ReadFile(saved)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "configBindDn") {
		t.Fatalf("a plan made with no configuration identity names one:\n%s", data)
	}
	r := s.run(t, runOpts{}, "apply", "--plan", saved, "--yes", "--allow-deletes")
	expectCode(t, r, ExitOK)
}

func TestTheSavedEnvironmentIsStillReadStrictly(t *testing.T) {
	s := inputPlanStub(t, true)
	saved := saveInputAs(t, s, "--config-bind-dn", savedConfigDN)
	edit(t, saved, `"configBindDn":`, `"configBindPassword": "x", "configBindDn":`)
	r := s.run(t, withConfigPassword, "apply", "--plan", saved, "--yes", "--allow-deletes", "--config-bind-dn", savedConfigDN)
	expectCode(t, r, ExitNotApplicable)
	if !strings.Contains(r.stderr, "configBindPassword") {
		t.Errorf("stderr does not name the unknown field:\n%s", r.stderr)
	}
}

const projectConfigBind = "    config-bind-dn: cn=Directory Manager\n    config-bind-password-env: ALDER_DEV_CONFIG_PASSWORD\n"

var projectRunWithConfig = runOpts{noConnection: true, env: map[string]string{
	"ALDER_DEV_BIND_PASSWORD": testSecret, "ALDER_DEV_CONFIG_PASSWORD": testSecret,
}}

// withProjectConfigBind gives the dev environment a configuration identity.
func withProjectConfigBind(t *testing.T, dir string) {
	t.Helper()
	edit(t, filepath.Join(dir, "alder.yaml"), "    bind-password-env: ALDER_DEV_BIND_PASSWORD\n",
		"    bind-password-env: ALDER_DEV_BIND_PASSWORD\n"+projectConfigBind)
}

func TestAProjectPlanRecordsTheConfigBindDN(t *testing.T) {
	s := savedPlanStub(t, true)
	dir := writeSavedPlanProject(t, s)
	withProjectConfigBind(t, dir)
	out := filepath.Join(t.TempDir(), "dev.plan.json")
	r := s.run(t, projectRunWithConfig, "project", "plan", "--project", dir, "--env", "dev", "--out", out)
	expectCode(t, r, ExitOK)
	environment, _ := readSaved(t, out)["environment"].(map[string]any)
	if environment["configBindDn"] != savedConfigDN {
		t.Errorf("environment = %v, want configBindDn %q", environment, savedConfigDN)
	}

	r = s.run(t, projectRunWithConfig, "project", "apply", "--project", dir, "--env", "dev", "--plan", out, "--yes")
	expectCode(t, r, ExitOK)
}

func TestProjectApplyPlanRefusesAnotherConfigBindDNBeforeConnecting(t *testing.T) {
	for _, tc := range []struct {
		name         string
		before       func(t *testing.T, dir string)
		after        func(t *testing.T, dir string)
		wantInStderr string
	}{
		{"the environment no longer has one", withProjectConfigBind, func(t *testing.T, dir string) {
			edit(t, filepath.Join(dir, "alder.yaml"), projectConfigBind, "")
		}, "now binds for configuration without config-bind-dn"},
		{"the environment now has one", nil, withProjectConfigBind,
			"now binds for configuration as " + savedConfigDN},
		{"the environment has another", withProjectConfigBind, func(t *testing.T, dir string) {
			edit(t, filepath.Join(dir, "alder.yaml"), "config-bind-dn: cn=Directory Manager", "config-bind-dn: cn=config-admin,dc=alder,dc=test")
		}, "now binds for configuration as cn=config-admin,dc=alder,dc=test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := savedPlanStub(t, true)
			dir := writeSavedPlanProject(t, s)
			if tc.before != nil {
				tc.before(t, dir)
			}
			out := filepath.Join(t.TempDir(), "dev.plan.json")
			r := s.run(t, projectRunWithConfig, "project", "plan", "--project", dir, "--env", "dev", "--out", out)
			expectCode(t, r, ExitOK)
			tc.after(t, dir)
			before := len(s.calls)
			r = s.run(t, projectRunWithConfig, "project", "apply", "--project", dir, "--env", "dev", "--plan", out, "--yes")
			expectCode(t, r, ExitNotApplicable)
			if !strings.Contains(r.stderr, tc.wantInStderr) {
				t.Errorf("stderr does not say %q:\n%s", tc.wantInStderr, r.stderr)
			}
			if len(s.calls) != before {
				t.Errorf("%d request(s) sent for a saved plan made as another configuration identity", len(s.calls)-before)
			}
		})
	}
}
