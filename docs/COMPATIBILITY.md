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

A response may be **streamed**, and a streamed one carries the same fields in a
different order. `POST /api/v1/search` writes its entries first and the fields
it cannot know until the search has finished — `truncated`, `cookie`, `took` —
last. JSON gives an object's members no order, so this is not a change to the
contract: a client that decodes the body sees exactly what it saw before.

What it does change is how a failure *after the first byte* reads. The status
code is settled by then, and the response has no field for "this went wrong", so
such a response is deliberately left unterminated — it fails to parse, with a
trailing comment saying why. Before 1.4.0 the same failure was a `502`. A short
document that parsed cleanly would be indistinguishable from a complete answer,
which is the one outcome worth ruling out. Every failure a search normally has
is still a proper status code: the first page and the schema are both fetched
before anything is sent.

`GET /api/v1/source` is covered too, even though it is served outside the
generated document — it carries the AGPL section 13 offer and must work without
a session. That it lives outside `openapi.yaml` is an untidiness worth fixing;
it does not make the endpoint less binding.

### 3. The command line and the configuration

`alder serve` keeps its flags, their meanings and their defaults. Configuration
file keys and their environment-variable equivalents keep theirs. New flags and
keys may appear; existing ones do not change under you.

The one thing that may tighten is a **security default**, and only with a way
back. `--i-know-this-is-insecure` exists because refusing plaintext LDAP by
default was worth doing; if another default has to move, it will come with an
explicit opt-out and a changelog entry, not silently.

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
