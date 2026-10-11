package project

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// symlinkOrSkip creates a symlink, or skips where the platform will not let
// this process make one (Windows without developer mode, for instance).
func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}
}

// A symlink inside the project that points elsewhere on the machine is
// refused, and its content is never read into a message: the file might be a
// runner's secret.
func TestASymlinkOutOfTheProjectIsRefusedUnread(t *testing.T) {
	outside := t.TempDir()
	const secret = "dn: uid=leak,ou=people,dc=alder,dc=test\ndescription: RUNNER-SECRET-CONTENT\n"
	target := filepath.Join(outside, "secret.ldif")
	if err := os.WriteFile(target, []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := write(t, valid())
	symlinkOrSkip(t, target, filepath.Join(dir, "people", "c.ldif"))

	_, err := load(t, dir)
	var inv *Invalid
	if !errors.As(err, &inv) {
		t.Fatalf("want an invalid project, got %v", err)
	}
	text := problemsOf(err)
	if !strings.Contains(text, "people/c.ldif resolves to a file outside the project directory") {
		t.Errorf("the refused file is not named:\n%s", text)
	}
	if strings.Contains(text, "RUNNER-SECRET-CONTENT") || strings.Contains(text, "uid=leak") {
		t.Errorf("a problem echoes the refused file's content:\n%s", text)
	}
}

// A symlink that stays inside the project is an ordinary file of it.
func TestASymlinkInsideTheProjectIsAllowed(t *testing.T) {
	files := valid()
	delete(files, "people/b.ldif")
	files["shared/b.ldif"] = peopleB
	dir := write(t, files)
	symlinkOrSkip(t, filepath.Join(dir, "shared", "b.ldif"), filepath.Join(dir, "people", "b.ldif"))

	p, err := load(t, dir)
	if err != nil {
		t.Fatalf("a symlink inside the project was refused: %v", problemsOf(err))
	}
	if p.Entries() != 3 {
		t.Errorf("entries = %d, want 3: the linked file should be read", p.Entries())
	}
}
