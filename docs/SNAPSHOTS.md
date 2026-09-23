# Snapshots and comparisons

A snapshot answers:

> What did this part of the directory hold, at a moment I can name?

A comparison answers:

> What differs between two states, in which direction, and how sure is that?

Alder captures a subtree as a **snapshot**, a versioned JSON document you keep.
It compares a snapshot with another snapshot, or with the directory as it is
now, and explains every difference. When one side is the live directory, the
differences you select become change requests. Those go through the plan, like
every other write. A comparison never writes anything.

Nothing is stored on the server. A snapshot exists only as the file you
download and upload.

In the interface this is the **Snapshots** tab. Over HTTP it is three
endpoints: `POST /snapshots/capture`, `POST /snapshots/inspect` and `POST /diff`.
`api/openapi.yaml` is the reference for their shapes. From a shell it is
`alder snapshot` and `alder diff`, which call those endpoints and write the same
files; see [CLI.md](CLI.md).

Since 1.10 the same endpoints also capture and compare the **schema** a server
publishes, as a snapshot of `kind: schema`. That document, its comparison and
how its differences become changes are described in
[SCHEMA-SNAPSHOTS.md](SCHEMA-SNAPSHOTS.md). Since 1.13 they also capture a
server's own **configuration**, as `kind: config`, which is provider-specific
and is only ever compared with another capture of the same server software; see
[CONFIG-SNAPSHOTS.md](CONFIG-SNAPSHOTS.md). This page is about data
snapshots.

---

## The format

```json
{
  "format": "alder-snapshot",
  "version": 1,
  "kind": "data",
  "createdAt": "2026-09-13T21:04:05Z",
  "source": {
    "vendor": "389 Project",
    "vendorVersion": "389-Directory/3.1.1",
    "base": "ou=people,dc=alder,dc=test",
    "scope": "sub",
    "filter": "(objectClass=*)"
  },
  "operationalAttributes": false,
  "schemaAvailable": true,
  "excluded": ["operational-attributes", "sensitive-values"],
  "completeness": "complete",
  "entryCount": 2,
  "attributes": [
    { "name": "cn", "equality": "caseIgnoreMatch", "syntax": "1.3.6.1.4.1.1466.115.121.1.15" },
    { "name": "userPassword", "equality": "octetStringMatch", "sensitive": true }
  ],
  "checksum": "sha256:…",
  "entries": [
    {
      "dn": "uid=alice,ou=people,dc=alder,dc=test",
      "id": "nsUniqueId=5f3c…",
      "attributes": [
        { "name": "objectClass", "values": [{ "text": "inetOrgPerson" }, { "text": "person" }, { "text": "top" }] },
        { "name": "cn", "values": [{ "text": "Alice" }] },
        { "name": "userPassword", "withheld": 1 }
      ]
    }
  ]
}
```

