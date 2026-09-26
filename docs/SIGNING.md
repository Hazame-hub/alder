# Signing and verifying documents

A checksum answers one question: was this file corrupted? It cannot answer the
other one, because anyone who edits a file can recompute a checksum. Signing
answers it:

> This document came from the holder of this key, and has not changed since.

Alder 1.15 signs a snapshot, a change package or a recovery bundle with a key
**you** hold, and verifies one against public keys **an operator** named. Two
sentences describe the whole design:

- **The private key never reaches Alder's server.** `alder sign` runs where you
  are, reads the key, uses it and forgets it. There is no flag that gives the
  server a private key, and no code path that would use one. Alder stays
  stateless and holds no secret on disk.
- **A signature wraps a document; it never enters one.** Nothing in the
  snapshot, package or bundle formats changes, and their checksums keep meaning
  exactly what they meant.

---

## Making a key

```console
$ alder key generate --output ~/.alder/signing.pem
Wrote the private key to ~/.alder/signing.pem and the public key to ~/.alder/signing.pub.pem.
Key id: 3b1b29eea0cd613896f75541a5081c0f187ffbef58dde6f497dd02520dec6d36
Keep the private key to yourself. Give the public key to whoever starts the Alder
server, as --trusted-keys ~/.alder/signing.pub.pem
```

Ed25519, from Go's standard library: small keys, no parameter choices to get
wrong, and no dependency added. The private key is PKCS#8 PEM, written readable
by you only; the public key is SPKI PEM with its key id in the header, so it can
be read without running anything. `alder key show FILE` prints the key id of a
public key and refuses a private one.

There is no passphrase. Protecting the file is your system's job, and a
passphrase Alder invented would be one more secret in one more place.

A **key id** is the SHA-256 of the public key, in hex. It says *which* key, not
whose: names are labels, and a signature carries one only because a person
reading a report wants to see it.

## Signing

```console
$ alder sign config.json --key ~/.alder/signing.pem --signer "alice" --output config.signed.json
Signed config.json with key 3b1b29ee… as alice.
1 signature(s) on the document.
```

The result is an envelope:

```json
{
  "format": "alder-signed",
  "version": 1,
  "payload": { "format": "alder-snapshot", "version": 1, "kind": "config", "...": "..." },
  "signatures": [
    {
      "algorithm": "ed25519",
      "keyId": "3b1b29ee…",
      "signer": "alice",
      "signedAt": "2026-09-23T18:18:12Z",
      "value": "…"
    }
  ]
}
```

- Signing needs **no server and no directory**. A snapshot can be signed on a
  laptop with no network.
- Signing a document that is already signed **adds** a signature, so two people
  can sign one document. One key signs one document once; a second attempt with
  the same key is refused, because it says nothing the first did not.
- What is signed is the payload's **compact form**, under the context line
  `alder-signed/1`. Compact, because writing an envelope indents what is inside
  it and a signature must survive a file being reformatted. The context line
  means a signature over one kind of document can never be replayed as a
  signature over another.
- Only a JSON object is signed: `alder sign` refuses LDIF, a bare string, or
  anything else that is not an Alder document.

## Verifying, wherever the document arrives

```console
$ alder verify config.signed.json --trusted-keys ~/keys/alice.pub.pem
config.signed.json: verified
  3b1b29ee…  trusted  alice 2026-09-23T18:18:12Z
```

`--trusted-keys` takes a PEM file, a file holding several keys, or a directory
of `.pem` files, and may be repeated. Verifying needs no server either.

| Answer | Exit | Meaning |
|---|---|---|
| `verified` | 0 | A signature is valid and by a key you named |
| `untrusted` | 3 | Every signature is valid and none is by a key you named. The document is intact; who signed it is not established |
| `unsigned` | 3 | The document carries no signature |
| `invalid` | 8 | A signature does not match the document, or names an algorithm this Alder does not know |

`--json` prints the result and what the document claims to be — its format,
version and kind — and never the document's contents.

## What the server does with a signature

The server **verifies and never signs**:

```console
$ alder serve --trusted-keys /etc/alder/keys/    # a file, or a directory of .pem files
$ alder serve --trusted-keys team.pem --require-signature
```

Every endpoint that reads a document unwraps it first: `POST /snapshots/inspect`,
`POST /diff` on either side, `POST /preflight`, `POST /packages/inspect`,
`POST /packages/validate` and `POST /recovery/inspect`.

- An **unsigned** document is read exactly as it always was. This is what keeps
  every file written by an earlier Alder working, and why signing is something a
  deployment adopts rather than something Alder imposes.
- A **valid signature by a trusted key** is read, and the answer says so:
  `signature: {status: verified, signers: [...]}` on the inspection, the
  comparison (for each side that was a file), the package inspection and the
  preflight report's source.
- A **valid signature by a key nobody named** is read too, reported as
  `untrusted`. Trust is the operator's to declare, and the document is intact.
- A signature that **does not match** refuses the request, `400
  signature_invalid`, before anything downstream sees the payload. That is not a
  matter of policy.
- `--require-signature` turns the first and third of those into `400
  signature_required`. It refuses to start without `--trusted-keys`, since every
  document would be refused.

In the interface, a snapshot loaded into the Snapshots & drift tab and an artifact in
the Preflight tab carry a badge for what their signature amounted to. An
unsigned document shows nothing: most documents are unsigned, and a badge on
every one of them would say only that the world is normal.

## What a signature is not

- **Not authorisation.** A verified document is one Alder knows the origin of.
  It still goes through the plan, the review and the apply, with no step
  skipped, and no directory permission follows from it.
- **Not encryption.** The payload is the document, in the clear. A snapshot
  holds no passwords, and a signed one holds no more.
- **Not a certificate.** There is no chain, no expiry, no revocation list and no
  authority. Trust is a list of public keys an operator named. To stop trusting
  a key, remove it and restart; to add one, add the file.
- **Not a timestamp.** `signedAt` is what the signer's clock said. It is inside
  the signed record but nothing verifies it.
- **Not a second checksum.** The document's own checksum still detects
  corruption, and still means what it meant.

## Compatibility

- A release before 1.15 refuses an envelope as a document it does not
  recognise, rather than half-reading one. Keep the unsigned document if an
  older Alder has to read it; signing does not change the document, so the two
  are interchangeable.
- The envelope is `alder-signed` version 1 and is read as strictly as every
  other document Alder reads: unknown fields, a version from the future, a
  payload that is not a JSON object, no signature, more than eight signatures,
  or a field longer than Alder reads are each refused.
- Ed25519 is the only algorithm in version 1. A signature naming another is
  invalid, not ignored.

## See also

- [SNAPSHOTS.md](SNAPSHOTS.md), [CHANGE-PACKAGES.md](CHANGE-PACKAGES.md),
  [RECOVERY.md](RECOVERY.md) — the documents that can be signed
- [CLI.md](CLI.md) — `alder key`, `alder sign`, `alder verify`
- [SECURITY.md](../SECURITY.md) — what Alder holds, and what it refuses to hold
