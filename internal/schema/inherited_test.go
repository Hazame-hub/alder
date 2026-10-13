package schema

import "testing"

// A subtype of a secret is a secret. Its name is its own, so the name tables
// never caught it, and its values -- the same hash userPassword would have
// withheld -- reached the browser.
func TestASubtypeOfASecretIsASecret(t *testing.T) {
	const appPassword = "1.3.6.1.4.1.99999.7.1"
	if IsSensitive("appPassword") {
		t.Fatal("appPassword is sensitive before any schema declared it; the test proves nothing")
	}
	Load("cn=Subschema", map[string][]string{
		AttrAttributeTypes: {
			"( 2.5.4.35 NAME 'userPassword' EQUALITY octetStringMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.40 )",
			"( " + appPassword + " NAME ( 'appPassword' 'appPwd' ) SUP userPassword )",
			// Two levels down, by OID.
			"( 1.3.6.1.4.1.99999.7.2 NAME 'appPasswordOld' SUP " + appPassword + " )",
			"( 1.3.6.1.4.1.99999.7.3 NAME 'appNote' SUP description )",
		},
	})
	for _, name := range []string{"appPassword", "APPPWD", "appPassword;binary", appPassword, "appPasswordOld", "1.3.6.1.4.1.99999.7.2"} {
		if !IsSensitive(name) {
			t.Errorf("IsSensitive(%q) = false after a schema declared it below userPassword", name)
		}
	}
	if IsSensitive("appNote") {
		t.Error("appNote is a subtype of description, and was treated as a secret")
	}
}

func TestAuthPasswordIsASecret(t *testing.T) {
	for _, name := range []string{"authPassword", "authpassword;binary", "1.3.6.1.4.1.4203.1.3.4"} {
		if !IsSensitive(name) {
			t.Errorf("IsSensitive(%q) = false; RFC 3112 authPassword holds a password digest", name)
		}
	}
	if Releasable("authPassword", []byte("SHA256$c2FsdA==$aGFzaA==")) {
		t.Error("an authPassword value was releasable; without a {scheme} prefix it must be treated as the credential")
	}
}
