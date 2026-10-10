# The Alder charter

What Alder is, what it will and will not do, and the rules no change may
break. Code comments and `docs/DECISIONS.md` cite this document by section
number ("section 3 of the charter"), and the numbers are stable: a section
is amended in place, never renumbered.

A change to sections 2, 4 or 7 is a decision, not an edit. It is made
deliberately, and recorded with its reason in `docs/DECISIONS.md` before
any code is written for it.

---

## 1. What Alder is

A modern, self-hosted web UI for engineering LDAP directories.

The user is a platform or infrastructure engineer who owns a directory and
manages it today with `ldapsearch`, `ldapmodify`, hand-written LDIF and an
old admin tool they do not trust.

> A directory engineering tool whose output is code.

Everything Alder does to a directory can be previewed as LDIF and exported
as an Ansible task. It is not a "click here and hope" admin panel; it is an
authoring environment for directory changes, with a browser attached.

## 2. Scope

The most important section. Scope grows only on the record.

### In scope

1. Connect to a directory with simple bind over LDAPS or StartTLS.
2. Browse the directory tree, loaded lazily.
3. A schema browser: object classes, attribute types, syntaxes and matching
   rules, cross-linked, with full-text search.
4. An entry viewer and editor driven by the schema: required and optional
   attributes, single- and multi-valued, inputs suited to each syntax.
5. **An LDIF preview on every write.** No modification reaches a server
   without the exact LDIF change record being shown first and confirmed.
6. LDIF export of an entry or a subtree, and LDIF import.
7. Ansible task export of any pending change.
8. Search, with a filter builder and a raw RFC 4515 filter input.
9. Changesets: stage several changes, review them as one LDIF document,
   export them as one Ansible playbook, apply them in order.
10. Schema editing, 389 DS first.

Added since, each on the record in `docs/DECISIONS.md`:

- Snapshots, drift comparison and recovery bundles, all of them expressed
  as ordinary reviewed changes.
- In the configuration tree beyond the schema: the settings a comparison
  has proved, creating OpenLDAP overlays whose module is already loaded,
  and switching 389 DS plugins on or off. Nothing else.
- Access rules are **read** and shown against an entry, each server's in
  its own words. They are not evaluated, translated or written: the editor
  shows `aci` and `olcAccess` read-only.
- The Alder project format (2026-10-10): a specification first, no code
  until it has been reviewed.

### Out of scope

Not built, not scaffolded, no configuration keys for them:

- Writing access rules, and configuration-tree editing beyond the list
  above.
- Any multi-user concept: roles, delegation, approval workflows.
- OIDC, SAML, single sign-on.
- A persisted audit log.
- FreeIPA, Active Directory and Entra ID drivers.
- Bulk or CSV provisioning.
- End-user self-service or password reset.
- Any database.
- Telemetry, licensing or paywall plumbing.

Alder is **stateless**: no database, no migrations, no persistence layer.

## 3. Target servers

**OpenLDAP** and **389 Directory Server**, both equally well. That is the
quality bar, not a stretch goal.

Known divergences, handled deliberately:

| Concern | OpenLDAP | 389 DS |
|---|---|---|
| Schema location | `subschemaSubentry` (`cn=Subschema`) | `cn=schema` |
| Entry UUID | `entryUUID` | `nsUniqueId` |
| Change sequence | `entryCSN` | `nsUniqueId` + `modifyTimestamp` |
| Password policy | `ppolicy` overlay | native policy |
| Access rules | `olcAccess` | `aci` attribute |

**Never branch on vendor name.** The RootDSE is read once at connect time
into a capabilities structure -- supported controls and extensions, SASL
mechanisms, naming contexts, the subschema entry -- and code branches on
those, or on what an entry itself publishes. Vendor identification is for
display only.

## 4. Stack

Not substituted without a decision on the record.

- **Go** (the floor is the `go` line in `go.mod`, currently 1.27.2), Fiber
  for HTTP, `go-ldap/ldap/v3` for the protocol, `slog` for logging, Cobra
  for the command line. No ORM, no database.
