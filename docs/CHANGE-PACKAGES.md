# Change packages

A change package answers:

> What do I intend to change, in a form I can carry to another directory?

It is **not** a plan, a snapshot, a backup, a recovery bundle, a transaction, or
a job. It is the intent, written down:

```
add attribute type 1.3.6.1.4.1.99999.7.1
add object class 1.3.6.1.4.1.99999.7.2, which needs it
add uid=pat,ou=people,dc=example,dc=com, which uses the class
```

Each directory is asked separately what that means against its current state,
and each makes its own plan:

```
intent
  ↓
change package
  ↓
validate against this target
  ↓
ordinary change requests
  ↓
plan
  ↓
review
  ↓
apply
```

There is no path from a package to an apply that skips the plan, in the
interface, on the command line, or in the API.

In the interface this is the **Packages** tab. Over HTTP it is three endpoints:
`POST /packages/build`, `POST /packages/inspect` and `POST /packages/validate`.
From a shell it is `alder package create`, `inspect` and `validate`, plus
`alder plan --package` and `alder apply --package`.

Alder stores nothing. The package is a file you keep.

---

## Why a package is not a plan

A plan is about **one directory at one moment**. It reads every entry a change
names, decides what the change would do against what is there now, and issues a
`baseline` — a MAC computed with a key that exists only in that server process,
bound to that session. The apply hands the baseline back, the server recomputes
it, and refuses if the directory moved in between.

None of that can travel:

- a baseline from another process means nothing here, and cannot be checked;
- an expectation about an entry's current values describes the environment it
  was written in, and would make promotion fail wherever the environments
  legitimately differ;
- a schema modification is server-specific: installing an attribute type is a
  modify of `attributeTypes` on `cn=schema` on 389 DS, and of
  `olcAttributeTypes` on a configuration collection on OpenLDAP.

So a package carries none of them. It carries what the operator meant, and each
target works out what that means for itself.

**Promotion is revalidation, not replay.** The same package bytes go to test and
to production. Test accepting a change says nothing about production, and the
plan in production is made from production's own state.

---

## The format

```json
{
  "format": "alder-change-package",
  "version": 1,
  "id": "3f1b8a52-6a0e-4f1e-9a3e-77a4e1f0c111",
  "createdAt": "2026-09-16T10:00:00Z",
  "title": "team schema and its first user",
  "source": { "alderVersion": "1.11.0", "method": "changeset" },
  "assumptions": { "namingContexts": ["dc=example,dc=com"] },
  "counts": { "changes": 3, "data": 1, "schema": 2, "destructive": 0, "omitted": 0 },
  "changes": [
    { "id": "c1", "kind": "schema", "destructive": false,
      "schema": { "element": "attributeType", "op": "add", "oid": "1.3.6.1.4.1.99999.7.1",
                  "definition": "( 1.3.6.1.4.1.99999.7.1 NAME 'alderTeam' … )" } },
    { "id": "c2", "kind": "schema", "destructive": false, "dependsOn": ["c1"],
      "schema": { "element": "objectClass", "op": "add", "oid": "1.3.6.1.4.1.99999.7.2", "definition": "( … )" } },
    { "id": "c3", "kind": "data", "destructive": false, "dependsOn": ["c2"],
      "data": { "dn": "uid=pat,ou=people,dc=example,dc=com", "type": "add", "attributes": [ … ] } }
  ],
  "omitted": [],
  "checksum": "sha256:…"
}
```

| Field | Meaning |
|---|---|
| `format`, `version` | Always `alder-change-package` and, for now, `1`. |
| `id` | A generated UUID, stable across copies and independent of the filename. **Provenance only**: it authorises nothing, and Alder keeps no record of it. |
| `createdAt` | When it was made. Outside the checksum. |
| `source` | Where it came from: the Alder version, how it was made, and — only if asked for — the vendor and naming contexts of the directory it was made against. Never a host, a bind DN, a path or a credential. |
| `assumptions` | What the package expects of a target: naming contexts, schema OIDs, object classes. Checked at validation. |
| `counts` | Recomputed when the package is read; a document whose counts disagree with its content is refused. |
| `changes` | The intended changes, in canonical order. |
| `omitted` | What was deliberately **not** carried, with a machine-readable reason. |
| `checksum` | SHA-256 over the canonical content without `createdAt` and without the checksum itself. |

### Deterministic

The same intent produces the same bytes: fields in a fixed order, dependency
lists sorted and deduplicated, schema definitions canonicalised by the same
writer the schema editor uses, and changes in dependency order with ties broken
by identifier. `createdAt` is outside the checksum, so the same intent captured
twice has the same checksum — for the same package identity. A different
identity is a different package, and the checksum says so.

