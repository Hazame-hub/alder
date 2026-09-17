# Migration preflight

A preflight answers one question:

> Given this artifact and the directory I am connected to, what would carry
> across, and what would not?

The artifact is something you hold -- a change package, a schema snapshot or a
data snapshot, made on this directory or any other. The directory is live. The
answer is a report: finding by finding, what the target already holds, what it
could take, what needs something done first, what contradicts it, what it
cannot represent, and what could not be seen.

A preflight is **analysis and nothing else**. It:

- never writes to the directory -- the session it is given has no write method,
  and the conformance suite captures the whole directory before and after every
  mode and requires the same documents;
- never changes the artifact -- the bytes you send are the bytes it reads;
- never rewrites a DN, maps an attribute or an object class, converts a syntax,
  drops a value, translates an ACL or migrates a password;
- produces nothing a plan or an apply reads -- no baseline, no plan token, no
  prepared change.

```
artifact ──▶ preflight ──▶ report ──▶ your decision
                                          │
          (a package, a comparison) ◀─────┘
                    │
                    ▼
     intent ──▶ plan ──▶ review ──▶ apply
```

Whatever you decide to do about a report goes through the path every change
takes: open the package in Packages and validate it, or compare snapshots and
stage what you select, then plan, review and apply. A preflight is never a
step inside that path.

## Why it is not package validation

Package validation (1.11) asks: *can these package items be interpreted
against this target right now?* It prepares the change requests a plan would
take. A preflight asks something broader: *what of this source survives on
this target?* For a package, validation is the first step and is not repeated
-- its status for each item is carried on every finding about that item, as
`validationStatus` -- and the preflight adds what validation does not ask:

- whether the target publishes the syntaxes and matching rules a definition
  needs;
- whether an entry meets the MUST, may-hold and SINGLE-VALUE rules of the schema
  it would meet, with the package's own definitions in place;
- what its DN-valued attributes name, and whether those entries are there;
- which of its attributes the target generates or owns;
- why one item cannot carry across because another cannot.

## Inputs

| Artifact | Recognised by | What is judged |
|---|---|---|
| Change package | `format: alder-change-package` | Every item: schema intent, then data, in dependency order |
| Schema snapshot | `format: alder-snapshot`, `kind: schema` | Every attribute type and object class it holds |
| Data snapshot | `format: alder-snapshot`, `kind: data` | Every entry it holds |

The artifact is recognised by what it says it is, never by a file name, and is
decoded as strictly as its own endpoints decode it: an unknown field, a checksum
that does not match, or a version this release does not read is refused with
the same error code (`package_checksum_mismatch`, `snapshot_unsupported_version`
and so on). A document that is none of the three is refused with
`preflight_artifact_unsupported`. Arbitrary LDIF is not read; make a package or
a snapshot from it first. A **configuration** snapshot (1.13, `kind: config`)
is not a preflight artifact: configuration is provider-specific, and what a
preflight would have to say about it is exactly what the exclusion below already
says. See [CONFIG-SNAPSHOTS.md](CONFIG-SNAPSHOTS.md).

The source is always an artifact and the target always live. Alder does not
connect to two directories at once, and a preflight needs no connection to the
directory an artifact came from.

A snapshot is **state, not intent**. An entry a data snapshot does not hold is
not one to remove, and an entry only the target holds is not a finding. The
target's own definitions that a schema snapshot does not hold are not findings
either. A preflight asks what would carry across, never what the target would
lose.

## The report

```json
{
  "reportVersion": 1,
  "source":  { "type": "change_package", "id": "…", "checksum": "sha256:…", "integrity": "verified", "vendor": "OpenLDAP", "objects": 4 },
  "target":  { "vendor": "389 Project", "namingContexts": ["dc=alder,dc=test"], "schemaWritable": true, "crossVendor": true },
  "overall": "compatible_with_prerequisites",
  "complete": true,
  "reasons": [],
  "counts": { "portable": 3, "alreadySatisfied": 1, "prerequisiteRequired": 1, "incompatible": 0, "unsupported": 0, "unknown": 0, "excluded": 0 },
  "sections": [ { "category": "schema", "counts": { … } } ],
  "capabilities": [ { "capability": "schema_write", "available": true, "requiredBy": "schema definitions", "explanation": "…" } ],
  "notEvaluated": [ { "area": "access_control", "reason": "…" }, { "area": "server_configuration", "reason": "…" }, { "area": "secret_values", "reason": "…" } ],
  "findings": [ … ],
  "truncated": false
}
```

