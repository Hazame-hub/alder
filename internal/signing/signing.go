// Package signing signs and verifies the documents Alder hands people: a
// snapshot, a change package, a recovery bundle.
//
// A checksum says a document was not corrupted. It says nothing about who
// made it, because anyone who edits a file can recompute one. A signature is
// the other question, and it is a different question: this document came from
// the holder of this key, and has not changed since.
//
// Three decisions shape everything here.
//
// The key belongs to the person, not to the server. Signing happens where the
// operator is -- `alder sign --key` -- with a private key Alder never sees,
// never asks for and cannot store. The server only ever verifies, against
// public keys an operator names when starting it. Alder stays stateless and
// holds no secret on disk.
//
// A signature wraps a document rather than entering it. A signed file is an
// envelope: `alder-signed` version 1, carrying the payload document byte for
// byte and the signatures beside it. Nothing in the snapshot, package or
// bundle formats changes, their checksums keep meaning exactly what they
// meant, and a release too old to know about signing refuses an envelope as a
// document it does not recognise instead of half-reading one.
//
// What is signed is the payload in its compact form, under a context line that
// names the format and version. Compact, because writing an envelope indents
// what is inside it and reading one must not depend on how a tool laid the
// file out; the payload's own checksum still covers its content. The context
// line means a signature over one kind of document can never be replayed as a
// signature over another.
package signing

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// Format and Version identify a signed envelope.
const (
	Format  = "alder-signed"
	Version = 1
)

// Algorithm is the one signature algorithm Alder makes or reads.
//
// Ed25519, from the standard library: small keys, no parameter choices to get
// wrong, no dependency to add. A second algorithm would be a second thing to
// get right, and nothing needs one yet.
const Algorithm = "ed25519"

// context is prefixed to the payload before signing, so a signature is bound
// to the format and version it was made for.
const context = "alder-signed/1\n"

// Bounds. An envelope arrives from anywhere.
const (
	// MaxPayload is the largest payload an envelope may carry, which is the
	// server's own request limit: an envelope is not a way to send more.
	MaxPayload = 16 << 20
	// MaxSignatures is how many signatures one envelope may carry. Several is
	// ordinary -- two people, or a person and a pipeline -- a thousand is an
	// attempt to make verification expensive.
	MaxSignatures = 8
	maxFieldLen   = 4096
)

// Signature is one signature over an envelope's payload.
type Signature struct {
	// Algorithm is always ed25519 in version 1.
	Algorithm string `json:"algorithm"`
	// KeyID identifies the public key, as sha256 of its DER encoding, hex, in
	// full. It is not a secret and it is not a name: it says which key, not
	// whose.
	KeyID string `json:"keyId"`
	// Signer is what the person who signed called themselves. It is a label
	// and nothing is decided by it.
	Signer string `json:"signer,omitempty"`
	// SignedAt is when the signature was made, in RFC 3339. It is part of the
	// signature's own record and not covered by it.
	SignedAt string `json:"signedAt,omitempty"`
	// Value is the signature, base64.
	Value string `json:"value"`
}

// Envelope is a signed document.
type Envelope struct {
	Format  string `json:"format"`
	Version int    `json:"version"`
	// Payload is the document that was signed, exactly as it was signed. It is
	// kept as raw JSON so that signing and verifying see the same bytes.
	Payload    json.RawMessage `json:"payload"`
	Signatures []Signature     `json:"signatures"`
}

// Errors an envelope's reader returns. Each is a different answer, and a
// caller reports them differently.
var (
	// ErrNotEnvelope: the document is not a signed envelope. Callers treat
	// this as "an ordinary, unsigned document".
	ErrNotEnvelope = errors.New("signing: the document is not a signed envelope")
	// ErrInvalid: it claims to be an envelope and is not a usable one.
	ErrInvalid = errors.New("signing: the signed envelope is not valid")
	// ErrUnsupportedVersion: a version this build does not read.
	ErrUnsupportedVersion = errors.New("signing: the signed envelope is of a version this Alder does not read")
)

// Status is what verification concluded about a document.
type Status string

const (
	// Unsigned: the document carried no signature. Not a failure; most
	// documents are unsigned.
	Unsigned Status = "unsigned"
	// Verified: at least one signature is valid and was made by a key the
	// verifier trusts.
	Verified Status = "verified"
	// Untrusted: every signature is valid and no key is one the verifier was
	// told to trust. The document is intact; who signed it is not established.
	Untrusted Status = "untrusted"
	// Invalid: a signature does not match the payload, or names an algorithm
	// this build does not know. Something changed after signing, or the file
	// was assembled by hand.
	Invalid Status = "invalid"
)

// Result is what a verifier concluded, in the form a report shows.
type Result struct {
	Status Status `json:"status"`
	// Signers are the signatures that verified, in the order the envelope
	// holds them.
	Signers []Signer `json:"signers,omitempty"`
	// Detail explains an invalid or untrusted answer, for a person.
	Detail string `json:"detail,omitempty"`
}

