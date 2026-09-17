package config

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// Capturing a configuration is reading a subtree and then deciding what each
// attribute of each entry is. These tests are about the deciding: that a
// database is named by its suffix rather than by the number in its DN, that
// two overlays of the same name on two databases are two settings, that a
// secret is never read into the document, that runtime state is not read at
// all, and that a tree that cannot be read produces an answer that says so
// rather than a smaller configuration.

type fakeReader struct {
	caps    directory.Capabilities
	entries []*directory.Entry
	err     error
	// pageSize, when set, makes the reader answer in pages, so a capture is
	// exercised the way it runs against a real server.
	pageSize int
	searches int
}

func (f *fakeReader) Capabilities() directory.Capabilities { return f.caps }

func (f *fakeReader) Search(_ context.Context, req directory.SearchRequest) (*directory.SearchResult, error) {
	f.searches++
	if f.err != nil {
		return nil, f.err
	}
	base := strings.ToLower(req.BaseDN.String())
	var matched []*directory.Entry
	for _, entry := range f.entries {
		text := strings.ToLower(entry.DN.String())
		if text == base || strings.HasSuffix(text, ","+base) {
			matched = append(matched, entry)
		}
	}
	if f.pageSize <= 0 {
		return &directory.SearchResult{Entries: matched}, nil
	}
	from := 0
	if len(req.Cookie) > 0 {
		from = int(req.Cookie[0])
	}
	to := from + f.pageSize
	if to > len(matched) {
		to = len(matched)
	}
	res := &directory.SearchResult{Entries: matched[from:to]}
	if to < len(matched) {
		res.Cookie = []byte{byte(to)}
	}
	return res, nil
}

func entry(t *testing.T, text string, attrs ...string) *directory.Entry {
	t.Helper()
	d, err := dn.Parse(text)
	if err != nil {
		t.Fatalf("dn %q: %v", text, err)
	}
	e := directory.NewEntry(d)
	for i := 0; i+1 < len(attrs); i += 2 {
		name, value := attrs[i], attrs[i+1]
		existing := e.Get(name)
		e.Set(name, append(existing, []byte(value)))
	}
	return e
}

func openldapTree(t *testing.T) []*directory.Entry {
	t.Helper()
	return []*directory.Entry{
		entry(t, "cn=config",
			"objectClass", "olcGlobal",
			"cn", "config",
			"olcArgsFile", "/run/slapd/slapd.args",
			"olcIdleTimeout", "0",
			"olcLogLevel", "stats",
			"olcTLSCertificateFile", "/etc/ldap/tls/server.crt",
			"entryUUID", "a1b2c3d4-0000-0000-0000-000000000001",
			"modifyTimestamp", "20260917100000Z",
		),
		entry(t, "cn=module{0},cn=config",
			"objectClass", "olcModuleList",
			"cn", "module{0}",
			"olcModulePath", "/usr/lib/ldap",
			"olcModuleLoad", "{0}back_mdb",
			"olcModuleLoad", "{1}memberof",
		),
		entry(t, "cn=schema,cn=config", "objectClass", "olcSchemaConfig", "cn", "schema"),
		entry(t, "cn={0}core,cn=schema,cn=config",
			"objectClass", "olcSchemaConfig", "cn", "{0}core",
			"olcAttributeTypes", "( 2.5.4.3 NAME 'cn' SUP name )",
		),
		entry(t, "cn={1}cosine,cn=schema,cn=config", "objectClass", "olcSchemaConfig", "cn", "{1}cosine"),
		entry(t, "olcDatabase={1}mdb,cn=config",
			"objectClass", "olcMdbConfig",
			"olcDatabase", "{1}mdb",
			"olcSuffix", "dc=alder,dc=test",
			"olcRootDN", "cn=admin,dc=alder,dc=test",
			"olcRootPW", "alder-admin",
			"olcDbMaxSize", "1073741824",
			"olcAccess", "{0}to attrs=userPassword by self write by * none",
			"olcAccess", "{1}to * by users read",
			"olcIdleTimeout", "0",
		),
		entry(t, "olcDatabase={2}mdb,cn=config",
			"objectClass", "olcMdbConfig",
			"olcDatabase", "{2}mdb",
			"olcSuffix", "dc=second,dc=test",
			"olcDbMaxSize", "104857600",
		),
		entry(t, "olcOverlay={0}memberof,olcDatabase={1}mdb,cn=config",
			"objectClass", "olcMemberOf",
			"olcOverlay", "{0}memberof",
			"olcMemberOfRefint", "TRUE",
		),
		entry(t, "olcOverlay={0}memberof,olcDatabase={2}mdb,cn=config",
			"objectClass", "olcMemberOf",
			"olcOverlay", "{0}memberof",
			"olcMemberOfRefint", "FALSE",
		),
	}
}

