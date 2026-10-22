package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/hazame-hub/alder/internal/apiclient"
	"github.com/hazame-hub/alder/internal/envflags"
	"github.com/hazame-hub/alder/internal/project"
)

// project search: a project's saved searches (docs/PROJECT.md, "Saved
// searches"). Without a name it lists them and connects to nothing; with one
// it runs that search against an environment, as an export, so the result
// is the byte-stable LDIF GET /export/ldif writes and can be committed and
// diffed.

func projectSearchCmd(env *Env) *cobra.Command {
	var o projectOptions
	cmd := command(env, &cobra.Command{
		Use:   "search [NAME --env ENV]",
		Short: "List a project's saved searches, or run one against an environment",
		Long: "A saved search is a file named NAME.search.yaml anywhere below alder.yaml,\n" +
			"outside dot-directories: a base, a scope, a filter, the attributes to ask\n" +
			"for and a limit. Its name is its path without the suffix, such as\n" +
			"people/locked-accounts. See docs/PROJECT.md.\n\n" +
			"Without a name, every search is listed and nothing is connected to.\n\n" +
			"With a name and --env, the search runs against that environment, and its\n" +
			"entries are written to standard output as LDIF: exactly what GET\n" +
			"/export/ldif writes for the same base, scope, filter, limit and\n" +
			"attributes, so a result can be committed and compared. --json writes the\n" +
			"entries POST /search returns, as one array, instead. An environment with\n" +
			"a suffix of its own moves the search's base onto it; the filter is sent\n" +
			"as written, and a DN below the project's suffix inside it is said on\n" +
			"standard error.\n\n" +
			"A search that stopped at its limit says so on standard error. Sensitive\n" +
			"attributes are never asked for, and never written.\n\n" +
			"--env is read from the command line only, never from ALDER_ENV.\n\n" +
			"Exit status: 0 the search ran (whether or not it matched anything) or the\n" +
			"searches were listed, 3 the project is not valid, 7 usage or a name the\n" +
			"project does not have, 8 the directory refused the search or another\n" +
			"failure.",
		Args: argsBetween(0, 1, "at most one search name"),
	}, func(ctx context.Context, args []string) error {
		name := ""
		if len(args) == 1 {
			name = args[0]
		}
		return runProjectSearch(ctx, env, o, name)
	})
	o.registerPath(cmd)
	f := cmd.Flags()
	f.StringVar(&o.envName, "env", "", "the environment to run the search against, as alder.yaml names it")
	f.BoolVar(&o.json, "json", false, "write JSON to standard output")
	f.DurationVar(&o.timeout, "timeout", 10*time.Minute, "give up on the whole command after this long; 0 waits indefinitely")
	envflags.Exclude(f, "project", "env", "json")
	return cmd
}

// searchListing is one search as project search --json lists it: the file's
// values, as the server's POST /project/plan returns them.
type searchListing struct {
	Name        string   `json:"name"`
	File        string   `json:"file"`
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description,omitempty"`
	Base        string   `json:"base"`
	Scope       string   `json:"scope"`
	Filter      string   `json:"filter"`
	Attributes  []string `json:"attributes"`
	Columns     []string `json:"columns"`
	Limit       int      `json:"limit"`
}

