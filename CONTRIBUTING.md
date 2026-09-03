# Contributing

## Getting set up

You need Go 1.25+, Node 22+, and Docker.

```sh
task compose:up             # OpenLDAP and 389 DS, seeded, with TLS
task check                  # vet, lint, unit tests — what CI runs
task test:conformance      # the suite that decides whether a change is done
```

For working on the UI, run the API and the Vite dev server separately so the
SPA reloads on save without a Go rebuild:

```sh
go run ./cmd/alder serve --addr 127.0.0.1:8899 --allow-http --log-level debug
cd web && npm run dev
```

Vite proxies `/api` to the Go server, so the browser sees one origin and the
session cookie behaves exactly as it will in production.

## The bar

**A change is done when `task test:conformance` is green.** Not when it works
against the server you happened to test with. OpenLDAP and 389 Directory Server
are both first-class, and the suite is one table that runs every case against
both. If you find yourself wanting a per-vendor test file, the change is
probably wrong.

**Never branch on the vendor.** Read the RootDSE, put what you learn in
`Capabilities`, and branch on that. `VendorName` exists to display, and that is
all. This is what lets the same code find the schema at `cn=Subschema` on
OpenLDAP and `cn=schema` on 389 DS without knowing which it is talking to.

**Ask before adding a dependency.** Any dependency, however small. The stack is
Go with Fiber, `go-ldap/ldap/v3` and oapi-codegen on the server; React 19,
TanStack Query, Tailwind and shadcn/ui in the browser. No ORM and no database:
v1 is stateless by design.

## The rules that are not style preferences

These are correctness and security requirements, not style preferences:

1. **No write without a `ChangeRecord`.** If you are calling `conn.Modify()`
   outside `Session.Apply`, stop.
2. **DNs are never strings.** Use `internal/dn`. There is no exported way to
   build one by concatenation, deliberately.
3. **Filters are never interpolated.** Use `internal/filter`. User-supplied
   values get RFC 4515 escaping.
4. **Every search is paged and bounded.** There is no unbounded search.
5. **Credentials never persist**, and are never logged at any level.
6. **Sensitive attribute values are never logged or sent to the browser.**
7. **TLS is on by default** in both directions.

## The API is spec-first

`api/openapi.yaml` is the source of truth. The Go server interface and the
TypeScript client are both generated from it:

```sh
task generate
```

Edit the spec, regenerate, then satisfy the compiler on both sides. Handlers are
hand-written against the generated interface; do not edit `api.gen.go`.

## Tests

`internal/ldif` and `internal/schema` carry unit tests and fuzz targets, and
both parsers have earned them — fuzzing has found real bugs in each. If you
touch either, run:

```sh
task fuzz
```

A crash found by fuzzing gets its input committed to `testdata/fuzz` as a
regression case. Several already there came from exactly that.

Behaviour that spans a directory belongs in `test/conformance`, behind the
`conformance` build tag so a plain `go test ./...` needs no Docker.

## The CLA

There is a one-time [Contributor Licence Agreement](CLA.md) to sign before a
pull request can be merged. A bot will prompt you on your first one; you reply
to it once and never again.

The reason is stated plainly in the document rather than buried. Alder is
AGPL-3.0, and the project keeps the option of licensing it on other terms as
well. That option exists only while every line is covered by a grant permitting
it, and a single contribution without one removes it permanently. **You keep the
copyright in your work** — the CLA grants a licence, it does not assign
ownership.

If your employer has rights in the code you write, which most employment
contracts arrange, read section 5 before signing.

## Commits

Conventional commits. Small and focused. Never `--no-verify`.

Write the message for someone reading `git log` in a year: what changed, and
why that was the right call. A commit that says what a diff already says is a
wasted opportunity.

## Releases

Nobody tags a release by hand. Every push to `main` refreshes a **release pull
request** that release-please maintains: it reads the conventional commits since
the last release, works out the next version, and writes the changelog it would
produce. Merging that pull request is the release.

Merging it tags the version, publishes the GitHub release with the changelog,
and then builds and attaches the binaries for six platforms plus the multi-arch
image on `ghcr.io/hazame-hub/alder`.

This is the reason commit prefixes matter beyond tidiness. `feat:` and `fix:`
appear in the changelog and move the version; `docs:`, `ci:`, `chore:` and
`test:` do not. A breaking change is a `!` after the type, or a
`BREAKING CHANGE:` footer.

While the version is below 1.0, a breaking change moves the minor version
rather than the major one, which is the usual pre-1.0 convention.

### The release token

GitHub does not trigger workflows from events made with the default
`GITHUB_TOKEN`, so a release pull request opened by it never gets its required
checks reported and cannot be merged without nudging it by hand.

The fix is a repository secret named `RELEASE_PLEASE_TOKEN`: a fine-grained
personal access token scoped to this repository alone, with **Contents:
read and write** and **Pull requests: read and write**, and nothing else. The
release workflow falls back to `GITHUB_TOKEN` when the secret is absent, so the
pipeline works either way — the token only removes the two manual steps.

If a release pull request ever sits with no checks reported, that secret has
expired.

## Scope

v1 does eight things: connect over LDAPS or StartTLS, browse the DIT, browse the
schema, view and edit entries, preview every write as LDIF, export LDIF, export
Ansible, and search.

Everything else is excluded on purpose, and the list is load-bearing rather than
a backlog:

- schema editing, and ACL or `cn=config` editing
- any multi-user concept: RBAC, delegation, approval workflows
- OIDC, SAML, or SSO
- a persisted audit log
- FreeIPA, Active Directory or Entra ID drivers
- bulk or CSV provisioning
- an end-user self-service or password reset portal
- a database of any kind
- telemetry

If you want one of these, open an issue and make the case first. A pull request
implementing one will be closed.

## Decisions log

[`docs/DECISIONS.md`](docs/DECISIONS.md) is an append-only record of decisions
that were settled once. If you settle something non-obvious — especially where
the code turned out to contradict the plan — add an entry so nobody relitigates
it later.
