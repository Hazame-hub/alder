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

**A request body is JSON because the contract says so.** Every operation
here that takes a body declares `application/json`, and a body of
any other type is refused with `415` before a handler reads it. Earlier
releases decoded a form-encoded body too, which nothing in the contract
offered and which let a page on another origin of the same site submit a
change (see the 2026-10-12 decision). A client that sent what the contract
describes is unaffected.

Tightening a promise is allowed: a field that was optional in a response may
become required, because a client that already handles it absent still works.

1.5 changed the plan more than any release since it appeared, and this is how
those rules were applied:

- **A stale plan is still `409` with `error: conflict`,** exactly as in 1.4. The
  precise reason is a new `cause: plan_stale` beside it, and a new `affected`
  lists every change it applies to. A client that switches on `conflict` keeps
  working; an earlier draft that replaced the code was caught by the 1.4 test
  pinning it.
- **New identifiers:** the `invalid` plan action; the `plan_mismatch` and
  `ldif_mode_mismatch` errors; the plan fields `intent`, `kind`, `problem`,
  `membership`, `references`, `impact` and `subtrees`; `ldif` and `mode` on the
  plan request, which previously required `changes` and now requires exactly one
  of the two.
- **A planned change that is not the planned operation is refused** with
  `plan_mismatch`. In 1.4 a baseline bound only the state, so a client could plan
  one change and apply a different one against the same attributes. That is the
  guarantee a plan exists to give, so this is treated as a correctness fix.
- **Sensitive values are withheld** from plan records (`{"size": n}` in place of
  the value), from previews (`withheld (n bytes)`), and from the LDIF in apply
  responses. Before 1.5 a change that set `userPassword` directly had the value
  echoed back in its preview and its apply response. A value sent back with only
  a `size` is refused with `400` rather than written as empty.
- **Import with `reconcile: true` reconciles only records with no
  `changetype`.** It used to turn an explicit `changetype: add` of an existing
  entry into a modification too. The document said add; this is a correctness
  fix, and the one place a 1.4 request now produces a different result.

1.6 moved every single-change write in the interface onto the plan. The HTTP
contract changed only by tightening what a token proves:

- **`POST /changes/preview` is kept, and marked deprecated.** The interface no
  longer calls it: its dialog plans the change and shows the preview that comes
  back inside the plan item, which is rendered by the same code. The endpoint
  still renders exactly what it did, for any 1.x client that uses it, and it is
  not removed within 1.x.
- **`POST /changes/apply` without a `baseline` applies as it always has.** The
  interface now always sends one.
- **A secret sent under a token must be the secret that was planned.** A
  `set_password` change, or a sensitive attribute value, that differs from the
  planned one is `400 plan_mismatch`; in 1.5 a value of the same length was
  accepted. This is the guarantee a token exists to give, so it is treated as a
  correctness fix. A consequence: a token carrying a secret verifies only in the
  session that planned it, where 1.5's would verify in any session of the same
  process.

1.7 added snapshots and comparisons, only by adding:

- **New endpoints:** `POST /snapshots/capture`, `POST /snapshots/inspect` and
  `POST /diff`, and the error identifiers `snapshot_invalid`,
  `snapshot_unsupported_version`, `snapshot_checksum_mismatch`,
  `snapshot_too_large` and `snapshot_scope_unsupported`.
- **The snapshot document is covered like the API.** Snapshot format version 1,
  described in [SNAPSHOTS.md](SNAPSHOTS.md), was introduced in Alder 1.7. Every
  later 1.x release will continue to read it, and two captures of the same state
  will produce the same document apart from `createdAt`. Releases before 1.7 have
  no snapshot support at all. A later 1.x may write a new version, or a new
  `kind`, and will still read version 1. Readers refuse unknown fields rather
  than ignore them, so a document that uses a field an older release does not
  know is refused by that release with `snapshot_invalid`. That is deliberate: a
  field the reader skipped could change what the comparison means.
- **In 1.8, two snapshots are compared without a session.** `POST /diff` with a
  snapshot on both sides used to be refused with `401` unless a session was
  open, and is now answered either way. A request that succeeded before
  succeeds the same way; a comparison with a live side still needs a session.
- **Comparison enums may grow.** New values in `DiffKind`, `DiffReasonCode` and
  `DiffCandidateBlocked` follow the enum rule above. A client that does not
  recognise a `blocked` value should treat the item as offering no change.

1.9 added recovery bundles, only by adding:

- **A new endpoint and new optional fields.**
  - The endpoint is `POST /recovery/inspect`.
  - The new error identifiers are `recovery_invalid`,
    `recovery_unsupported_version`, `recovery_checksum_mismatch` and
    `recovery_too_large`.
  - The new plan problem code is `expected_state_differs`.
  - The new optional fields are `recovery` on `ChangesetRequest`,
    `ChangesetResult`, `ApplyResult` and `PlanItem`; `?recovery=true` on
    `POST /changes/apply`; and `expect` on `ChangeRequest`.

  A request that uses none of them is answered as it was in 1.8.
