package recovery

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/schema"
)

const (
	alice  = "cn=alice,ou=people,dc=alder,dc=test"
	secret = "correct-horse-battery-staple"
)

func testSchema(t testing.TB) *schema.Schema {
	t.Helper()
	sch := schema.Load("cn=subschema", map[string][]string{
		schema.AttrObjectClasses: {
			"( 2.5.6.0 NAME 'top' ABSTRACT MUST objectClass )",
			"( 2.5.6.6 NAME 'person' SUP top STRUCTURAL MUST ( sn $ cn ) " +
				"MAY ( userPassword $ telephoneNumber $ description $ mail ) )",
		},
		schema.AttrAttributeTypes: {
			"( 2.5.4.0 NAME 'objectClass' EQUALITY objectIdentifierMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.38 )",
			"( 2.5.4.3 NAME 'cn' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
			"( 2.5.4.4 NAME 'sn' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
			"( 2.5.4.13 NAME 'description' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
			"( 2.5.4.20 NAME 'telephoneNumber' EQUALITY telephoneNumberMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.50 )",
			"( 2.5.4.35 NAME 'userPassword' EQUALITY octetStringMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.40 )",
			"( 0.9.2342.19200300.100.1.3 NAME 'mail' EQUALITY caseIgnoreIA5Match SYNTAX 1.3.6.1.4.1.1466.115.121.1.26 )",
			"( 2.5.4.99 NAME 'photo' SYNTAX 1.3.6.1.4.1.1466.115.121.1.40 )",
			"( 1.3.6.1.1.16.4 NAME 'entryUUID' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 " +
				"NO-USER-MODIFICATION SINGLE-VALUE USAGE directoryOperation )",
			"( 2.5.18.2 NAME 'modifyTimestamp' SYNTAX 1.3.6.1.4.1.1466.115.121.1.24 " +
				"NO-USER-MODIFICATION SINGLE-VALUE USAGE directoryOperation )",
		},
	})
	if len(sch.Errors) != 0 {
		t.Fatalf("the test schema does not parse: %v", sch.Errors)
	}
	return sch
}

func mustDN(t testing.TB, s string) dn.DN {
	t.Helper()
	d, err := dn.Parse(s)
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return d
}

func entry(t testing.TB, d string, pairs ...[]string) *directory.Entry {
	t.Helper()
	e := directory.NewEntry(mustDN(t, d))
	for _, p := range pairs {
		values := make([][]byte, 0, len(p)-1)
		for _, v := range p[1:] {
			values = append(values, []byte(v))
		}
		e.Set(p[0], values)
	}
	return e
}

func mod(op directory.ModOp, name string, values ...string) directory.Mod {
	m := directory.Mod{Op: op, Name: name}
	for _, v := range values {
		m.Values = append(m.Values, []byte(v))
	}
	return m
}

