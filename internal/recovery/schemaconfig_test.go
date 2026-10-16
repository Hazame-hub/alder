package recovery

import (
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
)

const (
	configDB   = "olcDatabase={1}mdb,cn=config"
	schemaColl = "cn={4}alder,cn=schema,cn=config"
	subschema  = "cn=schema"

	probeOID   = "1.3.6.1.4.1.99997.1.7"
	probeType  = "( 1.3.6.1.4.1.99997.1.7 NAME 'alderProbe' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )"
	probeClass = "( 1.3.6.1.4.1.99997.2.7 NAME 'alderProbeClass' AUXILIARY MAY alderProbe )"
	otherType  = "( 1.3.6.1.4.1.99997.1.8 NAME 'alderOther' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )"
)

// definitionAttrs is what a server announces it writes definitions to, under
// both arrangements.
func definitionAttrs(name string) bool {
	switch strings.ToLower(name) {
	case "attributetypes", "objectclasses", "olcattributetypes", "olcobjectclasses":
		return true
	}
	return false
}

func configTarget(t *testing.T) Target { return Target{Kind: KindConfig, Schema: testSchema(t)} }

func schemaTarget() Target { return Target{Kind: KindSchema, Definitions: definitionAttrs} }

func modify(t *testing.T, d string, mods ...directory.Mod) directory.ChangeRecord {
	return directory.ChangeRecord{DN: mustDN(t, d), Type: directory.ChangeModify, Mods: mods}
}

func reasonCodes(step Step) map[ReasonCode]string {
	out := map[ReasonCode]string{}
	for _, r := range step.Reasons {
		out[r.Code] = r.Attribute
	}
	return out
}

// --- configuration -----------------------------------------------------------

func TestConfigSettingIsPutBackAsTheServerStoredIt(t *testing.T) {
	// The server stores its own spelling: what the expectation holds is what
	// it read back, not what was sent.
	pre := entry(t, configDB, []string{"olcSizeLimit", "500"})
	post := entry(t, configDB, []string{"olcSizeLimit", "size.soft=900"})
	record := modify(t, configDB, mod(directory.ModReplace, "olcSizeLimit", "900"))

	step := Derive(0, record, pre, post, configTarget(t))
	if step.Recoverability != Exact || step.Kind != KindConfig || len(step.Compensation) != 1 {
		t.Fatalf("step %+v", step)
	}
	c := step.Compensation[0]
	if len(c.Mods) != 1 || c.Mods[0].Op != "replace" || !equalStrings(texts(t, c.Mods[0].Values), []string{"500"}) {
		t.Errorf("compensation %+v, want replace olcSizeLimit with 500", c.Mods)
	}
	if len(c.Expect.Attributes) != 1 || !equalStrings(texts(t, c.Expect.Attributes[0].Values), []string{"size.soft=900"}) {
		t.Errorf("expect %+v, want the stored value", c.Expect)
	}
}

func TestConfigOrderedValuesGoBackInTheirOrder(t *testing.T) {
	pre := entry(t, configDB, []string{"olcLimits", "{0}dn.exact=a size=1", "{1}dn.exact=b size=2"})
	// An insertion at the top renumbers everything after it.
	post := entry(t, configDB, []string{"olcLimits", "{0}dn.exact=c size=3", "{1}dn.exact=a size=1", "{2}dn.exact=b size=2"})
	record := modify(t, configDB, mod(directory.ModAdd, "olcLimits", "{0}dn.exact=c size=3"))

	step := Derive(0, record, pre, post, configTarget(t))
	c := step.Compensation[0]
	if step.Recoverability != Exact || c.Mods[0].Op != "replace" ||
		!equalStrings(texts(t, c.Mods[0].Values), []string{"{0}dn.exact=a size=1", "{1}dn.exact=b size=2"}) {
		t.Errorf("step %+v, want the earlier values replaced in order, positions and all", step)
	}

	// A reorder alone changes every value, and is put back.
	post = entry(t, configDB, []string{"olcLimits", "{0}dn.exact=b size=2", "{1}dn.exact=a size=1"})
	record = modify(t, configDB, mod(directory.ModReplace, "olcLimits", "dn.exact=b size=2", "dn.exact=a size=1"))
	if step := Derive(0, record, pre, post, configTarget(t)); len(step.Compensation) != 1 {
		t.Errorf("a reorder was not compensated: %+v", step)
	}
}