- **The recovery document is covered like the snapshot.** Recovery format
  version 1, described in [RECOVERY.md](RECOVERY.md), was introduced in Alder
  1.9. Later Alder 1.x releases will continue to read it.
  - Releases before 1.9 have no recovery support.
  - A later 1.x may write a new version and will still read version 1.
  - Readers refuse unknown fields rather than ignore them, as they do for
    snapshots.
- **Recovery enums may grow.** New values in `RecoveryReasonCode` and
  `RecoveryDriftState` follow the enum rule above.
  - A client that does not recognise a reason should treat the step as not
    exactly recoverable.
  - A client that does not recognise a drift state should treat the change as
    not ready.

1.10 added schema snapshots and schema comparison, only by adding:

- **New optional fields and values, and no new endpoint.**
  - `kind` on `SnapshotCaptureRequest`, whose `base` is now needed only for
    `kind: data`, the default.
  - `kind` and `schemaTarget` on `DiffLiveSide`; `kind` and `schema` on `Diff`;
    `definitionCount` on `DiffSideSummary`; `schema` on `SnapshotInspection`.
  - A `SchemaSnapshot` document as the body of capture, inspect and either side
    of a diff.
  - New enum values: `metadata_only` in `DiffKind`; `schema_partial` in
    `DiffReasonCode`; `metadata_only`, `schema_not_editable`,
    `schema_target_required`, `server_defined`, `non_numeric_oid`,
    `unresolved_reference`, `dependency_cycle` and `unbuildable_definition` in
    `DiffCandidateBlocked`; `dependency_required` and `referenced_by_schema` in
    `PlanProblemCode`.

  A request that uses none of them is answered as it was in 1.9, except that
  every `Diff` now says `kind: data`.
- **The schema snapshot is covered like the data snapshot.** Schema snapshots
  were introduced in Alder 1.10. Later Alder 1.x releases will continue to read
  schema snapshot kind/version 1. A release before 1.10 refuses one with
  `snapshot_invalid`, as a kind it does not know, and readers refuse unknown
  fields rather than ignore them.
- **A set of schema changes can plan as a conflict where it did not.** A set that
  adds a definition before what it names exists, or removes one the schema still
  names, plans as `conflict` with `dependency_required` or
  `referenced_by_schema`. Before 1.10 it planned as applicable and the directory
  refused it during the apply.
- **The command line gained options, not commands:** `alder snapshot --kind`
  and `alder diff --schema-target`. `--stage` and `--stage-deletion` also take a
  schema OID. A `metadata_only` difference alone exits 0.

1.11 added change packages, only by adding:

- **New endpoints:** `POST /packages/build`, `POST /packages/inspect` and
  `POST /packages/validate`, and the error identifiers `package_invalid`,
  `package_unsupported_version`, `package_checksum_mismatch` and
  `package_too_large`. `POST /packages/inspect` needs no session, like a
  comparison of two snapshots.
- **The change package document is covered like the snapshot.** Change package
  format version 1, described in [CHANGE-PACKAGES.md](CHANGE-PACKAGES.md), was
  introduced in Alder 1.11. Every later 1.x release will continue to read it.
  Releases before 1.11 have no package support at all. Readers refuse unknown
  fields rather than ignore them, so a document that uses a field an older
  release does not know is refused with `package_invalid`.
- **Package enums may grow.** New values in `PackageItemStatus`,
  `PackageProblem` and the omission reasons follow the enum rule above. A client
  that does not recognise a status should treat the change as not ready.
- **The command line gained one command family and one input:**
  `alder package create|inspect|validate`, and `--package` on `alder plan` and
  `alder apply`. There is no command that applies a package directly, and there
  will not be one.

1.12 added migration preflight, only by adding:

- **One endpoint:** `POST /preflight`, and the error identifier
  `preflight_artifact_unsupported`. An artifact that is malformed, forged or
  from a newer version is refused with the identifier its own endpoints already
  use.
- **The preflight report is covered like any response.** `reportVersion` is 1.
  Finding codes, classifications, categories, scopes and `notEvaluated` areas
  are stable identifiers and follow the enum rule above: new ones may be added,
  and a client that does not recognise a classification should treat the finding
  as unknown, which keeps a report from reading as compatible. `overall` is
  derived from the findings as described in [PREFLIGHT.md](PREFLIGHT.md) and
  will stay derived. Finding ids are positions in a sorted report and are only
  stable for the same artifact against the same target state.
- **The command line gained one command:** `alder preflight`, reusing the
  existing exit codes -- 0, 1, 2 and 3 -- as described in [CLI.md](CLI.md).
