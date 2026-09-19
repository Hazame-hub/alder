package config

import (
	"strings"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// 389 Directory Server's configuration model.
//
// The configuration is cn=config and what hangs beneath it: the global entry,
// encryption, the backends under cn=ldbm database, the mapping tree, the
// plugins, replication. A backend is identified by its suffix where it has one
// and by its name otherwise; a plugin by its name. Nothing is identified by
// position.
//
// Two subtrees below cn=config are not configuration and are not captured:
// cn=tasks, which is how a running server is told to do something, and the
// cn=monitor entries, which are counters. The schema is not under cn=config
// here at all -- it is cn=schema, and it has its own snapshot kind.

var ds389Model = model{
	provider: snapshot.Provider389DS,
	classifier: classifier{
		section: ds389Section,
		// Settings Alder already changes through the ordinary plan: one
		// attribute of one configuration entry.
		writable: set(
			"nsslapd-idletimeout", "nsslapd-sizelimit", "nsslapd-timelimit", "nsslapd-lookthroughlimit",
			"nsslapd-pagedsizelimit", "nsslapd-errorlog-level", "nsslapd-accesslog-level",
			"nsslapd-readonly", "nsslapd-cachememsize", "nsslapd-dncachememsize", "nsslapd-cachesize",
			"nsslapd-require-index", "nsslapd-maxdescriptors", "nsslapd-ioblocktimeout",
			"nsslapd-accesslog-logging-enabled", "nsslapd-auditlog-logging-enabled",
		),
		readOnly: set(
			"nsslapd-backendconfig", "nsslapd-betype", "nsslapd-plugin", "nsslapd-privatenamespaces",
			"nsslapd-instancedir", "nsslapd-schemadir", "nsslapd-lockdir", "nsslapd-tmpdir",
			"nsslapd-rundir", "nsslapd-certdir", "nsslapd-ldifdir", "nsslapd-bakdir", "nsslapd-errorlog",
			"nsslapd-accesslog", "nsslapd-auditlog", "nsslapd-auditfaillog", "nsslapd-directory",
			"nssslsupportedciphers", "nsslapd-versionstring", "nsslapd-localhost", "cn",
			"nsslapd-pluginversion", "nsslapd-pluginid", "nsslapd-pluginvendor", "nsslapd-plugindescription",
			"nsslapd-plugininitfunc", "nsslapd-pluginpath", "nsslapd-plugintype", "cacertextractfile",
		),
		structured: set("nsslapd-referral", "nsindexattribute", "nssystemindex", "nsmatchingrule",
			"nsslapd-allowed-sasl-mechanisms", "nsslapd-anonlimitsdn", "aci", "nsslapd-haproxy-trusted-ip"),
		sensitive: set("nsslapd-rootpw", "nsds5replicacredentials", "nsds5replicabindcredentials",
			"nsmultiplexorcredentials", "nsslapd-keypassword"),
	},
	skipTree: func(entry *directory.Entry) bool {
		text := strings.ToLower(entry.DN.String())
		// Tasks are instructions to a running server, and a monitor entry is
		// counters. Neither is configuration. A monitor entry appears at more
		// than one depth -- the server's own, and one under every backend --
		// so the whole DN is checked for the component rather than its start.
		if text == "cn=tasks,cn=config" || strings.HasSuffix(text, ",cn=tasks,cn=config") {
			return true
		}
		return strings.HasPrefix(text, "cn=monitor,") || text == "cn=monitor" ||
			strings.Contains(text, ",cn=monitor,")
	},
	resource: ds389Resource,
}

func ds389Section(resource Resource, attribute string) string {
	key := strings.ToLower(attribute)
	switch {
	case strings.HasPrefix(key, "nsssl") || strings.HasPrefix(key, "nstls") || key == "nsslapd-security" ||
		key == "nsslapd-certdir" || key == "cacertextractfile" || strings.HasPrefix(key, "nsslapd-ssl"):
		return SectionTLS
	case key == "aci":
		return SectionAccessControl
	case strings.HasPrefix(key, "nsds5") || strings.HasPrefix(key, "nsds50") || key == "nsslapd-changelogdir" ||
		strings.HasPrefix(key, "nsslapd-changelog") || resource.Section == SectionReplication:
		// A change log is how a replica catches up, not somewhere a server
		// writes about itself, so this is decided before logging is.
		return SectionReplication
	case aboutLogging(key):
		return SectionLogging
	case strings.HasPrefix(key, "nsslapd-pw") || strings.HasPrefix(key, "passwordpolicy") ||
		strings.HasPrefix(key, "nsslapd-password"):
		return SectionPasswordPolicy
	case key == "nsslapd-sizelimit" || key == "nsslapd-timelimit" || key == "nsslapd-idletimeout" ||
		key == "nsslapd-lookthroughlimit" || key == "nsslapd-pagedsizelimit" ||
		key == "nsslapd-pagedlookthroughlimit" || key == "nsslapd-rangelookthroughlimit" ||
		key == "nsslapd-maxdescriptors" || key == "nsslapd-ioblocktimeout" || key == "nsslapd-maxbersize":
		return SectionLimits
	case strings.Contains(key, "cache") || strings.Contains(key, "threadnumber") ||
		strings.HasPrefix(key, "nsslapd-db-") || key == "nsslapd-import-cachesize" ||
		key == "nsslapd-dbcachesize" || key == "nsslapd-search-return-original-type":
		return SectionPerformance
	case key == "nsslapd-port" || key == "nsslapd-secureport" || key == "nsslapd-listenhost" ||
		key == "nsslapd-securelistenhost" || key == "nsslapd-ldapifilepath" || key == "nsslapd-ldapilisten" ||
		strings.HasPrefix(key, "nsslapd-haproxy"):
		return SectionNetwork
	case key == "nsslapd-suffix" || key == "nsslapd-directory" || key == "nsslapd-readonly" ||
		key == "nsslapd-require-index" || strings.HasPrefix(key, "nsslapd-backend") || key == "nsslapd-state":
		return SectionBackend
	case strings.HasPrefix(key, "nsslapd-plugin") || key == "nsslapd-pluginenabled":
		return SectionPlugins
	}
	switch resource.Kind {
	case KindPlugin:
		return SectionPlugins
	case KindBackend:
		return SectionBackend
	case KindEncryption:
		return SectionTLS
	case KindMappingTree:
		return SectionBackend
	case KindReplication:
		return SectionReplication
	}
	if resource.Kind == KindArea {
		return SectionOther
	}
	return SectionServer
}

func ds389Resource(entry *directory.Entry, _ map[string]Resource) (Resource, bool) {
	d := entry.DN
	text := strings.ToLower(d.String())
	name := rdnValue(entry)
	switch {
	case text == "cn=config":
		return Resource{Section: SectionServer, Kind: KindGlobal, Name: "global", DN: d.String(), Label: "server"}, true
	case strings.HasSuffix(text, ",cn=ldbm database,cn=plugins,cn=config"):
		// A backend, or one of the entries beneath it -- an index, a
		// container. The backend itself is named by its suffix; the entries
		// under it by their path beneath it, so two backends' indexes never
		// collide and neither is matched by position.
		suffix := firstValue(entry, "nsslapd-suffix")
		path := pathName(text, name)
		if suffix != "" {
			path = suffix
		}
		return Resource{Section: SectionBackend, Kind: KindBackend, Name: path, DN: d.String(), Label: name}, true
	case text == "cn=ldbm database,cn=plugins,cn=config":
		return Resource{Section: SectionBackend, Kind: KindBackend, Name: "ldbm", DN: d.String(), Label: "ldbm database"}, true
	case strings.HasSuffix(text, ",cn=plugins,cn=config"):
		// A plugin, or an entry beneath one: named by the path of names, so
		// two plugins' configuration entries never collide.
		return Resource{Section: SectionPlugins, Kind: KindPlugin, Name: pathName(d.String(), name), DN: d.String(), Label: name}, true
	case text == "cn=encryption,cn=config" || strings.HasSuffix(text, ",cn=encryption,cn=config"):
		return Resource{Section: SectionTLS, Kind: KindEncryption, Name: pathName(d.String(), name), DN: d.String(), Label: name}, true
	case text == "cn=mapping tree,cn=config" || strings.HasSuffix(text, ",cn=mapping tree,cn=config"):
		return Resource{Section: SectionBackend, Kind: KindMappingTree, Name: name, DN: d.String(), Label: name}, true
	case text == "cn=replication,cn=config" || strings.HasSuffix(text, ",cn=replication,cn=config") ||
		text == "cn=replschema,cn=config" || strings.HasSuffix(text, ",cn=replschema,cn=config"):
		return Resource{Section: SectionReplication, Kind: KindReplication, Name: pathName(d.String(), name), DN: d.String(), Label: name}, true
	}
	return Resource{Section: SectionOther, Kind: KindArea, Name: strings.ToLower(d.String()), DN: d.String(), Label: name}, true
}

// pathName is the path of names from the area an entry belongs to down to the
// entry itself, which is what identifies a plugin's or a backend's
// configuration entry whatever order the server returns things in. Only the
// containers at the end of the DN are stripped -- a plugin whose own
// configuration entry is called cn=config keeps that name, and does not
// collide with the plugin above it.
func pathName(target, fallback string) string {
	parts := strings.Split(strings.ToLower(target), ",")
	for len(parts) > 0 {
		last := strings.TrimSpace(parts[len(parts)-1])
		if !configContainers[last] {
			break
		}
		parts = parts[:len(parts)-1]
	}
	var names []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if value, ok := strings.CutPrefix(part, "cn="); ok {
			names = append([]string{value}, names...)
			continue
		}
		names = append([]string{part}, names...)
	}
	if len(names) == 0 {
		return strings.ToLower(fallback)
	}
	return strings.Join(names, "/")
}

// configContainers are the entries that group other configuration entries.
// They name an area rather than a thing, so they are not part of a resource's
// identity.
var configContainers = map[string]bool{
	"cn=config": true, "cn=plugins": true, "cn=encryption": true, "cn=replication": true,
	"cn=replschema": true, "cn=ldbm database": true, "cn=mapping tree": true,
}

// aboutLogging reports a setting that concerns the server's logs rather than
// one that merely has the letters in its name: a login is not a log.
func aboutLogging(key string) bool {
	for i := 0; i+3 <= len(key); i++ {
		if key[i:i+3] != "log" {
			continue
		}
		switch rest := key[i+3:]; {
		case strings.HasPrefix(rest, "in"), strings.HasPrefix(rest, "ic"):
			continue
		}
		return true
	}
	return false
}
