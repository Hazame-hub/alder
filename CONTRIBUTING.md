# Contributing

## Getting set up

You need Go 1.25+, Node 22+, and Docker.

```sh
make compose-up            # OpenLDAP and 389 DS, seeded, with TLS
make check                 # vet, lint, unit tests — what CI runs
make test-conformance      # the suite that decides whether a change is done
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

**A change is done when `make test-conformance` is green.** Not when it works
against the server you happened to test with. OpenLDAP and 389 Directory Server
are both first-class, and the suite is one table that runs every case against
both. If you find yourself wanting a per-vendor test file, the change is
probably wrong.

**Never branch on the vendor.** Read the RootDSE, put what you learn in
`Capabilities`, and branch on that. `VendorName` exists to display, and that is
all. This is what lets the same code find the schema at `cn=Subschema` on
OpenLDAP and `cn=schema` on 389 DS without knowing which it is talking to.

**Ask before adding a dependency.** Any dependency, however small. `docs/DECISIONS.md`
records the stack and why.

## The rules that are not style preferences

These are in `docs/DECISIONS.md` in full. The short version:

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
make generate
```

Edit the spec, regenerate, then satisfy the compiler on both sides. Handlers are
hand-written against the generated interface; do not edit `api.gen.go`.

## Tests

`internal/ldif` and `internal/schema` carry unit tests and fuzz targets, and
both parsers have earned them — fuzzing has found real bugs in each. If you
touch either, run:

```sh
make fuzz
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

## Scope

`docs/DECISIONS.md` section 2 lists what v1 is and is not. The out-of-scope list is
deliberate and load-bearing — schema editing, ACL editing, multi-user concepts,
SSO, other directory drivers, bulk provisioning, and anything requiring a
database are all excluded on purpose. If you want to add one, open an issue and
make the case first; a pull request implementing it will be closed.

## Decisions log

`docs/DECISIONS.md` ends with an append-only decisions log. If you settle something
non-obvious — especially where the code turned out to contradict the plan — add
an entry so nobody relitigates it later.
