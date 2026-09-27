package replication

import (
	"strings"
	"testing"
	"time"
)

// The parts worth pinning without a directory: reading each server's change
// sequence into the same pair, and not carrying a credential out of a value
// that contains one.

func TestBothServersChangeSequencesReadIntoTheSameAnswer(t *testing.T) {
	// The same moment, written by the two servers in their own ways. If
	// these two ever stop landing on the same answer, a comparison between a
	// supplier and its consumer stops meaning anything.
	openldap, ok := parseOpenLDAPCSN("20260927082421.145666Z#000000#001#000000")
	if !ok {
		t.Fatal("OpenLDAP's form was not read")
	}
	if openldap.Origin != "1" {
		t.Errorf("origin is %q, want the server id", openldap.Origin)
	}
	if got := openldap.At.UTC().Format(time.RFC3339Nano); got != "2026-09-27T08:24:21.145666Z" {
		t.Errorf("time is %s", got)
	}

	// The sid is hexadecimal. Reading it as text made server 16 report as 10
	// and server 10 report as the letter a, so the cursor and the "this
	// server is id N" line beside it named the same server two ways -- on the
	// one screen an operator is told to compare against another server.
	for _, tc := range []struct{ csn, want string }{
		{"20260927082421.145666Z#000000#00a#000000", "10"},
		{"20260927082421.145666Z#000000#010#000000", "16"},
		{"20260927082421.145666Z#000000#fff#000000", "4095"},
		// Legal, and what a provider with no olcServerID stamps.
		{"20260927082421.145666Z#000000#000#000000", "0"},
	} {
		got, ok := parseOpenLDAPCSN(tc.csn)
		if !ok {
			t.Fatalf("%q was not read", tc.csn)
		}
		if got.Origin != tc.want {
			t.Errorf("%q has origin %q, want %q", tc.csn, got.Origin, tc.want)
		}
	}
	if _, ok := parseOpenLDAPCSN("20260927082421.145666Z#000000#zzz#000000"); ok {
		t.Error("a sid that is not a number was read as one")
	}

	ds389, ok := parse389CSN("6ab8d2b6000b00010000")
	if !ok {
		t.Fatal("389 DS's form was not read")
	}
	if ds389.Origin != "1" {
		t.Errorf("origin is %q, want the replica id", ds389.Origin)
	}
	if ds389.At.IsZero() || ds389.At.Year() != 2026 {
		t.Errorf("time is %s", ds389.At)
	}
}

func TestTheReplicaUpdateVectorIsReadAsTheLatestChange(t *testing.T) {
	// Two CSNs: where this server started with that replica, and where it has
	// got to. The second is the one that answers "how far along".
	cursor, ok := parseRUV("{replica 1 ldap://ds389:3389} 6ab8d208000000010000 6ab8d2b6000b00010000")
	if !ok {
		t.Fatal("the vector was not read")
	}
	if cursor.Origin != "1" {
		t.Errorf("origin is %q", cursor.Origin)
	}
	later, _ := parse389CSN("6ab8d2b6000b00010000")
	if !cursor.At.Equal(later.At) {
		t.Errorf("the vector was read as %s, want the last change %s", cursor.At, later.At)
	}
	if !strings.Contains(cursor.Raw, "{replica 1") {
		t.Errorf("the value the server wrote was not kept: %q", cursor.Raw)
	}

	// The generation line names no replica and is not something anybody can
	// act on.
	if _, ok := parseRUV("{replicageneration} 6ab8d205000000010000"); ok {
		t.Error("the generation line was read as a replica's position")
	}
	if _, ok := parseRUV("nonsense"); ok {
		t.Error("a value that is not a vector was read as one")
	}
}

func TestASyncreplLinkCarriesNoCredential(t *testing.T) {
	// The value as slapd stores it, credential and all. This is the one
	// assertion in this package that would matter most if it failed.
	value := `{0}rid=001 provider=ldap://openldap:389 bindmethod=simple timeout=0 ` +
		`network-timeout=0 binddn="cn=admin,dc=alder,dc=test" credentials="alder-admin" ` +
		`keepalive=0:0:0 starttls=no filter="(objectclass=*)" searchbase="dc=alder,dc=test" ` +
		`scope=sub type=refreshAndPersist retry="5 +"`

	link := openldapLink(value, "olcDatabase={1}mdb,cn=config")
	rendered := link.Name + link.Peer + link.BindDN + link.Transport + link.Status +
		strings.Join(link.Notes, " ")
	if strings.Contains(rendered, "alder-admin") || strings.Contains(strings.ToLower(rendered), "credentials") {
		t.Fatalf("the credential survived into the link: %+v", link)
	}

	if link.Name != "rid=001" {
		t.Errorf("name is %q, want the rid", link.Name)
	}
	if link.Peer != "ldap://openldap:389" {
		t.Errorf("provider is %q", link.Peer)
	}
	if link.BindDN != "cn=admin,dc=alder,dc=test" {
		t.Errorf("bind DN is %q; a quoted value holding commas must survive whole", link.BindDN)
	}
	if link.Direction != DirectionIncoming {
		t.Errorf("a syncrepl link runs %q", link.Direction)
	}
	if link.State != StateUnknown || link.Status == "" {
		t.Errorf("OpenLDAP records no status and the report must say so: %q / %q", link.State, link.Status)
	}
}

