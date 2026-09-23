// Package config reads a directory server's own configuration and normalises
// it into the model that server's software actually has.
//
// There is no portable configuration model here, and there will not be one.
// OpenLDAP and 389 Directory Server keep different things in different places
// and mean different things by them: an overlay is not a plugin, olcDbMaxSize
// is not nsslapd-cachememsize, and an access rule in one is not an access rule
// in the other. What the two share is categories -- both have logging, both
// have backends -- and a category is a place to look, not a claim that two
// settings correspond.
//
// So each provider has its own model, and a comparison only ever compares two
// captures of the same one. What the models agree on is the shape of a fact:
//
//   - a setting is one attribute of one configuration entry;
//   - it belongs to a section and, where the provider repeats things, to a
//     resource identified by what the provider itself names it by -- a suffix,
//     an overlay's name, a plugin's name -- never by its position;
//   - it is writable, read-only or unknown, as the model states, which is not
//     the same question as whether this bind may write it;
//   - a secret is recorded as present and counted, never as a value;
//   - runtime state is not configuration and is not read at all.
package config

import (
	"encoding/base64"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/schema"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// Sections a configuration model may use. A section existing in both providers
// says where to look, never that two settings mean the same thing.
const (
	SectionServer         = "server"
	SectionNetwork        = "network"
	SectionBackend        = "backend"
	SectionLimits         = "limits"
	SectionLogging        = "logging"
	SectionTLS            = "tls"
	SectionPlugins        = "plugins"
	SectionReplication    = "replication"
	SectionPerformance    = "performance"
	SectionPasswordPolicy = "password_policy"
	SectionAccessControl  = "access_control"
	SectionSchema         = "schema"
	SectionOther          = "other"
)

// Sections is every section, in report order.
var Sections = []string{SectionServer, SectionNetwork, SectionBackend, SectionLimits, SectionLogging,
	SectionTLS, SectionPlugins, SectionReplication, SectionPerformance, SectionPasswordPolicy,
	SectionAccessControl, SectionSchema, SectionOther}

// Resource kinds. Provider-specific words, used as the provider uses them.
const (
	KindGlobal      = "global"
	KindDatabase    = "database"
	KindOverlay     = "overlay"
	KindModule      = "module"
	KindBackend     = "backend"
	KindPlugin      = "plugin"
	KindEncryption  = "encryption"
	KindMappingTree = "mapping-tree"
	KindReplication = "replication"
	KindArea        = "area"
)

// Value types. Display and comparison read them; nothing branches on them.
const (
	TypeString     = "string"
	TypeInt        = "int"
	TypeBool       = "bool"
	TypeDN         = "dn"
	TypePath       = "path"
	TypeURL        = "url"
	TypeStructured = "structured"
	// TypeBinary is a value that is not text. It is recorded as base64, so a
	// configuration a server keeps a blob in still compares.
	TypeBinary = "binary"
)

// attributes every entry carries that a server maintains for itself. They are
// state about the entry, not configuration, and are never captured.
var serverMaintained = map[string]bool{
	"entryuuid": true, "entrycsn": true, "entrydn": true, "creatorsname": true,
	"modifiersname": true, "createtimestamp": true, "modifytimestamp": true,
	"structuralobjectclass": true, "subschemasubentry": true, "hassubordinates": true,
	"numsubordinates": true, "contextcsn": true, "nsuniqueid": true, "objectclass": true,
	"nstombstonecsn": true, "parentid": true, "entryid": true,
}

// sensitiveWords name a value that *is* a credential, whatever provider it
// belongs to. Alder's own sensitivity list is consulted first; this catches
// what a configuration tree adds to it.
//
// The words are deliberately narrow. A configuration is full of names with
// "password" in them that hold no secret at all -- a password policy's minimum
// length, whether a password may be changed, which account administers
// passwords -- and withholding those would hide ordinary configuration while
// teaching nobody anything. What is withheld is a value that could be used:
// a credential, a secret, a key, or an attribute whose name ends in the word
// password itself.
var sensitiveWords = []string{"credential", "secret", "rootpw", "bindpw", "cryptkey",
	"symmetrickey", "privatekey", "passphrase", "sharedkey", "authtoken", "apikey", "keypassword"}

// sensitiveSuffixes name an attribute that holds a password rather than
// describing one: userPassword and nsslapd-keyPassword do, passwordMinLength
// and passwordAdminDN do not.
var sensitiveSuffixes = []string{"password", "passwd", "-pw", "pwd"}

// notSecretEvenSoWords are names that match one of the rules above and are not
// a secret: they point at a secret rather than being one. They are operational
// metadata, and marked as such.
var notSecretEvenSoWords = []string{"file", "path", "dir", "storagescheme", "syntax", "policysubentry"}

// operationalWords mark a value that is not secret and still says something
// about the machine Alder is talking to: a path, a port, a host, a file.
var operationalWords = []string{"file", "path", "dir", "directory", "host", "port", "socket", "uri", "url", "address"}

var indexPrefix = regexp.MustCompile(`^\{(-?\d+)\}`)

// Setting is one normalised setting, before it becomes a snapshot's.
type Setting = snapshot.ConfigSetting

// Resource is one normalised configuration object.
type Resource = snapshot.ConfigResource

// classifier is what a provider's model decides about one attribute of one
// entry. Everything here is the provider's own vocabulary.
type classifier struct {
	// section returns the section an attribute belongs to, given the resource
	// it is on.
	section func(resource Resource, attribute string) string
	// writable names the settings this model states are ordinary configuration
	// and that Alder can already change through the plan: an attribute of an
	// existing configuration entry, modified like any other entry's.
	writable map[string]bool
	// readOnly names the settings the server maintains or fixes at start.
	readOnly map[string]bool
	// ordered names the settings whose value order carries meaning.
	ordered map[string]bool
	// structured names the settings with a syntax inside the value that this
	// model does not parse; they are compared as text and say so.
	structured map[string]bool
	// sensitive names settings that are secrets whatever they look like.
	sensitive map[string]bool
	// skipped names attributes that are runtime state on a configuration entry.
	skipped map[string]bool
	// writableOn narrows a writable setting to the resources where changing it
	// is safe. A setting absent from it is writable wherever it appears. The
	// case it exists for is read-only mode: on one database it stops writes to
	// that database, and on the server's global entry it stops writes to the
	// configuration too -- including the one that would switch it back.
	writableOn func(resource Resource, key string) bool
	// restart names the settings the server itself says take effect only after
	// a restart. Read from the server, never from a list of vendor facts: a
	// setting that does nothing until a restart is not one Alder changes.
	restart map[string]bool
}

func (c classifier) mutability(resource Resource, attribute string) string {
	key := strings.ToLower(attribute)
	switch {
	case c.readOnly[key]:
		return snapshot.MutabilityReadOnly
	case c.restart[key]:
		return snapshot.MutabilityReadOnly
	case c.writable[key]:
		if c.writableOn != nil && !c.writableOn(resource, key) {
			return snapshot.MutabilityReadOnly
		}
		return snapshot.MutabilityWritable
	}
	return snapshot.MutabilityUnknown
}

// isSensitive decides whether a value must never be recorded.
//
// Alder's own list first, then the provider's, then the names a configuration
// tree uses for secrets -- with the exception of the names that point at a
// secret rather than holding one, such as a key *file*.
func (c classifier) isSensitive(attribute string) bool {
	key := strings.ToLower(attribute)
	if c.sensitive[key] {
		return true
	}
	if schema.IsSensitive(attribute) {
		return true
	}
	for _, exception := range notSecretEvenSoWords {
		if strings.Contains(key, exception) {
			return false
		}
	}
	for _, word := range sensitiveWords {
		if strings.Contains(key, word) {
			return true
		}
	}
	for _, suffix := range sensitiveSuffixes {
		if strings.HasSuffix(key, suffix) {
			return true
		}
	}
	return false
}

func isOperational(attribute string, values []string) bool {
	key := strings.ToLower(attribute)
	for _, word := range operationalWords {
		if strings.Contains(key, word) {
			return true
		}
	}
	for _, v := range values {
		if strings.HasPrefix(v, "/") || strings.Contains(v, "://") {
			return true
		}
	}
	return false
}

// valueType is what the values look like. It is a display and comparison hint
// derived from the values themselves, never a schema claim.
func valueType(attribute string, values []string, structured bool) string {
	switch {
	case structured:
		return TypeStructured
	case len(values) == 0:
		return TypeString
	}
	allInt, allBool, allDN, anyPath, anyURL := true, true, true, false, false
	for _, v := range values {
		if _, err := strconv.ParseInt(v, 10, 64); err != nil {
			allInt = false
		}
		switch strings.ToLower(v) {
		case "true", "false", "on", "off", "yes", "no":
		default:
			allBool = false
		}
		if parsed, err := dn.Parse(v); err != nil || len(parsed) == 0 {
			allDN = false
		}
		if strings.HasPrefix(v, "/") {
			anyPath = true
		}
		if strings.Contains(v, "://") {
			anyURL = true
		}
	}
	switch {
	case allInt:
		return TypeInt
	case allBool:
		return TypeBool
	case anyURL:
		return TypeURL
	case anyPath:
		return TypePath
	case allDN && !strings.EqualFold(attribute, "cn"):
		return TypeDN
	}
	return TypeString
}

// normalise puts a value in the form two captures of the same configuration
// both produce.
//
// Only what the model is sure of: an integer's spelling, and the ordering
// prefix a server keeps in front of an ordered
// value, which changes when unrelated values are removed and means nothing on
// its own. Anything else is left exactly as the server gave it.
func normalise(valueKind string, ordered bool, values []string) ([]string, bool) {
	out := make([]string, 0, len(values))
	changed := false
	type indexed struct {
		order int
		value string
	}
	var withIndex []indexed
	for i, v := range values {
		text := v
		order := i
		if m := indexPrefix.FindStringSubmatch(text); m != nil {
			n, err := strconv.Atoi(m[1])
			if err == nil {
				order = n
				text = text[len(m[0]):]
				changed = true
			}
		}
		switch valueKind {
		case TypeInt:
			if n, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64); err == nil {
				if formatted := strconv.FormatInt(n, 10); formatted != text {
					changed = true
					text = formatted
				}
			}
		}
		withIndex = append(withIndex, indexed{order: order, value: text})
	}
	if ordered {
		// Sorted by the server's own ordering, then written without it: two
		// configurations that differ only in renumbering are the same
		// configuration.
		for i := 0; i < len(withIndex); i++ {
			for j := i + 1; j < len(withIndex); j++ {
				if withIndex[j].order < withIndex[i].order {
					withIndex[i], withIndex[j] = withIndex[j], withIndex[i]
				}
			}
		}
	}
	for _, v := range withIndex {
		out = append(out, v.value)
	}
	return out, changed
}