### Findings

Every finding has:

| Field | Meaning |
|---|---|
| `id` | `f1`, `f2`, … assigned after sorting, so the same artifact against the same target state gives the same ids |
| `code` | A stable identifier; see the table below |
| `classification` | What it concludes about one source object |
| `category` | The report section: `artifact`, `schema`, `naming`, `entries`, `references`, `capabilities`, `operational`, `sensitive` |
| `scope` | What it is about: `artifact`, `item`, `definition`, `entry`, `attribute`, `value` |
| `source` | The source object: `item`, `element`, `oid`, `name`, `dn`, `attribute`, `value` |
| `target` | What the target showed: a stable `fact` and a `detail` |
| `explanation` | For a person |
| `blocksPortability` | While it stands, the object cannot carry across |
| `blocksPlan` | A plan made now for the change it concerns would refuse it |
| `manualAction` | You have something to do that Alder will not do for you |
| `prerequisites` | What has to be true first, as data: `{type: schema, oid}`, `{type: entry, dn}`, `{type: syntax, oid}` … with `providedBy` naming the finding of the source object that supplies it |
| `causes` | The ids of the findings that explain this one |
| `count` | How many source objects an aggregated finding stands for |
| `validationStatus` | For a package item, what package validation concluded |

### Classifications

| Classification | Meaning |
|---|---|
| `portable` | The target can take it, once anything the same source supplies is in place |
| `already_satisfied` | The target already holds it, with the same meaning |
| `prerequisite_required` | It can carry across once something else is done first, and the finding says what |
| `incompatible` | The target holds something that contradicts it |
| `unsupported` | The target cannot represent it at all |
| `unknown` | It could not be decided from what this bind can see |
| `excluded` | It does not travel by design -- a server-generated value, a withheld secret, a server's own definition -- and does not by itself stop anything else |

### Overall

`overall` is derived from the findings and never set on its own:

1. **`incompatible`** if any finding that blocks portability is incompatible or
   unsupported. A definite contradiction is an answer however much else is
   unknown.
2. otherwise **`incomplete`** if anything is unknown, or the report was
   truncated, or the target could not be read. The thing not known might be the
   thing that does not fit, so this is never a pass.
3. otherwise **`compatible_with_prerequisites`** if anything needs a
   prerequisite or a manual action -- including a secret that has to be set
   separately.
4. otherwise **`compatible`**.

`complete` is false, and `reasons` lists stable codes, whenever something could
not be decided. There are no scores and no percentages. An artifact with nothing
in it is `incomplete` with `source_empty`: nothing was evaluated, and a pipeline
must not read that as a pass.

### Causes

A report explains a chain rather than listing symptoms. A package that adds an
attribute type, an object class that needs it, and an entry that uses the class,
against a target that already defines the attribute type's OID with a different
meaning, gives:

```
schema   definition_conflict  incompatible  c1 attributeType 1.3.6.1.4.1.99999.40.1  (singleValue differs)
schema   dependency_blocked   incompatible  c2 objectClass alderStaff                because of the conflict
entries  dependency_blocked   incompatible  c3 uid=pat,… uses alderStaff            because of c2's finding
entries  entry_blocked        incompatible  c3 uid=pat,…                            because of the line above
```

The links are ids in `causes`, bounded (16 per finding, 32 levels deep), and a
cycle in a source's definitions is reported as `dependency_cycle` rather than
followed.

## Finding codes

### Schema

