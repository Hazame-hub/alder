package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/hazame-hub/alder/internal/envflags"
	"github.com/hazame-hub/alder/internal/ldif"
)

func projectFmtCmd(env *Env) *cobra.Command {
	var (
		path  string
		check bool
	)
	cmd := command(env, &cobra.Command{
		Use:   "fmt",
		Short: "Rewrite a project's LDIF files in Alder's canonical form",
		Long: "Rewrites every LDIF file the project names in the form Alder's own exports\n" +
			"take, so that a Git diff of one shows what changed and not how it was\n" +
			"wrapped. Records, attributes and values keep their order and attribute\n" +
			"names their spelling; comments are kept exactly as written. What changes is\n" +
			"how each line is written down: folded at 76 columns, one space after the\n" +
			"colon, base64 only where a value needs it, DNs written as Alder writes\n" +
			"them, one blank line between records, LF line endings.\n\n" +
			"The project is validated first, and an invalid one is not touched. Each\n" +
			"file's new form is read back and checked to say exactly what the old one\n" +
			"said before anything is written, and a file is replaced whole or not at all.\n" +
			"Nothing connects to a directory.\n\n" +
			"The files it rewrote are listed. --check writes nothing and lists the files\n" +
			"that are not in canonical form, for a CI job or a pre-commit hook.\n\n" +
			"Exit status: 0 formatted, or with --check already canonical; 1 with --check,\n" +
			"some file is not; 3 the project is not valid; 7 usage; 8 a file could not be\n" +
			"read or written.",
		Args: argsBetween(0, 0, "no arguments; name the project with --project"),
	}, func(_ context.Context, _ []string) error {
		return runProjectFmt(env, path, check)
	})
	f := cmd.Flags()
	f.StringVar(&path, "project", "",
		"the project: its directory or its alder.yaml (default: the current directory)")
	f.BoolVar(&check, "check", false, "write nothing; list the files that are not canonical and exit 1 if any is not")
	envflags.Exclude(f, "project", "check")
	return cmd
}

// fmtFile is one project file and its canonical form.
type fmtFile struct {
	shown, real string
	formatted   []byte
	mode        os.FileMode
}

func runProjectFmt(env *Env, path string, check bool) error {
	p, err := loadProject(env, path)
	if err != nil {
		return err
	}
	// Every file is formatted before any is written, so a file that cannot
	// be formatted leaves the whole project as it was.
	var changed []fmtFile
	for _, st := range p.Managed {
		for _, rel := range st.Files {
			shown := p.Path(filepath.FromSlash(rel))
			// Load has already refused a symlink that leaves the project.
			// One that stays inside is written through, at its target, so it
			// stays a symlink.
			real, err := filepath.EvalSymlinks(shown)
			if err != nil {
				return failf("input", "cannot resolve %s: %v", shown, err)
			}
			info, err := os.Stat(real)
			if err != nil {
				return failf("input", "cannot read %s: %v", shown, err)
			}
			raw, err := os.ReadFile(real) // #nosec G304 -- a file the validated project names
			if err != nil {
				return failf("input", "cannot read %s: %v", shown, err)
			}
			formatted, err := ldif.Format(raw)
			if err != nil {
				return failf("format", "cannot format %s: %v", shown, err)
			}
			if !bytes.Equal(raw, formatted) {
				changed = append(changed, fmtFile{shown: shown, real: real, formatted: formatted, mode: info.Mode().Perm()})
			}
		}
	}

	for _, f := range changed {
		if !check {
			if err := replaceFile(f.real, f.formatted, f.mode); err != nil {
				return failf("output", "cannot write %s: %v", f.shown, err)
			}
		}
		writef(env.Stdout, "%s\n", f.shown)
	}
	if check && len(changed) > 0 {
		return &ExitError{Code: ExitDifferences}
	}
	return nil
}

// replaceFile replaces a file whole, keeping its permissions: the new content
// goes to a temporary file beside it, which then takes its name, so an
// interrupted run leaves the old file or the new one and never half of each.
func replaceFile(path string, data []byte, mode os.FileMode) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.partial")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Chmod(mode); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
