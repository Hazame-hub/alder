# Alder

A modern web UI for OpenLDAP and 389 Directory Server. Browse the schema, edit
entries safely, export every change as LDIF or Ansible.

> A directory engineering tool whose output is code.

Alder is for the platform engineer who owns a directory and currently manages it
with `ldapsearch`, hand-written LDIF, and a twenty-year-old PHP tool they do not
trust. Everything Alder does to a directory can be previewed as LDIF and
exported as an Ansible task. It is not a click-here-and-hope admin panel; it is
an authoring environment for directory changes, with a browser attached.

Alder wood hardens instead of rotting when submerged. Venice stands on alder
piles that have held for centuries. It seemed a fitting name for the directory
everything else authenticates against.

## Status

**M0, foundations.** Not usable yet. There is no server and no UI.

| | |
|---|---|
| M0 | Repo, test harness, `dn` and `filter` packages, CI. **Done.** |
| M1 | Driver interface, LDAP driver, RootDSE capabilities, RFC 4512 schema parser, conformance suite. |
| M2 | React UI: connect, browse the tree, view entries, browse the schema. Read-only, and the first demoable state. |
| M3 | Write path: `ChangeRecord`, LDIF preview with a mandatory confirm, schema-driven editor. |
| M4 | LDIF and Ansible export. |
| M5 | Release. |

`docs/DECISIONS.md` holds the full plan, the scope boundaries, and the decisions log.

## Working on it

```sh
make help            # every target
make check           # vet, lint, test: what CI runs
make compose-up      # two directory servers, seeded, with TLS
make test-conformance
```

You need Go 1.23 or newer and Docker. See `test/compose/README.md` for what the
harness gives you.

## What is in the box so far

| Package | |
|---|---|
| `internal/dn` | RFC 4514 distinguished names. DNs are never strings; there is no exported way to build one by concatenating text. |
| `internal/filter` | RFC 4515 search filters, built and parsed. Values are escaped, attribute names are validated, and a raw filter typed by a user is parsed rather than passed through. |
| `test/compose` | OpenLDAP and 389 DS, TLS from one CA, 318 identical entries each. |