func texts(t testing.TB, values []Value) []string {
	t.Helper()
	out := make([]string, 0, len(values))
	for _, v := range values {
		raw, err := v.Bytes()
		if err != nil {
			t.Fatalf("value: %v", err)
		}
		out = append(out, string(raw))
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func jsonOf(t testing.TB, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// --- modifications -----------------------------------------------------------

func TestModifyReplaceOfAMultiValuedAttribute(t *testing.T) {
	pre := entry(t, alice, []string{"objectClass", "person"}, []string{"description", "a", "b"})
	record := directory.ChangeRecord{DN: mustDN(t, alice), Type: directory.ChangeModify,
		Mods: []directory.Mod{mod(directory.ModReplace, "description", "b", "c")}}

	step := Derive(0, record, pre, testSchema(t), KindData)
	if step.Recoverability != Exact || len(step.Reasons) != 0 {
		t.Fatalf("recoverability %s %v, want exact", step.Recoverability, step.Reasons)
	}
	if len(step.Compensation) != 1 {
		t.Fatalf("compensation %+v", step.Compensation)
	}
	c := step.Compensation[0]
	if c.Type != "modify" || c.DN != alice || len(c.Mods) != 2 {
		t.Fatalf("compensation %+v", c)
	}
	if c.Mods[0].Op != "delete" || !equalStrings(texts(t, c.Mods[0].Values), []string{"c"}) {
		t.Errorf("first mod %+v, want delete c", c.Mods[0])
	}
	if c.Mods[1].Op != "add" || !equalStrings(texts(t, c.Mods[1].Values), []string{"a"}) {
		t.Errorf("second mod %+v, want add a", c.Mods[1])
	}
	if c.Expect == nil || len(c.Expect.Attributes) != 1 || c.Expect.Exhaustive ||
		!equalStrings(texts(t, c.Expect.Attributes[0].Values), []string{"b", "c"}) {
		t.Errorf("expect %+v, want description = b, c", c.Expect)
	}
}

func TestModifyAddToAnAbsentAttributeAndRemoveAWholeOne(t *testing.T) {
	pre := entry(t, alice, []string{"objectClass", "person"}, []string{"telephoneNumber", "1", "2"})
	record := directory.ChangeRecord{DN: mustDN(t, alice), Type: directory.ChangeModify, Mods: []directory.Mod{
		mod(directory.ModAdd, "mail", "alice@alder.test"),
		mod(directory.ModDelete, "telephoneNumber"),
	}}
	step := Derive(0, record, pre, testSchema(t), KindData)
	c := step.Compensation[0]
	if step.Recoverability != Exact || len(c.Mods) != 2 {
		t.Fatalf("step %+v", step)
	}
	if c.Mods[0].Op != "delete" || c.Mods[0].Name != "mail" || !equalStrings(texts(t, c.Mods[0].Values), []string{"alice@alder.test"}) {
		t.Errorf("mail compensation %+v", c.Mods[0])
	}
	if c.Mods[1].Op != "add" || c.Mods[1].Name != "telephoneNumber" || !equalStrings(texts(t, c.Mods[1].Values), []string{"1", "2"}) {
		t.Errorf("telephoneNumber compensation %+v", c.Mods[1])
	}
	// The removed attribute is expected absent, the added one to hold exactly its value.
	if got := c.Expect.Attributes[1]; got.Name != "telephoneNumber" || len(got.Values) != 0 {
		t.Errorf("telephoneNumber expectation %+v, want absent", got)
	}
}

func TestModifySeveralModificationsOfOneAttributeAreFollowedInOrder(t *testing.T) {
	pre := entry(t, alice, []string{"description", "keep", "drop"})
	record := directory.ChangeRecord{DN: mustDN(t, alice), Type: directory.ChangeModify, Mods: []directory.Mod{
		mod(directory.ModAdd, "description", "new"),
		mod(directory.ModDelete, "description", "drop"),
		mod(directory.ModDelete, "description", "new"),
		mod(directory.ModAdd, "description", "newer"),
	}}
	c := Derive(0, record, pre, testSchema(t), KindData).Compensation[0]
	if len(c.Mods) != 2 || c.Mods[0].Op != "delete" || !equalStrings(texts(t, c.Mods[0].Values), []string{"newer"}) ||
		c.Mods[1].Op != "add" || !equalStrings(texts(t, c.Mods[1].Values), []string{"drop"}) {
		t.Errorf("compensation %+v, want delete newer, add drop", c.Mods)
	}
	if !equalStrings(texts(t, c.Expect.Attributes[0].Values), []string{"keep", "newer"}) {
		t.Errorf("expect %v", texts(t, c.Expect.Attributes[0].Values))
	}
}

func TestModifyUsesTheEqualityRule(t *testing.T) {
	// A delete of "BOB" removes "bob" under caseIgnoreMatch; the compensation
	// puts back the value as it was stored.
	pre := entry(t, alice, []string{"description", "bob", "carol"})
	record := directory.ChangeRecord{DN: mustDN(t, alice), Type: directory.ChangeModify,
		Mods: []directory.Mod{mod(directory.ModDelete, "description", "BOB")}}
	c := Derive(0, record, pre, testSchema(t), KindData).Compensation[0]
	if len(c.Mods) != 1 || c.Mods[0].Op != "add" || !equalStrings(texts(t, c.Mods[0].Values), []string{"bob"}) {
		t.Errorf("compensation %+v, want add bob", c.Mods)
	}

	// Equal by the rule, stored differently: the earlier form is written back.
	pre = entry(t, alice, []string{"description", "Alice"})
	record.Mods = []directory.Mod{mod(directory.ModReplace, "description", "alice")}
	c = Derive(0, record, pre, testSchema(t), KindData).Compensation[0]
	if len(c.Mods) != 1 || c.Mods[0].Op != "replace" || !equalStrings(texts(t, c.Mods[0].Values), []string{"Alice"}) {
		t.Errorf("compensation %+v, want replace Alice", c.Mods)
	}

	// Truly unchanged: nothing to compensate, and still exact.
	record.Mods = []directory.Mod{mod(directory.ModReplace, "description", "Alice")}
	step := Derive(0, record, pre, testSchema(t), KindData)
	if step.Recoverability != Exact || len(step.Compensation) != 0 {
		t.Errorf("step %+v, want exact with nothing to do", step)
	}
}

func TestModifyOfABinaryValueIsCarriedAsBase64(t *testing.T) {
	pre := directory.NewEntry(mustDN(t, alice))
	pre.Set("photo", [][]byte{{0xff, 0xd8, 0x00, 0x01}})
	record := directory.ChangeRecord{DN: mustDN(t, alice), Type: directory.ChangeModify,
		Mods: []directory.Mod{{Op: directory.ModDelete, Name: "photo"}}}
	c := Derive(0, record, pre, testSchema(t), KindData).Compensation[0]
	if v := c.Mods[0].Values[0]; v.Base64 == nil || v.Text != nil {
		t.Errorf("binary value %+v, want base64", v)
	}
}

func TestModifyOfAPasswordIsNeverCaptured(t *testing.T) {
	pre := entry(t, alice, []string{"userPassword", "{SSHA}old-hash"}, []string{"description", "before"})
	record := directory.ChangeRecord{DN: mustDN(t, alice), Type: directory.ChangeModify, Mods: []directory.Mod{
		mod(directory.ModReplace, "userPassword", secret),
		mod(directory.ModReplace, "description", "after"),
	}}
	step := Derive(0, record, pre, testSchema(t), KindData)
	if step.Recoverability != Partial {
		t.Errorf("recoverability %s, want partial", step.Recoverability)
	}
	if len(step.Reasons) != 1 || step.Reasons[0].Code != ReasonSensitiveNotCaptured || step.Reasons[0].Attribute != "userPassword" {
		t.Errorf("reasons %+v", step.Reasons)
	}
	out := jsonOf(t, step)
	if strings.Contains(out, secret) || strings.Contains(out, "old-hash") {
		t.Fatalf("a password reached the step: %s", out)
	}
	for _, m := range step.Compensation[0].Mods {
		if strings.EqualFold(m.Name, "userPassword") {
			t.Errorf("the compensation touches userPassword: %+v", m)
		}
	}

	record.Mods = record.Mods[:1]
	step = Derive(0, record, pre, testSchema(t), KindData)
	if step.Recoverability != Unavailable || len(step.Compensation) != 0 {
		t.Errorf("password-only step %+v, want unavailable with no compensation", step)
	}
}

func TestModifyOfAServerOwnedAttributeIsNotCompensated(t *testing.T) {
	pre := entry(t, alice, []string{"modifyTimestamp", "20260101000000Z"}, []string{"description", "x"})
	record := directory.ChangeRecord{DN: mustDN(t, alice), Type: directory.ChangeModify, Mods: []directory.Mod{
		mod(directory.ModReplace, "modifyTimestamp", "20270101000000Z"),
		mod(directory.ModReplace, "description", "y"),
	}}
	step := Derive(0, record, pre, testSchema(t), KindData)
	if step.Recoverability != Partial || step.Reasons[0].Code != ReasonServerOwnedAttribute {
		t.Errorf("step %+v", step)
	}
}

// --- add and delete ----------------------------------------------------------

func TestAddIsCompensatedByADeleteThatExpectsWhatWasAdded(t *testing.T) {
	record := directory.ChangeRecord{DN: mustDN(t, alice), Type: directory.ChangeAdd, Attrs: []directory.Attribute{
		{Name: "objectClass", Values: [][]byte{[]byte("person")}},
		{Name: "sn", Values: [][]byte{[]byte("Liddell")}},
		{Name: "userPassword", Values: [][]byte{[]byte(secret)}},
	}}
	step := Derive(3, record, nil, testSchema(t), KindData)
	if step.Recoverability != Exact || step.Index != 3 || len(step.Compensation) != 1 {
		t.Fatalf("step %+v", step)
	}
	c := step.Compensation[0]
	if c.Type != "delete" || c.DN != alice || c.Expect == nil || !c.Expect.Exhaustive {
		t.Fatalf("compensation %+v", c)
	}
	names := []string{}
	for _, a := range c.Expect.Attributes {
		names = append(names, a.Name)
	}
	if !equalStrings(names, []string{"sn", "cn"}) {
		t.Errorf("expected attributes %v, want sn and the naming cn", names)
	}
	if out := jsonOf(t, step); strings.Contains(out, secret) {
		t.Fatalf("the added password reached the step: %s", out)
	}

	// An add over an entry that existed was not an add Alder ran.
	if got := Derive(0, record, entry(t, alice), testSchema(t), KindData); got.Recoverability != Unavailable {
		t.Errorf("add over an existing entry: %s", got.Recoverability)
	}
}

func TestDeleteRestoresOnlyWhatWasReadAndSaysSo(t *testing.T) {
	pre := entry(t, alice,
		[]string{"objectClass", "top", "person"},
		[]string{"cn", "alice"},
		[]string{"sn", "Liddell"},
		[]string{"userPassword", "{SSHA}old-hash"},
		[]string{"entryUUID", "5f1d..."},
		[]string{"modifyTimestamp", "20260101000000Z"},
	)
	record := directory.ChangeRecord{DN: mustDN(t, alice), Type: directory.ChangeDelete}
	step := Derive(0, record, pre, testSchema(t), KindData)
	if step.Recoverability != Partial {
		t.Errorf("recoverability %s, want partial: a recreated entry is not the same entry", step.Recoverability)
	}
	codes := map[ReasonCode]bool{}
	for _, r := range step.Reasons {
		codes[r.Code] = true
	}
	for _, want := range []ReasonCode{ReasonIdentityRegenerated, ReasonHiddenAttributesUnknown, ReasonSensitiveNotRestored} {
		if !codes[want] {
			t.Errorf("reasons %+v lack %s", step.Reasons, want)
		}
	}
	c := step.Compensation[0]
	names := []string{}
	for _, a := range c.Attributes {
		names = append(names, a.Name)
	}
	if c.Type != "add" || !equalStrings(names, []string{"objectClass", "cn", "sn"}) {
		t.Errorf("recreated attributes %v, want objectClass cn sn and nothing the server owns", names)
	}
	if out := jsonOf(t, step); strings.Contains(out, "old-hash") || strings.Contains(out, "5f1d") {
		t.Fatalf("a password or an identity reached the step: %s", out)
	}
}

// --- renames -----------------------------------------------------------------

func TestRenameMoveAndBoth(t *testing.T) {
	yes, no := true, false
	cases := []struct {
		name        string
		record      directory.ChangeRecord
		pre         *directory.Entry
		dn          string
		newRDN      string
		deleteOld   *bool
		newSuperior string
	}{
		{
			name:   "rename",
			record: directory.ChangeRecord{DN: mustDN(t, alice), Type: directory.ChangeModRDN, NewRDN: "cn=alicia", DeleteOldRDN: true},
			pre:    entry(t, alice, []string{"cn", "alice"}),
			dn:     "cn=alicia,ou=people,dc=alder,dc=test", newRDN: "cn=alice", deleteOld: &yes,
		},
		{
			name:   "rename to a value the entry already held",
			record: directory.ChangeRecord{DN: mustDN(t, alice), Type: directory.ChangeModRDN, NewRDN: "cn=Alicia", DeleteOldRDN: false},
			pre:    entry(t, alice, []string{"cn", "alice", "alicia"}),
			dn:     "cn=Alicia,ou=people,dc=alder,dc=test", newRDN: "cn=alice", deleteOld: &no,
		},
		{
			name: "move",
			record: directory.ChangeRecord{DN: mustDN(t, alice), Type: directory.ChangeModRDN, NewRDN: "cn=alice",
				DeleteOldRDN: true, NewSuperior: mustDN(t, "ou=former,dc=alder,dc=test")},
			pre: entry(t, alice, []string{"cn", "alice"}),
			dn:  "cn=alice,ou=former,dc=alder,dc=test", newRDN: "cn=alice", deleteOld: &no,
			newSuperior: "ou=people,dc=alder,dc=test",
		},
		{
			name: "rename and move",
			record: directory.ChangeRecord{DN: mustDN(t, alice), Type: directory.ChangeModRDN, NewRDN: "cn=alicia",
				DeleteOldRDN: false, NewSuperior: mustDN(t, "ou=former,dc=alder,dc=test")},
			pre: entry(t, alice, []string{"cn", "alice"}),
			dn:  "cn=alicia,ou=former,dc=alder,dc=test", newRDN: "cn=alice", deleteOld: &yes,
			newSuperior: "ou=people,dc=alder,dc=test",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			step := Derive(0, tc.record, tc.pre, testSchema(t), KindData)
			if step.Recoverability != Exact || len(step.Compensation) != 1 {
				t.Fatalf("step %+v", step)
			}
			c := step.Compensation[0]
			if c.Type != "modrdn" || !mustDN(t, c.DN).Equal(mustDN(t, tc.dn)) || c.NewRDN != tc.newRDN ||
				c.DeleteOldRDN == nil || *c.DeleteOldRDN != *tc.deleteOld || c.NewSuperior != tc.newSuperior {
				t.Errorf("compensation %+v (deleteOldRdn %v), want %s -> %s deleteOld %v superior %q",
					c, *c.DeleteOldRDN, tc.dn, tc.newRDN, *tc.deleteOld, tc.newSuperior)
			}
			if step.Original.TargetDN == "" {
				t.Error("the original rename does not record where the entry went")
			}
		})
	}
}

// --- what cannot be recovered ------------------------------------------------

func TestPasswordChangeSchemaAndConfigAreUnavailable(t *testing.T) {
	password := directory.ChangeRecord{DN: mustDN(t, alice), Type: directory.ChangeSetPassword, NewPassword: secret}
	step := Derive(0, password, entry(t, alice), testSchema(t), KindData)
	if step.Recoverability != Unavailable || step.Reasons[0].Code != ReasonPasswordNotCaptured || len(step.Compensation) != 0 {
		t.Errorf("password step %+v", step)
	}
	if out := jsonOf(t, step); strings.Contains(out, secret) {
		t.Fatalf("the new password reached the step: %s", out)
	}

	schemaChange := directory.ChangeRecord{DN: mustDN(t, "cn=schema"), Type: directory.ChangeModify,
		Mods: []directory.Mod{mod(directory.ModAdd, "attributeTypes", "( 1.2.3 NAME 'x' )")}}
	for _, kind := range []string{KindSchema, KindConfig} {
		step := Derive(0, schemaChange, entry(t, "cn=schema"), testSchema(t), kind)
		if step.Recoverability != Unavailable || step.Reasons[0].Code != ReasonSchemaOrConfig {
			t.Errorf("%s step %+v", kind, step)
		}
	}

	// A modify of an entry that could not be read has nothing to derive from.
	modify := directory.ChangeRecord{DN: mustDN(t, alice), Type: directory.ChangeModify,
		Mods: []directory.Mod{mod(directory.ModReplace, "description", "x")}}
	if step := Derive(0, modify, nil, testSchema(t), KindData); step.Reasons[0].Code != ReasonPreStateUnavailable {
		t.Errorf("unread entry step %+v", step)
	}
}

func TestReadAttributes(t *testing.T) {
	modify := directory.ChangeRecord{DN: mustDN(t, alice), Type: directory.ChangeModify, Mods: []directory.Mod{
		mod(directory.ModReplace, "description", "x"), mod(directory.ModAdd, "Description", "y"), mod(directory.ModAdd, "mail", "z"),
	}}
	if got := ReadAttributes(modify); !equalStrings(got, []string{"objectClass", "description", "mail"}) {
		t.Errorf("modify reads %v", got)
	}
	rename := directory.ChangeRecord{DN: mustDN(t, alice), Type: directory.ChangeModRDN, NewRDN: "uid=alice+cn=a", DeleteOldRDN: true}
	if got := ReadAttributes(rename); len(got) != 3 {
		t.Errorf("rename reads %v", got)
	}
	if got := ReadAttributes(directory.ChangeRecord{Type: directory.ChangeDelete}); !equalStrings(got, []string{"*"}) {
		t.Errorf("delete reads %v", got)
	}
	if !Covers([]string{"*"}, []string{"mail"}) || Covers([]string{"cn"}, []string{"mail"}) || !Covers([]string{"Mail;lang-en", "cn"}, []string{"mail"}) {
		t.Error("Covers")
	}
}

func TestOverall(t *testing.T) {
	s := func(r Recoverability) Step { return Step{Recoverability: r} }
	cases := []struct {
		steps []Step
		want  Recoverability
	}{
		{nil, Unavailable},
		{[]Step{s(Exact), s(Exact)}, Exact},
		{[]Step{s(Exact), s(Unavailable)}, Partial},
		{[]Step{s(Partial)}, Partial},
		{[]Step{s(Unavailable), s(Unavailable)}, Unavailable},
	}
	for _, tc := range cases {
		if got := Overall(tc.steps); got != tc.want {
			t.Errorf("Overall(%v) = %s, want %s", tc.steps, got, tc.want)
		}
	}
}

func TestEncodingNeverEscapesAwayAControlCharacterAsText(t *testing.T) {
	values := encode([][]byte{[]byte("plain"), []byte("bell\x07"), {0xff}})
	if values[0].Text == nil || values[1].Base64 == nil || values[2].Base64 == nil {
		t.Errorf("encode %+v", values)
	}
	var buf bytes.Buffer
	b, _ := New(Origin{}, []Step{{Kind: KindData, Recoverability: Exact, Original: Original{Type: "modify", DN: alice},
		Compensation: []Change{{DN: alice, Type: "modify", Mods: []Mod{{Op: "add", Name: "description", Values: values}}}}}}, fixedTime)
	if err := Encode(&buf, b); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(buf.String(), 0x07) {
		t.Error("a raw control character reached the encoded bundle")
	}
}
