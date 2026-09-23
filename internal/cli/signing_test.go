package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 1.15: signing on the command line.
//
// The point of these is that the private key stays a file on this machine: no
// command sends one anywhere, signing needs no server at all, and what a
// verify says about a document is decided here from keys the person named.

const signableDoc = `{"format":"alder-snapshot","version":1,"kind":"data","createdAt":"2026-09-23T10:00:00Z",
  "source":{"base":"dc=alder,dc=test","scope":"sub","filter":"(objectClass=*)"},
  "operationalAttributes":false,"schemaAvailable":true,"excluded":[],"entryCount":0,"entries":[],
  "checksum":"sha256:0000"}`

// generateKey makes a key pair through the command, which is the only way a
// person makes one.
func generateKey(t *testing.T, dir, name string) (private, public string) {
	t.Helper()
	private = filepath.Join(dir, name+".pem")
	r := newStub(t).run(t, runOpts{noConnection: true}, "key", "generate", "--output", private)
	expectCode(t, r, ExitOK)
	public = filepath.Join(dir, name+".pub.pem")
	if _, err := os.Stat(public); err != nil {
		t.Fatalf("the public key was not written beside the private one: %v", err)
	}
	return private, public
}

func TestAKeyIsMadeHereAndTheServerNeverSeesThePrivateHalf(t *testing.T) {
	dir := t.TempDir()
	private, public := generateKey(t, dir, "signing")

	privPEM, err := os.ReadFile(private)
	if err != nil {
		t.Fatal(err)
	}
	pubPEM, err := os.ReadFile(public)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(privPEM), "BEGIN PRIVATE KEY") {
		t.Fatalf("the private key is not a PEM private key:\n%s", privPEM)
	}
	if strings.Contains(string(pubPEM), "PRIVATE") {
		t.Fatal("the public key file mentions a private key")
	}

	// The key id is printed, and is the one the public key file carries.
	r := newStub(t).run(t, runOpts{noConnection: true}, "key", "show", public)
	expectCode(t, r, ExitOK)
	id := strings.TrimSpace(r.stdout)
	if len(id) != 64 || !strings.Contains(string(pubPEM), id) {
		t.Fatalf("key id %q", id)
	}

	// A private key is refused where a public one belongs.
	expectCode(t, newStub(t).run(t, runOpts{noConnection: true}, "key", "show", private), ExitFailed)
	// And a key is never written to standard output.
	expectCode(t, newStub(t).run(t, runOpts{noConnection: true}, "key", "generate", "--output", "-"), ExitUsage)
	// Nor over an existing file without --force: overwriting a signing key is
	// the one mistake that cannot be undone.
	expectCode(t, newStub(t).run(t, runOpts{noConnection: true}, "key", "generate", "--output", private), ExitUsage)
}

func TestSigningNeedsNoServer(t *testing.T) {
	dir := t.TempDir()
	private, public := generateKey(t, dir, "signing")
	doc := writeTemp(t, dir, "snapshot.json", signableDoc)
	out := filepath.Join(dir, "signed.json")

	// No --api-url, no directory: a stub that answers nothing.
	s := newStub(t)
	r := s.run(t, runOpts{noConnection: true}, "sign", doc, "--key", private, "--signer", "alice", "--output", out)
	expectCode(t, r, ExitOK)
	if len(s.calls) != 0 {
		t.Fatalf("signing called the server %d times: %+v", len(s.calls), s.calls)
	}
	if !strings.Contains(r.stderr, "Signed") || !strings.Contains(r.stderr, "as alice") {
		t.Errorf("stderr: %s", r.stderr)
	}

	signed, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Format     string          `json:"format"`
		Version    int             `json:"version"`
		Payload    json.RawMessage `json:"payload"`
		Signatures []struct {
			Algorithm string `json:"algorithm"`
			KeyID     string `json:"keyId"`
			Signer    string `json:"signer"`
			Value     string `json:"value"`
		} `json:"signatures"`
	}
	if err := json.Unmarshal(signed, &envelope); err != nil {
		t.Fatalf("%v:\n%s", err, signed)
	}
	if envelope.Format != "alder-signed" || envelope.Version != 1 || len(envelope.Signatures) != 1 {
		t.Fatalf("envelope: %+v", envelope)
	}
	if envelope.Signatures[0].Algorithm != "ed25519" || envelope.Signatures[0].Signer != "alice" {
		t.Fatalf("signature: %+v", envelope.Signatures[0])
	}
	// The document inside is the document that went in.
	var inner, original map[string]any
	if err := json.Unmarshal(envelope.Payload, &inner); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(signableDoc), &original); err != nil {
		t.Fatal(err)
	}
	if inner["checksum"] != original["checksum"] || inner["kind"] != original["kind"] {
		t.Fatalf("the payload is not the document that was signed: %v", inner)
	}
	// And no part of the private key is in it.
	if strings.Contains(string(signed), "PRIVATE") {
		t.Fatal("the signed document carries key material")
	}

	// Verifying it with the matching public key.
	v := newStub(t).run(t, runOpts{noConnection: true}, "verify", out, "--trusted-keys", public)
	expectCode(t, v, ExitOK)
	if !strings.Contains(v.stderr, "verified") || !strings.Contains(v.stderr, "trusted") {
		t.Errorf("stderr: %s", v.stderr)
	}
}