func TestConfigSettingThatWasUnsetIsRemoved(t *testing.T) {
	pre := entry(t, configDB, []string{"objectClass", "olcMdbConfig"})
	post := entry(t, configDB, []string{"olcTimeLimit", "3600"})
	step := Derive(0, modify(t, configDB, mod(directory.ModAdd, "olcTimeLimit", "3600")), pre, post, configTarget(t))
	c := step.Compensation[0]
	if step.Recoverability != Exact || c.Mods[0].Op != "delete" || len(c.Mods[0].Values) != 0 {
		t.Errorf("step %+v, want the whole attribute removed", step)
	}
}

func TestConfigNothingStoredDifferentlyIsNothingToDo(t *testing.T) {
	pre := entry(t, configDB, []string{"olcSizeLimit", "500"})
	step := Derive(0, modify(t, configDB, mod(directory.ModReplace, "olcSizeLimit", "500")), pre, pre, configTarget(t))
	if step.Recoverability != Exact || len(step.Compensation) != 0 {
		t.Errorf("step %+v, want exact with nothing to do", step)
	}
}

func TestConfigSecretsAndAccessRulesAreNotCompensated(t *testing.T) {
	const rootHash = "{SSHA}root-hash"
	pre := entry(t, configDB, []string{"olcRootPW", rootHash}, []string{"olcSizeLimit", "500"},
		[]string{"olcAccess", "{0}to * by * read"},
		[]string{"olcSyncrepl", "{0}rid=001 provider=ldap://a bindmethod=simple credentials=" + secret})
	post := entry(t, configDB, []string{"olcRootPW", "{SSHA}new"}, []string{"olcSizeLimit", "900"},
		[]string{"olcAccess", "{0}to * by * none"},
		[]string{"olcSyncrepl", "{0}rid=001 provider=ldap://b bindmethod=simple credentials=" + secret})
	record := modify(t, configDB,
		mod(directory.ModReplace, "olcRootPW", secret),
		mod(directory.ModReplace, "olcSizeLimit", "900"),
		mod(directory.ModReplace, "olcAccess", "to * by * none"),
		mod(directory.ModReplace, "olcSyncrepl", "rid=001 provider=ldap://b bindmethod=simple credentials="+secret))

	step := Derive(0, record, pre, post, configTarget(t))
	codes := reasonCodes(step)
	if step.Recoverability != Partial || codes[ReasonSensitiveNotCaptured] == "" || codes[ReasonAccessRuleNotWritten] != "olcAccess" {
		t.Errorf("step %+v", step)
	}
	if mods := step.Compensation[0].Mods; len(mods) != 1 || mods[0].Name != "olcSizeLimit" {
		t.Errorf("compensation %+v, want olcSizeLimit and nothing else", mods)
	}
	if out := jsonOf(t, step); strings.Contains(out, secret) || strings.Contains(out, rootHash) || strings.Contains(out, "by * read") {
		t.Fatalf("a secret or an access rule reached the step: %s", out)
	}

	// Nothing else touched: nothing to compensate at all.
	record.Mods = record.Mods[:1]
	if step := Derive(0, record, pre, post, configTarget(t)); step.Recoverability != Unavailable || len(step.Compensation) != 0 {
		t.Errorf("secret-only step %+v", step)
	}
}

