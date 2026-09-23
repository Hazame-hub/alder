package api

import (
	"errors"

	"github.com/gofiber/fiber/v2"

	"github.com/hazame-hub/alder/internal/preflight"
	"github.com/hazame-hub/alder/internal/signing"
)

// Signed documents, as the server sees them.
//
// The server verifies and never signs. It holds public keys an operator named
// when starting it, and no private key: there is no flag that gives it one and
// no code path that would use one.
//
// Every endpoint that reads a document -- a snapshot, a change package, a
// recovery bundle, a preflight artifact -- unwraps it here first. An unsigned
// document goes through exactly as before, which is what keeps every file
// written by an earlier Alder working. A signed one is checked, and what was
// concluded rides along in the answer so a person sees who signed what they
// are looking at.
//
// A signature that does not match refuses the request. Nothing downstream ever
// sees a payload that changed after it was signed, whatever the trust list
// says: that is not a question of policy.

// openDocument unwraps a document that may be signed and reports what
// verification concluded.
//
// The third return says whether the caller may carry on. False means this
// function has already answered the request -- the same shape as
// liveRequestFrom -- because writeError writes the response and a handler that
// treated that as an error to pass on would answer twice.
func (s *Server) openDocument(c *fiber.Ctx, data []byte) ([]byte, signing.Result, bool) {
	payload, result, err := signing.Open(data, s.cfg.TrustedKeys)
	if err == nil {
		if s.cfg.RequireSignature && result.Status != signing.Verified {
			return nil, result, fail(refuseUnsigned(c, result))
		}
		return payload, result, true
	}
	if errors.Is(err, signing.ErrUnsupportedVersion) {
		return nil, result, fail(writeError(c, fiber.StatusBadRequest, ErrorErrorSignatureInvalid,
			"The signed document is of a version this Alder does not read.",
			"Upgrade Alder, or ask for the document from a release this one reads."))
	}
	detail := "The document is wrapped in a signature that does not check out."
	if result.Detail != "" {
		detail = result.Detail
	}
	return nil, result, fail(writeError(c, fiber.StatusBadRequest, ErrorErrorSignatureInvalid,
		"The document's signature does not match it.", detail))
}

// refuseUnsigned answers a document that is not signed by a trusted key, on a
// server started with --require-signature.
func refuseUnsigned(c *fiber.Ctx, result signing.Result) error {
	detail := "This Alder was started with --require-signature, so it reads only documents signed by a key it trusts."
	if result.Status == signing.Untrusted {
		detail = "The document is signed, and by a key this Alder was not told to trust. " +
			"Ask whoever runs it to add the key with --trusted-keys."
	}
	return writeError(c, fiber.StatusBadRequest, ErrorErrorSignatureRequired,
		"This Alder reads signed documents only.", detail)
}

// signatureView is what a response says about a document's signature. Absent
// where nothing was read, so an unsigned document's answer is the same shape
// it has always been apart from one field.
func signatureView(result signing.Result) *DocumentSignature {
	if result.Status == "" {
		return nil
	}
	view := &DocumentSignature{Status: DocumentSignatureStatus(result.Status)}
	if result.Detail != "" {
		view.Detail = ptr(result.Detail)
	}
	if len(result.Signers) > 0 {
		signers := make([]DocumentSigner, 0, len(result.Signers))
		for _, signer := range result.Signers {
			s := DocumentSigner{KeyId: signer.KeyID, Trusted: signer.Trusted}
			optional(&s.Signer, signer.Signer)
			optional(&s.SignedAt, signer.SignedAt)
			signers = append(signers, s)
		}
		view.Signers = &signers
	}
	return view
}

// withSignature records on a preflight report what its artifact's signature
// amounted to. The report is the server's own type, so this sets the field
// rather than rebuilding it.
func withSignature(report *preflight.Report, result signing.Result) *preflight.Report {
	if report == nil || result.Status == "" || result.Status == signing.Unsigned {
		return report
	}
	copied := result
	report.Source.Signature = &copied
	return report
}

// signedDiff records on a comparison what each side's signature amounted to,
// for the sides that were documents.
func signedDiff(view Diff, body diffBody) Diff {
	view.SourceSignature = signatureView(body.signatures["source"])
	view.TargetSignature = signatureView(body.signatures["target"])
	return view
}