func TestAQuotedFieldIsNotCutInHalf(t *testing.T) {
	fields := splitSyncrepl(`rid=001 filter="(&(objectclass=*)(cn=a b))" scope=sub`)
	if len(fields) != 3 {
		t.Fatalf("split into %d fields: %q", len(fields), fields)
	}
	if fields[1] != `filter="(&(objectclass=*)(cn=a b))"` {
		t.Errorf("the filter was cut: %q", fields[1])
	}
}

func TestTheServersOwnLastWordDecidesTheVerdict(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     string
		statusJSON string
		inProgress bool
		want       string
	}{
		{"green", "Error (0) Replica acquired successfully: Incremental update succeeded",
			`{"state": "green", "ldap_rc": "0"}`, false, StateOK},
		{"red", "Error (-1) Can't contact LDAP server",
			`{"state": "red", "ldap_rc": "-1"}`, false, StateFailing},
		{"in progress beats the colour", "Error (0) ok", `{"state": "green"}`, true, StateWorking},
		{"no json, code zero", "Error (0) Replica acquired successfully", "", false, StateOK},
		{"no json, code set", "Error (49) Invalid credentials", "", false, StateFailing},
		{"nothing recorded", "", "", false, StateUnknown},
	} {
		if got := stateFrom(tc.status, tc.statusJSON, tc.inProgress); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestARoleIsWhatTheServerCanActuallyDo(t *testing.T) {
	for _, tc := range []struct {
		supplies, consumes bool
		want               string
	}{
		{true, false, RoleSupplier},
		{false, true, RoleConsumer},
		{true, true, RoleBoth},
		{false, false, RoleNone},
	} {
		if got := roleOf(tc.supplies, tc.consumes); got != tc.want {
			t.Errorf("supplies=%v consumes=%v is %q, want %q", tc.supplies, tc.consumes, got, tc.want)
		}
	}
	// A server that supplies one suffix and consumes another is both, which
	// is an ordinary topology rather than a contradiction.
	mixed := []Suffix{{Role: RoleSupplier}, {Role: RoleConsumer}}
	if got := overallRole(mixed); got != RoleBoth {
		t.Errorf("a server supplying one suffix and consuming another is %q", got)
	}
	// And the sentence behind the word never claims to know who is listening,
	// because on OpenLDAP nothing can be asked that.
	for _, role := range []string{RoleSupplier, RoleBoth} {
		if strings.Contains(whyRole(role, mixed), "sends changes to") {
			t.Errorf("%q claims to know a consumer exists: %q", role, whyRole(role, mixed))
		}
	}
	// Nor that it receives nothing. A 389 DS read-write replica is type 3
	// whether it is the only supplier or one of several, so the negative was
	// a claim the server had not made -- and it contradicted the origins
	// listed on the same card.
	if strings.Contains(whyRole(RoleSupplier, mixed), "receives none") {
		t.Errorf("the supplier sentence asserts what it cannot know: %q", whyRole(RoleSupplier, mixed))
	}
}

func TestHowFarAlongIsMeasuredFromTheLatestChange(t *testing.T) {
	now := time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)
	s := Suffix{Cursors: []Cursor{
		{Origin: "1", At: now.Add(-10 * time.Minute)},
		{Origin: "2", At: now.Add(-2 * time.Minute)},
	}}
	age, origin, ok := s.Behind(now)
	if !ok || origin != "2" || age != 2*time.Minute {
		t.Fatalf("behind by %s from %q (ok=%v)", age, origin, ok)
	}

	// A peer whose clock is ahead of ours would otherwise report a negative
	// age, which reads as nonsense on a screen.
	ahead := Suffix{Cursors: []Cursor{{Origin: "1", At: now.Add(time.Minute)}}}
	if age, _, _ := ahead.Behind(now); age != 0 {
		t.Errorf("a peer whose clock is ahead reports %s", age)
	}

	if _, _, ok := (Suffix{}).Behind(now); ok {
		t.Error("a suffix with no change sequence claimed to know how far along it is")
	}
}

func TestATimestampTheServerNeverWroteStaysZero(t *testing.T) {
	if got := parseGeneralizedTime("20260927080004Z"); got.IsZero() {
		t.Error("a real timestamp was dropped")
	}
	for _, empty := range []string{"", "0", "19700101000000Z"} {
		if got := parseGeneralizedTime(empty); !got.IsZero() {
			t.Errorf("%q was read as %s, and a screen would show it as a real moment", empty, got)
		}
	}
}