- **The artifacts are unchanged.** Change package format version 1 and snapshot
  format version 1 are read exactly as 1.11 and 1.10 read them; nothing is
  added to either.
- **The schema comparison gained an option, not a behaviour:** a preflight
  compares schema without computing the dependency order a staged comparison
  needs. `POST /diff` is unchanged.

1.13 added configuration snapshots and comparison, only by adding:

- **A new snapshot kind, not a new endpoint.** `POST /snapshots/capture`,
  `POST /snapshots/inspect` and `POST /diff` take `kind: config`, and the new
  error identifier is `config_model_unavailable` -- the server announced no
  configuration tree, this session cannot read it, or Alder has no model for
  that server's software.
- **The configuration document is covered like the others.** Snapshot format
  version 1, kind `config`, described in
  [CONFIG-SNAPSHOTS.md](CONFIG-SNAPSHOTS.md), was introduced in Alder 1.13, and
  every later 1.x release will continue to read it. Releases before 1.13 refuse
  it as a kind they do not know rather than misreading it, which is what the
  kind field is for. Readers refuse unknown fields rather than ignore them.
- **A kind is only ever compared with its own.** Mixing kinds in one comparison
  is `400 bad_request` on both sides of the pair, as it already was for schema
  against data.
- **Two configurations of different providers are compared and answered**, with
  `providerMismatch: true`, no items and `complete: false` -- not refused. A
  client that does not know the field would read a comparison with no
  differences, which is why it also carries reason code `provider_mismatch` and
  a summary of each side.
- **Configuration enums may grow.** `ConfigProvider`, `ConfigMutability`,
  `ConfigActionable`, the section names, the value types, the `incomplete`
  reasons and the item problems follow the enum rule above. A client that does
  not recognise an `actionable` value should treat the difference as one it
  cannot act on.
- **Nothing new can be written.** A configuration comparison proposes changes
  only for settings Alder already wrote through the ordinary plan before 1.13;
  a comparison creates no new write capability, and recovery stays unavailable
  for configuration.
- **The command line gained one kind:** `alder snapshot --kind config`, and
  `alder diff` reads configuration documents, reusing the existing exit codes.
- **Preflight is unchanged.** Configuration compatibility is still not
  evaluated, cross-provider or otherwise, and every report still says so.

1.14 widened what configuration Alder changes and made a configuration snapshot
a preflight artifact, only by adding -- with one correction called out below:

- **More settings are writable.** A setting is listed only once the
  conformance suite has written, read back and restored it through Alder on
  both servers. Settings the server itself lists as needing a restart, values
  compared as text, and read-only mode outside an ordinary database or backend
  are no longer offered, so a 1.13 comparison and a 1.14 comparison of the same
  documents may differ in `actionable` for those settings. That is a
  correction: each of them offered a change the server would refuse or that
  could not be undone through Alder.
- **Booleans keep the server's spelling.** A 1.14 configuration snapshot
  records `TRUE` where 1.13 recorded `true`, so the checksum of a new capture of
  an unchanged server differs from a 1.13 capture. Comparing the two is still
  correct: booleans compare without regard to case, and a change derived from a
  1.13 document writes the boolean the way the live server spells its own.
- **A configuration snapshot is a preflight artifact.** `POST /preflight`
  accepts `kind: config`; the report gains the `configuration` category, the
  `config_snapshot` source type, `setting` and `resource` on source references
  and prerequisites, and the `config_*` finding codes. Enums grow under the rule
  above.
- **The plan no longer calls a modification invalid for an attribute the entry
  already holds and the published schema does not define.** 389 DS keeps its
  configuration in such attributes. The directory still judges the change.

1.15 added signing, only by adding:

- **A new document, not a change to any existing one.** `alder-signed` version 1
  wraps a snapshot, change package or recovery bundle unchanged. Every existing
  document is still read exactly as before, and a document's own checksum keeps
  its meaning. A release before 1.15 refuses an envelope as a document it does
  not recognise, rather than half-reading one, which is what the format field is
  for.
- **Two error identifiers:** `signature_invalid` for a document whose signature
  does not match, and `signature_required` on a server started with
  `--require-signature`.
- **One optional field on four responses:** `signature` on a snapshot
  inspection, a package inspection and a preflight report's source, and
  `sourceSignature` / `targetSignature` on a comparison. Absent where no
  document was read, and absent for an unsigned document, so every existing
  client sees what it saw before.
- **`DocumentSignatureStatus` may grow**, under the enum rule above. A client
  that does not recognise a status should treat the document as it treats
  `untrusted`.
- **Two serve flags and three commands:** `alder serve --trusted-keys` and
  `--require-signature`; `alder key`, `alder sign` and `alder verify`, which
  take no connection flags because they need no server.
