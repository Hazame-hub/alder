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
