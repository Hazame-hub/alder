package api

import (
	"bytes"
	"crypto/ed25519"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hazame-hub/alder/internal/signing"
)

// 1.15: the server verifies, and never signs.
//
// What these establish: a signed document is read and its signature reported,
// a changed one is refused before anything reads the payload, a key nobody
// named is untrusted rather than rejected, an unsigned document works exactly
// as it always did, and --require-signature turns the last two into refusals.

func signDocument(t *testing.T, document string, key ed25519.PrivateKey, signer string) string {
	t.Helper()
	e, err := signing.Sign([]byte(document), key, signer, time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	var buf bytes.Buffer
	if err := signing.Encode(&buf, e); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func signingKeys(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := signing.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

// trustingRig is a server told to trust one key, with a directory behind it.
func trustingRig(t *testing.T, pub ed25519.PublicKey, require bool) *testRig {
	t.Helper()
	trusted := signing.TrustedKeys{}
	if pub != nil {
		trusted.Add(pub)
	}
	return newRig(t, Config{TrustedKeys: trusted, RequireSignature: require}, &fakeSession{caps: defaultCaps()})
}

func TestASignedSnapshotIsReadAndItsSignatureReported(t *testing.T) {
	pub, priv := signingKeys(t)
	rig := trustingRig(t, pub, false)
	document := dataDocument(t, testSchema(t), "dc=alder,dc=test")

	res := post(t, rig, "/api/v1/snapshots/inspect", signDocument(t, document, priv, "alice"))
	if res.Status != http.StatusOK {
		t.Fatalf("inspect: %d %s", res.Status, res.Body)
	}
	in := decode[SnapshotInspection](t, res)
	if in.Signature == nil {
		t.Fatal("the inspection says nothing about the signature")
	}
	if in.Signature.Status != DocumentVerified {
		t.Fatalf("status %s", in.Signature.Status)
	}
	signers := *in.Signature.Signers
	if len(signers) != 1 || !signers[0].Trusted || signers[0].KeyId != signing.KeyID(pub) {
		t.Fatalf("signers: %+v", signers)
	}
	if signers[0].Signer == nil || *signers[0].Signer != "alice" {
		t.Fatalf("signer: %+v", signers[0])
	}
	// And the document inside was read as itself.
	if in.EntryCount != 0 || in.Integrity != SnapshotIntegrityVerified {
		t.Fatalf("inspection: %+v", in)
	}
}

func TestAnUnsignedDocumentIsReadExactlyAsBefore(t *testing.T) {
	pub, _ := signingKeys(t)
	rig := trustingRig(t, pub, false)
	res := post(t, rig, "/api/v1/snapshots/inspect", dataDocument(t, testSchema(t), "dc=alder,dc=test"))
	if res.Status != http.StatusOK {
		t.Fatalf("inspect: %d %s", res.Status, res.Body)
	}
	in := decode[SnapshotInspection](t, res)
	if in.Signature != nil && in.Signature.Status != DocumentUnsigned {
		t.Fatalf("an unsigned document reported %+v", in.Signature)
	}
}

func TestAChangedSignedDocumentIsRefusedBeforeItIsRead(t *testing.T) {
	pub, priv := signingKeys(t)
	rig := trustingRig(t, pub, false)
	signed := signDocument(t, dataDocument(t, testSchema(t), "dc=alder,dc=test"), priv, "alice")
	tampered := strings.Replace(signed, "dc=alder,dc=test", "dc=evil,dc=test", 1)
	if tampered == signed {
		t.Fatal("nothing was changed")
	}

	for _, path := range []string{"/api/v1/snapshots/inspect", "/api/v1/preflight"} {
		body := tampered
		if path == "/api/v1/preflight" {
			body = `{"artifact":` + tampered + `}`
		}
		res := post(t, rig, path, body)
		if res.Status != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", path, res.Status, res.Body)
		}
		if got := errorCode(t, res); got != ErrorErrorSignatureInvalid {
			t.Fatalf("%s: code %s", path, got)
		}
		if strings.Contains(res.Body, "dc=evil") {
			t.Fatalf("%s: the refusal echoes the changed document", path)
		}
	}
}

func TestAKeyTheServerWasNotToldAboutIsUntrusted(t *testing.T) {
	_, priv := signingKeys(t)
	other, _ := signingKeys(t)
	rig := trustingRig(t, other, false)

	res := post(t, rig, "/api/v1/snapshots/inspect", signDocument(t, dataDocument(t, testSchema(t), "dc=alder,dc=test"), priv, "stranger"))
	if res.Status != http.StatusOK {
		t.Fatalf("inspect: %d %s", res.Status, res.Body)
	}
	in := decode[SnapshotInspection](t, res)
	if in.Signature == nil || in.Signature.Status != DocumentUntrusted {
		t.Fatalf("signature: %+v", in.Signature)
	}
	if in.Signature.Detail == nil || *in.Signature.Detail == "" {
		t.Fatal("an untrusted document does not say why")
	}
	signers := *in.Signature.Signers
	if len(signers) != 1 || signers[0].Trusted {
		t.Fatalf("signers: %+v", signers)
	}
}

func TestRequireSignatureRefusesWhatIsNotSignedByATrustedKey(t *testing.T) {
	pub, priv := signingKeys(t)
	_, stranger := signingKeys(t)
	rig := trustingRig(t, pub, true)
	document := dataDocument(t, testSchema(t), "dc=alder,dc=test")

	t.Run("unsigned", func(t *testing.T) {
		res := post(t, rig, "/api/v1/snapshots/inspect", document)
		if res.Status != http.StatusBadRequest || errorCode(t, res) != ErrorErrorSignatureRequired {
			t.Fatalf("%d %s", res.Status, res.Body)
		}
	})
	t.Run("signed by a stranger", func(t *testing.T) {
		res := post(t, rig, "/api/v1/snapshots/inspect", signDocument(t, document, stranger, "stranger"))
		if res.Status != http.StatusBadRequest || errorCode(t, res) != ErrorErrorSignatureRequired {
			t.Fatalf("%d %s", res.Status, res.Body)
		}
		if !strings.Contains(res.Body, "trusted") {
			t.Errorf("the refusal does not say what is wrong: %s", res.Body)
		}
	})
	t.Run("signed by a trusted key", func(t *testing.T) {
		res := post(t, rig, "/api/v1/snapshots/inspect", signDocument(t, document, priv, "alice"))
		if res.Status != http.StatusOK {
			t.Fatalf("%d %s", res.Status, res.Body)
		}
	})
}

func TestBothSidesOfAComparisonMayBeSigned(t *testing.T) {
	pub, priv := signingKeys(t)
	other, otherPriv := signingKeys(t)
	trusted := signing.TrustedKeys{}
	trusted.Add(pub)
	_ = other
	rig := newRig(t, Config{TrustedKeys: trusted}, &fakeSession{caps: defaultCaps()})

	document := dataDocument(t, testSchema(t), "dc=alder,dc=test")
	mine := signDocument(t, document, priv, "alice")
	theirs := signDocument(t, document, otherPriv, "stranger")

	res := post(t, rig, "/api/v1/diff", `{"source":{"snapshot":`+mine+`},"target":{"snapshot":`+theirs+`}}`)
	if res.Status != http.StatusOK {
		t.Fatalf("diff: %d %s", res.Status, res.Body)
	}
	d := decode[Diff](t, res)
	if d.SourceSignature == nil || d.SourceSignature.Status != DocumentVerified {
		t.Fatalf("source: %+v", d.SourceSignature)
	}
	if d.TargetSignature == nil || d.TargetSignature.Status != DocumentUntrusted {
		t.Fatalf("target: %+v", d.TargetSignature)
	}
	if d.Counts.Added != 0 || d.Counts.Removed != 0 {
		t.Fatalf("two copies of one document differ: %+v", d.Counts)
	}
}

func TestTheServerHoldsPublicKeysOnly(t *testing.T) {
	// The type is the guarantee: somewhere to put public keys, nowhere to put
	// a private one. A field for one would have to be added here first, which
	// is a change a reviewer sees.
	cfg := Config{TrustedKeys: signing.TrustedKeys{}}
	pub, priv := signingKeys(t)
	cfg.TrustedKeys.Add(pub)
	if len(cfg.TrustedKeys) != 1 {
		t.Fatal("a public key could not be trusted")
	}
	for _, held := range cfg.TrustedKeys {
		if len(held) != ed25519.PublicKeySize {
			t.Fatalf("a trusted key is %d bytes, and a public key is %d", len(held), ed25519.PublicKeySize)
		}
		if bytes.Contains(priv, held) && len(priv) == len(held) {
			t.Fatal("a private key was stored as a trusted key")
		}
	}
}