| Code | Classification | When |
|---|---|---|
| `definition_portable` | portable | The target does not define the OID, and everything the definition names and needs is on the target or supplied by the source |
| `definition_present` | already_satisfied | The target defines the OID with the same meaning. Provenance extensions (`X-ORIGIN`, `X-SCHEMA-FILE`) alone never make a difference |
| `definition_extensions_differ` | already_satisfied | Same meaning, different non-provenance extensions -- `X-ORDERED` for example -- which can change how a server behaves, reported separately |
| `definition_description_differs` | already_satisfied | Same meaning, different `DESC` |
| `definition_conflict` | incompatible | The target defines the OID with a different meaning; `target.detail` names the fields |
| `name_conflict` | incompatible | The target uses the name for a definition with another OID |
| `syntax_unavailable` | unsupported | The target publishes syntaxes and not this one. A syntax is implemented by a server and cannot be added as a definition |
| `syntax_unknown` | unknown | The target publishes no syntaxes to check against |
| `matching_rule_unavailable` | unsupported | The target publishes matching rules and not this equality, ordering or substring rule |
| `matching_rule_unknown` | unknown | The target publishes no matching rules |
| `undefined_reference` | prerequisite_required | A SUP, MUST or MAY names something neither the target nor the source defines |
| `dependency_blocked` | the cause's | Something it names is supplied by the source and cannot itself carry across |
| `dependency_cycle` | unknown | Its dependencies loop, or run too deep to follow |
| `schema_not_writable` | unsupported | It would have to be added and this connection cannot write the schema |
| `schema_target_required` | prerequisite_required | The target keeps schema in several entries and none was chosen (`schemaTarget`) |
| `source_server_defined` | excluded | A schema snapshot's own server supplied the definition, and the target lacks it or defines it differently. A server's built-in definitions are not carried across |
| `definition_unknown` | unknown | It could not be compared: unparsed, ambiguous, or an unrecognised keyword |
| `change_portable`, `change_already_satisfied`, `change_not_applicable` | … | A package item that removes a definition |

Which snapshot definitions a server supplied is read from what the snapshot
recorded, never from the vendor: a server that keeps schema in configuration
collections records the collection of each definition an administrator loaded,
and one with a single subschema entry marks the ones an administrator added
with `X-ORIGIN 'user defined'`.

### Naming

| Code | Classification | When |
|---|---|---|
| `naming_context_mismatch` | incompatible | The DN is outside every naming context the target holds |
| `invalid_dn` | incompatible | The DN, or a DN-valued value, does not parse |
| `parent_missing` | prerequisite_required | The parent is not on the target, as the server reports it to this bind, and the source does not supply it |
| `parent_unknown` | unknown | The parent exists and this bind may not read it, or it could not be read |

### Entries

| Code | Classification | When |
|---|---|---|
| `entry_portable`, `change_portable` | portable | The target can take it |
| `entry_present_equivalent`, `change_already_satisfied` | already_satisfied | The target already holds it, compared by each attribute's matching rule |
| `existing_entry_differs` | prerequisite_required | The target has an entry at that DN with different content; `target.detail` names the attributes. Whether to change it is a decision for a plan |
| `object_class_missing`, `attribute_type_missing` | prerequisite_required | The target does not define it and the source does not supply it |
| `attribute_not_allowed`, `missing_required_attribute`, `single_value_violation` | incompatible | The schema the entry would meet refuses its content |
| `entry_blocked` | the worst of its causes | The entry cannot carry across; `causes` says why |
| `entry_unknown` | unknown | What the target holds at that DN could not be decided |
| `change_not_applicable` | incompatible, unsupported or prerequisite_required | A package change the target cannot take as written |

### References

Attributes are recognised as references by their syntax on the target -- DN,
or Name and Optional UID -- never by a list of names.

| Code | Classification | When |
|---|---|---|
| `reference_ready` | portable | The named entry is on the target (aggregated per attribute, with `count`) |
| `reference_provided_by_source` | portable | The same source supplies it |
| `reference_missing` | prerequisite_required | It is not on the target as the server reports it to this bind, and the source does not supply it |
| `reference_unknown` | unknown | It exists and this bind may not read it, or it could not be read, or the read budget was spent |
| `reference_outside_naming_contexts` | incompatible | It names a DN outside every naming context the target holds |

### Operational and sensitive data

| Code | Classification | When |
|---|---|---|
| `ignored_operational` | excluded | An operational attribute in a snapshot. Servers generate or maintain it; it is not migrated |
| `server_owned_attribute` | excluded (snapshot) or unsupported (package) | `NO-USER-MODIFICATION` on the target. A package that tries to write one cannot |
| `target_generated` | excluded | Entries carry identities their server assigned (`entryUUID`, `nsUniqueId`); the target assigns its own |
| `sensitive_value_not_migratable` | excluded, manual action | A withheld password or other sensitive value, or a secret a package was made without |

