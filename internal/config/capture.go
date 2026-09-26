package config

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/filter"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// Reading a server's configuration.
//
// One paged search of the configuration tree, the same way everything else in
// Alder reads a subtree, and then the provider's model over what came back.
// Nothing is written, nothing is probed outside the tree the server itself
// announced or answered at, and an entry the bind may not read simply does not
// arrive -- which is why a capture that could not read everything says so
// rather than reporting a smaller configuration.

// model is one provider's configuration model.
type model struct {
	provider   string
	classifier classifier
	// skipTree reports an entry that belongs to something else: the schema,
	// the task queue, a monitor.
	skipTree func(*directory.Entry) bool
	// resource names the configuration object an entry is.
	resource func(entry *directory.Entry, parents map[string]Resource) (Resource, bool)
	// restartRequired reads, from the tree itself, which settings the server
	// says need a restart. Nil for a server that publishes no such list.
	restartRequired func(entries []*directory.Entry) map[string]bool
	// switchablePlugins reads which plugins may be switched on or off without
	// stopping the server from starting. Nil where the question does not
	// arise.
	switchablePlugins func(entries []*directory.Entry) map[string]bool
}

// ErrNoModel is returned for a server Alder has no configuration model for.
var ErrNoModel = errors.New("config: Alder has no configuration model for this server")

// ErrUnreadable is returned when the configuration tree cannot be read at all.
var ErrUnreadable = errors.New("config: this session cannot read the server's configuration tree")

// Reader is what a capture needs: a paged search, and nothing that writes.
type Reader interface {
	Capabilities() directory.Capabilities
	Search(ctx context.Context, req directory.SearchRequest) (*directory.SearchResult, error)
}

// ProviderFor returns the configuration model identifier for a server, from
// what the server says about itself.
//
// This is the one place a vendor name is read for something other than
// display, and it is unavoidable: a configuration model belongs to a piece of
// software, and nothing in LDAP announces which model a configuration tree
// follows. It is a last resort rather than a first: the shape of the tree
// decides first, and the name only breaks a tie.
func ProviderFor(caps directory.Capabilities) string {
	vendor := strings.ToLower(caps.VendorName)
	switch {
	case strings.Contains(vendor, "389"), strings.Contains(vendor, "red hat"), strings.Contains(vendor, "netscape"):
		return snapshot.Provider389DS
	case strings.Contains(vendor, "openldap"):
		return snapshot.ProviderOpenLDAP
	}
	return ""
}

// ProviderFromTree decides a provider from what the configuration tree holds,
// which is a fact about the server rather than about its name: only OpenLDAP's
// configuration is written in olc* attributes, and only 389 DS's in nsslapd-*.
func ProviderFromTree(entries []*directory.Entry) string {
	olc, nsslapd := 0, 0
	for _, entry := range entries {
		for name := range entry.Attributes {
			name = strings.ToLower(name)
			switch {
			case strings.HasPrefix(name, "olc"):
				olc++
			case strings.HasPrefix(name, "nsslapd-"), strings.HasPrefix(name, "nsds5"):
				nsslapd++
			}
		}
	}
	switch {
	case olc > nsslapd && olc > 0:
		return snapshot.ProviderOpenLDAP
	case nsslapd > 0:
		return snapshot.Provider389DS
	}
	return ""
}

func modelFor(provider string) (model, bool) {
	switch provider {
	case snapshot.ProviderOpenLDAP:
		return openldapModel, true
	case snapshot.Provider389DS:
		return ds389Model, true
	}
	return model{}, false
}

// Options steer a capture.
type Options struct {
	// Now is the clock the snapshot is stamped with.
	Now func() time.Time
	// PageSize is how many entries one search page asks for.
	PageSize int
}

func (o Options) now() time.Time {
	if o.Now == nil {
		return time.Now()
	}
	return o.Now()
}

// tree is a configuration tree as it was read, with the provider's model
// chosen for it.
//
// Reading the tree, deciding which model it follows and building the
// classifier over it are the same three steps whether the answer wanted is a
// whole snapshot or what the model says about one entry. They are here once so
// the two answers cannot come from different readings of the same server.
type tree struct {
	caps      directory.Capabilities
	base      dn.DN
	entries   []*directory.Entry
	truncated bool
	model     model
}

func readModel(ctx context.Context, r Reader, opts Options) (*tree, error) {
	caps := r.Capabilities()
	root := caps.Config.DN
	if root == "" {
		root = caps.ConfigContext
	}
	if root == "" {
		return nil, ErrUnreadable
	}
	base, err := dn.Parse(root)
	if err != nil {
		return nil, ErrUnreadable
	}

	entries, truncated, err := readTree(ctx, r, base, opts)
	if err != nil {
		// The server announced a configuration tree this session cannot read.
		// That is a fact about the bind, not a missing entry, and it is said
		// as such rather than passed on as the directory's own error.
		return nil, errors.Join(ErrUnreadable, err)
	}
	provider := ProviderFromTree(entries)
	if provider == "" {
		provider = ProviderFor(caps)
	}
	m, ok := modelFor(provider)
	if !ok {
		return nil, ErrNoModel
	}
	// Entries in DN order, shortest first, so a parent is always known before
	// the entries beneath it: an overlay is named after its database.
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i].DN.String(), entries[j].DN.String()
		if strings.Count(a, ",") != strings.Count(b, ",") {
			return strings.Count(a, ",") < strings.Count(b, ",")
		}
		return strings.ToLower(a) < strings.ToLower(b)
	})
	return &tree{caps: caps, base: base, entries: entries, truncated: truncated, model: m}, nil
}