func openldapReader(t *testing.T) *fakeReader {
	t.Helper()
	return &fakeReader{
		caps: directory.Capabilities{
			VendorName: "OpenLDAP", VendorVersion: "2.6.7", ConfigContext: "cn=config",
			Config: directory.ConfigAccess{DN: "cn=config", Readable: true},
		},
		entries: openldapTree(t),
	}
}

func ds389Tree(t *testing.T) []*directory.Entry {
	t.Helper()
	return []*directory.Entry{
		entry(t, "cn=config",
			"objectClass", "nsslapdConfig",
			"cn", "config",
			"nsslapd-port", "389",
			"nsslapd-rootpw", "{PBKDF2-SHA512}10000$abcdef$0123456789",
			"nsslapd-idletimeout", "0",
			"nsslapd-errorlog-level", "16384",
			"nsslapd-instancedir", "/usr/lib64/dirsrv/slapd-alder",
			"nsUniqueId", "00000000-00000000-00000000-00000000",
		),
		entry(t, "cn=encryption,cn=config", "objectClass", "nsEncryptionConfig", "cn", "encryption",
			"nsSSL3Ciphers", "+all"),
		entry(t, "cn=ldbm database,cn=plugins,cn=config",
			"objectClass", "nsBackendInstance", "cn", "ldbm database",
			"nsslapd-pluginEnabled", "on"),
		entry(t, "cn=userRoot,cn=ldbm database,cn=plugins,cn=config",
			"objectClass", "nsBackendInstance", "cn", "userRoot",
			"nsslapd-suffix", "dc=alder,dc=test",
			"nsslapd-cachememsize", "536870912",
			"nsslapd-readonly", "off"),
		entry(t, "cn=index,cn=userRoot,cn=ldbm database,cn=plugins,cn=config",
			"objectClass", "nsContainer", "cn", "index"),
		entry(t, "cn=uid,cn=index,cn=userRoot,cn=ldbm database,cn=plugins,cn=config",
			"objectClass", "nsIndex", "cn", "uid", "nsSystemIndex", "false", "nsIndexType", "eq"),
		entry(t, "cn=monitor,cn=userRoot,cn=ldbm database,cn=plugins,cn=config",
			"objectClass", "nsBackendMonitor", "cn", "monitor",
			"entrycachehits", "44127"),
		entry(t, "cn=tasks,cn=config", "objectClass", "nsContainer", "cn", "tasks"),
		entry(t, "cn=import,cn=tasks,cn=config", "objectClass", "nsContainer", "cn", "import"),
		entry(t, "cn=Account Policy Plugin,cn=plugins,cn=config",
			"objectClass", "nsSlapdPlugin", "cn", "Account Policy Plugin",
			"nsslapd-pluginEnabled", "off",
			"nsslapd-pluginPath", "libacctpolicy-plugin"),
		entry(t, "cn=config,cn=Account Policy Plugin,cn=plugins,cn=config",
			"objectClass", "nsContainer", "cn", "config",
			"alwaysrecordlogin", "no"),
	}
}

func ds389Reader(t *testing.T) *fakeReader {
	t.Helper()
	return &fakeReader{
		caps: directory.Capabilities{
			VendorName: "389 Project", VendorVersion: "389-Directory/3.0.5",
			ConfigContext: "cn=config", Config: directory.ConfigAccess{DN: "cn=config", Readable: true},
		},
		entries: ds389Tree(t),
	}
}