### Integrity, not authentication

The checksum detects corruption and editing after the fact. It is not a
signature: anyone who edits a package can recompute it. Packages are not signed,
and 1.11 introduces no keys.

---

## What a package carries

| Change | Packaged as | Portable OpenLDAP ↔ 389 DS |
|---|---|---|
| Add an entry | `data`, `type: add` | yes |
| Modify attributes | `data`, `type: modify` | yes |
| Rename, move, or both | `data`, `type: rename` | yes |
| Delete an entry | `data`, `type: delete`, `destructive: true` | yes |
| Add a schema definition | `schema`, `op: add` | yes — each target builds its own modification |
| Replace a schema definition | `schema`, `op: replace` | yes, where the server permits changing it |
| Delete a schema definition | `schema`, `op: delete`, `destructive: true` | yes, where the server permits it |
| Set a password | **never** | — |
| Anything on a schema entry that is not a definition | **never** | — |

Schema changes are carried as **intent** — element, operation, OID and RFC 4512
definition — not as the modification one server needed. Provenance extensions
(`X-ORIGIN`, `X-SCHEMA-FILE`) are dropped: they say where a definition came from
on one server, and each target records its own.

### Nothing is dropped silently

A change that cannot be packaged is refused, and the package records it:

```json
"omitted": [
  { "subject": "uid=alice,ou=people,dc=example,dc=com", "kind": "data",
    "reason": "secret_not_portable",
    "detail": "a password change is an extended operation carrying the new password…" }
]
```

Reasons are `secret_not_portable`, `unsupported_change` and `server_specific`.

---

## Secrets

**A package never contains a secret.** Not a password, not a hash, not a
reversible form, not a placeholder that could replay one, not a bind credential,
not an API token, not a session MAC.

- A `setpassword` change is refused at creation and recorded as an omission.
- A change naming a sensitive attribute (`userPassword` and the rest of the
  deny list) is refused outright — including one written into a document by
  hand, which is refused when the package is read.
- Alder invents no escrow, and a package cannot fully promote user provisioning
  when a password is part of it. Set passwords where the account lives.

---

## Intent: operations, not desired state

Package items are **explicit operations**. There is one narrow exception, and it
is one the planner already has: an `add` may carry `intent: desired`, which
reconciles it against the entry that is there, changing only the attributes it
names.

**Absence never implies deletion.** A deletion is only ever an explicit `delete`
item, marked `destructive`.

---

## Dependencies

Dependencies are structural, never positional:

```json
{ "id": "c3", "dependsOn": ["c2"] }
```

Alder derives them from the changes themselves when a package is built — an
object class after the attribute types it names, an entry after the class it
uses and after its parent, a child's deletion before its parent's — and writes
them into the document. They can be edited by hand afterwards.

A package is refused when its graph is unusable: a dependency that is not in the
package, a change that depends on itself, or a cycle. Nothing is quietly
reordered or repaired.

The canonical order in the file is a topological order, but execution order
comes from the graph, which validation walks itself. Reordering the array
changes nothing.

---

## Validation against a target

Validation asks: *can this intent be interpreted here, and what would it mean?*

| Status | Meaning |
|---|---|
| `ready` | The target can take it; the ordinary change requests are prepared. |
| `already_satisfied` | The target already holds what the item intends. A success, not a failure. |
| `no_op` | The item resolves to no operation at all. |
| `conflict` | The current state refuses it as written — the entry exists, is missing, or the OID is defined as something else. |
| `dependency_missing` | It names something absent that the package does not provide: a parent entry, a schema element, or an item that is itself unusable. |
| `unsupported` | This server cannot do it at all — the schema is not writable here. |
| `target_incompatible` | An assumption is false here, or the DN is outside every naming context. |
| `unknown` | It could not be decided. **Never** read as ready. |

Problems carry the plan's and the schema comparison's vocabulary where the
situation is the same: `entry_missing`, `entry_exists`, `parent_missing`,
`rename_target_exists`, `attribute_undefined`, `object_class_undefined`,
`definition_missing`, `definition_differs`, `dependency_required`,
`referenced_by_schema`, `schema_not_editable`, `schema_target_required`,
`server_defined`, `outside_naming_contexts`, `assumption_unmet`, `read_failed`,
`unbuildable_change`.

Validation **prepares** change requests and stops. It writes nothing, issues no
baseline, and is not something to keep: it is not a plan, and it is not reusable
in another environment. Carry the package there and validate it again.