// classify is the provider's classifier with what this particular server says
// about itself folded in: which settings need a restart, which plugins it can
// run without.
func (t *tree) classify() classifier {
	c := t.model.classifier
	if t.model.restartRequired != nil {
		c.restart = t.model.restartRequired(t.entries)
	}
	if t.model.switchablePlugins != nil {
		c.switchable = t.model.switchablePlugins(t.entries)
	}
	return c
}

// Capture reads a server's configuration and returns it as a snapshot.
func Capture(ctx context.Context, r Reader, opts Options) (*snapshot.ConfigSnapshot, error) {
	read, err := readModel(ctx, r, opts)
	if err != nil {
		return nil, err
	}
	caps, base, entries, truncated, m := read.caps, read.base, read.entries, read.truncated, read.model

	capture := snapshot.ConfigCapture{
		Provider: m.provider, Vendor: caps.VendorName, VendorVersion: caps.VendorVersion,
		Root: base.String(), Sections: Sections,
		Excluded:  []string{snapshot.ExcludedRuntime, snapshot.ExcludedSchema, snapshot.ExcludedSecrets, snapshot.ExcludedServerOwned},
		CreatedAt: opts.now(),
	}
	if truncated {
		capture.Incomplete = append(capture.Incomplete, snapshot.ConfigIncomplete{
			Reason: snapshot.IncompleteLimit, Scope: base.String(),
			Detail: "the configuration tree holds more entries than one capture reads"})
	}
	if !caps.Config.Readable {
		capture.Incomplete = append(capture.Incomplete, snapshot.ConfigIncomplete{
			Reason: snapshot.IncompleteAccess, Scope: base.String(), Detail: caps.Config.Reason})
	}

	classify := read.classify()

	resources := map[string]Resource{}
	byDN := map[string]Resource{}
	var settings []Setting
	schemaEntries := 0
	for _, entry := range entries {
		if m.skipTree != nil && m.skipTree(entry) {
			if m.provider == snapshot.ProviderOpenLDAP {
				schemaEntries++
			}
			continue
		}
		resource, ok := m.resource(entry, byDN)
		if !ok {
			continue
		}
		byDN[strings.ToLower(entry.DN.String())] = resource
		if _, seen := resources[resource.ID()]; !seen {
			resources[resource.ID()] = resource
		}
		for _, name := range attributeNames(entry) {
			setting, keep := settingFrom(classify, resource, entry, name, entry.GetStrings(name))
			if !keep {
				continue
			}
			settings = append(settings, setting)
		}
	}

	// Which schema entries are loaded is configuration; what they define is a
	// schema snapshot's subject.
	if m.provider == snapshot.ProviderOpenLDAP && schemaEntries > 0 {
		collections := openldapSchemaCollections(entries)
		// Names only, and no DN: this is not one entry's attribute but a fact
		// about the tree, and nothing in it comes from a schema definition.
		settings = append(settings, Setting{
			Section: SectionSchema, Key: "schemaCollections", Values: collections,
			Type: TypeString, Mutability: snapshot.MutabilityReadOnly, Comparison: snapshot.ComparisonNormalised,
		})
	}

	list := make([]Resource, 0, len(resources))
	for _, resource := range resources {
		list = append(list, resource)
	}
	return snapshot.BuildConfig(capture, list, settings)
}

// readTree reads the configuration subtree, in pages, up to the bound a
// snapshot holds.
func readTree(ctx context.Context, r Reader, base dn.DN, opts Options) ([]*directory.Entry, bool, error) {
	page := opts.PageSize
	if page <= 0 || page > directory.MaxPageSize {
		page = directory.MaxPageSize
	}
	all, err := filter.Parse("(objectClass=*)")
	if err != nil {
		return nil, false, err
	}
	var entries []*directory.Entry
	var cookie []byte
	truncated := false
	for {
		want := snapshot.MaxConfigResources + 1 - len(entries)
		if want > page {
			want = page
		}
		if want <= 0 {
			truncated = true
			break
		}
		res, err := r.Search(ctx, directory.SearchRequest{
			BaseDN: base, Scope: directory.ScopeSubtree, Filter: all,
			Attributes: []string{"*"}, Limit: want, PageSize: want, Cookie: cookie,
		})
		if err != nil {
			if len(entries) == 0 {
				return nil, false, err
			}
			// Part of the tree answered and part did not: what was read is
			// reported, and the capture says it is partial.
			return entries, true, nil
		}
		entries = append(entries, res.Entries...)
		if len(res.Referrals) > 0 || (res.Truncated && len(res.Cookie) == 0) {
			truncated = true
			break
		}
		if len(entries) > snapshot.MaxConfigResources {
			entries = entries[:snapshot.MaxConfigResources]
			truncated = true
			break
		}
		if len(res.Cookie) == 0 {
			break
		}
		cookie = res.Cookie
	}
	return entries, truncated, nil
}

// attributeNames are an entry's attributes in the order the server returned
// them, which a snapshot then canonicalises away.
func attributeNames(entry *directory.Entry) []string {
	if len(entry.Order) == len(entry.Attributes) {
		return entry.Order
	}
	out := make([]string, 0, len(entry.Attributes))
	for name := range entry.Attributes {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// WritableKeys lists, sorted, the settings a provider's model states Alder
// changes through the ordinary plan -- before narrowing by resource and before
// anything the server says needs a restart. The conformance suite proves each
// one against the harness, and the documentation is written from it.
func WritableKeys(provider string) []string {
	m, ok := modelFor(provider)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(m.classifier.writable))
	for key := range m.classifier.writable {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