func runProjectSearch(ctx context.Context, env *Env, o projectOptions, name string) (err error) {
	defer func() { err = asJSON(env, o.json, err) }()
	switch {
	case name == "" && o.envName != "":
		return usagef("--env runs a search, and no search was named; list them with alder project search")
	case name != "" && o.envName == "":
		return usagef("--env is required to run %s: the environment to search, as alder.yaml names it", safe(name))
	}
	p, err := loadProject(env, o.path)
	if err != nil {
		return err
	}
	if name == "" {
		return listSearches(env, p, o.json)
	}
	s, ok := p.SearchNamed(name)
	if !ok {
		have := "none"
		if names := p.SearchNames(); len(names) > 0 {
			have = strings.Join(names, ", ")
		}
		return usagef("%s has no search %q; it has %s", safe(p.File), name, safe(have))
	}
	e, ok := p.Environments[o.envName]
	if !ok {
		return usagef("%s has no environment %q; it has %s", safe(p.File), o.envName, safe(strings.Join(p.EnvironmentNames(), ", ")))
	}
	conn := connectionFor(p, e, o.timeout)
	if err := conn.check(false); err != nil {
		return err
	}
	run, leftAsWritten := p.SearchForEnvironment(o.envName, s)

	writef(env.Stderr, "alder: searching %s for %s in environment %s: %s through %s\n",
		safe(run.Base.String()), safe(s.Name), safe(o.envName), safe(conn.label()), safe(conn.apiURL))
	if from, to, rebasing := p.Rebasing(o.envName); rebasing {
		writef(env.Stderr, "alder: the base is moved from %s to %s\n", safe(from.String()), safe(to.String()))
		if leftAsWritten {
			writef(env.Stderr, "alder: the filter names a DN below %s, and is sent as written (%s)\n",
				safe(from.String()), project.ContainsDN)
		}
	}

	ctx, cancel := conn.bound(ctx)
	defer cancel()
	r, err := conn.open(ctx, env)
	if err != nil {
		return err
	}
	defer r.close()

	if o.json {
		return searchJSON(ctx, env, r, run)
	}
	return searchLDIF(ctx, env, r, run)
}

func listSearches(env *Env, p *project.Project, asJSONDoc bool) error {
	if asJSONDoc {
		out := make([]searchListing, 0, len(p.Searches))
		for _, s := range p.Searches {
			out = append(out, searchListing{Name: s.Name, File: s.File, Title: s.Title, Description: s.Description,
				Base: s.Base.String(), Scope: s.Scope, Filter: s.Filter,
				Attributes: append([]string{}, s.Attributes...), Columns: append([]string{}, s.Columns...), Limit: s.Limit})
		}
		doc, err := json.Marshal(out)
		if err != nil {
			return failf("output", "cannot encode the searches: %v", err)
		}
		if err := writeDocument(env.Stdout, doc); err != nil {
			return failf("output", "cannot write to standard output: %v", err)
		}
		return nil
	}
	if len(p.Searches) == 0 {
		writef(env.Stdout, "%s: no saved searches. A search is a file named NAME%s below it.\n",
			safe(p.File), project.SearchSuffix)
		return nil
	}
	for _, s := range p.Searches {
		heading := s.Name
		if s.Title != "" {
			heading += " -- " + s.Title
		}
		writef(env.Stdout, "%s\n  base %s (%s), limit %d\n  filter %s\n", safe(heading), safe(s.Base.String()), s.Scope,
			s.Limit, safe(s.Filter))
		if len(s.Attributes) > 0 {
			writef(env.Stdout, "  attributes %s\n", safe(strings.Join(s.Attributes, ", ")))
		}
	}
	return nil
}