- **Spec-first API.** `api/openapi.yaml` is the source of truth; the Go
  server interface and the TypeScript client are both generated from it.
- **Frontend:** React and TypeScript on Vite, TanStack Router and Query,
  shadcn/ui and Tailwind.
- **Packaging:** a single binary with the SPA embedded; `alder serve`
  starts the web UI. A distroless container image from a multi-stage
  Dockerfile.

## 5. Layout

The parts that matter for orientation; the tree has more.

```
api/openapi.yaml        the HTTP API, source of truth
cmd/alder/              the command line
internal/directory/     the Driver interface, Capabilities, ChangeRecord
  ldapdriver/           the one driver
internal/dn/            DNs, RFC 4514
internal/filter/        filters, RFC 4515
internal/schema/        schema, RFC 4512
internal/ldif/          LDIF, RFC 2849
internal/ansible/       ChangeRecord to Ansible
internal/plan/          planning a change against the directory as it is
internal/api/           HTTP handlers against the generated interface
internal/session/       the in-memory session store
web/                    the SPA
test/compose/           OpenLDAP and 389 DS, with TLS and seed data
test/conformance/       one suite, run against both servers
test/e2e/               the real UI in a browser, against both servers
```

## 6. The core abstraction

```go
type Driver interface {
    Connect(ctx context.Context, cfg ConnConfig) (Session, error)
}

type Session interface {
    Capabilities() Capabilities
    Schema(ctx context.Context) (*schema.Schema, error)
    Search(ctx context.Context, req SearchRequest) (*SearchResult, error)
    Read(ctx context.Context, dn dn.DN, attrs []string) (*Entry, error)
    Apply(ctx context.Context, ch ChangeRecord) error
    Close() error
}
```

Exactly one driver implements it. The interface exists so other directory
backends could be added later without a rewrite; it is not an invitation
to start them.

**`ChangeRecord` is the centrepiece.** Every mutation is expressed as one
before it is applied. It renders to LDIF (`ChangeRecord.LDIF()`) and to an
Ansible task (`ansible.Task`), and it is what the user confirms. There is
exactly one code path that writes to a directory, and it takes a
`ChangeRecord`.

## 7. Hard rules

Correctness and security requirements, not style.

1. **No write without a ChangeRecord.** Nothing calls the protocol's write
   operations outside `Session.Apply`.
2. **DNs are never strings.** They are built with the `dn` package, with
   RFC 4514 escaping, always. No string formatting to build a DN.
3. **Filters are never interpolated.** They are built with the `filter`
   package; user-supplied values are escaped per RFC 4515.
4. **Every search is paged** with the simple paged results control, with a
   default page size and a hard cap on what reaches the UI.
5. **Credentials never persist.** Bind credentials live in an in-memory
   session keyed by an httpOnly, Secure, SameSite=Strict cookie. Not on
   disk, not in a token, not in browser storage.
6. **Sensitive values are never logged:** `userPassword`, `unicodePwd`,
   `nsslapd-rootpw`, anything on the configurable deny list, and bind
   credentials at any level.
7. **TLS by default.** Plaintext LDAP is refused unless
   `--i-know-this-is-insecure` is passed.
8. **Capability checks, not vendor checks**, as section 3 says.

## 8. The harness

`test/compose` brings up OpenLDAP and 389 DS with TLS and identical seed
data: a few hundred users, nested groups, custom object classes, values
that need base64 and DNs that are not ASCII. `test/conformance` is **one**
table-driven suite that runs against both servers through the `Driver`
interface and asserts identical behaviour; `task test:conformance` runs
it, and it is green before any feature is done. `test/e2e` drives the real
UI against the same two servers and checks the result with the servers'
own tools, never through Alder.

The harness is what makes support for two servers real rather than
aspirational. It is a deliverable in its own right, not test scaffolding.

The LDIF and schema parsers also carry unit and fuzz tests.

## 9. Decisions

Every decision that shaped Alder, with its reason, is in
`docs/DECISIONS.md`, newest last. That file is where to look before
relitigating anything here.