- **Nothing is signed by default**, and nothing is required to be signed. A
  deployment adopts signing by naming keys.

1.32 and 1.33 reach a release together in 1.33.0. Between them they add three endpoints and take nothing away.

1.33 adds one endpoint and takes nothing away.

- **`POST /snapshots/capture/from`** captures the configuration of a
  directory other than the session's own and returns the document. It reads
  and closes; nothing is written to that server, no session is opened on it,
  and the credentials are not kept. The target allowlist applies exactly as
  it does to a connection, and the TLS rules are the same ones a connection
  goes through.
- Only a configuration capture is offered. `kind` exists on the request so a
  later kind needs no second endpoint.

1.32 adds two endpoints and takes nothing away.

- **`GET /config/indexes`** lists each backend that serves a naming context
  and the attributes it indexes, with what each index covers and either the
  change that would remove it or the reason there is none — in a code to
  branch on and a sentence to print.
- **`POST /config/indexes/candidate`** derives the write this server wants
  for an index on one attribute. It applies nothing; the change goes through
  the ordinary plan and apply.
- **No existing response changed.** The configuration comparison still
  offers the same index rows it did, from the same derivation.

1.31 narrows one option and widens nothing.

- **`includeSensitive` on an export releases a password digest and never a
  usable credential.** A value carrying an RFC 2307 `{scheme}` prefix travels
  as before. A value without one is the password itself and is no longer
  written, and neither is anything in the small set that carries a credential
  inside a longer value — `olcSyncrepl`, `olcDbCryptKey`,
  `nsDS5ReplicaBindCredentials`, `nsMultiplexorCredentials`,
  `nsslapd-keyPassword`, `nsSymmetricKey`, `nsds5ReplicaCredentials`,
  `krbPrincipalKey`.

  **This changes what an export contains.** On a directory that stores
  passwords unhashed, `userPassword` no longer appears in one at all. The
  file names what it withheld, at the end, so a restore knows what it has to
  set by hand. A UI audit found the previous behaviour writing a root
  password in plain text into a file, from a screen that had just declined to
  show it; `SECURITY.md` says such a fix is still a fix, and this is the
  compatibility note saying so plainly.
- **`DELETE /api/v1/session` and `GET /api/v1/session` and `/api/v1/source`
  are no longer subject to `--max-in-flight`.** They ask the directory
  nothing. Previously a busy Alder answered 503 to a disconnect.

1.28, 1.29 and 1.30 reach a release together, the way 1.16 and 1.17 reached
one in 1.18.0. Between them they added three fields, stopped sending one, and
hold back two attributes the entry editor used to offer. Everything below is
an addition unless it says otherwise. (1.27 is not in this list: asking
`GET /access` about another identity shipped inside 1.26.0, and is described
in that section.)

- **`as=anonymous` on `GET /access`** asks what an unauthenticated client may
  do, which is the one question on that screen with no distinguished name to
  put in it (1.28). The word is reserved: a DN always contains an equals
  sign, so no DN can collide with it, and it is folded ASCII-only on both
  sides.
- **`askedAbout` on the access report** says which identity Alder put the
  question about, which is the only way to tell the anonymous answer from one
  the server declined (1.28).
- **`effective.subject` is documented as what Alder asked**, not as the
  server's attestation. The Get Effective Rights response carries no subject
  at all. The field is unchanged; the description was wrong (1.28).
- **`unlock` on the policy report** carries the change that clears the lock
  the report found, derived from the attribute the entry actually holds. A
  lock Alder does not recognise reports no unlock rather than a guess (1.29).
- **`elsewhere` on an attribute's kind** says why the entry editor does not
  offer that attribute, and where it is edited instead. Absent means the
  editor offers it. Distinct from `readOnly`, which is the server's refusal;
  this one is Alder's (1.30).
- **`ldapCode` is no longer sent when there is no result code** (1.30). A dead
  connection or an unparseable reply produces none; the client library numbers
  those conditions from 200 in the same field, so a client reading it saw a
  failure carrying what looks like a success. `detail` no longer renders those
  numbers as result codes either, and the message for one says the directory
  did not answer rather than that it returned an error.
- **`aci` and `olcAccess` are no longer offered as attributes to add** (1.30),
  so they are absent from `requirements.may`, from
  `requirements.settableOperational` and from `candidateKinds`. They are still
  read, still exported and still shown, with `elsewhere` saying why there is
  no field for them. The schema definition attributes are held back the same
  way -- both the subschema's (`objectClasses`, `attributeTypes`, ...) and
  OpenLDAP's writable `olcAttributeTypes` and `olcObjectClasses` -- because
  the schema editor writes those one definition at a time, and a text box
  holding fifty of them can only express a replacement of the lot. Alder does
  not write access rules, and the entry editor was a way around that.
