package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The signing commands keep the client's rules: an inherited environment
// cannot replace a key, --json is one document, and a refusal leaves no
// half-written key pair behind.

func TestAnInheritedALDER_FORCEDoesNotReplaceAKey(t *testing.T) {
	dir := t.TempDir()
	private, _ := generateKey(t, dir, "signing")
	before, err := os.ReadFile(private)
	if err != nil {
		t.Fatal(err)
	}
	r := newStub(t).run(t, runOpts{noConnection: true, env: map[string]string{"ALDER_FORCE": "true"}},
		"key", "generate", "--output", private)
	expectCode(t, r, ExitUsage)
	after, err := os.ReadFile(private)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("ALDER_FORCE=true from the environment replaced an existing private key")
	}
}

func TestAKeyPairIsWrittenWholeOrNotAtAll(t *testing.T) {
	t.Run("an existing public key stops it before the private key is written", func(t *testing.T) {
		dir := t.TempDir()
		private := filepath.Join(dir, "k.pem")
		writeTemp(t, dir, "k.pub.pem", "someone's public key")
		r := newStub(t).run(t, runOpts{noConnection: true}, "key", "generate", "--output", private)
		expectCode(t, r, ExitUsage)
		if _, err := os.Stat(private); err == nil {
			t.Error("a private key was written although its public key could not be")
		}
	})
	t.Run("one path for both halves is refused, even with --force", func(t *testing.T) {
		dir := t.TempDir()
		private := filepath.Join(dir, "k.pem")
		r := newStub(t).run(t, runOpts{noConnection: true},
			"key", "generate", "--output", private, "--public-output", private, "--force")
		expectCode(t, r, ExitUsage)
		if _, err := os.Stat(private); err == nil {
			t.Error("a file was written")
		}
	})
}

func TestVerifyJSONIsOneDocumentWhateverTheAnswer(t *testing.T) {
	dir := t.TempDir()
	private, public := generateKey(t, dir, "mine")
	_, otherPublic := generateKey(t, dir, "theirs")
	doc := writeTemp(t, dir, "snapshot.json", signableDoc)
	signedPath := filepath.Join(dir, "signed.json")
	expectCode(t, newStub(t).run(t, runOpts{noConnection: true}, "sign", doc, "--key", private, "--output", signedPath), ExitOK)
	signed, err := os.ReadFile(signedPath)
	if err != nil {
		t.Fatal(err)
	}
	tampered := writeTemp(t, dir, "tampered.json", strings.Replace(string(signed), "sha256:0000", "sha256:0001", 1))

	for _, tc := range []struct {
		name, file, key, status string
		code                    int
	}{
		{"untrusted", signedPath, otherPublic, "untrusted", ExitNotApplicable},
		{"invalid", tampered, public, "invalid", ExitFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newStub(t).run(t, runOpts{noConnection: true}, "verify", tc.file, "--trusted-keys", tc.key, "--json")
			expectCode(t, r, tc.code)
			out := oneJSONDocument(t, r.stdout)
			if out["status"] != tc.status {
				t.Errorf("status = %v, want %s", out["status"], tc.status)
			}
		})
	}
}

func TestSignRefusesTwoDocumentsOnStandardOutput(t *testing.T) {
	dir := t.TempDir()
	private, _ := generateKey(t, dir, "mine")
	doc := writeTemp(t, dir, "snapshot.json", signableDoc)
	r := newStub(t).run(t, runOpts{noConnection: true}, "sign", doc, "--key", private, "--output", "-", "--json")
	expectCode(t, r, ExitUsage)
	if r.stdout != "" {
		t.Errorf("stdout should be empty:\n%s", r.stdout)
	}
}