func capture(t *testing.T, r Reader) *snapshot.ConfigSnapshot {
	t.Helper()
	s, err := Capture(context.Background(), r, Options{
		Now: func() time.Time { return time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	return s
}

func setting(t *testing.T, s *snapshot.ConfigSnapshot, id string) *snapshot.ConfigSetting {
	t.Helper()
	got, ok := s.SettingByID(id)
	if !ok {
		t.Fatalf("the snapshot has no setting %q", id)
	}
	return got
}

func TestOpenLDAPConfigurationIsReadAsOpenLDAPArrangesIt(t *testing.T) {
	s := capture(t, openldapReader(t))

	if s.Source.Provider != snapshot.ProviderOpenLDAP {
		t.Fatalf("provider is %q", s.Source.Provider)
	}
	if s.Completeness != snapshot.ConfigComplete {
		t.Fatalf("completeness is %q: %+v", s.Completeness, s.Incomplete)
	}

	// A database is the suffix it serves, not the number in front of it.
	if _, ok := s.ResourceByID("database:dc=alder,dc=test"); !ok {
		t.Fatalf("no database named by its suffix; resources are %v", resourceIDs(s))
	}
	for _, r := range s.Resources {
		if strings.Contains(r.Name, "{") {
			t.Fatalf("resource %q is named by its position", r.Name)
		}
	}

	// The two overlays have the same name and belong to different databases,
	// so they are two resources and their settings are two settings.
	first := setting(t, s, "plugins/overlay:dc=alder,dc=test/memberof/olcmemberofrefint")
	second := setting(t, s, "plugins/overlay:dc=second,dc=test/memberof/olcmemberofrefint")
	if first.Values[0] == second.Values[0] {
		t.Fatalf("two overlays' settings collapsed into one: %v and %v", first.Values, second.Values)
	}

	// A boolean is normalised, so TRUE and true are one configuration.
	if first.Values[0] != "true" {
		t.Fatalf("a boolean was not normalised: %v", first.Values)
	}

	// Sections place a setting where a person would look for it.
	for id, want := range map[string]string{
		"limits//olcidletimeout":                             SectionLimits,
		"logging//olcloglevel":                               SectionLogging,
		"tls//olctlscertificatefile":                         SectionTLS,
		"access_control/database:dc=alder,dc=test/olcaccess": SectionAccessControl,
	} {
		if got := setting(t, s, id).Section; got != want {
			t.Fatalf("%s is in section %q, want %q", id, got, want)
		}
	}
}

func TestAnOpenLDAPAccessRuleIsOrderedAndComparedAsText(t *testing.T) {
	s := capture(t, openldapReader(t))
	access := setting(t, s, "access_control/database:dc=alder,dc=test/olcaccess")
	if !access.Ordered {
		t.Fatal("an access rule list was recorded as unordered; its order is the configuration")
	}
	if access.Comparison != snapshot.ComparisonRaw {
		t.Fatalf("comparison is %q, want raw: nothing here parses an access rule", access.Comparison)
	}
	if access.Values[0] != "to attrs=userPassword by self write by * none" {
		t.Fatalf("the ordering prefix was not stripped, or the order was lost: %v", access.Values)
	}
}

func TestTheSchemaIsNotInAConfigurationSnapshot(t *testing.T) {
	s := capture(t, openldapReader(t))
	for _, setting := range s.Settings {
		if strings.Contains(strings.ToLower(setting.DN), "cn=schema") {
			t.Fatalf("a schema entry's attribute is in the configuration: %s", setting.ID())
		}
		if strings.EqualFold(setting.Key, "olcAttributeTypes") || strings.EqualFold(setting.Key, "olcObjectClasses") {
			t.Fatalf("a schema definition is in the configuration: %s", setting.ID())
		}
	}
	// Which schema entries are loaded is configuration, so the names are here.
	collections := setting(t, s, "schema//schemacollections")
	if len(collections.Values) != 2 || collections.DN != "" {
		t.Fatalf("schema collections are %v from %q, want two names and no entry", collections.Values, collections.DN)
	}
	for _, v := range collections.Values {
		if strings.Contains(v, "{") {
			t.Fatalf("a schema collection is named by its position: %q", v)
		}
	}
}

func TestAServerMaintainedAttributeIsNotConfiguration(t *testing.T) {
	for _, r := range []*fakeReader{openldapReader(t), ds389Reader(t)} {
		s := capture(t, r)
		for _, setting := range s.Settings {
			switch strings.ToLower(setting.Key) {
			case "entryuuid", "modifytimestamp", "nsuniqueid", "objectclass":
				t.Fatalf("%s captured %q, which the server maintains", s.Source.Provider, setting.Key)
			}
		}
	}
}

func Test389ConfigurationIsReadAs389ArrangesIt(t *testing.T) {
	s := capture(t, ds389Reader(t))

	if s.Source.Provider != snapshot.Provider389DS {
		t.Fatalf("provider is %q", s.Source.Provider)
	}
	// A backend is named by the suffix it serves.
	if _, ok := s.ResourceByID("backend:dc=alder,dc=test"); !ok {
		t.Fatalf("no backend named by its suffix; resources are %v", resourceIDs(s))
	}
	// An index below a backend is named by its path below it, so two backends'
	// indexes never collide.
	if _, ok := s.SettingByID("other/backend:userroot/index/uid/nsindextype"); !ok {
		if _, ok := s.SettingByID("backend/backend:userroot/index/uid/nsindextype"); !ok {
			t.Fatalf("the index's setting is missing; settings under the backend are %v", settingIDsUnder(s, "index"))
		}
	}
	// A plugin's own configuration entry is called cn=config and must not
	// collide with the server's global entry.
	if _, ok := s.SettingByID("plugins/plugin:account policy plugin/config/alwaysrecordlogin"); !ok {
		t.Fatalf("a plugin's configuration entry was lost; resources are %v", resourceIDs(s))
	}
	if setting(t, s, "network//nsslapd-port").Section != SectionNetwork {
		t.Fatal("the port is not in the network section")
	}
}

func TestRuntimeStateIsNotConfiguration(t *testing.T) {
	s := capture(t, ds389Reader(t))
	for _, setting := range s.Settings {
		text := strings.ToLower(setting.DN)
		if strings.Contains(text, "cn=monitor") || strings.Contains(text, "cn=tasks") {
			t.Fatalf("%s is runtime state and was captured as configuration", setting.ID())
		}
		if strings.EqualFold(setting.Key, "entrycachehits") {
			t.Fatal("a counter was captured as configuration")
		}
	}
}

func TestASecretIsNeverReadIntoAConfigurationSnapshot(t *testing.T) {
	cases := []struct {
		name   string
		reader *fakeReader
		id     string
		secret string
	}{
		{"OpenLDAP keeps its root password in the clear", openldapReader(t),
			"backend/database:dc=alder,dc=test/olcrootpw", "alder-admin"},
		{"389 DS keeps a hash, which is still a secret", ds389Reader(t),
			"password_policy//nsslapd-rootpw", "{PBKDF2-SHA512}10000$abcdef$0123456789"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := capture(t, c.reader)
			got, ok := s.SettingByID(c.id)
			if !ok {
				// Section placement differs between models; find it by key.
				for i := range s.Settings {
					if strings.Contains(strings.ToLower(s.Settings[i].Key), "rootpw") {
						got, ok = &s.Settings[i], true
					}
				}
			}
			if !ok {
				t.Fatal("the root password setting vanished entirely; its presence is configuration")
			}
			if !got.Sensitive || len(got.Values) != 0 || got.Withheld != 1 {
				t.Fatalf("the secret was not withheld: %+v", *got)
			}
			for _, setting := range s.Settings {
				for _, v := range setting.Values {
					if strings.Contains(v, c.secret) {
						t.Fatalf("the secret is in %s", setting.ID())
					}
				}
			}
		})
	}
}

func TestSomethingNamedAfterAPasswordWithoutHoldingOneIsNotWithheld(t *testing.T) {
	// A configuration is full of settings that describe passwords rather than
	// holding one. Withholding those would hide ordinary configuration and
	// protect nothing.
	c := classifier{}
	for _, key := range []string{"passwordMinLength", "passwordMaxAge", "passwordStorageScheme",
		"passwordAdminDN", "nsslapd-pwpolicy-local", "olcPPolicyHashCleartext", "nsslapd-keyfile"} {
		if c.isSensitive(key) {
			t.Errorf("%q was withheld and holds no secret", key)
		}
	}
	for _, key := range []string{"olcRootPW", "nsslapd-rootpw", "userPassword", "nsds5ReplicaCredentials",
		"olcDbCryptKey", "nsslapd-keyPassword"} {
		if !c.isSensitive(key) {
			t.Errorf("%q holds a secret and was not withheld", key)
		}
	}
}

func TestAConfigurationThatCannotBeReadIsAnAnswerThatSaysSo(t *testing.T) {
	r := openldapReader(t)
	r.err = errors.New("insufficient access")
	_, err := Capture(context.Background(), r, Options{})
	if !errors.Is(err, ErrUnreadable) {
		t.Fatalf("got %v, want an unreadable configuration", err)
	}

	// A server that announces no configuration tree at all is the same answer.
	empty := &fakeReader{caps: directory.Capabilities{VendorName: "OpenLDAP"}}
	if _, err := Capture(context.Background(), empty, Options{}); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("got %v, want an unreadable configuration", err)
	}
}

func TestAServerWithNoConfigurationModelIsRefusedRatherThanGuessedAt(t *testing.T) {
	r := &fakeReader{
		caps: directory.Capabilities{VendorName: "Some Other Directory", ConfigContext: "cn=config",
			Config: directory.ConfigAccess{DN: "cn=config", Readable: true}},
		entries: []*directory.Entry{entry(t, "cn=config", "objectClass", "top", "cn", "config",
			"someSetting", "somewhere")},
	}
	if _, err := Capture(context.Background(), r, Options{}); !errors.Is(err, ErrNoModel) {
		t.Fatalf("got %v, want no model", err)
	}
}

func TestTheProviderIsDecidedByTheTreeBeforeTheName(t *testing.T) {
	// A server calling itself something else, whose configuration is plainly
	// OpenLDAP's, is read with OpenLDAP's model: the tree is a fact and the
	// name is a label.
	r := openldapReader(t)
	r.caps.VendorName = "Acme Directory Server"
	s := capture(t, r)
	if s.Source.Provider != snapshot.ProviderOpenLDAP {
		t.Fatalf("provider is %q, want openldap from the tree", s.Source.Provider)
	}
	if s.Source.Vendor != "Acme Directory Server" {
		t.Fatalf("the vendor name was rewritten to %q; it is for display", s.Source.Vendor)
	}
}

func TestAnUnreadableSectionMakesTheCapturePartialRatherThanSmaller(t *testing.T) {
	r := openldapReader(t)
	r.caps.Config.Readable = false
	r.caps.Config.Reason = "this bind may read only part of the configuration"
	s := capture(t, r)
	if s.Completeness != snapshot.ConfigPartial {
		t.Fatal("a capture that could not read everything claimed to be complete")
	}
	if len(s.Incomplete) == 0 || s.Incomplete[0].Reason != snapshot.IncompleteAccess {
		t.Fatalf("incomplete says %+v, want insufficient access", s.Incomplete)
	}
}

func TestACaptureIsPagedLikeEveryOtherSearch(t *testing.T) {
	r := openldapReader(t)
	r.pageSize = 2
	s := capture(t, r)
	if r.searches < 3 {
		t.Fatalf("the tree was read in %d searches; a configuration is read in pages", r.searches)
	}
	if s.Counts.Settings == 0 {
		t.Fatal("paging lost the configuration")
	}
	whole := capture(t, openldapReader(t))
	if s.Checksum != whole.Checksum {
		t.Fatal("reading in pages produced a different configuration than reading in one")
	}
}

func TestAValueThatIsNotTextIsStillCaptured(t *testing.T) {
	r := ds389Reader(t)
	r.entries = append(r.entries, entry(t, "cn=replica,cn=replication,cn=config",
		"objectClass", "nsds5replica", "cn", "replica",
		"nsState", "\x01\x00\xff\xfe binary state"))
	s := capture(t, r)
	var found *snapshot.ConfigSetting
	for i := range s.Settings {
		if strings.EqualFold(s.Settings[i].Key, "nsState") {
			found = &s.Settings[i]
		}
	}
	if found == nil {
		t.Fatal("a value that is not text was dropped; the snapshot would be quietly smaller than the configuration")
	}
	if found.Type != TypeBinary {
		t.Fatalf("type is %q, want binary", found.Type)
	}
	for _, v := range found.Values {
		if strings.ContainsFunc(v, isControlByte) {
			t.Fatal("a control byte reached the document")
		}
	}
}

func TestCaptureNeverWritesAnything(t *testing.T) {
	// The Reader interface is the proof: a capture is given a search and the
	// capabilities, and there is nothing else it could call.
	var r Reader = openldapReader(t)
	if _, ok := r.(interface {
		Apply(context.Context, directory.ChangeRecord) error
	}); ok {
		t.Fatal("a configuration capture was handed something that can write")
	}
}

func resourceIDs(s *snapshot.ConfigSnapshot) []string {
	out := make([]string, 0, len(s.Resources))
	for _, r := range s.Resources {
		out = append(out, r.ID())
	}
	return out
}

func settingIDsUnder(s *snapshot.ConfigSnapshot, contains string) []string {
	var out []string
	for _, setting := range s.Settings {
		if strings.Contains(setting.ID(), contains) {
			out = append(out, setting.ID())
		}
	}
	return out
}