- **A settable operational attribute an entry already carries is editable**
  (1.30). Nothing changed on the wire for this: `operational` and `readOnly`
  were both reported correctly all along and the interface read the wrong one.

1.26 added three endpoints and one field, and narrowed one parameter:

- **`GET /replication`**, **`GET /replication/entry?dn=`** and
  **`GET /replication/conflicts?dn=`** report what a server records about its
  own replication. Read-only, and Alder never contacts a peer.
- **`types` on a configuration object** in a comparison, saying what an index
  covers. Absent for every other kind of object.
- **`as` on `GET /access` must now be a distinguished name**, and anything
  else is refused with a 400 rather than passed to the server. A client that
  only ever sent a DN sees no change; one that sent something else was getting
  "the server declined to say" for a malformed value, which was never an
  answer about access.
- **A configuration snapshot of a replicating 389 Directory Server has a
  different checksum after this release.** Replication's counters and
  timestamps are no longer captured, because they are running state and made
  a snapshot differ from itself seconds later. Nothing an administrator set
  has moved.
- **Six configuration attributes are now withheld from the entry view and from
  an LDIF export** -- `olcRootPW`, `olcSyncrepl`, `olcDbCryptKey`,
  `nsDS5ReplicaBindCredentials`, `nsMultiplexorCredentials`,
  `nsslapd-keyPassword`. This is a security fix and it does break the promise
  that a read returns what the server holds; `SECURITY.md` says such a fix is
  still a fix, and this is the changelog saying so plainly. The values can
  still be set; they can no longer be read back.

1.23, 1.24 and 1.25 are milestone numbers with no tag of their own -- indexes,
the replica harness and replication visibility all reached a release in 1.26.0,
the same way 1.16 and 1.17 reached one in 1.18.0. What they added is listed
above under 1.26.

1.22 added one field to an error, and nothing else:

- **`remedy` on the error body**, beside the existing `hint`: where to look
  about a refusal -- the access rules, the password policy, the schema, or the
  parent that is missing -- with the entry to open it on. A client that
  ignores it sees exactly what 1.21 returned.
- **`find` in the interface's query string**, so the schema browser can be
  opened looking for an attribute.

1.21 added one endpoint, and changed how one DN is written:

- **`GET /policy?dn=`** reports the password policy in force on an entry and
  what the server records about the account. Read-only.
- **A DN value containing `=` is now rendered escaped** -- `cn\=x` rather than
  `cn=x` inside an RDN value. RFC 4514 allows either; 389 Directory Server
  answers "no such object" to the bare form of a DN it gave you escaped, so an
  entry whose RDN value contains `=` was unreachable. A snapshot holding such
  a DN will have a different checksum after this release; nothing else moves.

1.20 added to the access report, only by adding:

- **`effective` and `rightsNote` on `AccessReport`**, and the `as` parameter on
  `GET /access`: the server's own answer to what an identity may do, from the
  Get Effective Rights control, where the server publishes it. A client that
  ignores the new fields sees exactly what 1.19 returned.
- **`effectiveRights` on the session capabilities**, true where the server
  publishes that control.

1.19 added one endpoint and changed nothing else:

- **`GET /access?dn=`** reports the access control rules a server holds that
  bear on an entry: `aci` attributes on the entry and its ancestors,
  `olcAccess` on the database entry in the configuration tree. Read-only, and
  never an evaluation.
- **No existing response changed.** A client that does not call it sees exactly
  what it saw before.

**1.16 and 1.17 were never tagged.** Nothing was released between 1.15.0 and
1.18.0, so a reader upgrading from 1.15.0 gets everything the three sections
below describe in one step. The milestone numbers are kept because each
statement is true of the release it names.

1.18 added one endpoint and changed nothing else:

- **`GET /config/entry?dn=`** answers what the configuration model says about
  one entry's attributes. Nothing else moved: the same model, the same words,
  the same values a capture already reported.
- **A client that does not call it sees no difference at all.**

1.16 added configuration objects, only by adding:

- **One field on the configuration comparison:** `objects`, listing each
  configuration object and what can be done about a difference. A client that
  ignores it sees exactly what it saw before.
- **`nsslapd-pluginEnabled` became writable** for the plugins a 389 Directory
  Server can run without, decided from that server's own tree. A 1.15
  comparison called every plugin read-only, so a 1.16 comparison of the same
  documents may offer a change where 1.15 offered none.
- **`alder diff --stage` and `--stage-deletion` take an object identifier.**
  `--stage-deletion` was refused outright for configuration in 1.13 to 1.15;
  it now selects the removal of a configuration object, and nothing else.
- **No document changed.** A configuration snapshot is what it was; objects are
  the resources it already recorded, compared.

