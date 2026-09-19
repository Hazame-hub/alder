# Schema snapshots and schema comparison

A schema snapshot answers:

> What schema did this server publish, at a moment I can name?

A schema comparison answers:

> Which definitions differ between two schemas, in what, and which of those
> differences could this directory be changed to match?

Alder 1.10 captures the schema a server publishes as a deterministic document,
compares two of them — or one and the live schema — by meaning rather than by
text, and turns the differences you select into ordinary change requests. Those
go through the same plan and apply as every other write. It uses the Snapshot →
Diff → Plan model already used for directory data; see
[SNAPSHOTS.md](SNAPSHOTS.md) and [PLAN.md](PLAN.md).

It is the same three endpoints with `kind: schema`, the **Schema** option in the
Snapshots tab, and `alder snapshot --kind schema` and `alder diff` from a shell.
Nothing is stored on the server.

---

## What is captured

| Element | Captured | Compared | Actionable | Notes |
|---|---|---|---|---|
| `attributeTypes` | yes | yes | add, modify, remove | where the server permits it; see [Which differences become changes](#which-differences-become-changes) |
| `objectClasses` | yes | yes | add, modify, remove | as above |
| `ldapSyntaxes` | as context | no | no | kept so a reader sees what a `SYNTAX` names |
| `matchingRules` | as context | no | no | resolves `EQUALITY`, `ORDERING` and `SUBSTR` names to OIDs, so a rule written by name equals the same rule written by OID |
| `matchingRuleUse` | as context | no | no | computed by some servers from the other definitions |
| `dITContentRules` | as context | no | no | |
| `nameForms` | as context | no | no | |
| `dITStructureRules` | no | no | no | listed in `coverage.notCaptured`; identified by an integer rule ID rather than an OID |

A document says this about itself in `coverage`: `compared`, `context` and
`notCaptured`. Server configuration is not in a schema snapshot: since 1.13 it
has a kind of its own, `config` (see
[CONFIG-SNAPSHOTS.md](CONFIG-SNAPSHOTS.md)), and a schema snapshot holds
schema.

A definition that does not parse is **kept, not dropped**: its text and the
parse error go in `unparsed`, and the snapshot is `completeness: partial`. The
same happens to a second definition claiming an OID already captured. A schema
larger than 50,000 definitions is refused with `snapshot_too_large` rather than
captured in part.

A live capture reads the schema fresh, so a change someone else made since the
session connected is in it.

## The format

```json
{
  "format": "alder-snapshot",
  "version": 1,
  "kind": "schema",
  "createdAt": "2026-09-14T12:00:00Z",
  "source": { "vendor": "389 Project", "vendorVersion": "3.1.1", "subschemaEntry": "cn=schema", "collections": false },
  "completeness": "complete",
  "coverage": { "compared": ["attributeTypes", "objectClasses"], "context": ["…"], "notCaptured": ["dITStructureRules"] },
  "counts": { "attributeTypes": 1026, "objectClasses": 203, "…": 0, "unparsed": 0 },
  "attributeTypes": [
    { "oid": "1.3.6.1.4.1.99999.1.1", "names": ["alderTeam"], "desc": "Team the person belongs to",
      "equality": "caseIgnoreMatch", "substr": "caseIgnoreSubstringsMatch",
      "syntax": "1.3.6.1.4.1.1466.115.121.1.15", "singleValue": true, "usage": "userApplications",
      "extensions": { "X-ORIGIN": ["Alder test harness"] },
      "definition": "( 1.3.6.1.4.1.99999.1.1 NAME 'alderTeam' … )" }
  ],
  "objectClasses": [ "…" ],
  "context": { "ldapSyntaxes": [], "matchingRules": [], "matchingRuleUse": [], "ditContentRules": [], "nameForms": [] },
  "unparsed": [],
  "checksum": "sha256:…"
}
```

Each definition holds both its **parsed fields** and its **definition text** as
the server published it. A comparison reads the fields; a person reads the text.
When a document is read, every definition is parsed again and must produce
exactly the fields stored beside it, so the two can never tell different
stories. `collection` is set where the server keeps schema in configuration
collections and the capturing session could read them (`source.collections`).

It is `kind: schema` of the existing format version 1, with its own field set.
A data snapshot reader refuses it by its kind, and a schema snapshot reader
refuses a data snapshot the same way.

### Deterministic

Two captures of an unchanged schema are the same document except `createdAt`,
and have the same checksum. The checksum is SHA-256 over the canonical content
without `createdAt` and `checksum`, exactly as for a data snapshot. Definitions
are sorted by OID, numerically arc by arc; context by OID then text;
unparsed definitions by attribute and text.

### Reading a schema snapshot

A document is untrusted input, and is refused unless:

- `format`, `version` and `kind` are ones this Alder reads, no field is unknown
  and nothing follows the document;
- `createdAt` is RFC 3339, `subschemaEntry` is a DN, `completeness` and
  `coverage` are what the document holds;
- every OID is well formed, and no OID appears twice within a kind;
- every definition parses, and parses to its stored fields;
- `counts` match the lists, and the total is within the bound;
- no `SUP` chain among attribute types or object classes is a cycle;
- a checksum, if present, matches. Without one it is read as `unverified`.

## Identity

**The OID is the identity. Names are aliases.**

- Two definitions with the same OID are the same element, whatever they are
  called. Renaming one is a modification of its `names`.
- Two definitions with different OIDs are different elements, however alike.
  Nothing is paired by name or by resemblance.
- A definition whose NAME another definition of the same kind also claims is
  `unknown` (`ambiguous_name`): a reference to that name cannot be resolved.
- An OID that is not numeric — 389 DS publishes a few descriptor-style ones such
  as `nsHost-oid` — is compared by its exact text, reported with
  `non_numeric_oid`, and never becomes a change.

## Semantic comparison

Definitions are compared field by field, as what they mean.

| Makes no difference | Because |
|---|---|
| whitespace, quoting, keyword order | definitions are parsed |
| the order and case of `NAME` aliases | names compare as a set, case-insensitively |
| the order, case and repetition of `SUP`, `MUST` and `MAY` in a class | they compare as sets of resolved OIDs |
| a reference written as a name or as an OID | references resolve against the side's own definitions |
| a matching rule written as a name or as an OID | rules resolve through that side's `matchingRules` |
| the case of a syntax OID | syntax OIDs are case-folded |
| an omitted `USAGE` or class kind | the default is written out, so absent equals `userApplications` / `STRUCTURAL` |
| the case of an extension's keyword | extension keywords are upper-cased |

Everything else is meaningful and compared: `obsolete`, `sup`, the three
matching rules, `syntax` and its length bound, `singleValue`, `collective`,
`noUserModification`, `usage`, a class's `kind`, `must` and `may`, and the exact
text of `desc`. The values of an extension keep their order.

Each field that differs is reported with its category:

| Category | Fields | Effect |
|---|---|---|
| `core` | every field above except `desc` | the definition is `modified` |
| `description` | `desc` | the definition is `modified`; the change is a person's text |
| `extension` | any `X-` keyword | alone, the definition is `metadata_only` |

A keyword Alder does not know that is not an `X-` extension is recorded in
`unrecognized`, and makes that definition `unknown` unless both sides wrote
identical text: it could change what the definition means.

## Vendor metadata

`X-` extensions are server metadata, not schema semantics. A difference only in
them is **`metadata_only`**: reported, counted separately, and **never proposed
as a change**.

The two servers record provenance differently, which is why this matters:

- 389 DS adds `X-ORIGIN` to every definition, and `X-ORIGIN 'user defined'` to
  one added at runtime.
- OpenLDAP discards `X-ORIGIN` when it loads a schema file, and says where a
  definition came from by which configuration collection holds it.

So the harness's own schema, installed identically on both, compares with no
core difference and a handful of `metadata_only` ones. When Alder writes a
definition it leaves `X-ORIGIN` and `X-SCHEMA-FILE` out, and the server records
its own; other `X-` extensions are kept.

## Comparing

The direction is **SOURCE → TARGET**, as for data: `added` is defined in the
target and not the source, `removed` in the source and not the target. Kinds:
`added`, `removed`, `modified`, `metadata_only`, `unchanged` and `unknown`.

- `unknown` is a definition Alder could not understand on at least one side:
  unparsed, ambiguous by name, or with an unrecognised keyword. It is never
  treated as absent.
- A side with anything unparsed makes the comparison **incomplete**
  (`schema_partial`). An incomplete comparison proposes no removal: absence
  might be a definition that failed to parse.
- Two schemas from different products are compared, and `crossVendor` says so.
  Built-in schema differs between products for that reason alone; the
  harness-defined arc is what the conformance suite compares.

The response keeps the data-shaped `counts`, with `metadata_only` counted as
`unchanged`, and adds a `schema` section: counts per element kind, every item
with its field changes, references and problems, and `order`. Exit status from
`alder diff` follows the data rules: 0 equal, 1 differs, 2 incomplete or
unknown; a `metadata_only` difference alone is 0.

## Dependencies and order

Each item lists what its definition refers to (`requires`: `sup`, `must`,
`may`, resolved to OIDs) and, for removals and modifications, what refers to it
in the source (`requiredBy`). `order` lists the items a change could be derived
for, in the order they can be applied:

- for additions and modifications, what a definition needs comes first — a
  parent before its child, an attribute type before the class that names it;
- for removals, what refers to a definition goes first — the class before the
  attribute type it names;
- removals come after everything else.

Ties break deterministically: attribute types before object classes (object
classes first among removals), then by OID. `order` is never lexical, and a
dependency cycle is not ordered at all: its items carry `dependency_cycle` and
offer no change. A reference to nothing on the target side is
`unresolved_reference`, and blocks that change.

## Which differences become changes

When the **source is the live schema**, each item carries a `candidate`: the
same modifications of the schema entry the schema editor builds, through the
same code, with the value being removed read back from the server as it stores
it.

| Difference | Candidate |
|---|---|
| `added` | an add of the target's definition to the schema entry |
| `modified` | a replace of the definition the server holds with the target's |
| `removed` | a delete of the definition, `destructive: true` |

A candidate is **blocked** when:

| `blocked` | Why |
|---|---|
| `source_not_live` | both sides are snapshots |
| `nothing_to_change` | unchanged |
| `unknown_difference` | the difference is unknown |
| `metadata_only` | only extensions differ |
| `non_numeric_oid` | the OID is not numeric |
| `dependency_cycle` | the definition is part of a `SUP` cycle |
| `unresolved_reference` | the definition names something the target does not define |
| `incomplete_comparison` | a removal, in an incomplete comparison |
| `schema_not_editable` | this connection found no writable schema location |
| `schema_target_required` | an addition, where the server has several schema entries and none was chosen (`live.schemaTarget`) |
| `server_defined` | a modification or removal of a definition Alder cannot show was added by an administrator |
| `unbuildable_definition` | the change could not be built, for example the server no longer holds the value |

**Actionability rests on what the server states, never on a guess about what is
"custom".** Where schema lives in configuration collections, a definition is
changeable when a readable collection holds it, and the change is aimed at that
collection. Where the subschema entry is directly writable, a definition is
changeable when the server marked it `X-ORIGIN 'user defined'`. Anything else is
`server_defined`. An OID arc says nothing about origin, so it is not consulted.
The server may still refuse a change Alder offers; the plan and apply report it.

A removal's candidate also carries its **impact**:

- `referenced_by_schema` — a source definition still refers to it;
- `used_by_entries` — a search found an entry using it;
- `usage_unknown` — no such entry was found, or the search was refused or not
  run. **Alder never reports a definition as unused.** Not finding an entry,
  as an identity that may not see every entry, is not evidence that none exists.
  At most 50 such searches run per comparison.

### Selection and the plan

**Nothing is selected by default.** "Select every non-destructive change shown"
in the interface selects no removal, and each removal has its own checkbox; from
a shell, a removal is named with `--stage-deletion`. A selected change that
needs another is refused with `dependency_required` until that one is selected
too — Alder never adds it for you. Selected changes are staged in `order`,
whatever order they were chosen in.

The plan then checks the set again, against the live schema, in the order it
would apply (see [PLAN.md](PLAN.md)):

- `dependency_required` — a definition names another that neither the live
  schema nor an earlier change in the set defines;
- `referenced_by_schema` — a definition is removed while the schema, less what
  the set removes, still names it.

Both are conflicts: nothing is reordered or added on anyone's behalf.

## Security

- Every string in a schema snapshot or a comparison — `NAME`, `DESC`, extension
  values, definition text — is directory-controlled. The interface renders it
  through the same escaping as other directory text, and the command line
  escapes control and bidirectional characters. Neither changes the stored or
  sent string.
- A schema snapshot is untrusted input and is read strictly, as above. A
  document whose fields disagree with its definition text is refused rather than
  believed.
- Collections are bounded (50,000 definitions); a definition's text is bounded
  (64 KiB); inheritance chains are checked for cycles before anything walks them.
- A schema snapshot holds no credential, no server address and no configuration.

## Performance

Measured on synthetic schemas (four attribute types to each object class, in
supertype chains, `go test -bench Schema ./internal/diff/`, i7-11370H):

| Definitions | Capture (parse + build) | Encode + strict decode | Compare + order | Derive every change |
|---|---|---|---|---|
| 1,000 | 30 ms | 37 ms | 48 ms | < 1 ms |
| 5,000 | 119 ms | 433 ms | 312 ms | < 1 ms |

The interface filters and pages a comparison 50 items at a time.

## Not in this version

- **Schema recovery.** A recovery bundle still does not cover schema changes.
- **Configuration snapshots.**
- **Comparing context elements** — syntaxes, matching rules and the rest are
  read, not compared or changed.
- **`dITStructureRules`.**
- **Storage, history, scheduling, Git, automatic synchronisation.**

Schema snapshots were introduced in Alder 1.10. Later Alder 1.x releases will
continue to read schema snapshot kind/version 1. See
[COMPATIBILITY.md](COMPATIBILITY.md).
