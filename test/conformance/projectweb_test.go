//go:build conformance

package conformance

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/cli"
	"github.com/hazame-hub/alder/internal/directory"
)

// POST /project/plan, the web interface's Open project, against both real
// directories: examples/project, sent as the browser sends it, plans to
// exactly what `alder project plan --json` plans for the same files against
// the same directory, and reports exactly the same unmanaged entries. That
// is the promise of the accepted specification -- one implementation of the
// format, so a project cannot plan one way in the browser and another in CI
// -- held to the directories rather than to a fake.

// webProjectBase is where the example's dc=example,dc=com is moved to, so
// the example plans against the harness without touching its seeded data.
const webProjectBase = "ou=alder-webproject," + suffix

func webProjectDN(rdn string) string { return rdn + "," + webProjectBase }

func clearWebProject(t *testing.T, sess directory.Session) {
	t.Helper()
	deleteQuietly(t, sess,
		webProjectDN("uid=stray,ou=people"),
		webProjectDN("uid=ada,ou=people"),
		webProjectDN("ou=people"),
		webProjectBase)
}

// seedWebProject makes a directory the example has something to say about:
// its people OU, Ada with a title the example changes, and an entry no file
// describes. The groups OU is left out, so that subtree's base is absent.
func seedWebProject(t *testing.T, sess directory.Session) {
	t.Helper()
	clearWebProject(t, sess)
	t.Cleanup(func() { clearWebProject(t, sess) })
	ou := func(d, name string) directory.ChangeRecord {
		return directory.ChangeRecord{DN: mustDN(t, d), Type: directory.ChangeAdd, Attrs: []directory.Attribute{
			{Name: "objectClass", Values: [][]byte{[]byte("top"), []byte("organizationalUnit")}},
			{Name: "ou", Values: [][]byte{[]byte(name)}},
		}}
	}
	mustApply(t, sess, ou(webProjectBase, "alder-webproject"))
	mustApply(t, sess, ou(webProjectDN("ou=people"), "people"))
	mustApply(t, sess, person(t, webProjectDN("uid=ada,ou=people"), "ada", [2]string{"title", "Before"}))
	mustApply(t, sess, person(t, webProjectDN("uid=stray,ou=people"), "stray"))
}

// writeExampleProject copies examples/project/directory with its suffix
// moved under the harness, and its environments replaced by one that
// reaches the harness through the Alder at base -- which the client follows
// and the server, given the same alder.yaml, validates and ignores.
func writeExampleProject(t *testing.T, s server, base string) string {
	t.Helper()
	src := filepath.Join("..", "..", "examples", "project", "directory")
	ca, err := filepath.Abs(filepath.Join("..", "compose", "certs", "ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		text := strings.ReplaceAll(string(raw), "dc=example,dc=com", webProjectBase)
		if filepath.ToSlash(rel) == "alder.yaml" {
			at := strings.Index(text, "\nenvironments:")
			if at < 0 {
				t.Fatalf("examples/project's alder.yaml has no environments section")
			}
			text = text[:at+1] + `environments:
  harness:
    api-url: ` + base + `
    host: ` + s.host + `
    port: ` + strconv.Itoa(s.port) + `
    ca-file: ` + filepath.ToSlash(ca) + `
    server-name: localhost
    bind-dn: ` + s.bindDN + `
    bind-password-env: ALDER_HARNESS_BIND_PASSWORD
`
		}
		writeFile(t, dir, filepath.ToSlash(rel), text)
		return nil
	})
	if err != nil {
		t.Fatalf("copying examples/project: %v", err)
	}
	return dir
}

// browserFiles is what the web interface sends for a chosen folder:
// alder.yaml at its top and every *.ldif below it outside dot-directories.
func browserFiles(t *testing.T, dir string) string {
	t.Helper()
	type file struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	var files []file
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if path != dir && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel != "alder.yaml" && !strings.HasSuffix(strings.ToLower(rel), ".ldif") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files = append(files, file{Path: rel, Content: string(raw)})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"files": files})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestAProjectSentAsFilesPlansAsTheClientDoesOnBothServers(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		client, base := alder(t, s)
		seedWebProject(t, sess)
		dir := writeExampleProject(t, s, base)

		r := runProject(t, s, "project", "plan", "--project", dir, "--env", "harness", "--json")
		wantExit(t, r, cli.ExitOK, "alder project plan")
		fromCLI := decodeMap(t, r.stdout)

		res := post(t, client, base+"/project/plan", browserFiles(t, dir))
		if res.status != http.StatusOK {
			t.Fatalf("POST /project/plan: status %d\n%s", res.status, res.body)
		}
		fromWeb := decodeMap(t, res.body)

		// The same directory, named the same way.
		if fromWeb["host"] != fromCLI["host"] || fromWeb["bindDn"] != s.bindDN {
			t.Errorf("host = %v, bindDn = %v; the client says %v", fromWeb["host"], fromWeb["bindDn"], fromCLI["host"])
		}

		// The same plan, item for item, apart from the session's tokens.
		// A baseline binds a plan to the Alder session that made it.
		cliPlan, webPlan := decodeMap(t, r.stdout)["plan"], decodeMap(t, res.body)["plan"]
		withoutBaselines(cliPlan)
		withoutBaselines(webPlan)
		if !reflect.DeepEqual(cliPlan, webPlan) {
			a, _ := json.MarshalIndent(cliPlan, "", "  ")
			b, _ := json.MarshalIndent(webPlan, "", "  ")
			t.Errorf("the plans differ\n--- alder project plan --json\n%s\n--- POST /project/plan\n%s", a, b)
		}

		// The same unmanaged report, in the same shape.
		if !reflect.DeepEqual(fromCLI["unmanaged"], fromWeb["unmanaged"]) {
			t.Errorf("unmanaged differs\n--- client: %v\n--- server: %v", fromCLI["unmanaged"], fromWeb["unmanaged"])
		}

		// And it is the report the seed implies: the stray person is
		// unmanaged, Ada is not, and the absent groups base has nothing.
		unmanaged, _ := fromWeb["unmanaged"].([]any)
		if len(unmanaged) != 2 {
			t.Fatalf("unmanaged = %v", fromWeb["unmanaged"])
		}
		people := unmanaged[0].(map[string]any)
		var got []string
		for _, d := range people["entries"].([]any) {
			got = append(got, strings.ToLower(d.(string)))
		}
		if strings.Join(got, "|") != strings.ToLower(webProjectDN("uid=stray,ou=people")) || people["error"] != nil {
			t.Errorf("unmanaged in people = %v (error %v)", got, people["error"])
		}
		if groups := unmanaged[1].(map[string]any); len(groups["entries"].([]any)) != 0 || groups["error"] != nil {
			t.Errorf("unmanaged in the absent groups base = %v", groups)
		}

		actions := map[string]string{}
		plan, _ := fromWeb["plan"].(map[string]any)
		for _, raw := range plan["items"].([]any) {
			item := raw.(map[string]any)
			actions[strings.ToLower(item["dn"].(string))] = item["action"].(string)
		}
		if a := actions[strings.ToLower(webProjectDN("uid=ada,ou=people"))]; a != "modify" {
			t.Errorf("Ada is planned %q, want modify (plan: %v)", a, actions)
		}
		if a := actions[strings.ToLower(webProjectDN("uid=grace,ou=people"))]; a != "add" {
			t.Errorf("Grace is planned %q, want add (plan: %v)", a, actions)
		}

		// Planning wrote nothing.
		if exists(t, sess, webProjectDN("uid=grace,ou=people")) {
			t.Error("Grace exists after planning; a plan must not write")
		}
	})
}