// settingFrom turns one attribute of one entry into a normalised setting.
func settingFrom(c classifier, resource Resource, entry *directory.Entry, attribute string, values []string) (Setting, bool) {
	key := strings.ToLower(attribute)
	if serverMaintained[key] || c.skipped[key] {
		return Setting{}, false
	}
	setting := Setting{
		Section: c.section(resource, attribute),
		Key:     attribute,
		DN:      entry.DN.String(),
	}
	if resource.Kind != KindGlobal {
		setting.Resource = resource.ID()
	}
	setting.Mutability = c.mutability(resource, attribute)
	if c.isSensitive(attribute) {
		setting.Sensitive = true
		setting.Withheld = len(values)
		setting.Comparison = snapshot.ComparisonNormalised
		setting.Type = TypeString
		return setting, true
	}
	if binary, encoded := asBinary(values); binary {
		setting.Type = TypeBinary
		setting.Values = encoded
		setting.Comparison = snapshot.ComparisonNormalised
		return setting, true
	}
	structured := c.structured[key]
	setting.Ordered = c.ordered[key]
	setting.Type = valueType(attribute, values, structured)
	normalised, changed := normalise(setting.Type, setting.Ordered, values)
	setting.Values = normalised
	switch {
	case structured:
		setting.Comparison = snapshot.ComparisonRaw
	case changed || setting.Type == TypeInt || setting.Type == TypeBool:
		setting.Comparison = snapshot.ComparisonNormalised
	default:
		setting.Comparison = snapshot.ComparisonNormalised
	}
	setting.Operational = isOperational(attribute, values)
	return setting, true
}