Operational is decided from the target's schema -- usage and
`NO-USER-MODIFICATION` -- and from what the snapshot recorded about the source's,
not from a list of names. A migrated entry is **not byte-identical** to its
source: timestamps, identities, replication metadata and every other value a
server maintains are the target's own.

Passwords are never compared, hashed, copied or invented. A report says they
must be set on the target separately, and lists `password_modify` among the
capabilities when a source withheld one, so you know how Alder would set it.

### Capabilities

A capability is listed only when the source needs it -- the report never
penalises a target for a capability this artifact does not use.

| Capability | Needed when |
|---|---|
| `schema_write` | The source has definitions the target lacks |
| `paged_results` | A data snapshot is compared with the target's entries under the same base |
| `password_modify` | A source withheld passwords, which must be set separately |

### Artifact

| Code | Classification | When |
|---|---|---|
| `source_partial` | unknown | A schema snapshot holds definitions its capture could not parse |
| `source_empty` | unknown | The artifact holds nothing to evaluate |
| `report_truncated` | (reason) | More than 100 000 findings |

## Naming contexts, and why nothing is rewritten

A package made on `dc=dev,dc=example,dc=com` preflighted against a target that
holds `dc=example,dc=com` reports `naming_context_mismatch` for each entry, and
stops there. It does not propose `dc=dev,dc=example,dc=com → dc=example,dc=com`.

An implicit mapping that caught the DNs and missed a `member`, a `manager`, a
`seeAlso` or a `uniqueMember` would be a silent corruption, and one that caught
all of them would be a transformation engine with a preview and a model for every
reference. Neither is analysis. If Alder ever supports mappings they will be
explicit, reviewed transformations of the source -- not something a preflight
does on the way to an answer.

## Unknown is not missing

A directory hides entries from binds that may not read them, and servers do it
differently:

- **389 Directory Server** answers a read of a hidden entry with no entries and
  a Compare with *insufficient access* -- an admission that it exists. The
  preflight reports `parent_unknown` or `reference_unknown`, with
  `target.fact: hidden`.
- **OpenLDAP** answers both with *no such object*, exactly as for a missing
  entry. Nothing a bind can ask tells the two apart. The preflight reports
  `parent_missing` or `reference_missing`, and the explanation says it is "as the
  server reports it to this bind" rather than stating it as fact.

Either way the report is not `compatible`: a hidden prerequisite is unknown, and
a concealed one is a prerequisite. The probe is a Compare on `cn`, never on
`objectClass` -- OpenLDAP refuses a compare of an OID-syntax attribute with any
other value before it looks at the entry, which would make every absent entry
unknown. Probes are bounded (500 per preflight), and past the bound an unread
entry is unknown.

## Cross-vendor results

A vendor name decides nothing. `target.crossVendor` is reported as metadata;
every finding rests on a concrete difference. Measured against the harness:

| Artifact | OpenLDAP → 389 DS | 389 DS → OpenLDAP |
|---|---|---|
| A package with custom schema, an entry using it and a group naming the entry and a seeded user | `compatible`: 2 definitions portable, 2 changes portable, references ready and supplied | `compatible`: the same |
| A data snapshot of the seeded groups | `compatible`: 11 entries present and equivalent | `compatible`: the same |
| A full schema snapshot | `incompatible`: 103 definitions already present, 12 portable, 80 incompatible, 7 unsupported, 208 excluded | `compatible`: 103 already present, 1126 of 389 DS's own definitions excluded |

The asymmetry in the last row is real, not a vendor rule. OpenLDAP keeps every
loaded schema file -- `core`, `cosine`, `inetorgperson`, `nis` -- in configuration
collections, so its snapshot records them as loaded by an administrator, and
where 389 DS defines the same OIDs differently (syntax lengths, equality rules,
the MAY list of `groupOfNames`, the password policy attribute names) that is a
concrete conflict. 389 DS marks its own definitions as its own, so they are
excluded. Preflighting your own definitions -- a package, or the definitions you
added -- is the useful question; a whole schema compared with another vendor's
mostly measures how the two ship the standards.

## Command line

