package config

import (
	"strings"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// OpenLDAP's configuration model.
//
// The configuration is the cn=config tree: a global entry, the modules it
// loads, one entry per database, and one per overlay beneath its database. A
// database is identified by its suffix and an overlay by its name, because the
// {n} in their DNs is a position -- it changes when something else is removed,
// and it is not what anybody means by "the mdb database".
//
// The schema lives in this tree too, under cn=schema,cn=config, and is not
// captured here: it has a snapshot kind of its own, and duplicating thousands
// of definitions into a configuration document would make two answers to the
// same question. Which schema entries are loaded is configuration, so the names
// are recorded and nothing else.

const openldapSchemaSuffix = "cn=schema,cn=config"

var openldapModel = model{
	provider: snapshot.ProviderOpenLDAP,
	classifier: classifier{
		section: openldapSection,
		// Settings Alder already changes the way it changes any entry: one
		// attribute of one configuration entry, through the ordinary plan.
		// Nothing is here because it would be nice to support; each is a
		// scalar an administrator sets, on an entry that already exists.
		//
		// Every one is written, read back and restored through Alder on the
		// harness by the conformance suite; a setting that cannot be proved
		// that way is not here. Left out on purpose, with the reason in
		// DECISIONS.md: anything that can lock a person out (TLS, SASL, root
		// DN, access rules, olcLocalSSF, the socket buffer limits), anything
		// read only when the server starts (olcDbMaxReaders, olcListenerThreads),
		// anything that silently invalidates an index (olcIndex*, olcDbIndex),
		// and the password policy overlay's switches.
		writable: set(
			"olcidletimeout", "olcsizelimit", "olctimelimit", "olcloglevel",
			"olcreadonly", "olcdbmaxsize", "olclastmod", "olcmaxderefdepth",
			"olcdbnosync", "olcdbsearchstack", "olcdbmaxentrysize",
			"olcwritetimeout", "olcconnmaxpending", "olcconnmaxpendingauth", "olcmonitoring",
			// Added in 1.14.
			"olcmaxfilterdepth", "olcthreads", "olcgentlehup", "olcdbrtxnsize", "olclastbind",
		),
		// Read-only mode on one database stops writes to that database. On the
		// global entry, the frontend or the configuration database it stops
		// writes to the configuration as well, and nothing could switch it back.
		writableOn: func(resource Resource, key string) bool {
			if key != "olcreadonly" {
				return true
			}
			return resource.Kind == KindDatabase &&
				!strings.EqualFold(resource.Name, "config") && !strings.EqualFold(resource.Name, "frontend")
		},
		readOnly: set(
			"olcconfigfile", "olcconfigdir", "olcargsfile", "olcpidfile", "olcdatabase",
			"olcoverlay", "cn", "olclocalssf", "olcdbdirectory",
		),
		// An access rule and a replication agreement are written with a
		// position in front of them, and that position is what orders them.
		ordered: set("olcaccess", "olcsyncrepl", "olclimits", "olcmodulepath", "olcmoduleload"),
		// Values with a syntax of their own that Alder does not parse.
		structured: set("olcaccess", "olclimits", "olcsyncrepl", "olcdbindex", "olcauthzregexp",
			"olcpasswordhash", "olcsecurity", "olctlsciphersuite"),
		// olcSyncrepl carries the credentials the replica binds with inside
		// the value, so the whole value is a secret.
		sensitive: set("olcrootpw", "olcsyncrepl", "olcdbcryptkey"),
	},
	// Everything under cn=schema,cn=config is the schema snapshot's subject.
	skipTree: func(entry *directory.Entry) bool {
		d := strings.ToLower(entry.DN.String())
		return d == openldapSchemaSuffix || strings.HasSuffix(d, ","+openldapSchemaSuffix)
	},
	resource: openldapResource,
}

// openldapSection places an attribute in a section, from the attribute's own
// name and the resource it sits on.
func openldapSection(resource Resource, attribute string) string {
	key := strings.ToLower(attribute)
	switch {
	case strings.HasPrefix(key, "olctls"):
		return SectionTLS
	case key == "olcaccess" || key == "olcaddcontentacl" || key == "olcauthzpolicy" || key == "olcauthzregexp":
		return SectionAccessControl
	case strings.HasPrefix(key, "olclog") || key == "olclogfile":
		return SectionLogging
	case strings.HasPrefix(key, "olcmodule"):
		return SectionPlugins
	case strings.HasPrefix(key, "olcsyncrepl") || key == "olcmirrormode" || key == "olcserverid" ||
		strings.HasPrefix(key, "olcrepl") || key == "olcupdateref" || key == "olcsyncusesubentry":
		return SectionReplication
	case strings.HasPrefix(key, "olcppolicy") || strings.HasPrefix(key, "olcpassword"):
		return SectionPasswordPolicy
	case key == "olcsizelimit" || key == "olctimelimit" || key == "olcidletimeout" ||
		key == "olclimits" || key == "olcmaxderefdepth" || key == "olcwritetimeout" ||
		key == "olcmaxfilterdepth" || key == "olcdbmaxentrysize":
		return SectionLimits
	case strings.HasPrefix(key, "olcdb") || key == "olcthreads" || key == "olctoolthreads" ||
		key == "olcconcurrency" || key == "olcthreadqueues" || strings.HasPrefix(key, "olcsockbuf") ||
		strings.HasPrefix(key, "olcindexsubstr") || key == "olcindexintlen" || key == "olcindexhash64" ||
		strings.HasPrefix(key, "olcconnmaxpending") || key == "olclistenerthreads":
		if resource.Kind == KindDatabase && strings.HasPrefix(key, "olcdb") {
			return SectionPerformance
		}
		return SectionPerformance
	case key == "olcsuffix" || key == "olcrootdn" || key == "olcrootpw" || key == "olcsubordinate" ||
		key == "olcreadonly" || key == "olclastmod" || key == "olclastbind" || key == "olcmonitoring":
		return SectionBackend
	case key == "olcreverselookup" || strings.HasPrefix(key, "olcsasl") || key == "olcauthidrewrite":
		return SectionNetwork
	}
	if resource.Kind == KindOverlay {
		return SectionPlugins
	}
	if resource.Kind == KindDatabase {
		return SectionBackend
	}
	return SectionServer
}

// openldapResource names the configuration object an entry is, and returns
// false for an entry the model does not cover.
func openldapResource(entry *directory.Entry, parents map[string]Resource) (Resource, bool) {
	d := entry.DN
	text := strings.ToLower(d.String())
	switch {
	case text == "cn=config":
		return Resource{Section: SectionServer, Kind: KindGlobal, Name: "global", DN: d.String(), Label: "server"}, true
	case strings.HasPrefix(text, "olcdatabase="):
		name := firstValue(entry, "olcSuffix")
		label := stripIndex(rdnValue(entry))
		if name == "" {
			name = label
		}
		return Resource{Section: SectionBackend, Kind: KindDatabase, Name: name, DN: d.String(), Label: label}, true
	case strings.HasPrefix(text, "olcoverlay="):
		overlay := stripIndex(rdnValue(entry))
		parent := parents[strings.ToLower(d.Parent().String())]
		name := overlay
		if parent.Name != "" {
			name = parent.Name + "/" + overlay
		}
		return Resource{Section: SectionPlugins, Kind: KindOverlay, Name: name, DN: d.String(), Label: overlay}, true
	case strings.HasPrefix(text, "cn=module"):
		// A module list is named by the path it loads from: its own RDN is
		// cn=module{0}, and that number is a position in the configuration
		// rather than anything about the modules.
		name := firstValue(entry, "olcModulePath")
		if name == "" {
			name = strings.TrimSuffix(stripIndex(rdnValue(entry)), "{0}")
		}
		return Resource{Section: SectionPlugins, Kind: KindModule, Name: name, DN: d.String(),
			Label: "modules"}, true
	}
	// Something this model does not know: recorded as an area of the server's
	// configuration rather than left out, so a difference in it is still seen.
	return Resource{Section: SectionOther, Kind: KindArea, Name: strings.ToLower(d.String()), DN: d.String(),
		Label: rdnValue(entry)}, true
}

// openldapSchemaCollections records which schema entries are loaded -- names
// only. What they define is a schema snapshot's subject, not this one's.
func openldapSchemaCollections(entries []*directory.Entry) []string {
	var out []string
	for _, entry := range entries {
		text := strings.ToLower(entry.DN.String())
		if !strings.HasSuffix(text, ","+openldapSchemaSuffix) {
			continue
		}
		out = append(out, stripIndex(rdnValue(entry)))
	}
	return out
}

func set(values ...string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, v := range values {
		out[v] = true
	}
	return out
}
