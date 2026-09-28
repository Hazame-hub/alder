package schema

import "testing"

// What an export may carry when the operator ticks "include sensitive
// attributes".
//
// The option was built for password digests: a hash can be carried to
// another server and a directory rebuilt from it, and a hash is useless
// against anything but the account it belongs to. The 1.26 security fix
// added the configuration tree's secrets to the same set the option
// releases, and so handed an operator the root password in plain text and
// the replication bind password inside an olcSyncrepl line -- from a screen
// that had just said "a secret; Alder never sends it to the browser", via a
// button that same screen recommended.

func TestAnOrdinaryAttributeIsAlwaysReleasable(t *testing.T) {
	for _, name := range []string{"cn", "mail", "olcDbIndex", "objectClass"} {
		if !Releasable(name, []byte("anything")) {
			t.Errorf("%s is treated as a secret", name)
		}
	}
}

func TestADigestMayTravel(t *testing.T) {
	// The restore case the option exists for.
	for _, v := range []string{"{SSHA}x9Qk", "{PBKDF2-SHA512}AAAA", "{CRYPT}ab"} {
		if !Releasable("userPassword", []byte(v)) {
			t.Errorf("a hashed userPassword (%s) is withheld, so the option does nothing", v)
		}
	}
	if !Releasable("nsslapd-rootpw", []byte("{PBKDF2-SHA512}AAAA")) {
		t.Error("a hashed root password is withheld")
	}
}

func TestACredentialInTheClearNeverTravels(t *testing.T) {
	// The finding. olcRootPW holds a digest on one server and the root
	// password itself on the next; the attribute cannot tell you which, and
	// the value can.
	if Releasable("olcRootPW", []byte("alder-admin")) {
		t.Error("a cleartext olcRootPW is written into an export")
	}
	if Releasable("userPassword", []byte("hunter2")) {
		t.Error("an unprefixed userPassword is the password itself, and it is written into an export")
	}
	// RFC 2307 says an unprefixed value IS the cleartext password, which is
	// what the entry viewer means when it prints "stored in the clear".
	if Releasable("userPassword", []byte("")) {
		t.Error("an empty value is treated as a digest")
	}
	if Releasable("userPassword", []byte("{}x")) {
		t.Error("an empty brace pair is treated as a scheme")
	}
}

func TestWhatIsNeverWrittenWhateverItLooksLike(t *testing.T) {
	// These carry a credential inside a longer structured value, or are key
	// material outright. A {scheme} prefix would mean nothing on them, and
	// an operator ticking a box is not the restore case.
	for _, name := range []string{
		"olcSyncrepl", "olcDbCryptKey", "nsDS5ReplicaBindCredentials",
		"nsMultiplexorCredentials", "nsslapd-keyPassword", "nsSymmetricKey",
		"nsds5ReplicaCredentials", "krbPrincipalKey",
	} {
		for _, v := range []string{"{SSHA}looks-hashed", "rid=001 credentials=\"secret\""} {
			if Releasable(name, []byte(v)) {
				t.Errorf("%s was released carrying %q", name, v)
			}
		}
	}
}

func TestOptionsOnTheAttributeDoNotEvadeTheRule(t *testing.T) {
	if Releasable("olcSyncrepl;binary", []byte("x")) {
		t.Error("an attribute option evaded the rule")
	}
	if Releasable("USERPASSWORD", []byte("hunter2")) {
		t.Error("case evaded the rule")
	}
}