func TestConfigEntriesAreNotCreatedRemovedOrRenamedBack(t *testing.T) {
	overlay := "olcOverlay=memberof," + configDB
	for _, record := range []directory.ChangeRecord{
		{DN: mustDN(t, overlay), Type: directory.ChangeAdd, Attrs: []directory.Attribute{
			{Name: "objectClass", Values: [][]byte{[]byte("olcOverlayConfig")}}}},
		{DN: mustDN(t, overlay), Type: directory.ChangeDelete},
		{DN: mustDN(t, overlay), Type: directory.ChangeModRDN, NewRDN: "olcOverlay=refint", DeleteOldRDN: true},
	} {
		step := Derive(0, record, entry(t, overlay), entry(t, overlay), configTarget(t))
		if step.Recoverability != Unavailable || reasonCodes(step)[ReasonConfigEntryNotCompensated] != "" ||
			len(step.Reasons) != 1 || len(step.Compensation) != 0 {
			t.Errorf("%s: step %+v", record.Type, step)
		}
	}
}

// --- schema ------------------------------------------------------------------

func TestSchemaAddIsCompensatedByRemovingWhatTheServerStored(t *testing.T) {
	pre := entry(t, schemaColl, []string{"olcAttributeTypes", "{0}" + otherType})
	// The server renders the definition its own way and gives it a position.
	stored := "{1}( 1.3.6.1.4.1.99997.1.7 NAME 'alderProbe' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )"
	post := entry(t, schemaColl, []string{"olcAttributeTypes", "{0}" + otherType, stored})
	record := modify(t, schemaColl, mod(directory.ModAdd, "olcAttributeTypes", probeType))

	step := Derive(0, record, pre, post, schemaTarget())
	if step.Recoverability != Partial || reasonCodes(step)[ReasonDefinitionMayBeInUse] != "olcAttributeTypes" || len(step.Reasons) != 1 {
		t.Fatalf("step %+v, want partial: entries may use what is removed", step)
	}
	c := step.Compensation[0]
	if len(c.Mods) != 1 || c.Mods[0].Op != "delete" || !equalStrings(texts(t, c.Mods[0].Values), []string{stored}) {
		t.Errorf("compensation %+v, want the stored value removed", c.Mods)
	}
	if c.Expect == nil || c.Expect.Attributes == nil || len(c.Expect.Attributes) != 0 || len(c.Expect.Definitions) != 1 {
		t.Fatalf("expect %+v, want one definition and no whole attribute", c.Expect)
	}
	d := c.Expect.Definitions[0]
	if d.Attribute != "olcAttributeTypes" || d.OID != probeOID || d.Value == nil || *d.Value.Text != stored {
		t.Errorf("expected definition %+v", d)
	}
}

func TestSchemaRemovalIsPutBackInItsPlace(t *testing.T) {
	stored := "{1}" + probeType
	pre := entry(t, schemaColl, []string{"olcAttributeTypes", "{0}" + otherType, stored})
	post := entry(t, schemaColl, []string{"olcAttributeTypes", "{0}" + otherType})
	record := modify(t, schemaColl, mod(directory.ModDelete, "olcAttributeTypes", stored))

	step := Derive(0, record, pre, post, schemaTarget())
	if step.Recoverability != Exact || len(step.Reasons) != 0 {
		t.Fatalf("step %+v, want exact", step)
	}
	c := step.Compensation[0]
	if len(c.Mods) != 1 || c.Mods[0].Op != "add" || !equalStrings(texts(t, c.Mods[0].Values), []string{stored}) {
		t.Errorf("compensation %+v, want the stored value added back with its position", c.Mods)
	}
	if d := c.Expect.Definitions[0]; d.OID != probeOID || d.Value != nil {
		t.Errorf("expected definition %+v, want it absent", d)
	}
}

