package signing

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// What a signature has to be worth: it covers the document's exact bytes, it
// cannot be moved to another document, an unsigned file still works, a key
// nobody trusts is said to be untrusted rather than called a forgery, and
// anything that does not match is refused before the payload is used.

// sameJSON compares two documents as documents. An envelope indents the
// payload it carries, which is why a signature covers the compact form.
func sameJSON(got []byte, want string) bool {
	var a, b bytes.Buffer
	if err := json.Compact(&a, got); err != nil {
		return false
	}
	if err := json.Compact(&b, []byte(want)); err != nil {
		return false
	}
	return a.String() == b.String()
}

const payload = `{"format":"alder-snapshot","version":1,"kind":"config","checksum":"sha256:abc"}`

func keys(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func signed_(t *testing.T, doc string, priv ed25519.PrivateKey, signer string) []byte {
	t.Helper()
	e, err := Sign([]byte(doc), priv, signer, time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	var buf bytes.Buffer
	if err := Encode(&buf, e); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func trust(pubs ...ed25519.PublicKey) TrustedKeys {
	t := TrustedKeys{}
	for _, p := range pubs {
		t.Add(p)
	}
	return t
}

func TestASignedDocumentOpensToExactlyWhatWasSigned(t *testing.T) {
	pub, priv := keys(t)
	doc := signed_(t, payload, priv, "alice")

	opened, result, err := Open(doc, trust(pub))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !sameJSON(opened, payload) {
		t.Fatalf("the payload came back as %s", opened)
	}
	if result.Status != Verified || len(result.Signers) != 1 || !result.Signers[0].Trusted {
		t.Fatalf("result: %+v", result)
	}
	if result.Signers[0].KeyID != KeyID(pub) || result.Signers[0].Signer != "alice" {
		t.Fatalf("signer: %+v", result.Signers[0])
	}
	if result.Signers[0].SignedAt != "2026-09-23T10:00:00Z" {
		t.Fatalf("signed at %q", result.Signers[0].SignedAt)
	}
}

func TestAnUnsignedDocumentStillWorks(t *testing.T) {
	opened, result, err := Open([]byte(payload), TrustedKeys{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !sameJSON(opened, payload) || result.Status != Unsigned {
		t.Fatalf("%s %+v", opened, result)
	}
}

func TestAChangedDocumentIsRefused(t *testing.T) {
	pub, priv := keys(t)
	doc := signed_(t, payload, priv, "alice")

	// One byte of the payload, inside the envelope.
	edited := bytes.Replace(doc, []byte("sha256:abc"), []byte("sha256:abd"), 1)
	if bytes.Equal(doc, edited) {
		t.Fatal("nothing was edited")
	}
	opened, result, err := Open(edited, trust(pub))
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("a changed document opened: %v", err)
	}
	if opened != nil {
		t.Fatal("a changed payload was handed back")
	}
	if result.Status != Invalid || result.Detail == "" {
		t.Fatalf("result: %+v", result)
	}
}

func TestASignatureCannotBeMovedToAnotherDocument(t *testing.T) {
	pub, priv := keys(t)
	first := signed_(t, payload, priv, "alice")

	var e Envelope
	if err := json.Unmarshal(first, &e); err != nil {
		t.Fatal(err)
	}
	// The same signature, over a different payload.
	e.Payload = json.RawMessage(`{"format":"alder-snapshot","version":1,"kind":"data","checksum":"sha256:abc"}`)
	moved, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if _, result, err := Open(moved, trust(pub)); !errors.Is(err, ErrInvalid) || result.Status != Invalid {
		t.Fatalf("a signature was reused on another document: %v %+v", err, result)
	}
}

func TestAKeyNobodyTrustsIsUntrustedRatherThanInvalid(t *testing.T) {
	_, priv := keys(t)
	other, _ := keys(t)
	doc := signed_(t, payload, priv, "someone")

	opened, result, err := Open(doc, trust(other))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !sameJSON(opened, payload) {
		t.Fatal("the payload should still be readable")
	}
	if result.Status != Untrusted || len(result.Signers) != 1 || result.Signers[0].Trusted {
		t.Fatalf("result: %+v", result)
	}
	if result.Detail == "" {
		t.Fatal("an untrusted answer says nothing about why")
	}
}

func TestOneOfTwoSignaturesBeingTrustedIsEnough(t *testing.T) {
	pubA, privA := keys(t)
	_, privB := keys(t)
	e, err := Sign([]byte(payload), privA, "alice", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := AddSignature(e, privB, "bob", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := AddSignature(e, privA, "alice again", time.Now()); err == nil {
		t.Fatal("one key signed the same document twice")
	}
	result := Verify(e, trust(pubA))
	if result.Status != Verified || len(result.Signers) != 2 {
		t.Fatalf("result: %+v", result)
	}
	trustedCount := 0
	for _, s := range result.Signers {
		if s.Trusted {
			trustedCount++
		}
	}
	if trustedCount != 1 {
		t.Fatalf("%d signers trusted, want 1", trustedCount)
	}
}

func TestAHostileEnvelopeIsRefusedRatherThanRead(t *testing.T) {
	_, priv := keys(t)
	good := string(signed_(t, payload, priv, "alice"))

	cases := map[string]struct {
		doc string
		err error
	}{
		"an unknown field":           {strings.Replace(good, `"version": 1`, `"version": 1,`+"\n"+`  "trustMe": true`, 1), ErrInvalid},
		"a version from the future":  {strings.Replace(good, `"version": 1`, `"version": 9`, 1), ErrUnsupportedVersion},
		"no signatures":              {`{"format":"alder-signed","version":1,"payload":{"a":1},"signatures":[]}`, ErrInvalid},
		"a payload that is not JSON": {`{"format":"alder-signed","version":1,"payload":"not a document","signatures":[{"algorithm":"ed25519","keyId":"x","value":"y"}]}`, nil},
		"a second document":          {good + good, ErrInvalid},
		"an empty signature":         {`{"format":"alder-signed","version":1,"payload":{"a":1},"signatures":[{"algorithm":"ed25519","keyId":"x","value":""}]}`, ErrInvalid},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Decode([]byte(c.doc))
			if err == nil {
				t.Fatalf("%s was accepted", name)
			}
			if c.err != nil && !errors.Is(err, c.err) {
				t.Fatalf("%s gave %v", name, err)
			}
		})
	}
}

func TestAnAlgorithmThisBuildDoesNotKnowIsInvalid(t *testing.T) {
	pub, priv := keys(t)
	e, err := Sign([]byte(payload), priv, "alice", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	e.Signatures[0].Algorithm = "rsa-pkcs1"
	if result := Verify(e, trust(pub)); result.Status != Invalid {
		t.Fatalf("an unknown algorithm gave %+v", result)
	}
}

func TestKeysRoundTripThroughPEM(t *testing.T) {
	pub, priv := keys(t)
	dir := t.TempDir()

	privPEM, err := EncodePrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	pubPEM, err := EncodePublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pubPEM), KeyID(pub)) {
		t.Error("the public key file does not carry its key id for a person to read")
	}
	if strings.Contains(string(pubPEM), "PRIVATE") {
		t.Fatal("the public key file mentions a private key")
	}

	privPath := filepath.Join(dir, "signing.pem")
	pubPath := filepath.Join(dir, "signing.pub.pem")
	if err := os.WriteFile(privPath, privPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pubPath, pubPEM, 0o644); err != nil {
		t.Fatal(err)
	}

	back, err := LoadPrivateKey(privPath)
	if err != nil {
		t.Fatalf("LoadPrivateKey: %v", err)
	}
	if !back.Equal(priv) {
		t.Fatal("the private key came back as another key")
	}
	trusted, err := LoadTrustedKeys([]string{pubPath})
	if err != nil {
		t.Fatalf("LoadTrustedKeys: %v", err)
	}
	if len(trusted) != 1 || trusted.IDs()[0] != KeyID(pub) {
		t.Fatalf("trusted: %v", trusted.IDs())
	}

	// A directory of keys, which is how a deployment adds one.
	keyDir := filepath.Join(dir, "trusted")
	if err := os.Mkdir(keyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	otherPub, _ := keys(t)
	otherPEM, err := EncodePublicKey(otherPub)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(keyDir, "a.pem"), pubPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(keyDir, "b.pem"), otherPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(keyDir, "notes.txt"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}
	fromDir, err := LoadTrustedKeys([]string{keyDir})
	if err != nil {
		t.Fatalf("LoadTrustedKeys(dir): %v", err)
	}
	if len(fromDir) != 2 {
		t.Fatalf("a directory of keys loaded %d", len(fromDir))
	}

	// A private key where a public one belongs is refused, so nobody publishes
	// one by mistake.
	if _, err := LoadTrustedKeys([]string{privPath}); !errors.Is(err, ErrNotAKey) {
		t.Fatalf("a private key was accepted as a trusted key: %v", err)
	}
	if _, err := LoadPrivateKey(pubPath); !errors.Is(err, ErrNotAKey) {
		t.Fatalf("a public key was accepted as a signing key: %v", err)
	}
}

func TestSigningRefusesWhatItCannotSign(t *testing.T) {
	_, priv := keys(t)
	if _, err := Sign(nil, priv, "", time.Now()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("signing nothing gave %v", err)
	}
	if _, err := Sign([]byte("dn: cn=x"), priv, "", time.Now()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("signing LDIF gave %v", err)
	}
	if _, err := Sign([]byte(payload), ed25519.PrivateKey("short"), "", time.Now()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("signing with a stub key gave %v", err)
	}
}
