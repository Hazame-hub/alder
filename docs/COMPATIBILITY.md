# What Alder 1.0 promises

Alder writes to directories that other systems authenticate against. Nobody
should have to read a changelog to find out whether a patch release moved
something they depend on. This document says what is covered by semantic
versioning, what is not, and why the line falls where it does.

The short version: **within 1.x, anything you can automate against keeps
working.** A minor release may add; only a major release may break.

---

## Covered

### 1. The LDIF and the Ansible

This is the promise that matters most, and the one nobody thinks to make.

Alder's output is code. An operator exports a subtree, commits it, and reviews
the next export as a diff. A change to attribute order, fold column, quoting or
task naming breaks every one of those diffs at once — not by being wrong, but by
being different. So within 1.x:

- The **LDIF** an export produces is byte-stable for unchanged input:
  attribute order as the server returned it, folding at column 76, the same
  base64 decisions, the same record separation.
- The **Ansible** an export produces is byte-stable in the same way: the same
  modules, the same task names, the same key order, the same comments.

What is promised is the **records**. Alder's own comment preamble around them —
the lines beginning `#` that name the base, the scope and the filter — carries
information about the export rather than content from the directory, and may
gain or move lines within 1.x. It moved once already, in 1.2.0: a streamed
export does not know its entry count or whether it truncated until it has
finished, so both are written at the end. That is worth more than the stability
it cost, because a file ending in its own summary is one you can tell arrived
whole, and a count at the top of a download that died halfway cannot be told
from an honest one.

Both are pinned by golden files in `internal/ldif/testdata/golden` and
`internal/ansible/testdata/golden`. A change to either fails a test on purpose.
Regenerating them with `-update` is not a routine step; it is how you notice you
are about to break somebody's diff.

The exception is a **correctness fix**. If a rendering means something other
than the change record it came from, the rendering is wrong and will be fixed in
a patch release, because a playbook that silently does the wrong thing is worse
than a diff that moves. Two such fixes shipped in 0.12.1 and 0.12.2. When it
happens, the changelog says so plainly.

### 2. The HTTP API

`api/openapi.yaml` is the contract. Within 1.x it changes only by adding:

- a new endpoint
- a new optional request field, with the old behaviour when it is omitted
- a new response field
- a new value in an enum, where a client that does not recognise it can fall
  through

It will not, within 1.x:

- remove or rename an endpoint, field or enum value
- make an optional request field required
- remove a field from a response's `required` list
- change the type or meaning of an existing field

Tightening a promise is allowed: a field that was optional in a response may
become required, because a client that already handles it absent still works.

1.5 changed the plan more than any release since it appeared, and this is how
those rules were applied:

- **A stale plan is still `409` with `error: conflict`,** exactly as in 1.4. The
  precise reason is a new `cause: plan_stale` beside it, and a new `affected`
  lists every change it applies to. A client that switches on `conflict` keeps
  working; an earlier draft that replaced the code was caught by the 1.4 test
  pinning it.
- **New identifiers:** the `invalid` plan action; the `plan_mismatch` and
  `ldif_mode_mismatch` errors; the plan fields `intent`, `kind`, `problem`,
  `membership`, `references`, `impact` and `subtrees`; `ldif` and `mode` on the
  plan request, which previously required `changes` and now requires exactly one
  of the two.
- **A planned change that is not the planned operation is refused** with
  `plan_mismatch`. In 1.4 a baseline bound only the state, so a client could plan
  one change and apply a different one against the same attributes. That is the
  guarantee a plan exists to give, so this is treated as a correctness fix.
- **Sensitive values are withheld** from plan records (`{"size": n}` in place of
  the value), from previews (`withheld (n bytes)`), and from the LDIF in apply
  responses. Before 1.5 a change that set `userPassword` directly had the value
  echoed back in its preview and its apply response. A value sent back with only
  a `size` is refused with `400` rather than written as empty.
- **Import with `reconcile: true` reconciles only records with no
  `changetype`.** It used to turn an explicit `changetype: add` of an existing
  entry into a modification too. The document said add; this is a correctness
  fix, and the one place a 1.4 request now produces a different result.

1.6 moved every single-change write in the interface onto the plan. The HTTP
contract changed only by tightening what a token proves:

- **`POST /changes/preview` is kept, and marked deprecated.** The interface no
  longer calls it: its dialog plans the change and shows the preview that comes
  back inside the plan item, which is rendered by the same code. The endpoint
  still renders exactly what it did, for any 1.x client that uses it, and it is
  not removed within 1.x.
- **`POST /changes/apply` without a `baseline` applies as it always has.** The
  interface now always sends one.
- **A secret sent under a token must be the secret that was planned.** A
  `set_password` change, or a sensitive attribute value, that differs from the
  planned one is `400 plan_mismatch`; in 1.5 a value of the same length was
  accepted. This is the guarantee a token exists to give, so it is treated as a
  correctness fix. A consequence: a token carrying a secret verifies only in the
  session that planned it, where 1.5's would verify in any session of the same
  process.

1.7 added snapshots and comparisons, only by adding:

