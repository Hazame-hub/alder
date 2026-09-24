package cli

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/hazame-hub/alder/internal/signing"
)

// Signing, on the machine the person is at.
//
// The private key never leaves here: no flag sends one to a server, no
// response carries one, and the client holds it only for the moment it takes
// to sign. What the server gets is the signed document, and what it does with
// it is verify -- against public keys whoever started it named.
//
// These three commands need no directory and no Alder server. Signing a
// snapshot on a laptop with no network is the ordinary case.

type keyOptions struct {
	out       string
	publicOut string
	force     bool
}

type signOptions struct {
	key    string
	signer string
	output string
	force  bool
	json   bool
}

type verifyOptions struct {
	trusted []string
	json    bool
}

func keyCmd(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "key",
		Short: "Make and inspect the keys documents are signed with",
		Long: "A signing key is yours. Alder makes one, writes the private half where you\n" +
			"say and the public half beside it, and never sees either again unless you\n" +
			"pass it back. The server is given public keys only.",
	}
	cmd.AddCommand(keyGenerateCmd(env), keyShowCmd(env))
	return cmd
}

func keyGenerateCmd(env *Env) *cobra.Command {
	var o keyOptions
	cmd := command(env, &cobra.Command{
		Use:   "generate --output FILE",
		Short: "Generate an Ed25519 signing key",
		Long: "Writes a new Ed25519 private key to --output and its public key to\n" +
			"--public-output, which defaults to the private key's path with .pub before\n" +
			"the extension. The private key is written readable by you only.\n\n" +
			"There is no passphrase: protecting the file is your system's job, and a\n" +
			"passphrase Alder invented would be one more secret in one more place.\n\n" +
			"Give the public key to whoever runs the Alder server, for --trusted-keys.",
		Args: argsBetween(0, 0, "no arguments"),
	}, func(_ context.Context, _ []string) error {
		return runKeyGenerate(env, o)
	})
	f := cmd.Flags()
	f.StringVar(&o.out, "output", "", "where to write the private key")
	f.StringVar(&o.publicOut, "public-output", "", "where to write the public key (default: the private key's path with .pub)")
	f.BoolVar(&o.force, "force", false, "overwrite existing files")
	_ = cmd.MarkFlagRequired("output")
	return cmd
}

func runKeyGenerate(env *Env, o keyOptions) error {
	if strings.TrimSpace(o.out) == "" || o.out == "-" {
		return usagef("--output must be a file: a private key is never written to standard output")
	}
	publicOut := strings.TrimSpace(o.publicOut)
	if publicOut == "" {
		publicOut = publicKeyPath(o.out)
	}
	pub, priv, err := signing.GenerateKey()
	if err != nil {
		return failf("key", "cannot generate a key: %v", err)
	}
	privPEM, err := signing.EncodePrivateKey(priv)
	if err != nil {
		return failf("key", "cannot encode the private key: %v", err)
	}
	pubPEM, err := signing.EncodePublicKey(pub)
	if err != nil {
		return failf("key", "cannot encode the public key: %v", err)
	}
	if err := writeFile(o.out, o.force, func(w io.Writer) error {
		_, werr := w.Write(privPEM)
		return werr
	}, func(f *os.File) error { return f.Chmod(0o600) }); err != nil {
		return err
	}
	if err := writeFile(publicOut, o.force, func(w io.Writer) error {
		_, werr := w.Write(pubPEM)
		return werr
	}, nil); err != nil {
		return err
	}
	writef(env.Stderr, "Wrote the private key to %s and the public key to %s.\nKey id: %s\n"+
		"Keep the private key to yourself. Give the public key to whoever starts the Alder\n"+
		"server, as --trusted-keys %s\n", o.out, publicOut, signing.KeyID(pub), publicOut)
	return nil
}