func TestSchemaReplaceIsWrittenTheWayTheChangeWasWritten(t *testing.T) {
	edited := strings.Replace(probeType, "NAME 'alderProbe'", "NAME 'alderProbe' DESC 'edited'", 1)

	// Removed and added: the edited one goes, the earlier one comes back.
	pre := entry(t, schemaColl, []string{"olcAttributeTypes", "{0}" + probeType, "{1}" + otherType})
	post := entry(t, schemaColl, []string{"olcAttributeTypes", "{0}" + otherType, "{1}" + edited})
	record := modify(t, schemaColl,
		mod(directory.ModDelete, "olcAttributeTypes", "{0}"+probeType), mod(directory.ModAdd, "olcAttributeTypes", edited))
	step := Derive(0, record, pre, post, schemaTarget())
	c := step.Compensation[0]
	if step.Recoverability != Partial || len(c.Mods) != 2 ||
		c.Mods[0].Op != "delete" || texts(t, c.Mods[0].Values)[0] != "{1}"+edited ||
		c.Mods[1].Op != "add" || texts(t, c.Mods[1].Values)[0] != "{0}"+probeType {
		t.Errorf("step %+v, want delete the edited one, add the earlier one", step)
	}

	// Replaced in place by an add alone: the earlier one is added the same way.
	pre = entry(t, subschema, []string{"attributeTypes", probeType + " X-ORIGIN 'user defined'"})
	post = entry(t, subschema, []string{"attributeTypes", edited + " X-ORIGIN 'user defined'"})
	record = modify(t, subschema, mod(directory.ModAdd, "attributeTypes", edited))
	step = Derive(0, record, pre, post, schemaTarget())
	c = step.Compensation[0]
	if step.Recoverability != Partial || len(c.Mods) != 1 || c.Mods[0].Op != "add" ||
		texts(t, c.Mods[0].Values)[0] != probeType+" X-ORIGIN 'user defined'" {
		t.Errorf("step %+v, want the earlier definition added in place", step)
	}
	if d := c.Expect.Definitions[0]; d.Value == nil || *d.Value.Text != edited+" X-ORIGIN 'user defined'" {
		t.Errorf("expected definition %+v", d)
	}
}

func TestSchemaClassGoesBeforeTheTypeItNames(t *testing.T) {
	pre := entry(t, subschema)
	post := entry(t, subschema, []string{"attributeTypes", probeType}, []string{"objectClasses", probeClass})
	record := modify(t, subschema,
		mod(directory.ModAdd, "attributeTypes", probeType), mod(directory.ModAdd, "objectClasses", probeClass))
	c := Derive(0, record, pre, post, schemaTarget()).Compensation[0]
	if len(c.Mods) != 2 || c.Mods[0].Name != "objectClasses" || c.Mods[1].Name != "attributeTypes" {
		t.Errorf("compensation %+v, want the class removed before the type", c.Mods)
	}
}

func TestSchemaWhatIsNotOneDefinitionIsNotCompensated(t *testing.T) {
	pre := entry(t, schemaColl, []string{"olcAttributeTypes", "{0}" + otherType})
	post := entry(t, schemaColl, []string{"olcAttributeTypes", "{0}" + otherType, "{1}" + probeType},
		[]string{"olcObjectIdentifier", "{0}alderOID 1.3.6.1.4.1.99997"})
	cases := []struct {
		name string
		mods []directory.Mod
		want Recoverability
	}{
		{"an attribute that carries no definitions", []directory.Mod{
			mod(directory.ModAdd, "olcObjectIdentifier", "alderOID 1.3.6.1.4.1.99997")}, Unavailable},
		{"a whole collection replaced", []directory.Mod{
			mod(directory.ModReplace, "olcAttributeTypes", otherType, probeType)}, Unavailable},
		{"a whole collection removed", []directory.Mod{mod(directory.ModDelete, "olcAttributeTypes")}, Unavailable},
		{"a value that is not a definition", []directory.Mod{mod(directory.ModAdd, "olcAttributeTypes", "nonsense")}, Unavailable},
		{"one of two", []directory.Mod{
			mod(directory.ModAdd, "olcAttributeTypes", probeType),
			mod(directory.ModAdd, "olcObjectIdentifier", "alderOID 1.3.6.1.4.1.99997")}, Partial},
	}
	for _, tc := range cases {
		step := Derive(0, modify(t, schemaColl, tc.mods...), pre, post, schemaTarget())
		if step.Recoverability != tc.want || reasonCodes(step)[ReasonSchemaOrConfig] == "" {
			t.Errorf("%s: step %+v", tc.name, step)
		}
		if tc.want == Unavailable && len(step.Compensation) != 0 {
			t.Errorf("%s: an unavailable step carries a compensation", tc.name)
		}
	}

	// The same OID twice is two definitions no compensation can tell apart.
	twice := entry(t, schemaColl, []string{"olcAttributeTypes", "{0}" + probeType, "{1}" + probeType})
	step := Derive(0, modify(t, schemaColl, mod(directory.ModAdd, "olcAttributeTypes", probeType)), pre, twice, schemaTarget())
	if step.Recoverability != Unavailable {
		t.Errorf("an ambiguous OID: %+v", step)
	}
}