| Field | Meaning |
|---|---|
| `format`, `version` | Always `alder-snapshot` and, for now, `1`. |
| `kind` | `data`. A schema snapshot (1.10) is `kind: schema` and a configuration snapshot (1.13) is `kind: config`, each a separate document with its own fields; see [SCHEMA-SNAPSHOTS.md](SCHEMA-SNAPSHOTS.md) and [CONFIG-SNAPSHOTS.md](CONFIG-SNAPSHOTS.md). A kind is only ever compared with its own. |
| `source` | Where it was captured: the server's announced vendor and version (for display, and possibly absent), the base, the scope and the filter. It never contains the bind DN, credentials, the server address or any local path. |
| `operationalAttributes` | Whether operational attributes were captured. |
| `schemaAvailable` | Whether the server's schema was readable at capture time. Without it every value compares byte for byte. |
| `excluded` | What was deliberately left out: `sensitive-values` always, `operational-attributes` unless they were asked for. |
| `completeness` | `complete`. There is no other value (see [Never partial](#never-partial)). |
| `attributes` | What the schema said about each attribute present: equality rule, syntax, single-valued, operational, sensitive. A comparison uses this, so a snapshot compares the same way wherever it is read. |
| `checksum` | See [The checksum](#the-checksum). |
| `entries[].id` | The entry's stable identity, `entryUUID=…` or `nsUniqueId=…`, when the server provides one. |
| `entries[].attributes[].values` | Each value is `{"text": …}` when it is valid UTF-8 with no control characters, otherwise `{"base64": …}`. |
| `entries[].attributes[].withheld` | For a sensitive attribute: how many values it held. No value is ever written. |

### Canonical, so the same state is the same document

Two captures of an unchanged directory are identical except for `createdAt`:

- entries are ordered parent before child, then by name, case folded;
- attributes are ordered with `objectClass` first, then by name;
- values are ordered by their comparison key under the attribute's equality
  rule (see [Equality](#equality)), then by their bytes;
- DNs are rendered through Alder's DN parser, so the same name is spelled one
  way;
- attribute names use the schema's canonical spelling;
- the file is indented JSON, one value per line, so a snapshot kept in version
  control diffs readably.

Value order carries no meaning for the data a version 1 snapshot holds. The
configuration attributes where order does matter (`X-ORDERED`) belong to a kind
version 1 does not capture.

### What is left out

- **Sensitive values, always.** `userPassword`, `unicodePwd`,
  `nsslapd-rootpw` and the rest of the deny list in `internal/schema` are
  recorded as a count and nothing else. This is not a hash. A hash of a password
  in a file you share is an offline guessing oracle, and a count is enough to say
  "Alice's password changed from one value to two".
- **Operational attributes, by default.** These are the attributes the schema
  marks as operational or `NO-USER-MODIFICATION`: `modifyTimestamp`, `entryCSN`,
  `creatorsName` and so on. They change on every write, so including them makes
  every entry differ. `operationalAttributes: true` captures them.
- **Identity attributes as state.** `entryUUID` and `nsUniqueId` are read so an
  entry's `id` can be recorded. They appear as attributes only when operational
  attributes are captured.

**Is a snapshot safe to commit?** It contains no password or other deny-listed
value, no credential and nothing about the connection. It does contain every
other value the bind DN could read: names, mail addresses, group memberships,
and any secret your directory keeps in an attribute that is not on the deny
list. Whether that belongs in a repository is your decision to make about your
data. Alder does not claim a snapshot is free of secrets it cannot recognise.

### The checksum

`checksum` is SHA-256 over the canonical snapshot payload: every field except
`createdAt` and `checksum` itself. Two captures of the same state therefore
carry the same checksum. Precisely:

- **Changing captured content invalidates it.** That means any entry, attribute,
  value or withheld count, and any covered metadata: `format`, `version`,
  `kind`, `source`, `operationalAttributes`, `schemaAvailable`, `excluded`,
  `completeness`, `entryCount` and `attributes`.
- **Changing `createdAt` does not.** The capture time is deliberately outside
  the checksum, so identical state gives identical checksums.
- **Reformatting or reordering does not.** The checksum is computed over the
  canonical form, so re-indenting the file, or reordering its entries,
  attributes or values, changes nothing it covers.

It is an **integrity check against corruption**. It is **not authentication and
not a signature**: it proves nothing about who made the file, because anyone who
edits one can recompute it. On reading:

| The document… | Result |
|---|---|
| carries a checksum that matches | `integrity: verified` |
| carries no checksum | `integrity: unverified`, still usable |
| carries a checksum that does not match | refused, `snapshot_checksum_mismatch` |

A snapshot edited on purpose can be used by deleting its `checksum` field. The
comparison then reports that side as `unverified`.

### Never partial

A snapshot is either the whole of what was asked for or it does not exist.
Capture reads every page of a paged search. If the server stops early at a size
limit, returns a referral, or the result exceeds 50,000 entries, capture fails
with `snapshot_too_large` and no document is produced: narrow the base or the
filter. Any other search failure fails the capture with that error.

An entry the bind DN cannot see is not in the search results. It is therefore
not in the snapshot, and nothing can distinguish it from an entry that does not
exist. This is how LDAP access control works, not something Alder can detect.
Capture with an account that can read what you want to compare.

### Reading a snapshot

A snapshot is untrusted input. Reading one executes nothing, fetches nothing,
writes nothing and never touches the directory. `POST /snapshots/inspect` is a
pure read. A document is refused with `400` and one of these codes:

| Code | Why |
|---|---|
| `snapshot_invalid` | Not an Alder snapshot, or invalid: malformed JSON, trailing content, an unknown field, a DN that does not parse, an entry outside the stated base and scope, the same entry twice, a sensitive attribute with values, a count that does not match. |
| `snapshot_unsupported_version` | A `version` this Alder does not read. |
| `snapshot_checksum_mismatch` | See above. |
| `snapshot_too_large` | More than 50,000 entries. |
| `snapshot_scope_unsupported` | A *data* capture, or the live side of a data comparison, whose base is under the schema or configuration tree. Each has its own kind. |
| `config_model_unavailable` | A configuration capture where the server announced no configuration tree, this session cannot read it, or Alder has no model for that server (1.13). |

**Unknown fields are refused**, not ignored. A field this version does not know
might change what the document means, and reading it as if it were absent could
produce a comparison that is wrong without saying so.

Requests to the server are limited to 16 MB. A snapshot's compact form is
roughly 500 bytes per ordinary entry: 10,000 people in one group come to 5 MB,
or 12 MB for the indented file. So one snapshot of up to about 30,000 entries
can be compared with the live directory, or two of about 15,000 with each
other. A larger capture can still be downloaded. The interface checks the size
before sending and says why it will not.

---

## Comparing

`POST /diff` takes a `source` and a `target`. Each is exactly one of
`{"snapshot": …}` or `{"live": {…}}`, and at most one side may be live.

Comparing two snapshots needs no session: the comparison reads only the two
documents, so it is answered without a directory connection and opens none. It
is still an ordinary request, held to the same size limit, in-flight limit and
timeout, and its snapshots are read as strictly as ever. A live side needs a
session, and without one the request is refused before either snapshot is read.

A live side captures the directory now, the same way a snapshot is captured.
By default it uses the other side's base, scope, filter and operational setting,
so "this snapshot against now" compares like with like. Any of those can be
given explicitly.

### Direction

A comparison always reads **from source to target**:

| Kind | Meaning |
|---|---|
| `added` | In the target and not in the source. |
| `removed` | In the source and not in the target. |
| `modified` | In both, with different attributes. Each attribute change lists the values `added` (in the target only) and `removed` (in the source only), up to 1,000 per attribute, with a count of the rest. |
| `renamed` | The same entry under a different DN. See [Renames](#renames). |
| `unchanged` | In both and equal. Listed only with `includeUnchanged: true`; always counted. |
| `unknown` | Could not be decided. It says why. |

To see what would bring the directory back to a snapshot, compare with the
directory as the **source** and the snapshot as the **target**. The differences
are then what has to happen to the directory.

### Equality

Two values are equal when the attribute's **equality matching rule** says so.
The rules Alder implements:

| Rule | Two values are equal when… |
|---|---|
| `distinguishedNameMatch`, `uniqueMemberMatch` | they parse to the same DN, case folded (and the same optional UID for `uniqueMember`) |
| `caseIgnoreMatch`, `caseIgnoreIA5Match`, `caseIgnoreListMatch` | they match case folded, with insignificant spaces collapsed |
| `caseExactMatch`, `caseExactIA5Match` | they match with insignificant spaces collapsed |
| `numericStringMatch` | they match with spaces removed |
| `telephoneNumberMatch` | they match case folded, with spaces and hyphens removed |
| `integerMatch` | they are the same integer |
| `booleanMatch` | they are the same boolean |
| `generalizedTimeMatch` | they are the same instant |
| `objectIdentifierMatch` | they match case folded |
| `octetStringMatch` | they are the same bytes |

These are Alder's implementations of the rules, not the server's. They follow
the RFC 4518 string preparation closely enough for directory data. They do not
perform full Unicode normalisation, so two spellings of the same accented name
in different normal forms compare as different.

An attribute is compared **byte for byte** when:

- a side has no schema;
- the rule is one Alder does not implement;
- the two sides name different rules for it.

The comparison says so, in `comparedByBytes` and `ruleDifferences`, rather than
implying a precision it does not have. Byte comparison can report a difference
that the server would call equal. It never reports equality the server would
not.

A **sensitive** attribute is compared by its withheld count alone. A password
replaced by another password is not detected. That is the price of never
capturing either one.

**Operational attributes** are compared only when both sides captured them.
Otherwise `operationalIgnored` is set.

### Renames

An entry that disappears from one DN and appears at another is a rename
**only** if both sides recorded the same `id` (the entry's `entryUUID` or
`nsUniqueId`). Without a shared identity it is a removal and an addition. Alder
does not guess from similar attributes.

An identity is per server, so a comparison between two different servers never
finds renames: the same person has a different `entryUUID` on each.

### Unknown is not absent

A comparison that could not see something says so instead of guessing.
`complete: false` carries `reasons`, and the affected items are `unknown`:

| Reason | When |
|---|---|
| `search_limit_reached` | The live read stopped before the end. An entry the snapshot has and the partial read lacks is `unknown`, not `removed`. |
| `scope_mismatch` | The two sides have different bases, scopes or filters. An entry only one side could contain is `unknown`. |
| `insufficient_access` | The live directory reports that the bind DN cannot read an attribute the snapshot holds. That attribute is `unknown`, not removed. |
| `access_not_verified` | Alder could not confirm whether the bind DN can read an attribute missing on the live side: the check failed, or more were needed than one comparison makes (500). Those attributes are `unknown`. |
| `schema_unavailable` | A side has no schema, so its values compare byte for byte. |

**A partial comparison never proposes a deletion.** Deleting an entry because a
search stopped early would destroy data Alder simply did not read.

### Between servers

A comparison is vendor-neutral. Values are compared by the rules above and
never by the product that stored them. `crossVendor` is set when the two sides
announce different vendors, or when one announces none (OpenLDAP does not).
Then:

- attributes one product adds and the other does not show as differences;
  capturing without operational attributes removes most of them;
- a schema that defines an attribute with a different equality rule compares
  that attribute byte for byte and lists it in `ruleDifferences`;
- renames are never found (see [Renames](#renames)).

The conformance suite seeds the same people into OpenLDAP and 389 DS and
checks that their snapshots compare with no differences.

---

## From differences to changes

When the **source is the live directory**, each difference carries a
`candidate`. This is the change requests that would move the directory toward
the target:

| Difference | Candidate |
|---|---|
| `added` | an `add` of the target's entry, without sensitive, operational or identity attributes |
| `modified` | one `modify`: for each attribute, a `delete` of the values the target lacks and an `add` of the values it has. An attribute absent from the target is deleted whole; one absent from the source is added whole. The values come from the full entries, not the 1,000 listed. |
| `renamed` | a `modrdn` to the target's RDN and parent, then a `modify` for any other attribute changes |
| `removed` | a `delete`, marked `destructive: true` |

A candidate is **not applied**. It is a list of the same change requests
`POST /plan` takes. The interface stages the selected ones into the changeset,
where they are planned against the directory as it is at that moment, reviewed
and applied like every other change. See [PLAN.md](PLAN.md). The directory may
have moved since the comparison. The plan re-reads it, and the apply refuses a
stale plan.

A candidate is `blocked`, with no changes, when:

| `blocked` | Why |
|---|---|
| `source_not_live` | Both sides are snapshots. There is no directory to change. |
| `incomplete_comparison` | A deletion, in a comparison that is not complete. |
| `unknown_difference` | The difference is `unknown`. |
| `nothing_to_change` | The entry is unchanged. |
| `only_unchangeable_attributes` | Everything that differs is sensitive, operational or identity. Alder never writes a password it never read. |
| `unresolvable_rename` | The target DN has no RDN to rename to. |

**Deletion is never inferred and never selected by default.** Selecting every
change on a page selects no deletions. Each deletion has its own
"include deleting this entry" checkbox. The plan then shows it, with its impact,
before anything is applied.

A change that sets a sensitive attribute cannot come from a comparison, because
a snapshot has no sensitive values. After a restore, passwords are whatever the
directory holds now.

---

## Not in version 1

- **Schema and configuration as data.** A data snapshot whose base is under the
  schema or configuration tree is refused with `snapshot_scope_unsupported`.
  Both have different identity and ordering rules, so each has a kind of its
  own: `schema` since 1.10 (see [SCHEMA-SNAPSHOTS.md](SCHEMA-SNAPSHOTS.md)) and
  `config` since 1.13 (see [CONFIG-SNAPSHOTS.md](CONFIG-SNAPSHOTS.md)).
  Configuration is provider-specific: a configuration is only ever compared
  with another capture of the same server software.
- **Storage.** Alder keeps no snapshots, no history and no schedule.
- **Intent.** A snapshot is a state, not a plan to reach one. What an operator
  means to change travels as a change package (1.11); see
  [CHANGE-PACKAGES.md](CHANGE-PACKAGES.md).
- **Compression, fuzzy rename detection, rollback, inverse LDIF.** Signing
  arrived in 1.15, outside the format: a signed document is an
  `alder-signed` envelope carrying the snapshot unchanged, so a snapshot's own
  checksum and every field still mean what they meant. See
  [SIGNING.md](SIGNING.md).

Compensating a change you are about to apply is not a snapshot's job. A
recovery bundle (1.9) is derived from the entries a change touches, immediately
before it runs, and its compensations go through the plan like these candidates
do. See [RECOVERY.md](RECOVERY.md). Restoring a snapshot is still not a feature:
a comparison proposes changes, and nothing restores a subtree wholesale.

Snapshot format version 1 was introduced in Alder 1.7. Every later 1.x release
will continue to read it. Schema snapshots were introduced in Alder 1.10 and
configuration snapshots in Alder 1.13; later Alder 1.x releases will continue to
read version 1 of both. A release older than the kind refuses the document as a
kind it does not know, rather than misreading it. See
[COMPATIBILITY.md](COMPATIBILITY.md).

## Preflight

A data or schema snapshot captured on one directory can be preflighted against
another (1.12): `alder preflight snapshot.json`, or the Preflight tab. The
report says, for each entry or definition the snapshot holds, whether the target
already holds it, could take it, needs something first, contradicts it, cannot
represent it, or could not be seen. A snapshot is state, not intent: nothing it
does not hold is reported as something to remove. See
[PREFLIGHT.md](PREFLIGHT.md).