A response may be **streamed**, and a streamed one carries the same fields in a
different order. `POST /api/v1/search` writes its entries first and the fields
it cannot know until the search has finished — `truncated`, `cookie`, `took` —
last. JSON gives an object's members no order, so this is not a change to the
contract: a client that decodes the body sees exactly what it saw before.

What it does change is how a failure *after the first byte* reads. The status
code is settled by then, and the response has no field for "this went wrong", so
such a response is deliberately left unterminated — it fails to parse, with a
trailing comment saying why. Before 1.3.1 the same failure was a `502`. A short
document that parsed cleanly would be indistinguishable from a complete answer,
which is the one outcome worth ruling out. Every failure a search normally has
is still a proper status code: the first page and the schema are both fetched
before anything is sent.

`GET /api/v1/source` is covered too, even though it is served outside the
generated document — it carries the AGPL section 13 offer and must work without
a session. That it lives outside `openapi.yaml` is an untidiness worth fixing;
it does not make the endpoint less binding.

### 3. The command line and the configuration

`alder serve` keeps its flags, their meanings and their defaults. Every flag
also reads an environment variable — the flag name upper-cased with dashes as
underscores, behind `ALDER_` — and those keep their meanings too. New flags and
variables may appear; existing ones do not change under you.

That promise was made at 1.0 and went several releases with no implementation
behind it: the binary was flags-only, and the sentence describing environment
equivalents described nothing. It is true now, rather than deleted, because the
promise was the useful half. The changelog entry that adds this says which
release it became true in — this document deliberately does not guess, having
guessed wrong once already.

The one thing that may tighten is a **security default**, and only with a way
back. `--i-know-this-is-insecure` exists because refusing plaintext LDAP by
default was worth doing; if another default has to move, it will come with an
explicit opt-out and a changelog entry, not silently.

The client commands -- `alder snapshot`, `diff`, `plan`, `apply` and `version`,
added in 1.8 -- are covered the same way. Their names and flags keep their
meanings, and so do the variables their connection flags read, the exit codes
in [CLI.md](CLI.md), and their `--json` output. That JSON is the API's own
documents, passed through, so it follows the API's rules above; what the client
adds around them -- `apply`'s envelope, and the error document's `origin` and
client error codes -- only grows. A flag that confirms or widens a write
(`--yes`, `--allow-deletes`, `--force`) will not start reading the environment.
1.9's `--recovery`, `--recovery-out` and `--allow-origin-mismatch` are covered
the same way, and `--recovery-out` and `--allow-origin-mismatch` do not read the
environment either.
Their human-readable output is not covered, for the same reason message text is
not.

### 1.34

`EffectiveAttributeRight` gains `present`: whether the entry actually holds
that attribute, as opposed to being permitted to. It is additive and
omitted when false, so a client reading 1.33's documents is unaffected. It
is the server's own statement — taken from the same response that carries
the rights — and not something a client should recompute by re-reading the
entry.

The same release widens what the effective-rights search asks the server
about, from `*` to `*` and `+`. A verdict that covered sixty-one attributes
now covers a hundred and forty-one on the same entry, operational ones
among them. Nothing was removed; a client that enumerated the list will see
more of it.

### 1.36

Two additions, both for a case that previously had no way to be told apart
from an ordinary failure: a change whose connection was lost while it was
being sent, which the directory may or may not have applied.

`Error.error` gains `write_outcome_unknown`, returned with **409**. The
status is deliberate: a 502 is what proxies and retry middleware are built
to try again, and automatically repeating an ambiguous write is the one
thing this must never cause. A client that does not know the code sees a
409 and stops, which is the right behaviour without understanding anything.

`ChangesetOutcome` gains `state`, one of `applied`, `failed`, `unknown` or
`not_attempted`. It is additive and absent on older documents.

**One honest wrinkle.** `applied` is a boolean, and a change whose outcome
is unknown reads `false` there, which an older client will take as "not
applied". That is a weaker statement than the truth rather than a different
one, and the alternative — flipping it to `true` — would be far worse.
`applied` now means "the directory confirmed it", and `state` carries the
rest. The schema says so.

### 1.37

1.37.0 added, only by adding: `alder project validate` and `alder project
plan` ([PROJECT.md](PROJECT.md)), and `--detailed-exitcode` on `alder plan`
and `alder project plan`, off by default.

**Then the exit-code promise was broken once, deliberately, in the patch
that followed it** -- hours after 1.37.0, before anyone outside the project
had run it. Two things changed:

- **`--detailed-exitcode` reports "changes pending" as 2, not 1.** 2 is the
  number `terraform plan -detailed-exitcode` uses, which is what anyone
  wiring a plan into CI already switches on. 0 and 3 are unchanged, and
  without the flag nothing changed at all.