// --- assessment, before anything is read --------------------------------------

func TestAssessSchemaAndConfig(t *testing.T) {
	cases := []struct {
		name   string
		record directory.ChangeRecord
		where  Target
		want   Recoverability
		reason ReasonCode
	}{
		{"a definition added", modify(t, subschema, mod(directory.ModAdd, "attributeTypes", probeType)),
			schemaTarget(), Partial, ReasonDefinitionMayBeInUse},
		{"a definition removed", modify(t, subschema, mod(directory.ModDelete, "attributeTypes", probeType)),
			schemaTarget(), Exact, ""},
		{"a definition replaced", modify(t, schemaColl,
			mod(directory.ModDelete, "olcAttributeTypes", "{0}"+probeType), mod(directory.ModAdd, "olcAttributeTypes", probeType)),
			schemaTarget(), Partial, ReasonDefinitionMayBeInUse},
		{"a setting", modify(t, configDB, mod(directory.ModReplace, "olcSizeLimit", "900")), configTarget(t), Exact, ""},
		{"a setting and a secret", modify(t, configDB,
			mod(directory.ModReplace, "olcSizeLimit", "900"), mod(directory.ModReplace, "olcRootPW", secret)),
			configTarget(t), Partial, ReasonSensitiveNotCaptured},
		{"an access rule", modify(t, configDB, mod(directory.ModAdd, "olcAccess", "to * by * none")),
			configTarget(t), Unavailable, ReasonAccessRuleNotWritten},
		{"an overlay", directory.ChangeRecord{DN: mustDN(t, "olcOverlay=memberof,"+configDB), Type: directory.ChangeAdd,
			Attrs: []directory.Attribute{{Name: "objectClass", Values: [][]byte{[]byte("olcOverlayConfig")}}}},
			configTarget(t), Unavailable, ReasonConfigEntryNotCompensated},
	}
	for _, tc := range cases {
		got, reasons := Assess(tc.record, tc.where)
		if got != tc.want {
			t.Errorf("%s: %s %v, want %s", tc.name, got, reasons, tc.want)
		}
		if tc.reason != "" && (len(reasons) == 0 || reasons[0].Code != tc.reason) {
			t.Errorf("%s: reasons %v, want %s", tc.name, reasons, tc.reason)
		}
	}
}

// --- through a bundle ----------------------------------------------------------

func schemaSteps(t *testing.T) []Step {
	t.Helper()
	stored := "{1}" + probeType
	added := Derive(0, modify(t, schemaColl, mod(directory.ModAdd, "olcAttributeTypes", probeType)),
		entry(t, schemaColl, []string{"olcAttributeTypes", "{0}" + otherType}),
		entry(t, schemaColl, []string{"olcAttributeTypes", "{0}" + otherType, stored}), schemaTarget())
	removed := Derive(1, modify(t, schemaColl, mod(directory.ModDelete, "olcAttributeTypes", "{0}"+otherType)),
		entry(t, schemaColl, []string{"olcAttributeTypes", "{0}" + otherType, stored}),
		entry(t, schemaColl, []string{"olcAttributeTypes", "{0}" + probeType}), schemaTarget())
	setting := Derive(2, modify(t, configDB, mod(directory.ModReplace, "olcSizeLimit", "900")),
		entry(t, configDB, []string{"olcSizeLimit", "500"}), entry(t, configDB, []string{"olcSizeLimit", "900"}), configTarget(t))
	return []Step{added, removed, setting}
}