```sh
alder preflight package.json          [connection flags]
alder preflight schema.json           [connection flags]
alder preflight data.json --json      [connection flags] > preflight.json
cat package.json | alder preflight -  [connection flags]
```

One command for all three artifacts, recognised by the server. The client reads
the file, refuses what is not JSON before sending anything, sends the bytes as
they are, and prints the report: the overall result, per-section counts, the
capabilities the artifact needs, every finding that needs attention (`--all`
lists every finding), and what was not evaluated. `--json` writes the report
exactly as Alder answered. `--schema-target` names the schema entry for a
package's definitions where the server keeps several.

| Exit | Meaning |
|---|---|
| 0 | `compatible` |
| 1 | `compatible_with_prerequisites` |
| 2 | `incomplete` |
| 3 | `incompatible` |
| 7 | Usage |
| 8 | Refused or failed (a malformed or forged artifact, a connection failure) |

A pipeline that rejects a promotion before anyone opens the interface:

```sh
alder preflight release-package.json --json > preflight.json
case $? in
  0|1) echo "portable";;
  *)   echo "not portable: see preflight.json"; exit 1;;
esac
```

## Web interface

The **Preflight** tab opens an artifact, runs the preflight, and shows the
overall result, per-section counts (select one to filter), the capabilities
needed, and the findings -- filterable by section, status and text, with "only
what needs attention" on by default. A finding expands to its target fact, its
prerequisites and its chain of causes, each linking to the finding it names.
There is no *migrate*, *apply* or *stage* button: to act, open the package in
Packages or compare snapshots, and plan there.

## Not evaluated

Every report lists these, because pretending they do not exist would make a
`compatible` mean more than it does:

- **Access control.** `aci` values and `olcAccess` rules are neither read nor
  translated. An entry that carries across may not be readable or writable by the
  same people on the target.
- **Server configuration.** Overlays, plugins, password policy, limits, indexes,
  referential integrity and every other setting the source relies on. 1.13
  captures and compares configuration, but only ever within one server's own
  software; whether one server's configuration is compatible with another's is
  not evaluated here or anywhere else in Alder.
- **Secret values.** Passwords and other sensitive values are never in an
  artifact.

## Limits

The artifact is untrusted and the report is bounded however large or hostile it
is: the request body is 16 MB, the artifact's own limits apply (50 000 snapshot
entries or schema definitions, 2000 package changes), a report holds at most
100 000 findings (past that it is `truncated` and incomplete), 16 causes and 16
prerequisites per finding, explanations of 1024 characters and target facts of
512, dependency chains followed 32 levels deep, 50 per-value reference findings
per attribute (the rest counted), 2000 reference reads and 500 presence probes
per preflight, and 5000 entry reads when a data snapshot's base could not be
captured in one read. Text from the artifact is carried as data and shown with
control and bidirectional characters escaped.

## Performance

Measured with `go test -run '^$' -bench Preflight -benchmem ./internal/preflight/`
against an in-memory target:

| Artifact | Time | Allocated | Reads | Report |
|---|---|---|---|---|
| Package, 100 changes | 4 ms | 1.9 MB | 183 | 72 KB |
| Package, 1000 changes | 51 ms | 18 MB | 1803 | 707 KB |
| Data snapshot, 1000 entries (half on the target) | 63 ms | 26 MB | 1 capture | 545 KB |
| Data snapshot, 10 000 entries | 734 ms | 299 MB | 1 capture | 5.5 MB |
| Schema snapshot, 1000 definitions | 62 ms | 23 MB | -- | 502 KB |

The data snapshot's time is dominated by decoding the snapshot and the target's
capture, both linear. A preflight reads the target's entries under the snapshot's
base once, in pages, and answers presence, difference and most references from
that capture rather than with a read per entry. The schema comparison a preflight
runs skips the dependency ordering a staged comparison needs, which was the one
quadratic step (483 ms before, 62 ms after, for 1000 definitions).

## Not in this version

Automatic migration or transformation, DN rewriting, attribute or object class
mapping, syntax conversion, ACL conversion, configuration migration, password
migration, secret injection, connecting to two directories at once, an
environment registry, deployment history, Git integration, a provider
abstraction, Active Directory, Entra ID, automatic apply, and a policy engine.