### Offline versus target validation

Two different checks, deliberately separate:

- **The package alone** (`POST /packages/inspect`, `alder package inspect`):
  format, version, unknown fields, trailing content, duplicate identifiers,
  missing dependencies, self-dependencies, cycles, DNs, schema definitions that
  parse, secrets, size, and the checksum. **No directory, no session** — the
  check a pipeline runs before it has credentials.
- **The package against a directory** (`POST /packages/validate`): everything
  above, plus what is actually there.

---

## Drift, and why the answer differs per environment

The intent is `title → Senior Engineer`.

| Environment | It holds | Validation | Plan |
|---|---|---|---|
| test | `title: Engineer` | `ready` | modify to `Senior Engineer` |
| production | `title: Staff Engineer` | `ready` | modify to `Senior Engineer` |
| staging | `title: Senior Engineer` | `already_satisfied` | nothing |
| anywhere | no such entry | `conflict`, `entry_missing` | — |

Production is not overwritten because test accepted the package; it is planned
and reviewed on its own terms, and the plan shows exactly what would change.

A package that is entirely `already_satisfied` is a success:

```
12 changes
 9 ready
 3 already satisfied
```

Nothing is written for the three.

---

## Destructive changes

- A deletion is in a package only because somebody put it there.
- It is marked `destructive` in the document, and the mark is checked against
  the operation when the package is read.
- Importing a package selects nothing. The interface does not preselect a
  destructive item, and `Select every non-destructive change shown` skips them.
- Validation re-checks it against the target, and the plan performs its ordinary
  impact analysis — children, references, and the rest.
- Absence of an entry from a package never means "delete it".

---

## Recovery

A recovery bundle belongs to an apply, not to intent:

```
package → validate → plan → apply (--recovery-out) → recovery bundle for THAT directory
```

Recovery is derived from the entries as they were immediately before the change,
in that directory. Two directories that held different values produce different
bundles from the same package, and a bundle made in test must never be used to
recover production. Packages never contain recovery bundles, and bundles never
contain packages.

---

## Limits

| Bound | Value |
|---|---|
| Changes in a package | 2,000, as for a changeset and a plan |
| Dependency edges | 20,000, and 64 per change |
| One string (definition, DN, value, title) | 64 KiB |
| Request body | 16 MB, the server's limit for every endpoint |

---

## Security

- A package is **untrusted input**, read as strictly as a snapshot: unknown
  fields are refused rather than ignored, nothing follows the document, and
  every DN and definition is parsed before it is used.
- A field a package must not have — `baseline`, `expect`, `newPassword` — is
  refused by the unknown-field rule, so a smuggled plan token cannot ride along.
- Directory text is kept exactly as given and escaped where it is shown, by the
  same display rules as everywhere else; control and bidirectional characters
  never reach a terminal or a DOM unescaped.
- There is no templating, no variable interpolation and no expression
  evaluation. A package is data.
- Nothing about a package authorises anything: the identity is provenance, and
  every change still goes through the session's own access rights, the plan, and
  the apply.

---

## Performance

Synthetic packages, one attribute type and one class for every twenty entries
(`go test -bench Package ./internal/changepkg/`, i7-11370H):

| Changes | Build (derive, canonicalise, checksum) | Read (parse, validate, checksum) | Validate against a directory |
|---|---|---|---|
| 100 | 0.7 ms | 2.7 ms | 1.7 ms, 1.8 reads per change |
| 1,000 | 16 ms | 37 ms | 25 ms, 1.8 reads per change |

Validation reads each entry a change names once, and resolves schema against one
read of the target's schema.

---

## Not in this version

- **DN translation between environments.** A package is portable only where the
  DNs are. Rewriting `dc=dev,dc=example,dc=com` to `dc=example,dc=com` is
  deliberately absent: an implicit rewrite that touched some references and not
  others would be a silent corruption, and an explicit one needs a preview and a
  model for every reference. See [DECISIONS.md](DECISIONS.md).
- **Package-to-package comparison.** The checksum tells you two packages differ.
- **Signing, approval workflows, deployment history, an environment registry,
  scheduling, Git integration, automatic promotion.** Alder stays stateless, and
  the package is yours.
- **Provider abstraction.** Portability is reported, not transformed: there is no
  OpenLDAP-to-389 DS rewriting.

Change package format version 1 was introduced in Alder 1.11. Later Alder 1.x
releases will continue to read version 1. See
[COMPATIBILITY.md](COMPATIBILITY.md).