func TestVerifyTellsTheThreeAnswersApart(t *testing.T) {
	dir := t.TempDir()
	private, public := generateKey(t, dir, "mine")
	_, otherPublic := generateKey(t, dir, "theirs")
	doc := writeTemp(t, dir, "snapshot.json", signableDoc)
	signedPath := filepath.Join(dir, "signed.json")
	expectCode(t, newStub(t).run(t, runOpts{noConnection: true}, "sign", doc, "--key", private, "--output", signedPath), ExitOK)

	t.Run("a key nobody named is untrusted", func(t *testing.T) {
		r := newStub(t).run(t, runOpts{noConnection: true}, "verify", signedPath, "--trusted-keys", otherPublic)
		expectCode(t, r, ExitNotApplicable)
		if !strings.Contains(r.stderr, "untrusted") {
			t.Errorf("stderr: %s", r.stderr)
		}
	})

	t.Run("a changed document is invalid", func(t *testing.T) {
		signed, err := os.ReadFile(signedPath)
		if err != nil {
			t.Fatal(err)
		}
		edited := strings.Replace(string(signed), "sha256:0000", "sha256:0001", 1)
		if edited == string(signed) {
			t.Fatal("nothing was edited")
		}
		tampered := writeTemp(t, dir, "tampered.json", edited)
		r := newStub(t).run(t, runOpts{noConnection: true}, "verify", tampered, "--trusted-keys", public)
		expectCode(t, r, ExitFailed)
		if !strings.Contains(r.stderr, "invalid") && !strings.Contains(r.stderr, "does not match") {
			t.Errorf("stderr: %s", r.stderr)
		}
	})

	t.Run("an unsigned document says so", func(t *testing.T) {
		r := newStub(t).run(t, runOpts{noConnection: true}, "verify", doc, "--trusted-keys", public)
		expectCode(t, r, ExitNotApplicable)
		if !strings.Contains(r.stderr, "no signature") {
			t.Errorf("stderr: %s", r.stderr)
		}
	})

	t.Run("and says what the document claims to be, without printing it", func(t *testing.T) {
		r := newStub(t).run(t, runOpts{noConnection: true}, "verify", signedPath, "--trusted-keys", public, "--json")
		expectCode(t, r, ExitOK)
		var out struct {
			Status  string         `json:"status"`
			Payload map[string]any `json:"payload"`
			Signers []struct {
				KeyID   string `json:"keyId"`
				Trusted bool   `json:"trusted"`
			} `json:"signers"`
		}
		if err := json.Unmarshal([]byte(r.stdout), &out); err != nil {
			t.Fatalf("%v: %s", err, r.stdout)
		}
		if out.Status != "verified" || len(out.Signers) != 1 || !out.Signers[0].Trusted {
			t.Fatalf("out: %+v", out)
		}
		if out.Payload["kind"] != "data" || out.Payload["format"] != "alder-snapshot" {
			t.Fatalf("payload summary: %v", out.Payload)
		}
		if strings.Contains(r.stdout, "dc=alder,dc=test") {
			t.Error("verify printed the document's contents")
		}
	})
}

func TestTwoPeopleCanSignOneDocument(t *testing.T) {
	dir := t.TempDir()
	first, firstPub := generateKey(t, dir, "first")
	second, secondPub := generateKey(t, dir, "second")
	doc := writeTemp(t, dir, "package.json", signableDoc)
	once := filepath.Join(dir, "once.json")
	twice := filepath.Join(dir, "twice.json")

	expectCode(t, newStub(t).run(t, runOpts{noConnection: true}, "sign", doc, "--key", first, "--signer", "alice", "--output", once), ExitOK)
	r := newStub(t).run(t, runOpts{noConnection: true}, "sign", once, "--key", second, "--signer", "bob", "--output", twice)
	expectCode(t, r, ExitOK)
	if !strings.Contains(r.stderr, "2 signature(s)") {
		t.Errorf("stderr: %s", r.stderr)
	}

	// Either key verifies it, and each says which signature it trusts.
	for _, key := range []string{firstPub, secondPub} {
		v := newStub(t).run(t, runOpts{noConnection: true}, "verify", twice, "--trusted-keys", key)
		expectCode(t, v, ExitOK)
		if strings.Count(v.stderr, "untrusted") != 1 || strings.Count(v.stderr, "  trusted") != 1 {
			t.Errorf("verify with %s:\n%s", key, v.stderr)
		}
	}

	// Signing twice with one key is refused: a second signature by the same
	// key says nothing the first did not.
	again := newStub(t).run(t, runOpts{noConnection: true}, "sign", twice, "--key", first, "--output", filepath.Join(dir, "thrice.json"))
	expectCode(t, again, ExitFailed)
}

func TestSigningRefusesWhatIsNotADocument(t *testing.T) {
	dir := t.TempDir()
	private, _ := generateKey(t, dir, "signing")
	ldif := writeTemp(t, dir, "changes.ldif", "dn: cn=x\nchangetype: add\n")
	expectCode(t, newStub(t).run(t, runOpts{noConnection: true}, "sign", ldif, "--key", private,
		"--output", filepath.Join(dir, "out.json")), ExitFailed)

	missing := filepath.Join(dir, "nowhere.pem")
	doc := writeTemp(t, dir, "snapshot.json", signableDoc)
	expectCode(t, newStub(t).run(t, runOpts{noConnection: true}, "sign", doc, "--key", missing,
		"--output", filepath.Join(dir, "out.json")), ExitFailed)
}