// Signer is one signature that verified, and whether its key is trusted.
type Signer struct {
	KeyID    string `json:"keyId"`
	Signer   string `json:"signer,omitempty"`
	SignedAt string `json:"signedAt,omitempty"`
	Trusted  bool   `json:"trusted"`
}

// KeyID is the identity of a public key: sha256 over its DER encoding, hex.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:])
}

// Sign wraps a payload in an envelope signed with one key.
//
// The payload is embedded exactly as given, so what a verifier checks is what
// the caller signed: this is the only place the bytes are decided.
func Sign(payload []byte, key ed25519.PrivateKey, signer string, now time.Time) (*Envelope, error) {
	if len(payload) == 0 {
		return nil, fmt.Errorf("%w: there is nothing to sign", ErrInvalid)
	}
	if len(payload) > MaxPayload {
		return nil, fmt.Errorf("%w: the document is larger than Alder signs", ErrInvalid)
	}
	if !isJSONObject(payload) {
		return nil, fmt.Errorf("%w: the document is not a JSON object", ErrInvalid)
	}
	if len(key) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%w: the key is not an Ed25519 private key", ErrInvalid)
	}
	pub, _ := key.Public().(ed25519.PublicKey)
	sig := ed25519.Sign(key, signed(payload))
	return &Envelope{Format: Format, Version: Version, Payload: append(json.RawMessage(nil), payload...),
		Signatures: []Signature{{Algorithm: Algorithm, KeyID: KeyID(pub), Signer: strings.TrimSpace(signer),
			SignedAt: now.UTC().Format(time.RFC3339), Value: base64.StdEncoding.EncodeToString(sig)}}}, nil
}

// AddSignature signs a payload that is already in an envelope, so two people
// can sign one document.
func AddSignature(e *Envelope, key ed25519.PrivateKey, signer string, now time.Time) error {
	if len(e.Signatures) >= MaxSignatures {
		return fmt.Errorf("%w: an envelope holds at most %d signatures", ErrInvalid, MaxSignatures)
	}
	next, err := Sign(e.Payload, key, signer, now)
	if err != nil {
		return err
	}
	for _, existing := range e.Signatures {
		if existing.KeyID == next.Signatures[0].KeyID {
			return fmt.Errorf("%w: that key has already signed this document", ErrInvalid)
		}
	}
	e.Signatures = append(e.Signatures, next.Signatures[0])
	return nil
}

// signed is the byte string a signature covers: the context line, then the
// payload with insignificant whitespace removed.
//
// Whitespace is the one thing that changes between writing a document and
// writing it again -- an envelope indents its payload, a pipeline may reformat
// it -- and nothing about a document's meaning lives there. Everything else,
// including the order of keys and every escape, is signed as it stands.
func signed(payload []byte) []byte {
	var compact bytes.Buffer
	compact.WriteString(context)
	if err := json.Compact(&compact, payload); err != nil {
		// Only reachable for a payload that is not JSON, which neither Sign
		// nor Decode admits.
		compact.Write(payload)
	}
	return compact.Bytes()
}

// Encode writes an envelope as its document.
func Encode(w io.Writer, e *Envelope) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(e)
}

// IsEnvelope reports whether a document claims to be a signed envelope,
// without decoding it. A caller uses it to tell "unsigned document" from
// "envelope that will not decode", which are different answers.
func IsEnvelope(data []byte) bool {
	var probe struct {
		Format string `json:"format"`
	}
	if err := json.NewDecoder(bytes.NewReader(data)).Decode(&probe); err != nil {
		return false
	}
	return probe.Format == Format
}

