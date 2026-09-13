# Plans

A plan answers one question before anything is written:

> Given this proposed change and the directory as it is now, what would Alder
> actually do?

It is available as `POST /api/v1/plan`. In the interface every write goes
through one: the confirmation dialog plans the single change it was handed — an
entry edit, a creation, a rename or move, a deletion, a password, a membership,
a schema definition, a configuration setting — and the changeset and the import
panel plan whole sets and documents. This document is the reference for what a
plan means. `api/openapi.yaml` is the reference for its shape.

---

## What goes in

Exactly one of:

- **`changes`** — change requests, the same objects `POST /changeset/apply`
  takes. Each is an **exact** operation. With `reconcile: true`, an `add` is read
  as desired state instead (the 1.4 behaviour, unchanged).
- **`ldif`** — an LDIF document, read according to `mode`.

### `mode: changes` (the default)

Every record is the exact operation it states.

| Record | Planned as |
|---|---|
| no `changetype` (a content record) | an `add` — what `ldapadd` does with it |
| `changetype: add` | an `add` |
| `changetype: modify` | the modifications as written |
| `changetype: delete` | a `delete` |
| `changetype: modrdn` / `moddn` | a `rename` |

An exact operation is checked against the directory and classified, and **never
rewritten**. An `add` of an entry that already exists is a conflict
(`entry_exists`), not a modification nobody asked for.

### `mode: desired`

The document is the state entries should be in.

- Only **content records** — no `changetype` — are accepted. A record with a
  `changetype` is refused with `400 ldif_mode_mismatch`, listing every such
  record in `affected`. A document that mixes "this entry looks like this" with
  "do this" has no single meaning, and Alder does not pick one.
- Each record is **reconciled** against the entry that is there:
  - absent → an `add` of the record;
  - present and already holding every value the record names → `unchanged`;
  - present and differing → a `modify` that replaces the attributes the record
    names and **leaves every other attribute alone**.
- Attributes the directory owns (operational or `NO-USER-MODIFICATION`) are left
  out of the reconciled modification and listed in `skippedAttributes`.

### What absence means

**Nothing.** An entry the document does not mention is not planned, and is never
deleted. There is no mode in which absence from a document means deletion. A
deletion is always an explicit `changetype: delete` record or an explicit
`delete` change.

### What is refused outright

- LDIF that does not parse, with the line in `detail`.
- `attr:< url` references and LDAP controls, as they are on import.
- More records than a changeset may hold.
- Neither or both of `changes` and `ldif`.

---

## What comes out

### `action` — what each change would do

| Action | Applies | Meaning |
|---|---|---|
| `add` | yes | creates an entry that is not there |
| `modify` | yes | changes an entry that is |
| `delete` | yes | removes a leaf entry |
| `rename` | yes | renames or moves an entry |
| `set_password` | yes | an RFC 3062 password change; never `unchanged`, because a directory cannot be asked whether a password is already the one being set |
| `unchanged` | no | the entry already holds what the change describes |
| `conflict` | no | the directory would refuse it as things stand |
| `invalid` | no | the schema forbids it whatever the state |

`action` and `problem.code` are stable identifiers. `reason` is prose for a
person and may be reworded in any release.

### `problem.code` — why a change would not apply

State conflicts (`conflict`), which may resolve when the directory changes:

| Code | When |
|---|---|
| `entry_missing` | a modify, rename or password change of an entry that does not exist |
| `entry_exists` | an exact add of an entry that does |
| `has_children` | a delete of an entry that is not a leaf |
| `rename_target_exists` | a rename onto a name that is taken |

Schema violations (`invalid`), which will not resolve by waiting:

| Code | When |
|---|---|
| `object_class_undefined` | an object class the schema does not define |
| `attribute_undefined` | an attribute type the schema does not define |
| `attribute_not_permitted` | no object class on the entry permits the attribute |
| `single_value_violation` | a single-valued attribute would hold more than one value |
| `missing_required_attribute` | an add omits an attribute its classes require |

Only rules the schema states outright are checked, and they are checked against
the operation that would actually run. A false positive here would withhold a
change the directory would have accepted, so anything that depends on server
behaviour is left for the directory to refuse:

- the RDN's own attribute counts as present in an add, because some servers add
  it from the name;
- operational attributes are not judged against the entry's classes —
  `nsAccountLock` and `pwdAccountLockedTime` are settable and appear in no `MAY`
  list;
- when no object classes are visible — a delegated bind the access rules forbid
  to read `objectClass` — permission and required-attribute rules are not applied
  at all.

A plan in which every item is `unchanged` is a successful answer, not an error.

### `record`, `preview` and `baseline`

For every item that applies, the plan returns:

- **`record`** — the exact operation, as a change request. For a reconciled
  change this is the modification that would run, not the add that was sent.
- **`preview`** — the same exact LDIF and Ansible the confirmation dialog shows,
  rendered from that record.
- **`baseline`** — a token binding that operation and the state it was planned
  against.

### `impact`

Facts, not a risk score.