func TestSchemaAndConfigStepsRoundTripThroughABundle(t *testing.T) {
	b, err := New(Origin{NamingContexts: []string{"dc=alder,dc=test"}}, schemaSteps(t), fixedTime)
	if err != nil {
		t.Fatal(err)
	}
	decoded, integrity, err := Decode(encoded(t, b))
	if err != nil || integrity != IntegrityVerified {
		t.Fatalf("decode: %v %s", err, integrity)
	}
	if decoded.Recoverability != Partial {
		t.Errorf("recoverability %s", decoded.Recoverability)
	}
	changes := decoded.Changes()
	if len(changes) != 2 || changes[0].DN != configDB || changes[1].DN != schemaColl {
		t.Fatalf("changes %+v, want the setting, then one modification of the collection", changes)
	}
	// The two schema compensations are one modification: the removal undone
	// first, then the addition, and an expectation naming both definitions as
	// they are now.
	merged := changes[1]
	if len(merged.Mods) != 2 || merged.Mods[0].Op != "add" || merged.Mods[1].Op != "delete" {
		t.Fatalf("merged mods %+v", merged.Mods)
	}
	record, expect, err := merged.Record()
	if err != nil {
		t.Fatal(err)
	}
	if record.Type != directory.ChangeModify || expect == nil || len(expect.Definitions) != 2 {
		t.Fatalf("record %+v expect %+v", record, expect)
	}
	// The probe was stored at {1} and has since moved to {0}: the expectation
	// still holds, because a position is not part of a definition.
	live := entry(t, schemaColl, []string{"olcAttributeTypes", "{0}" + probeType})
	if attr, differs := expect.Differs(nil, live); differs {
		t.Errorf("the expectation does not hold of the collection as the changes left it (%s)", attr)
	}
	live = entry(t, schemaColl, []string{"olcAttributeTypes", "{0}" + otherType, "{1}" + probeType})
	if _, differs := expect.Differs(nil, live); !differs {
		t.Error("the expectation holds of a collection somebody else has put a definition back into")
	}
}