- **A command line Alder cannot parse exits 7, not 1.** An unknown command,
  an unknown flag on the root, anything else Cobra refuses: 7 is what every
  other wrong command line already exits, and 1 is `diff`'s "differences
  found", which a script could not tell apart from a typo. `alder serve`
  started wrongly -- without TLS, say -- also exits 7; one that cannot listen
  exits 8.

An internal error, which used to crash with the Go runtime's exit 2, now
prints "alder: internal error" and exits 8, so a crash can never read as
"changes pending".

This is the only break in 1.x, and it is recorded here so it stays the only
one. From this patch on, these numbers are covered like every other.

### 1.38

Recovery covers schema and configuration modifications
([RECOVERY.md](RECOVERY.md#the-schema-and-the-configuration)). Only additions:

- **`RecoveryReasonCode` gains four values**: `post_state_unavailable`,
  `definition_may_be_in_use`, `config_entry_not_compensated` and
  `access_rule_not_written`. `schema_or_config_not_supported` stays, now for
  the parts of a schema or configuration change that are still not recovered.
  A client that switches on the code should already treat an unknown one as
  "not exact".
- **`ChangeExpectation` and `RecoveryExpect` gain an optional `definitions`**:
  one schema definition, by OID, expected as the original change left it.
- **The bundle format stays version 1.** A bundle that uses neither a new
  code nor `definitions` reads in older releases as before. One that does is
  refused by an older release as `recovery_invalid` -- its decoder is strict --
  which refuses rather than compensating without the expectation.

A project can be opened in the web interface
([proposals/web-projects.md](proposals/web-projects.md)). Only additions:

- **`POST /project/plan`**, a new endpoint: a project sent as files,
  validated, planned against the session's directory and reported with its
  unmanaged entries. Its `plan` is the `Plan` schema `POST /plan` returns,
  and its `unmanaged` is the shape `alder project plan --json` writes, so
  both are covered as those already are.
- **`Error.error` gains `project_invalid`**, and `Error` an optional
  `problems` list beside it. A client that does not know the code sees a
  400 with a message, as for any other refusal.
- **No other response changed**, and the command line's output is as it
  was: the comparison behind its unmanaged report moved into a package the
  server shares, and nothing it prints moved with it.

### 1.39

A plan can be saved and applied later
([proposals/saved-plans.md](proposals/saved-plans.md)). Only additions:

- **`alder project plan --out FILE` and `--force`**, and **`alder project
  apply --plan FILE`**. None of them reads the environment. With `--json`,
  `project plan`'s document gains `saved`.
- **The saved-plan format, `alder-plan` version 1.** Later 1.x releases will
  continue to read it, as they do recovery bundles. Its `changes` are the
  API's `ChangeRequest`, so they follow the API's rules above. Its reader is
  strict, so a file that uses a later addition is refused rather than
  half-read; keys are read exactly as written, and one given twice is
  refused. A modification must expect every attribute it changes, and a
  project's saved plan applies only changes the project itself makes for
  the environment (exit 3 otherwise).
- **`alder plan --out FILE` and `--force`**, and **`alder apply --plan
  FILE`**, for change sets that are not a project. None of them reads the
  environment. A saved plan of a change set has `input` where a project's
  has `project`, no environment `name`, and may hold deletions, each with an
  exhaustive `expect`.
  Either kind's `environment` carries an optional `configBindDn`, the
  configuration bind it was made with, and an apply under another is
  refused (exit 3).
- **`AttributeTypeSummary` gains an optional `noUserModification`**, the
  effective flag through `SUP`, beside `operational`. An apply of a
  project's saved plan reads both to refuse a replacement of an attribute
  the directory keeps. A saved plan is otherwise planned and applied
  through `POST /plan` and `POST /changeset/apply` as they are.
- **A saved plan can be signed.** `alder apply --plan` and `alder project
  apply --plan` read an `alder-signed` envelope whose payload is an
  `alder-plan`, and gain `--trusted-keys` (never read from the environment).
  Without that flag an unsigned plan applies exactly as before, and a signed
  one with its signature unchecked; with it, a signature by a named key that
  does not match is refused (exit 8). With `--json`, the apply document
  gains an optional `signature`. A release before this one refuses a signed
  plan as a file it does not read; keep the unsigned file for it. The web
  interface refuses a signed plan and names the command that checks it.

Four more configuration attributes are secrets, and are withheld from the entry
view and every export as `olcSyncrepl` is: a proxy back end's
`olcDbIDAssertBind`, `olcDbACLBind` and `olcDbACLPasswd`, and 389 DS's
`nsDS5ReplicaBootstrapCredentials`. They hold a password in the clear, and a
recovery bundle copied them. **This changes what a read and an export
contain**, as the same fix did for `olcSyncrepl`. A recovery step that
touches one, or any setting whose value carries `credentials=`, now says
`sensitive_value_not_captured`, and a definition written to a configuration
entry that is not a schema target says `schema_or_config_not_supported`;
both were `exact` before. Neither is a new reason code. As for `olcSyncrepl`,
a basket item, change package or saved plan that sets one of the four is
now refused (a plan shows their values as sizes only, so a plan posted back
has to supply the value again).

A session can hold a read-only reference server
([proposals/second-server.md](proposals/second-server.md)). Only additions:

- **`/session/reference`**, a new path: `GET`, `POST` and `DELETE`.
- **Optional parameters naming a server**: `on` on `GET /schema`,
  `GET /replication` and the `POST /snapshots/capture` body; `rightOn` on
  `GET /compare`. Omitted, each reads the session's own server as before.
- **`DiffSide` gains `reference`**, and **`DiffSideKind` gains `reference`**.
  A comparison sent without one is answered as before.
- **New optional response fields**: `SessionInfo.role`,
  `DiffSideSummary.server`, `ComparedEntry.server`.
- **`--max-sessions` counts directory connections**, so a reference takes a
  place. A deployment that attaches no reference sees no difference.

`GET /members` says why a member could not be read
([proposals/group-membership-tools.md](proposals/group-membership-tools.md),
open question 1):

- **`MemberList` gains an optional `errors`**, one `MemberReadError` (`dn`,
  `message`, and `ldapCode` when the directory sent one) per member whose read
  failed for a reason other than "no such object".
- **`dangling` narrows to "no such object".** A read refused, timed out or cut
  off was listed there and is now in `errors`. A client that reads only
  `dangling` sees fewer values, never different ones: each was already
  "status unknown", and what moved is the part that was not even that.

A plan compares `uniqueMember` under `uniqueMemberMatch`
([PLAN.md](PLAN.md)). **This changes two plan actions**, for a modification's
`add` or `delete` of a `uniqueMember` value that differs from a stored one only
in the spelling of its name: the add is now `unchanged` (the directory would
refuse it as a value it holds), and the delete `modify` (the directory would
remove the stored value). A UID suffix is compared exactly as written, so every
other pair plans as it did. No API or format change.

A project can hold saved searches
([PROJECT.md](PROJECT.md), "Saved searches"). Only additions:

- **The `.search.yaml` format, version 1**, a file of its own beside the
  LDIF, found by its suffix. Covered from this release, as `alder-plan`
  version 1 is: later 1.x releases read it, and its reader is strict, so a
  file using a later addition is refused rather than half read.
  `alder.yaml` does not change, so an older release plans and applies a
  project holding search files exactly as before. A search file never
  changes a plan or a saved plan's digest.
- **`alder project search`**, a new subcommand, with the existing exit codes
  (0, 3, 7, 8). Its LDIF is `GET /export/ldif`'s and its `--json` is an
  array of `POST /search` entries, so both follow those rules.
  `alder project validate` also checks search files, and says how many it
  found; a project whose search file does not validate is not valid
  anywhere, `plan` and `apply` included (exit 3).
- **`ProjectPlan` gains an optional `searches`**, of the new
  `SavedSearch` schema, and *Open project* sends `*.search.yaml` files to
  `POST /project/plan`. A problem in one is a `project_invalid` problem with
  its file and line, as an LDIF file's is.
- **`GET /export/ldif` gains an optional `attributes`**, comma-separated.
  Omitted, the export is every user attribute, as before; a sensitive
  attribute named in it is still left out unless `includeSensitive` is set.

A recovery bundle compensates a configuration-tree access rule
([proposals/access-rules-in-recovery.md](proposals/access-rules-in-recovery.md)):

- **`access_rule_not_written` is no longer produced.** It stays in
  `RecoveryReasonCode`, and a bundle 1.38 wrote with it reads and plans as
  it did. Where 1.38 said `partial` or `unavailable` for a change to
  `olcAccess`, or to an `aci` in the configuration tree, 1.39 says `exact`,
  in a plan's assessment and in the bundle.
- **A configuration step may say `hidden_attributes_unknown`**, an existing
  code, when the rules could not be read before or after the change.
- **No API or format change.** The compensation is a replace with an
  expectation, as for any other setting, so a 1.38 release reads a bundle
  1.39 wrote.

A membership audit reports the values groups name that the bind cannot find
([proposals/group-membership-tools.md](proposals/group-membership-tools.md)).
Only additions:

- **`POST /members/audit`**, a new read-only endpoint, and its response
  schema `MemberAudit`.
- **`Error.error` gains `audit_incomplete`**, for an audit refused because a
  search stopped early. Its ceiling refusal reuses `snapshot_too_large`. A
  client that does not know the code sees a 400 with a message.
- **A modify that leaves a required attribute with no value plans as
  `invalid` with `missing_required_attribute`**, an existing code, where it
  planned as `modify` and the directory then refused it.

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