- **`kind`** on each item, and **`impact.kinds`** in total: `schema` (the
  server's schema entries, where a write changes what every entry may hold),
  `config` (the server's own configuration tree), or `data`. Decided from the
  locations the server announces — `subschemaSubentry`, the configuration
  context, the schema write targets — never from what a DN looks like. On a
  server that keeps its schema inside its configuration, a schema entry is
  reported as `schema`.
- **`membership`** on each item, and **`impact.membership`** in total: what a
  change does to `member`, `uniqueMember`, `memberUid` and `memberURL`. Computed
  by replaying the change's modifications in order over the values the entry
  holds, so an add and a delete of the same member is no change. Member DNs are
  compared as DNs. Deleting a group reports every membership it held as removed.
- **`references`** on each deletion and rename, and **`impact.references`** in
  total: the entries that name the affected DN through `member`, `uniqueMember`,
  `owner`, `manager`, `seeAlso`, `roleOccupant` or `secretary`, by attribute, and
  how many the plan would leave **dangling** — not held by an entry the plan
  also deletes, and not removed from that attribute by another change in the
  same plan.
- **`subtrees`** — deletions grouped under the topmost deleted entry, so a
  subtree deletion of four hundred entries reads as one branch of four hundred,
  not as the entry at the top. Only branches of more than one entry are listed.

#### Limits of impact analysis

- **Reference search runs as the session's own bind.** An entry the access rules
  hide is absent from the answer exactly as one that does not exist is. There is
  no operation that reports "your search would have matched something you may
  not see", so this cannot be detected — only stated.
- It covers the reference attributes the connected server defines, in the
  naming context the affected entry is in. References from the configuration
  tree, or to entries outside every naming context, are not searched.
- Each search is bounded. `impact.references.truncated` says when a bound was
  reached, and there may then be more references than counted.
- A plan that deletes or renames more than 2,000 entries does not search at all:
  `impact.references.analysed` is `false` with a `reason`, and no count is
  offered in place of one.
- Membership is reported for the four attributes above only. Other attributes
  are not treated as group semantics.

---

## Applying a plan

A client applies a plan by sending the planned changes to
`POST /changeset/apply` (or one to `POST /changes/apply`) with each change's
`baseline`. Items that apply nothing — `unchanged`, `conflict`, `invalid` — are
left out.

The confirmation dialog is the single-change case of exactly this. It plans
`{"changes": [change]}`, shows the item — what it does, its problems and impact,
and the LDIF and Ansible the server rendered from the plan — and applies the
change it reviewed to `POST /changes/apply` with the item's `baseline`. A change
that plans as `unchanged` offers nothing to apply; a `conflict` or `invalid` one
is shown and not offered.

- For an **exact** item, send the change as the client staged it. It is the
  operation the plan bound.
- For a **desired-state** item, send the plan's `record`: the planner rewrote
  it, and the rewritten operation exists only in the plan.

Before anything runs, the server re-reads every entry that has a baseline and
checks each one:

| The request… | Response |
|---|---|
| is the planned operation, and the directory still matches | applied |
| is not the operation its baseline was issued for | `400 plan_mismatch`, every such change in `affected`, nothing applied |
| is the planned operation, and the directory has moved | `409 conflict` with `cause: plan_stale`, every stale change in `affected`, nothing applied |

A stale plan keeps the `conflict` code 1.4 used, so a 1.4 client still works;
`cause` is the precise signal.

**A stale or mismatched plan is never replanned and applied on the caller's
behalf.** A plan the operator did not see is not the plan they confirmed. The
interface disables Apply and offers **Recompute plan**; applying is possible
again only once the new plan has been shown.

A baseline covers only the attributes the change depended on, so an unrelated
edit to the same entry does not make a plan stale. Baselines are meaningless to
any other Alder process and do not survive a restart. A change sent without a
baseline is applied exactly as it was before plans existed.

---

## Changes from a comparison

A comparison between the live directory (as source) and a snapshot (as target)
proposes, for each difference, the change requests that would move the
directory toward the snapshot. See [SNAPSHOTS.md](SNAPSHOTS.md). They are
inputs to a plan and nothing else. Nothing applies a comparison directly.

- The interface stages the differences an operator selects into the changeset,
  which plans them as ordinary **exact** changes. The planner reads the
  directory as it is at that moment, not as it was when the comparison ran. A
  change the directory has since made unnecessary plans as `unchanged`, and one
  it has made impossible plans as `conflict` or `invalid`.
- A deletion is proposed only by a complete comparison, is marked
  `destructive`, and is staged only when selected on its own. Once staged it is
  an ordinary `delete`, and its plan shows its impact.
- No proposal ever carries a sensitive value, because a snapshot has none to
  offer.

The end-to-end test captures a snapshot, changes the directory, compares,
stages every proposal including an explicit deletion, plans, applies with the
plan's baselines, and compares again. It must find no difference, and the
operations the directory received must be exactly the planned ones.

---

## Passwords and other sensitive values

`userPassword` and the rest of the sensitive attribute list never appear in a
plan response, a preview, or an apply response:

- in `record`, a sensitive value is replaced by its length alone
  (`{"size": 42}`);
- in `preview`, it is rendered as `withheld (42 bytes)`;
- a `set_password` change never returns the password.

The baseline binds a secret — a `set_password` change's new password, or a
value of a sensitive attribute in an add or a modify — without carrying it. It
holds a keyed MAC of the value under a key derived from the server's fingerprint
key and the session. A client applies the plan by supplying the value from its
own copy of the change, and a different value, even one of the same length, is
refused with `400 plan_mismatch`. Before 1.6 these were bound by shape alone and
a substituted password of the same length was not detected.

Why this is not a guessing oracle:

- the fingerprint key is 32 random bytes held only in the server's memory, so
  nothing can be computed from a token without the server;
- the key is also derived per session, so asking the server to plan guesses and
  comparing tokens only reproduces a token inside the session that planned it,
  and that session already holds the value it sent;
- a token never contains the value, or a plain digest of it.

A token that binds a secret therefore verifies only in the session that planned
it. The *state* half of a token still binds a sensitive attribute already in the
directory by value count alone: a password rotated underneath a plan that does
not change it is not reported as drift.

A value sent with a `size` and no content is refused rather than written: a
client that posts a withheld record straight back is told, instead of having an
empty password written in its place.

A desired-state change whose reconciled record carries a sensitive value can
only be applied by a client that still has the value — the interface stages
such documents through the import panel, whose records carry what the file
contained.