func TestDecodeRefusesABadExpectedDefinition(t *testing.T) {
	good, err := New(Origin{NamingContexts: []string{"dc=alder,dc=test"}}, schemaSteps(t), fixedTime)
	if err != nil {
		t.Fatal(err)
	}
	raw := string(encoded(t, good))
	resealed := func(edit func(d *Definition)) string {
		copyOf, _, err := Decode([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		edit(&copyOf.Steps[0].Compensation[0].Expect.Definitions[0])
		sum, err := copyOf.contentChecksum()
		if err != nil {
			t.Fatal(err)
		}
		copyOf.Checksum = sum
		return string(encoded(t, copyOf))
	}
	for name, input := range map[string]string{
		"an OID that is not numeric":   resealed(func(d *Definition) { d.OID = "alderProbe" }),
		"a value of another OID":       resealed(func(d *Definition) { d.Value = &encode([][]byte{[]byte(otherType)})[0] }),
		"a sensitive attribute":        resealed(func(d *Definition) { d.Attribute = "userPassword" }),
		"an attribute that is not one": resealed(func(d *Definition) { d.Attribute = "olc attribute" }),
		"an unknown field":             strings.Replace(raw, `"oid": "`, `"apply": true, "oid": "`, 1),
	} {
		if _, _, err := Decode([]byte(input)); codeOf(err) != CodeInvalid {
			t.Errorf("%s: %v, want invalid", name, err)
		}
	}
}

// --- what a configuration step must never carry --------------------------------

const proxyDB = "olcDatabase={2}ldap,cn=config"

func TestConfigProxyAndReplicationCredentialsAreNotCaptured(t *testing.T) {
	// back-ldap, back-meta and asyncmeta carry the bind they make to the
	// remote server inside an ordinary configuration value; 389 DS keeps the
	// bootstrap bind of an agreement in an attribute of its own.
	for _, tc := range []struct{ dn, name, before, after string }{
		{proxyDB, "olcDbIDAssertBind",
			"bindmethod=simple binddn=cn=proxy,dc=alder,dc=test credentials=" + secret,
			"bindmethod=simple binddn=cn=proxy,dc=alder,dc=test credentials=new-" + secret},
		{proxyDB, "olcDbACLBind",
			"bindmethod=simple binddn=cn=acl,dc=alder,dc=test credentials=" + secret,
			"bindmethod=simple binddn=cn=acl,dc=alder,dc=test credentials=new-" + secret},
		{proxyDB, "olcDbACLPasswd", secret, "new-" + secret},
		{"cn=to-b,cn=replica,cn=dc\\3Dalder\\2Cdc\\3Dtest,cn=mapping tree,cn=config",
			"nsDS5ReplicaBootstrapCredentials", secret, "new-" + secret},
		// Not in the table: caught by its name, as a configuration read
		// withholds it.
		{proxyDB, "olcDbPseudoRootPW", secret, "new-" + secret},
		{"cn=config", "nsslapd-ldapssotoken-secret", secret, "new-" + secret},
		// Not listed anywhere: caught by its value.
		{proxyDB, "olcDbFutureBind",
			"bindmethod=simple binddn=cn=x credentials=" + secret,
			"bindmethod=simple binddn=cn=x Credentials=\"new-" + secret + "\""},
	} {
		pre := entry(t, tc.dn, []string{tc.name, tc.before}, []string{"olcDbTimeout", "5"})
		post := entry(t, tc.dn, []string{tc.name, tc.after}, []string{"olcDbTimeout", "9"})
		record := modify(t, tc.dn, mod(directory.ModReplace, tc.name, tc.after), mod(directory.ModReplace, "olcDbTimeout", "9"))

		step := Derive(0, record, pre, post, configTarget(t))
		if step.Recoverability != Partial || reasonCodes(step)[ReasonSensitiveNotCaptured] != tc.name {
			t.Errorf("%s: step %+v, want partial with the secret named", tc.name, step)
		}
		if out := jsonOf(t, step); strings.Contains(out, secret) {
			t.Errorf("%s: a credential reached the step: %s", tc.name, out)
		}
		got, reasons := Assess(modify(t, tc.dn, mod(directory.ModReplace, tc.name, tc.after)), configTarget(t))
		if got != Unavailable || len(reasons) != 1 || reasons[0].Code != ReasonSensitiveNotCaptured {
			t.Errorf("%s: assessed %s %v, want unavailable for a secret", tc.name, got, reasons)
		}
	}
}

func TestConfigDefinitionsAreNeverPutBackAsSettings(t *testing.T) {
	// cn=schema,cn=config itself, or a collection created after the session
	// connected, is not a listed schema target and arrives here as
	// configuration. A definition is not a setting: removing the attribute
	// would remove every definition it holds.
	const schemaRoot = "cn=schema,cn=config"
	for _, name := range []string{"olcAttributeTypes", "olcObjectClasses", "attributeTypes", "objectClasses",
		"olcDitContentRules", "olcLdapSyntaxes", "olcObjectIdentifier"} {
		record := modify(t, schemaRoot, mod(directory.ModAdd, name, probeType))
		pre := entry(t, schemaRoot, []string{"cn", "schema"})
		post := entry(t, schemaRoot, []string{"cn", "schema"}, []string{name, "{0}" + probeType})

		step := Derive(0, record, pre, post, configTarget(t))
		if step.Recoverability != Unavailable || reasonCodes(step)[ReasonSchemaOrConfig] != name || len(step.Compensation) != 0 {
			t.Errorf("%s: step %+v, want unavailable and nothing written", name, step)
		}
		if got, reasons := Assess(record, configTarget(t)); got != Unavailable || len(reasons) != 1 || reasons[0].Code != ReasonSchemaOrConfig {
			t.Errorf("%s: assessed %s %v, want unavailable", name, got, reasons)
		}
	}
}