// publicKeyPath is where the public key goes when nobody said: signing.pem
// becomes signing.pub.pem.
func publicKeyPath(private string) string {
	if i := strings.LastIndex(private, "."); i > strings.LastIndexAny(private, `/\`) {
		return private[:i] + ".pub" + private[i:]
	}
	return private + ".pub"
}

func keyShowCmd(env *Env) *cobra.Command {
	cmd := command(env, &cobra.Command{
		Use:   "show KEY_FILE",
		Short: "Print the key id of a public key file",
		Long: "Reads a public key file and prints the key id a signature names, so you can\n" +
			"tell which key signed a document without running a server. A private key is\n" +
			"refused: this command is for the half you hand out.",
		Args: argsBetween(1, 1, "one public key file"),
	}, func(_ context.Context, args []string) error {
		trusted, err := signing.LoadTrustedKeys([]string{args[0]})
		if err != nil {
			return failf("key", "%v", err)
		}
		for _, id := range trusted.IDs() {
			writef(env.Stdout, "%s\n", id)
		}
		return nil
	})
	return cmd
}

func signCmd(env *Env) *cobra.Command {
	var o signOptions
	cmd := command(env, &cobra.Command{
		Use:   "sign DOCUMENT_FILE | - --key KEY_FILE --output FILE",
		Short: "Sign a snapshot, change package or recovery bundle",
		Long: "Wraps an Alder document in a signed envelope: the document exactly as it\n" +
			"is, and a signature over it made with your key. Nothing inside the document\n" +
			"changes, and its own checksum still means what it meant.\n\n" +
			"Signing needs no server and no directory. The key is read, used and\n" +
			"forgotten; it is never sent anywhere.\n\n" +
			"Signing a document that is already signed adds your signature beside the\n" +
			"one that is there, so two people can sign one document.",
		Args: argsBetween(1, 1, "one document file, or - for standard input"),
	}, func(_ context.Context, args []string) error {
		return runSign(env, o, args)
	})
	f := cmd.Flags()
	f.StringVar(&o.key, "key", "", "the private key to sign with, in PEM")
	f.StringVar(&o.signer, "signer", "", "a label recorded beside the signature, such as a name or a pipeline")
	f.StringVar(&o.output, "output", "", "where to write the signed document, or - for standard output")
	f.BoolVar(&o.force, "force", false, "overwrite an existing file")
	f.BoolVar(&o.json, "json", false, "report what was signed as JSON on standard output")
	_ = cmd.MarkFlagRequired("key")
	_ = cmd.MarkFlagRequired("output")
	return cmd
}

func runSign(env *Env, o signOptions, args []string) (err error) {
	defer func() { err = asJSON(env, o.json, err) }()
	data, name, err := env.readInput(args[0], "the document", maxRequestBytes)
	if err != nil {
		return err
	}
	key, err := signing.LoadPrivateKey(o.key)
	if err != nil {
		return failf("key", "%v", err)
	}

	envelope, err := signedEnvelope(bytes.TrimSpace(data), key, o.signer)
	if err != nil {
		return failf("sign", "%s cannot be signed: %v", name, err)
	}
	if err := writeOutput(env, o.output, o.force, func(w io.Writer) error {
		return signing.Encode(w, envelope)
	}); err != nil {
		return err
	}
	last := envelope.Signatures[len(envelope.Signatures)-1]
	writef(env.Stderr, "Signed %s with key %s", name, last.KeyID)
	if last.Signer != "" {
		writef(env.Stderr, " as %s", safe(last.Signer))
	}
	writef(env.Stderr, ".\n%d signature(s) on the document.\n", len(envelope.Signatures))
	if !o.json {
		return nil
	}
	return writeJSON(env, signEnvelope{Signed: true, KeyID: last.KeyID, Signer: last.Signer,
		SignedAt: last.SignedAt, Signatures: len(envelope.Signatures)})
}

// signedEnvelope signs a document, or adds a signature to one that is already
// an envelope.
func signedEnvelope(document []byte, key ed25519.PrivateKey, signer string) (*signing.Envelope, error) {
	if signing.IsEnvelope(document) {
		existing, err := signing.Decode(document)
		if err != nil {
			return nil, err
		}
		if err := signing.AddSignature(existing, key, signer, time.Now()); err != nil {
			return nil, err
		}
		return existing, nil
	}
	return signing.Sign(document, key, signer, time.Now())
}

type signEnvelope struct {
	Signed     bool   `json:"signed"`
	KeyID      string `json:"keyId"`
	Signer     string `json:"signer,omitempty"`
	SignedAt   string `json:"signedAt,omitempty"`
	Signatures int    `json:"signatures"`
}

func verifyCmd(env *Env) *cobra.Command {
	var o verifyOptions
	cmd := command(env, &cobra.Command{
		Use:   "verify DOCUMENT_FILE | - --trusted-keys FILE",
		Short: "Check a document's signatures against keys you trust",
		Long: "Reads a signed document and says what its signatures amount to against the\n" +
			"public keys you name: verified, untrusted, invalid, or unsigned.\n\n" +
			"Verifying needs no server and no directory, so a document can be checked\n" +
			"wherever it arrives. --trusted-keys takes a PEM file, a file of several\n" +
			"keys, or a directory of .pem files, and may be repeated.\n\n" +
			"Exit status: 0 verified, 3 untrusted or unsigned, 8 invalid or unreadable.",
		Args: argsBetween(1, 1, "one document file, or - for standard input"),
	}, func(_ context.Context, args []string) error {
		return runVerify(env, o, args)
	})
	f := cmd.Flags()
	f.StringArrayVar(&o.trusted, "trusted-keys", nil, "a public key file or a directory of them; repeatable")
	f.BoolVar(&o.json, "json", false, "report the result as JSON on standard output")
	return cmd
}

func runVerify(env *Env, o verifyOptions, args []string) (err error) {
	defer func() { err = asJSON(env, o.json, err) }()
	data, name, err := env.readInput(args[0], "the document", maxRequestBytes)
	if err != nil {
		return err
	}
	trusted, err := signing.LoadTrustedKeys(o.trusted)
	if err != nil {
		return failf("key", "%v", err)
	}

	document := bytes.TrimSpace(data)
	if !signing.IsEnvelope(document) {
		writef(env.Stderr, "%s carries no signature.\n", name)
		return notApplicable("unsigned", "%s carries no signature", name)
	}
	envelope, derr := signing.Decode(document)
	if derr != nil {
		return failf("signature_invalid", "%s: %v", name, derr)
	}
	result := signing.Verify(envelope, trusted)

	writef(env.Stderr, "%s: %s\n", name, result.Status)
	for _, s := range result.Signers {
		trust := "untrusted"
		if s.Trusted {
			trust = "trusted"
		}
		writef(env.Stderr, "  %s  %s  %s %s\n", s.KeyID, trust, safe(s.Signer), safe(s.SignedAt))
	}
	if result.Detail != "" {
		writef(env.Stderr, "  %s\n", safe(result.Detail))
	}
	if o.json {
		if err := writeJSON(env, verifyEnvelope{Status: string(result.Status), Signers: result.Signers,
			Detail: result.Detail, Payload: payloadSummary(envelope.Payload)}); err != nil {
			return err
		}
	}
	switch result.Status {
	case signing.Verified:
		return nil
	case signing.Invalid:
		return failf("signature_invalid", "%s: %s", name, result.Detail)
	default:
		return notApplicable("signature_untrusted", "%s is signed by a key you did not name", name)
	}
}

type verifyEnvelope struct {
	Status  string           `json:"status"`
	Signers []signing.Signer `json:"signers,omitempty"`
	Detail  string           `json:"detail,omitempty"`
	Payload map[string]any   `json:"payload,omitempty"`
}

// payloadSummary is what the signed document says it is, for the report: its
// format, version and kind and nothing else. A verify never prints a
// document's contents.
func payloadSummary(payload []byte) map[string]any {
	var head struct {
		Format  string `json:"format"`
		Version int    `json:"version"`
		Kind    string `json:"kind"`
	}
	if err := json.Unmarshal(payload, &head); err != nil {
		return nil
	}
	out := map[string]any{"format": head.Format, "version": head.Version}
	if head.Kind != "" {
		out["kind"] = head.Kind
	}
	return out
}

// writeOutput writes to a file or to standard output, as --output says.
func writeOutput(env *Env, path string, force bool, write func(io.Writer) error) error {
	if path == "-" {
		return write(env.Stdout)
	}
	return writeFile(path, force, write, nil)
}

// writeJSON prints a command's own answer as one JSON document.
func writeJSON(env *Env, value any) error {
	doc, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return failf("output", "cannot encode the answer: %v", err)
	}
	return writeDocument(env.Stdout, doc)
}