// asBinary reports values that are not text, and encodes them so a document
// can hold them: a configuration may keep a blob, and refusing to capture it
// would make the snapshot quietly smaller than the configuration.
func asBinary(values []string) (bool, []string) {
	binary := false
	for _, v := range values {
		if !utf8.ValidString(v) || strings.ContainsFunc(v, isControlByte) {
			binary = true
			break
		}
	}
	if !binary {
		return false, nil
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, base64.StdEncoding.EncodeToString([]byte(v)))
	}
	return true, out
}

// isControlByte reports a character no configuration value should carry as
// text: anything below a space that is not a tab, a newline or a carriage
// return.
func isControlByte(r rune) bool {
	return r < 0x20 && r != '\t' && r != '\n' && r != '\r'
}

// stripIndex removes the {n} an OpenLDAP configuration entry's RDN carries.
func stripIndex(value string) string {
	if m := indexPrefix.FindStringSubmatch(value); m != nil {
		return value[len(m[0]):]
	}
	return value
}

// rdnValue is the value of an entry's first RDN attribute.
func rdnValue(entry *directory.Entry) string {
	rdn := entry.DN.RDN()
	if len(rdn) == 0 {
		return ""
	}
	return rdn[0].Value
}

func firstValue(entry *directory.Entry, attribute string) string {
	values := entry.GetStrings(attribute)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