- **New endpoints:** `POST /snapshots/capture`, `POST /snapshots/inspect` and
  `POST /diff`, and the error identifiers `snapshot_invalid`,
  `snapshot_unsupported_version`, `snapshot_checksum_mismatch`,
  `snapshot_too_large` and `snapshot_scope_unsupported`.
- **The snapshot document is covered like the API.** Snapshot format version 1,
  described in [SNAPSHOTS.md](SNAPSHOTS.md), was introduced in Alder 1.7. Every
  later 1.x release will continue to read it, and two captures of the same state
  will produce the same document apart from `createdAt`. Releases before 1.7 have
  no snapshot support at all. A later 1.x may write a new version, or a new
  `kind`, and will still read version 1. Readers refuse unknown fields rather
  than ignore them, so a document that uses a field an older release does not
  know is refused by that release with `snapshot_invalid`. That is deliberate: a
  field the reader skipped could change what the comparison means.
- **In 1.8, two snapshots are compared without a session.** `POST /diff` with a
  snapshot on both sides used to be refused with `401` unless a session was
  open, and is now answered either way. A request that succeeded before
  succeeds the same way; a comparison with a live side still needs a session.
- **Comparison enums may grow.** New values in `DiffKind`, `DiffReasonCode` and
  `DiffCandidateBlocked` follow the enum rule above. A client that does not
  recognise a `blocked` value should treat the item as offering no change.

A response may be **streamed**, and a streamed one carries the same fields in a
different order. `POST /api/v1/search` writes its entries first and the fields
it cannot know until the search has finished — `truncated`, `cookie`, `took` —
last. JSON gives an object's members no order, so this is not a change to the
contract: a client that decodes the body sees exactly what it saw before.

What it does change is how a failure *after the first byte* reads. The status
code is settled by then, and the response has no field for "this went wrong", so
such a response is deliberately left unterminated — it fails to parse, with a
trailing comment saying why. Before 1.3.1 the same failure was a `502`. A short
document that parsed cleanly would be indistinguishable from a complete answer,
which is the one outcome worth ruling out. Every failure a search normally has
is still a proper status code: the first page and the schema are both fetched
before anything is sent.

`GET /api/v1/source` is covered too, even though it is served outside the
generated document — it carries the AGPL section 13 offer and must work without
a session. That it lives outside `openapi.yaml` is an untidiness worth fixing;
it does not make the endpoint less binding.

### 3. The command line and the configuration

`alder serve` keeps its flags, their meanings and their defaults. Every flag
also reads an environment variable — the flag name upper-cased with dashes as
underscores, behind `ALDER_` — and those keep their meanings too. New flags and
variables may appear; existing ones do not change under you.

That promise was made at 1.0 and went several releases with no implementation
behind it: the binary was flags-only, and the sentence describing environment
equivalents described nothing. It is true now, rather than deleted, because the
promise was the useful half. The changelog entry that adds this says which
release it became true in — this document deliberately does not guess, having
guessed wrong once already.

The one thing that may tighten is a **security default**, and only with a way
back. `--i-know-this-is-insecure` exists because refusing plaintext LDAP by
default was worth doing; if another default has to move, it will come with an
explicit opt-out and a changelog entry, not silently.

The client commands -- `alder snapshot`, `diff`, `plan`, `apply` and `version`,
added in 1.8 -- are covered the same way. Their names and flags keep their
meanings, and so do the variables their connection flags read, the exit codes
in [CLI.md](CLI.md), and their `--json` output. That JSON is the API's own
documents, passed through, so it follows the API's rules above; what the client
adds around them -- `apply`'s envelope, and the error document's `origin` and
client error codes -- only grows. A flag that confirms or widens a write
(`--yes`, `--allow-deletes`, `--force`) will not start reading the environment.
Their human-readable output is not covered, for the same reason message text is
not.

---

## Not covered

These are deliberately outside the promise, so that the parts above can be kept.

- **The web interface.** Its routes, its search parameters, its DOM and its
  wording are free to change in any release. It is a program you look at, not
  one you automate against; if you are scripting the SPA, use the API instead.
- **Everything under `internal/`.** Go's own rules already make these
  unimportable. Alder is a program, not a library.
- **The exact text of messages**, in the UI or in an error body. Error
  *identifiers* are part of the API; the prose beside them is not.
- **Log output.** Format and content may change. It is for a person reading it,
  not for a parser.
- **The test harness.** `test/compose` and `test/conformance` are how the
  project proves itself on both servers, not an interface for anyone else.

---

## What a version number tells you

- **Patch** (1.0.x) — fixes. Includes correctness fixes to generated output, as
  above.
- **Minor** (1.x.0) — new features, new endpoints, new flags. Nothing you were
  already doing stops working.
- **Major** (2.0.0) — something above changed. It will be argued for in
  `docs/DECISIONS.md` before it is done.

Alder ships from `main` with release-please and conventional commits, so a
breaking change is marked at the commit that makes it and reaches the version
number by itself rather than by anyone remembering.

## Before 1.0

Versions 0.1 through 0.12 made no such promise, and the version number could not
have carried it: release-please was configured with `bump-minor-pre-major`, which
gives a breaking change and a new feature the same minor bump. Removing that
configuration is part of what 1.0 means here — the number can now say something
it previously could not.
