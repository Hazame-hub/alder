import type { components } from "@/lib/api.gen";

export type DocumentSignature = components["schemas"]["DocumentSignature"];
export type DocumentSigner = components["schemas"]["DocumentSigner"];

/**
 * Showing what a document's signature amounted to.
 *
 * The server decided this; nothing here re-decides it. A document whose
 * signature did not match never reaches the interface at all -- the request is
 * refused -- so what is shown is one of three honest answers: nobody signed
 * this, someone we trust signed it, or someone signed it and we do not know
 * who.
 */

export type SignatureLook = { label: string; variant: "success" | "outline" | "warning"; title: string };

export const SIGNATURE_LOOK: Record<DocumentSignature["status"], SignatureLook> = {
  unsigned: {
    label: "unsigned",
    variant: "outline",
    title: "The document carries no signature. Its checksum still shows it was not corrupted.",
  },
  verified: {
    label: "signed",
    variant: "success",
    title: "Signed by a key this server was told to trust.",
  },
  untrusted: {
    label: "signed, key not trusted",
    variant: "warning",
    title:
      "The signature is intact and the key is not one this server was told to trust, so who signed it is not established.",
  },
  invalid: {
    label: "signature does not match",
    variant: "warning",
    title: "The signature does not match the document. A document like this is refused rather than read.",
  },
};

/** Who signed, in one line: the label they gave, else the key. */
export function signerLine(signer: DocumentSigner): string {
  const who = signer.signer?.trim() || shortKey(signer.keyId);
  const when = signer.signedAt ? ` on ${signer.signedAt}` : "";
  return `${who}${when}${signer.trusted ? "" : " (key not trusted)"}`;
}

/** The first and last of a key id, which is what a person compares by eye. */
export function shortKey(keyId: string): string {
  return keyId.length > 16 ? `${keyId.slice(0, 8)}…${keyId.slice(-8)}` : keyId;
}

/** Whether a signature is worth showing at all: an unsigned document is ordinary. */
export function worthShowing(signature?: DocumentSignature): signature is DocumentSignature {
  return !!signature && signature.status !== "unsigned";
}
