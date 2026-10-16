package cli

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/hazame-hub/alder/internal/project"
)

// rebaseReport is what project plan and project apply say about an
// environment whose suffix is not the project's: the plan holds the files'
// entries under that environment's names, so it is no longer byte for byte
// what was reviewed, and this says how it differs.
type rebaseReport struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Entries and Values are how many entry names and DN-valued attribute
	// values were moved from below From to below To.
	Entries int `json:"entries"`
	Values  int `json:"values"`
	// LeftAsWritten are values that read as a DN below From, of attribute
	// types without DN syntax in the directory's schema. Not rewritten.
	LeftAsWritten []leftAsWritten `json:"leftAsWritten"`
}

type leftAsWritten struct {
	DN        string `json:"dn"`
	Attribute string `json:"attribute"`
}

// forEnvironment is the project as the named environment holds it. When the
// environment names a suffix of its own, the directory's schema is read to
// learn which attribute types hold DNs, and only their values are moved
// with the names: what a value looks like decides nothing.
func forEnvironment(ctx context.Context, r *remote, p *project.Project, envName string) (*project.Project, *rebaseReport, error) {
	if _, _, ok := p.Rebasing(envName); !ok {
		return p, nil, nil
	}
	kinds, err := dnKinds(ctx, r)
	if err != nil {
		return nil, nil, err
	}
	q, rb, err := p.ForEnvironment(envName, kinds)
	var un *project.Unrebasable
	switch {
	case errors.As(err, &un):
		// The files say what they say; it is this environment that cannot
		// take them as written, which is what 3 means.
		return nil, nil, &ExitError{Code: ExitNotApplicable, Local: "project_not_rebasable", Message: safe(err.Error())}
	case err != nil:
		return nil, nil, failf("project", "cannot rebase the project: %s", safe(err.Error()))
	}
	report := &rebaseReport{From: rb.From.String(), To: rb.To.String(), Entries: rb.Entries, Values: rb.Values,
		LeftAsWritten: make([]leftAsWritten, 0, len(rb.LeftAsWritten))}
	for _, u := range rb.LeftAsWritten {
		report.LeftAsWritten = append(report.LeftAsWritten, leftAsWritten{DN: u.DN.String(), Attribute: u.Attribute})
	}
	return q, report, nil
}

// dnKinds reads the schema of the directory the session is bound to, through
// Alder, and answers for each attribute type, by any of its names or its OID,
// whether its values are DNs. The syntax Alder reports is the effective one,
// inherited from a superior type where the type names none.
func dnKinds(ctx context.Context, r *remote) (project.DNKinds, error) {
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
	kinds := map[string]project.DNKind{}
	for _, at := range *res.JSON200.AttributeTypes {
		syntax := ""
		if at.Syntax != nil {
			syntax = *at.Syntax
		}
		k := project.DNKindOfSyntax(syntax)
		names := []string{at.Name, at.Oid}
		if at.Names != nil {
			names = append(names, *at.Names...)
		}
		for _, n := range names {
			if n != "" {
				kinds[strings.ToLower(n)] = k
			}
		}
	}
	return func(attr string) (project.DNKind, bool) {
		k, ok := kinds[strings.ToLower(attr)]
		return k, ok
	}, nil
}

// projectDocument renders a project as the one LDIF document a plan request
// carries, refusing one larger than a request may be.
func projectDocument(p *project.Project) (string, error) {
	document, err := p.Document()
	if err != nil {
		return "", failf("project", "cannot render the project as LDIF: %v", err)
	}
	if len(document) > maxLDIFBytes {
		return "", failf("project_too_large", "the project renders to %d bytes of LDIF, more than one plan request carries (%d)",
			len(document), maxLDIFBytes)
	}
	return document, nil
}

// announceRebase says, before anything is sent, that this environment's
// names are not the files' own.
func announceRebase(errs io.Writer, p *project.Project, envName string) {
	if from, to, ok := p.Rebasing(envName); ok {
		writef(errs, "alder: environment %s names the suffix %s: the project's %s is rebased onto it\n",
			safe(envName), safe(to.String()), safe(from.String()))
	}
}

// renderEnvironment is the heading above a project's plan: the environment
// and the directory, and, when the names were rebased, from and onto what.
// Values left as written are said on errs, with the rest of the commentary.
func renderEnvironment(w, errs io.Writer, envName, host string, rb *rebaseReport) {
	writef(w, "Environment %s (%s)\n", safe(envName), safe(host))
	if rb != nil {
		writef(w, "Suffix %s -> %s: %s and %s rebased from the files\n", safe(rb.From), safe(rb.To),
			plural(rb.Entries, "entry", "entries"), plural(rb.Values, "DN value", "DN values"))
		reportLeftAsWritten(errs, rb)
	}
	writeln(w)
}

func reportLeftAsWritten(errs io.Writer, rb *rebaseReport) {
	for _, u := range rb.LeftAsWritten {
		writef(errs, "alder: %s of %s reads as a DN below %s and was left as written: "+
			"this directory's schema does not give %s DN syntax\n",
			safe(u.Attribute), safe(u.DN), safe(rb.From), safe(u.Attribute))
	}
}