// Decode reads an envelope, strictly.
//
// Unknown fields, a version from the future, no payload, no signature, a
// signature that is not base64 or not the right length: each is refused here
// rather than carried into verification, where a partial answer would be worse
// than none.
func Decode(data []byte) (*Envelope, error) {
	if !IsEnvelope(data) {
		return nil, ErrNotEnvelope
	}
	if len(data) > MaxPayload+(1<<16) {
		return nil, fmt.Errorf("%w: the envelope is larger than Alder reads", ErrInvalid)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var e Envelope
	if err := dec.Decode(&e); err != nil {
		return nil, errors.Join(ErrInvalid, err)
	}
	if dec.More() {
		return nil, fmt.Errorf("%w: the document continues after the envelope", ErrInvalid)
	}
	if e.Version > Version {
		return nil, fmt.Errorf("%w: version %d", ErrUnsupportedVersion, e.Version)
	}
	if e.Version < 1 {
		return nil, fmt.Errorf("%w: version %d does not exist", ErrInvalid, e.Version)
	}
	if !isJSONObject(e.Payload) {
		return nil, fmt.Errorf("%w: the payload is missing, or is not a JSON object", ErrInvalid)
	}
	if len(e.Payload) > MaxPayload {
		return nil, fmt.Errorf("%w: the payload is larger than Alder reads", ErrInvalid)
	}
	if len(e.Signatures) == 0 {
		return nil, fmt.Errorf("%w: it carries no signature", ErrInvalid)
	}
	if len(e.Signatures) > MaxSignatures {
		return nil, fmt.Errorf("%w: it carries more than %d signatures", ErrInvalid, MaxSignatures)
	}
	for i, s := range e.Signatures {
		if len(s.KeyID) > maxFieldLen || len(s.Signer) > maxFieldLen || len(s.SignedAt) > maxFieldLen ||
			len(s.Value) > maxFieldLen {
			return nil, fmt.Errorf("%w: signature %d carries a field longer than Alder reads", ErrInvalid, i+1)
		}
		if strings.TrimSpace(s.Value) == "" || strings.TrimSpace(s.KeyID) == "" {
			return nil, fmt.Errorf("%w: signature %d has no key or no value", ErrInvalid, i+1)
		}
	}
	return &e, nil
}

// Verify checks an envelope's signatures against the keys a verifier trusts.
//
// Trust is the operator's to declare: a valid signature by a key nobody named
// is reported as untrusted rather than as a failure, because the document is
// intact and only its provenance is unestablished. A signature that does not
// match is invalid whatever the trust list says, and a caller refuses such a
// document.
func Verify(e *Envelope, trusted map[string]ed25519.PublicKey) Result {
	payload := signed(e.Payload)
	result := Result{Status: Untrusted}
	anyTrusted := false
	for i, s := range e.Signatures {
		if s.Algorithm != Algorithm {
			return Result{Status: Invalid,
				Detail: fmt.Sprintf("signature %d names algorithm %q, and this Alder reads %s only", i+1, bound(s.Algorithm), Algorithm)}
		}
		raw, err := base64.StdEncoding.DecodeString(s.Value)
		if err != nil || len(raw) != ed25519.SignatureSize {
			return Result{Status: Invalid, Detail: fmt.Sprintf("signature %d is not an Ed25519 signature", i+1)}
		}
		// The key the signature names has to be one the verifier holds, or
		// there is nothing to check it against. That is untrusted, not
		// invalid: the signature may be perfectly good.
		pub, known := trusted[strings.ToLower(s.KeyID)]
		if !known {
			result.Signers = append(result.Signers, Signer{KeyID: s.KeyID, Signer: s.Signer, SignedAt: s.SignedAt})
			continue
		}
		if !ed25519.Verify(pub, payload, raw) {
			return Result{Status: Invalid,
				Detail: fmt.Sprintf("signature %d does not match the document: it changed after it was signed, or the key it names is not the key that signed it", i+1)}
		}
		anyTrusted = true
		result.Signers = append(result.Signers, Signer{KeyID: s.KeyID, Signer: s.Signer, SignedAt: s.SignedAt, Trusted: true})
	}
	if anyTrusted {
		result.Status = Verified
		return result
	}
	result.Detail = "the document is signed by a key this Alder was not told to trust"
	return result
}

// Open reads a document that may or may not be an envelope, and returns the
// payload to work with and what verification concluded.
//
// An unsigned document passes through untouched with status unsigned, which is
// what keeps every existing file working. An envelope whose signature does not
// match is an error: nothing downstream should see a payload that was tampered
// with.
func Open(data []byte, trusted map[string]ed25519.PublicKey) ([]byte, Result, error) {
	if !IsEnvelope(data) {
		return data, Result{Status: Unsigned}, nil
	}
	e, err := Decode(data)
	if err != nil {
		return nil, Result{}, err
	}
	result := Verify(e, trusted)
	if result.Status == Invalid {
		return nil, result, fmt.Errorf("%w: %s", ErrInvalid, result.Detail)
	}
	return e.Payload, result, nil
}

// TrustedKeys is a set of public keys by key id, as a verifier holds them.
type TrustedKeys map[string]ed25519.PublicKey

// Add records a key to trust.
func (t TrustedKeys) Add(pub ed25519.PublicKey) string {
	id := KeyID(pub)
	t[strings.ToLower(id)] = pub
	return id
}

// IDs are the key ids in the set, sorted, for a report or a log line.
func (t TrustedKeys) IDs() []string {
	out := make([]string, 0, len(t))
	for id := range t {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func bound(s string) string {
	if len(s) > 64 {
		return s[:64] + "..."
	}
	return s
}

// isJSONObject reports a payload that is a JSON object: every document Alder
// signs is one, and a bare string or array in that place is a document nobody
// wrote.
func isJSONObject(payload []byte) bool {
	trimmed := bytes.TrimSpace(payload)
	return len(trimmed) > 0 && trimmed[0] == '{' && json.Valid(trimmed)
}