// searchLDIF writes the search's entries as the export writes them. The
// export's own trailer says whether it is complete; the client repeats a
// truncation or an error on standard error, where a person running it sees
// it, and an incomplete export fails the command.
func searchLDIF(ctx context.Context, env *Env, r *remote, s project.Search) error {
	what := "running the search " + s.Name
	scope := apiclient.ExportLdifParamsScope(s.Scope)
	params := &apiclient.ExportLdifParams{Dn: s.Base.String(), Scope: &scope, Filter: &s.Filter, Limit: &s.Limit}
	if len(s.Attributes) > 0 {
		params.Attributes = ptr(strings.Join(s.Attributes, ","))
	}
	res, err := r.api.ExportLdif(ctx, params)
	if err != nil {
		return transportFailure(ctx, what, err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		// The export answers "nothing matched" as a refusal, since an empty
		// file is not an export. For a search it is an answer: asked again
		// to be sure that is what it was.
		if res.StatusCode == http.StatusBadRequest {
			if matched, checkErr := searchMatches(ctx, r, s); checkErr == nil && !matched {
				writef(env.Stderr, "alder: %s matched no entry\n", safe(s.Name))
				return nil
			}
		}
		return r.refusal(ctx, what, res, body)
	}
	tail := &tailWriter{w: env.Stdout}
	if _, err := io.Copy(tail, res.Body); err != nil {
		return failf("incomplete_export", "the search %s stopped before its last entry: %v", safe(s.Name), err)
	}
	// Comment lines are the server's own: an entry's values are never at the
	// start of a line, so a value that says "# ERROR:" cannot read as one.
	switch end := "\n" + tail.String(); {
	case strings.Contains(end, "\n# ERROR: "):
		return failf("incomplete_export", "the search %s did not complete; the reason is at the end of the output", safe(s.Name))
	case strings.Contains(end, "\n# WARNING: the result was truncated"):
		writef(env.Stderr, "alder: %s stopped at its limit of %d entries; the directory holds more that match\n",
			safe(s.Name), s.Limit)
	}
	return nil
}

// searchMatches asks whether the search matches anything at all, for the
// only answer the export gives as a refusal.
func searchMatches(ctx context.Context, r *remote, s project.Search) (bool, error) {
	res, err := r.api.Search(ctx, apiclient.SearchRequest{BaseDn: s.Base.String(), Scope: apiclient.SearchRequestScope(s.Scope),
		Filter: s.Filter, Limit: ptr(1), Attributes: &[]string{"1.1"}})
	if err != nil {
		return false, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return false, fmt.Errorf("status %d", res.StatusCode)
	}
	var page struct {
		Entries []json.RawMessage `json:"entries"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&page); err != nil {
		return false, err
	}
	return len(page.Entries) > 0, nil
}

// searchJSON writes the entries POST /search returns for the search, as one
// array. One request: the server pages the directory itself up to the
// search's limit, which is at most 10,000 entries.
func searchJSON(ctx context.Context, env *Env, r *remote, s project.Search) error {
	what := "running the search " + s.Name
	req := apiclient.SearchRequest{BaseDn: s.Base.String(), Scope: apiclient.SearchRequestScope(s.Scope), Filter: s.Filter,
		Limit: ptr(s.Limit), PageSize: ptr(min(s.Limit, 1000))}
	if len(s.Attributes) > 0 {
		req.Attributes = ptr(append([]string{}, s.Attributes...))
	}
	res, err := r.api.Search(ctx, req)
	if err != nil {
		return transportFailure(ctx, what, err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		return r.refusal(ctx, what, res, body)
	}
	var page struct {
		Entries   json.RawMessage `json:"entries"`
		Truncated bool            `json:"truncated"`
		Referrals []string        `json:"referrals"`
	}
	if err := json.NewDecoder(res.Body).Decode(&page); err != nil {
		return failf("incomplete_search", "what Alder sent while %s is not a search result: %v", what, err)
	}
	if page.Truncated {
		writef(env.Stderr, "alder: %s stopped at its limit of %d entries; the directory holds more that match\n",
			safe(s.Name), s.Limit)
	}
	if len(page.Referrals) > 0 {
		writef(env.Stderr, "alder: %s met %d referral(s), not followed\n", safe(s.Name), len(page.Referrals))
	}
	entries := page.Entries
	if len(bytes.TrimSpace(entries)) == 0 || string(entries) == "null" {
		entries = json.RawMessage("[]")
	}
	if err := writeDocument(env.Stdout, entries); err != nil {
		return failf("output", "cannot write to standard output: %v", err)
	}
	return nil
}

// tailWriter passes everything through and keeps the last few kilobytes, so
// the export's trailer can be read without holding the export.
type tailWriter struct {
	w    io.Writer
	tail []byte
}

const tailBytes = 4 << 10

func (t *tailWriter) Write(p []byte) (int, error) {
	n, err := t.w.Write(p)
	t.tail = append(t.tail, p[:n]...)
	if len(t.tail) > tailBytes {
		t.tail = append(t.tail[:0], t.tail[len(t.tail)-tailBytes:]...)
	}
	return n, err
}

func (t *tailWriter) String() string { return string(t.tail) }
