# Decisions

An append-only record of decisions that were settled once and should not be
relitigated. Each entry says what was chosen and, more usefully, why the
alternative was rejected.

If you settle something non-obvious — especially somewhere the code turned out
to contradict the plan — add an entry.

### 2026-09-02 — bootstrap

- **Module path** is `github.com/hazame-hub/alder`. Confirmed before `go mod init`.
- **Schema editing stays out of v1.** It was requested early and the scope line was held. Schema is *browsable* and *exportable as LDIF* in v1. Nothing
  writes to `cn=schema` / `cn=config` until v2. Reason: schema writes are the most
  vendor-divergent operation in LDAP (OpenLDAP `cn=config` `olcObjectClasses`
  versus 389 DS `cn=schema` modify), and doing it once, correctly, requires the
  conformance harness to already be green.
- **Milestone cadence** is one milestone per review.
- **M0 adds no third-party dependencies.** `internal/dn` and `internal/filter` are
  stdlib-only, and `cmd/alder` is a stub. Cobra, Fiber and `go-ldap/ldap/v3`
  arrive in M1/M2 when there is something for them to do. `go.mod` has an empty
  require block on purpose.
- **Test harness suffix is `dc=alder,dc=test` on both servers.** Because the suffix
  is identical, the seed data LDIF is byte-identical for OpenLDAP and 389 DS, and
  only the schema-installation step is vendor-specific. This is what makes
  "assert identical behaviour" a meaningful assertion rather than a translation
  exercise.

### 2026-09-02 — M1 through M4, in one pass

- **The milestone cadence was collapsed once,** deliberately: M1 to M4 ran back to
  back rather than stopping for review at each. One milestone per review remains
  the default; that was an exception, not a new rule.
- **Go floor raised from 1.23 to 1.25.** `go-ldap/ldap/v3` v3.4.14 and the current
  `golang.org/x/crypto` both declare `go 1.25`. The alternative was pinning older
  versions of both, which means running older TLS code to satisfy a line in a
  document. CI and the Dockerfile follow. The floor is 1.25.
- **`AnsibleTask()` is `ansible.Task(ch)`, not a method on `ChangeRecord`.**
  The original design had it as a method, but `internal/ansible` has to import
  `internal/directory` for the type, so a method on the type would be an import
  cycle. `LDIF()` stays a method because `internal/ldif` has its own record type
  and does not import `directory`. The rule that mattered — one record, one write
  path, two renderings of the same thing — is intact.
- **Spec-first was kept.** `api/openapi.yaml` generates the Go server interface
  (oapi-codegen, models + fiber-server) and the TypeScript client
  (openapi-typescript + openapi-fetch). Handlers are hand-written against the
  generated interface: generated handler bodies would have to be edited to do
  anything, and an edited generated file is the worst of both worlds.
- **DNs are query and body parameters, never path segments.** A DN carries commas,
  equals signs, escaped characters and non-ASCII text, and no two proxies agree
  about double-encoding one. `/entry?dn=...`, not `/entries/{dn}`.
- **An attribute value on the wire is `{text}` or `{base64}`, never a bare string.**
  The choice is made from the bytes and the syntax together, so a JPEG whose
  leading bytes happen to be printable is still base64.
- **The LDIF preview is rendered by the server, not the browser.** Rendering it
  client-side would reintroduce the gap between what the user confirmed and what
  the server was sent, which is the one thing this product cannot have.
- **`replace` is the modification an edit produces,** rather than a computed pair
  of add and delete. The resulting LDIF reads "this attribute ends up as exactly
  this", which is what the user is being asked to confirm.
- **LDIF import applies one record at a time.** A directory has no transaction
  across entries, so a bulk apply that fails halfway leaves a state nobody chose.
  Applied records are marked so a partial run resumes rather than restarts.
- **`attr:< url` in LDIF is refused outright, not fetched.** The reader runs in a
  process holding a privileged bind; following a URL would be arbitrary file
  disclosure and server-side request forgery in one feature. LDAP controls in LDIF
  are refused for a related reason: ignoring one silently would apply a different
  change than the document describes.
- **The conformance suite is behind a `conformance` build tag** so `go test ./...`
  needs no Docker, and runs as a required CI job. It is green on both servers,
  with zero schema parse failures across 389 DS's 1026 attribute types and
  OpenLDAP's 293.
- **Still out of scope, and still not built:** schema editing, ACL or `cn=config`
  editing, any multi-user concept, SSO, a persisted audit log, other drivers,
  bulk provisioning, self-service, a database. v1 remains stateless.

### 2026-09-02 — M5, and the licence

- **AGPL-3.0-only.** Chosen over Apache-2.0 and MPL-2.0 deliberately. Alder is
  run as a service, so section 13 is the operative clause: someone who modifies
  Alder and offers it to others over a network has to offer them the source. That
  is what keeps `test/compose` and `test/conformance` — the expensive part, and
  the thing that makes multi-vendor support real rather than aspirational — from
  being absorbed into a closed product. It also leaves dual-licensing open,
  which the "no licensing or paywall plumbing" line in the scope list implies may
  matter later; that requires staying the sole copyright holder or collecting a
  CLA, so decide before accepting outside contributions.
- **Section 13 is discharged in the product, not the README.** `/api/v1/source`
  serves the offer with no session required, and the UI links it in the header.
  The obligation runs to the person using the running instance, and that person
  is looking at the page, not at a repository. `--source-url` is how an operator
  running a fork points it at their own source; the offer also reports whether
  the binary was built from a modified tree.
- **GoReleaser does not build the SPA.** `embed.FS` resolves at compile time, so
  a `before` hook would let a local `goreleaser build` silently ship whichever
  SPA the developer last built. The release workflow runs `task web` first and
  GoReleaser asserts the output exists, failing loudly if it does not.
- **The editor holds a frozen baseline.** It used to derive one from the live
  query, so any refetch discarded in-progress edits. Freezing it also made
  refetching useful: it is what detects another admin changing the entry
  underneath, which the editor now reports rather than silently overwriting.
- **`--warning-tint-foreground` exists because `--warning-foreground` is for
  text on a solid fill.** Every warning panel in the app is a 10% tint over the
  page background, where the solid-fill colour was dark-on-dark in dark mode.

### 2026-09-03 — the contributor licence agreement

- **A CLA, not a copyright assignment.** Contributors keep their copyright and
  grant a licence that explicitly includes the right to sublicense under any
  terms, which is the clause dual-licensing depends on. Assignment would give
  the Maintainer more, and most contributors and nearly every employer refuse it
  on sight; the licence grant buys everything the AGPL decision was made for at
  a fraction of the friction. Adapted from the Apache ICLA v2.0, which
  contributors already recognise.
- **The reason is stated in the first paragraph of the document, not buried.**
  A CLA that hides why it exists reads like a rights grab. This one says it lets
  the Maintainer relicense, and says on the same screen that the contributor
  keeps their copyright.
- **The counterparty is the GitHub account, for now, and the document says so.**
  A grant to an account name is weaker than one to a named person or company.
  It is defined once, at the top, so replacing it with a legal name is a
  one-line change rather than a redraft; contributions accepted meanwhile stay
  governed by the version signed at the time.
- **Enforced by a bot on every pull request, not by an honour-system note.** The
  option this whole arrangement protects is destroyed permanently by a single
  unsigned contribution, so it cannot depend on the maintainer remembering to
  check. Signatures live as JSON on the `cla-signatures` branch, so who agreed
  to what, and when, is in git rather than in a third-party service.
- **Timing mattered.** The CLA went in before the repository accepted any
  outside contribution, which is the only moment it is free to do.

### 2026-09-03 — passwords, DN pickers, and narrower modifications

- **Passwords are set with the RFC 3062 extended operation, never by writing a
  hash.** The server then chooses the scheme and applies its own password
  policy. This is not theoretical: the conformance suite shows OpenLDAP storing
  `{SSHA}` and 389 DS storing `{PBKDF2-…}` for the same call. Hashing in the
  client would have picked one and forced it on both, quietly downgrading 389
  DS. A server that does not advertise the extension is told so rather than
  silently downgraded.
- **A password change is a `ChangeRecord` but has no LDIF.** It goes through
  `Session.Apply` like every other write, so there is still exactly one write
  path, but the preview shows a notice and the equivalent `ldappasswd` command
  instead of a change record. Rendering `replace: userPassword` would describe
  an operation that does not happen, and the preview exists to describe the one
  that does. `NewPassword` is never rendered, logged, or returned.
- **The DN picker is generic, not a group feature.** Every attribute whose
  syntax is a DN gets it — `member`, `manager`, `seeAlso`, `owner` — because
  special-casing groups would have solved one case and left the rest. The filter
  it suggests is per-attribute and editable, since a group legitimately contains
  other groups.
- **An edit that only adds or only removes values emits `add` or `delete`, not
  `replace`.** The earlier rule — that a modification is always a replace, so
  the LDIF reads "this attribute ends up as exactly this" — is right for
  single-valued attributes and wholesale rewrites, and wrong for the case that
  matters most. Adding one person to a fifty-person group by replacing the whole
  member list silently removes anyone another administrator added since the
  entry was read. Group membership is the most concurrently edited attribute a
  directory has. A mixed edit or a reordering is still a replace, because
  nothing narrower describes it.
- **A copy leaves behind what the directory owns and what was never sent.**
  Operational and NO-USER-MODIFICATION attributes describe the original;
  sensitive ones were withheld from the browser by design. The dialog names what
  it could not copy rather than producing an account with no password and saying
  nothing.

### 2026-09-04 — changesets

- **The basket lives in the browser, not the server.** v1 is stateless, and a
  per-session basket on the server would be state to expire, to leak between
  tabs, and to clean up. The list arrives with each request, is rendered or
  applied, and is forgotten. The cost is that a refresh loses staged work, which
  the empty state says outright rather than leaving to be discovered.
- **And it is held in memory, not `sessionStorage`.** Surviving a refresh would
  be genuinely nicer. A staged password change carries the new password in
  plaintext, and writing that to browser storage to buy a convenience would
  break the rule that credentials never persist.
- **Alder warns about ordering and reorders nothing.** It reports what no single
  change can see — an entry created before its parent, an entry acted on after
  being deleted, the same entry changed twice — and leaves the order alone.
  Sorting automatically would be guessing at intent: a rename that moves an entry
  under something created later is legitimate, and rearranging it silently would
  apply something other than what was reviewed. None of the warnings block; the
  directory is the authority, and a warning that turns out to be wrong should
  cost a reading rather than a refusal.
- **The whole set is validated before any of it runs.** It cannot make the run
  atomic — nothing can, LDAP has no transaction across entries — but a malformed
  change at position twelve should not apply the first eleven first.
- **A partial run is a 200 with an outcome per change, not an error status.** The
  body says where it stopped; a non-200 invites callers to retry the whole set,
  which is exactly wrong when half of it already applied. Every change after the
  failure is reported by name as not attempted rather than omitted, so the panel
  can be checked against the list that was submitted. What did not apply stays
  staged, in order, so the fix is to correct one change and apply again.
- **Staging goes through the same confirmation as applying.** The button sits
  next to Apply in the dialog that already renders the LDIF. A changeset is a
  different moment to apply a reviewed change, never a way around reviewing it.
- **The combined document is a real multi-record LDIF file**, asserted by parsing
  it back with Alder's own reader rather than by matching text — so what the
  changeset view offers as a download is something Import can read. A password
  change, which has no LDIF form, still appears in it as comments; a document
  describing fewer steps than the run performs would be worse than none.

### 2026-09-04 — the configuration tree, and schema editing

- **Schema editing moved into scope, on the record.** It had been deferred to v2
  with a stated prerequisite: that the conformance harness be green first,
  because schema writes are the most vendor-divergent operation in LDAP. That
  prerequisite is met, and the decision to move it in was taken deliberately
  rather than by drift. The v1 scope list in CONTRIBUTING and the charter both
  say so.
- **A schema change is an ordinary modify, not a second write path.** A
  definition is a value of an attribute on an entry, which is what both
  arrangements actually are. Expressing it as a `ChangeRecord` means schema
  editing arrives already holding the LDIF preview, the Ansible task and a place
  in a changeset, instead of having to earn each of them again. `Session.Apply`
  is still the only method that writes.
- **Where the schema is kept is read from the server, not guessed from its
  name.** A server that publishes a `configContext` is declaring that its
  configuration is part of its protocol surface, and that is also what makes its
  subschema subentry a generated, read-only view of configuration entries. A
  server that publishes none has a subschema subentry that *is* the schema. The
  announcement is the signal, and it is the server's own statement about its
  architecture.
- **Announced and reachable are two different facts, kept apart.** An early
  version conflated them and broke the case that already worked: probing found a
  configuration tree on the server that keeps its schema in its subschema
  subentry, and the schema style followed the probe instead of the
  announcement. `ConfigContext` is now only ever what the server announced, and
  `Config.DN` is what can be browsed.
- **The configuration DN is preferred from the announcement, and otherwise
  found by trying the conventional location.** Trying `cn=config` and believing
  only what the server answers is observation, not a vendor check: the candidate
  is tried against every server, the answer decides, and an entry that turns out
  to sit inside a naming context is data rather than configuration and is
  rejected.
- **A connection may carry a second identity, for the configuration tree only.**
  The account that administers a suffix normally has no rights in the
  configuration, and the reverse. Without this, reaching the configuration means
  connecting as the configuration administrator and giving up the data — which,
  on a server that keeps its schema in its configuration, makes schema editing
  and entry browsing mutually exclusive. Operations are routed by DN, so neither
  identity borrows the other's rights. It is held in memory for the life of the
  session exactly like the bind password, and the connection screen remembers
  the DN and never the password.
- **A removal or an edit uses the value the server stores, never the one the
  browser displays.** They differ: a server keeping its schema in configuration
  prefixes each stored definition with its load order and strips that prefix
  from what it publishes, and 389 DS records `X-ORIGIN 'user defined'` on
  anything added at runtime. A change built from the displayed form matches
  nothing on a removal, and on an edit leaves two definitions of one OID. The
  conformance suite asserts this directly, because it is the defect most likely
  to make schema editing look as though it works right until it silently does
  not.
- **An edit is a delete and an add of one value, not a replace of the
  attribute.** The attribute holds every definition the target has — a thousand
  of them on 389 DS — and replacing it to change one would rewrite the lot and
  produce a preview nobody could read.
- **The collection is never preselected when there is a choice.** Where the
  schema lives in configuration a server holds several collections, they load in
  order, and the first is its core schema — the one place a new definition
  almost never belongs. A default here would quietly aim the change at the worst
  target, so the form asks. This was found by using it: the first version
  defaulted to the first collection and built a change against the server's core
  schema.
- **`internal/schema` gained rendering, and the round trip is what is tested.**
  `parse(render(x)) == x`, fuzzed, rather than assertions about formatting. It
  found a real parser bug in the first second: `SYNTAX {1}`, a length with no
  OID, was accepted and silently lost its length, so a definition read and
  written back meant something else. RFC 4512's `noidlen` requires the OID.
- **The parsed schema cache is per session and invalidated by that session's own
  writes.** A change made in one session is not seen by another until it
  reconnects. That is the same staleness the entry editor already reports for
  entries, it is bounded by how rarely schema changes, and a session that edits
  the schema sees its own change immediately — which is the case that matters,
  because it is what the entry editor consults to decide what an entry may hold.

### 2026-09-04 — configuration editing, found rather than built

- **It already worked, and the documentation said it did not.** Editing entries
  in the configuration tree was never implemented: it falls out of the entry
  editor being general and writes being routed by DN, so it arrived the moment
  the configuration tree became browsable. The scope list still said `cn=config`
  editing was out. Discovered by testing the shipped build rather than by
  reading the code, which is the only way this kind of gap ever surfaces.
- **Kept, not removed.** Taking it away would mean refusing to edit entries the
  operator can already edit with `ldapmodify`, in a tool whose whole argument is
  that it shows you exactly what it is about to send. The scope line changed to
  match the software instead.
- **But it now warns.** Editing an entry and editing the server that serves it
  are not the same act, and Alder was presenting them identically. A change
  addressed into the configuration tree says so; a change touching an attribute
  you can lock yourself out with names it and says why. Nothing blocks: the
  directory is the authority, and an operator who supplied configuration
  credentials is doing this deliberately.
- **Schema targets are exempt from the general warning.** Schema editing reaches
  the configuration tree too, through a form that already says what it is doing.
  Repeating the warning on every schema edit would teach people to click past it,
  which would cost more than it bought.
- **The dangerous-attribute list names attributes, not vendors.** It is the same
  approach the value deny-list already takes for `userPassword` and
  `nsslapd-rootpw`: what matters is what the attribute means, and a server with
  no attribute of that name simply never matches.
- **The warnings panel is no longer headed "the schema has something to say".**
  It carries two kinds of warning now, and the heading would have made the more
  serious one read as a footnote about the schema.
- **Ordered values were the hazard worth checking, and they hold.** A
  configuration entry keeps `{0}`, `{1}` prefixes on its multi-valued attributes
  and the published view of schema strips them — the same shape as the bug that
  nearly shipped in schema editing. Verified against a live server: deleting one
  access rule by its stored text works and the server renumbers the survivors,
  adding one without a prefix appends, and writing the whole set back unchanged
  is a true round trip. The conformance suite now asserts the round trip.
- **Two rough edges left deliberately.** A configuration entry created with a DN
  the server then renames — OpenLDAP assigns `cn={5}name` — reports success at a
  DN that no longer resolves. And a refusal such as "Unwilling to Perform" on a
  schema entry deletion is shown as the bare LDAP result code, with no hint that
  the server simply does not allow it. Both are reported rather than fixed here,
  because both want a wider answer than a special case.

### 2026-09-04 — the two rough edges, closed

- **An added entry is looked for, not assumed.** A directory need not store an
  entry under the name it was given: where an entry's position among its
  siblings forms part of that name, the server assigns the position and rewrites
  the RDN. Alder reported the DN it sent, so a successful create was followed by
  navigating to nothing. It now reads the requested DN back, and only when that
  fails searches one level of the parent for an entry whose RDN differs solely
  by a position prefix. The ordinary case costs one base read; the search happens
  only where something has already gone unexpectedly.
- **It claims a location or it says it could not find one.** Where the search
  finds exactly one candidate, that DN is reported. Where it finds none or
  several, the requested DN is returned with a note saying the location was not
  confirmed. Guessing between two entries whose names differ only by a position
  would be worse than admitting ignorance.
- **A refusal is explained from the result code and the attempt, never from what
  the server said.** The driver withholds the server's diagnostic on purpose —
  some servers name entries the caller may not be allowed to know exist, and
  some echo the request back — and that decision stands. What it left behind was
  "Unwilling To Perform (code 53)" and nothing else, which is not enough to act
  on. The explanation is now written by Alder from two things it knows for
  certain: the standard RFC 4511 code, and the change that was being made. It is
  phrased as the likely cause rather than a diagnosis of this particular failure.
- **Context sharpens the explanation without vendor branching.** The same code 53
  reads differently for deleting a schema collection, deleting some other
  configuration entry, renaming one, and refusing an ordinary change — because
  what was attempted differs, not because the server differs. Code 50 in the
  configuration tree points at the configuration credentials, which is the thing
  that would actually fix it.
- **Alder stays quiet where it has nothing to add.** Codes with no useful
  explanation produce no hint rather than a platitude, and a test asserts it.

### 2026-09-04 — the schema entry was unreachable on one of the two servers

- **Reported from outside.** A tester said he could not edit 99user.ldif on
  389 DS. No further detail was available, so the response was to remove every
  obstacle that could produce that sentence rather than to guess which one had.
- **The objective gap: a subschema subentry sits outside every naming context.**
  Where a server keeps its schema in configuration entries, browsing the
  configuration reaches it. Where the subentry *is* the schema, nothing reached
  it — not the naming contexts, not the configuration tree — and the schema
  browser was the only way in. It is now a root of its own.
- **Only where it is writable.** Offering a generated, read-only view of the
  schema as a browsable entry would be a trap: the edit control appears and the
  server refuses the write. So the root is added when the subentry is the thing
  that gets written, which is the same question as whether it is the real schema.
  Nothing changes on a server whose schema lives in configuration, because the
  entries holding it were already reachable.
- **Values are drawn fifty at a time.** That entry holds one value per definition
  the server knows: a thousand for attributeTypes alone, and about eighteen
  hundred across the entry. Rendering them all took seconds and, in the editor,
  would have meant a thousand text inputs. The rest are drawn on request, in both
  the reader and the editor, which also makes any other outsized entry usable.
- **The entry says where its definitions are edited.** Schema definitions are
  values of an operational attribute; they can be written, but not one at a time
  and not legibly. Rather than leave someone to conclude the schema cannot be
  edited at all, the entry carries a line saying the schema browser does it, and
  a button that goes there. That is the likeliest explanation for the report: the
  place a person looks and the place the work happens were not connected.

### 2026-09-04 — editing a schema definition that something uses

- **The bug the tester actually hit.** He could see the schema and not edit it.
  The cause was not access, not reachability: a server whose subschema subentry
  is the schema refuses to delete an attribute type that an object class names
  in its MUST or MAY, even inside the one operation that adds it straight back.
  Alder expressed every edit as delete-and-add, so any attribute type a class
  used could not be edited — which is most of the ones worth editing.
- **A replace is now written the way the server stores schema.** Where the
  subentry is the schema, offering the definition again under the same OID and
  names replaces it in place, and no delete is needed. Where the schema lives in
  configuration entries the values are an ordinary multi-valued attribute with no
  notion of replacing one by OID; an add alone is refused there, so the old value
  must go. Both forms were tried against both servers, and each server refuses
  the other one — this is a real difference in how schema is stored, not a
  preference.
- **A rename still removes the old definition.** An update in place matches by
  OID *and* name, so the same OID under a new name is a collision rather than an
  update. Renaming an attribute type a class uses therefore remains impossible on
  such a server; that is the server's rule, and the refusal now explains itself.
- **The suite was testing the easy half.** Every schema edit it covered was of a
  definition nothing referenced. The new case edits alderTeam, which
  alderEmployee requires on both servers, and it was confirmed to fail with the
  fix disabled — a test that passes either way would have been worse than none.

### 2026-09-04 — an edit was quietly deleting fields

- **Found by testing the fix rather than announcing it.** Asked to verify before
  handing the previous fix over, the first run through the actual editor showed
  a worse bug than the one just repaired: editing an attribute type to change
  its description also removed `SUBSTR caseIgnoreSubstringsMatch`. The change
  was accepted, nothing was said, and substring search on that attribute would
  simply have stopped working.
- **The editor was prefilled from the wrong thing.** It used the display
  summary, which is wrong for editing in two separate ways. It omits fields —
  SUBSTR, ORDERING, COLLECTIVE, NO-USER-MODIFICATION, USAGE, the syntax length
  and the extensions — so an edit wrote a definition without them. And it
  reports *effective* values, resolved through SUP, so an attribute inheriting
  its syntax from a superior would have had that syntax written in as its own.
- **The detail responses now carry the definition as it declares itself**, in
  the same shape a change posts back. Round-tripping is then true by
  construction rather than by keeping two lists of fields in step, and the tests
  assert exactly that: parse, hand to the editor, convert back, render, and the
  meaning is unchanged.
- **What the form cannot show travels with the request.** Submitting spreads the
  original definition and overrides only the fields the form owns. Extensions,
  and anything a later revision of the schema adds, pass through untouched
  instead of being dropped for want of an input.
- **The summary stays as it is.** Resolving inheritance is right for reading —
  somebody looking at an attribute wants to know what it effectively does — and
  wrong for writing. The mistake was using one view for both, not the view.

### 2026-09-05 — object views and the table (console slice 1)

- **A view is a search, not a stored list.** Users, Groups and Organizational
  units are `GET /views` — a filter and a set of columns derived from the schema
  the connected server published — run through the same `/search` as everything
  else. Nothing is cached, nothing is held server-side, and the page is bounded
  at 200 with truncation reported. The moment one of these becomes a list Alder
  keeps, v1 stops being stateless.
- **The anchors are standards-track class names, and that is not a vendor
  branch.** person, account and posixAccount; groupOfNames, groupOfUniqueNames,
  posixGroup and groupOfURLs; organizationalUnit. Each is used only if the
  server defines it, which is why the groups filter has four terms on 389 DS and
  three on OpenLDAP without a line of code knowing which is which. You cannot
  know what a "user" is without some anchor; what you can avoid is asserting one
  the server never published.
- **Site classes need nothing.** An entry carries its superclasses in its own
  objectClass, so `myCompanyPerson SUP person` is matched by
  `(objectClass=person)` without being named anywhere.
- **The columns walk down the class tree, not across it.** person permits no
  mail; inetOrgPerson does, and an inetOrgPerson entry is a user. Deriving the
  columns from the anchors alone dropped the most useful column in the table on
  the grounds that a superclass did not mention it. The permitted set is
  computed over every class the filter matches.
- **Every heading carries the attribute name under the label.** "Email" over
  `mail`, in the schema's own spelling. A heading that says only Email teaches
  somebody their directory has a field called Email, and the next thing they
  write is a filter on it. The label is a convenience; the name is the fact.
- **Sorting is over the rows that arrived, and says so.** Client-side, on the
  loaded page, with a line under the table whenever the search was truncated. A
  "first alphabetically" that is really "first out of the two hundred returned"
  is a lie a table tells very convincingly.
- **Bulk selection stages; it does not apply.** Selecting rows and choosing
  Stage deletions adds them to the basket and says so — the changeset view then
  shows all of them as one LDIF document with a single apply. No second write
  path was added, and the one confirmation step is where it always was.
- **Directory became a section with pages, rather than a dropdown.** Users is a
  destination; hiding it one click inside a menu would undo the reason for
  having it. The tree stays as the first page, unchanged.
- **A seventh Radix package, deliberately.** `react-dropdown-menu`, for the row
  menu. Menu keyboard behaviour — focus return, roving tabindex, typeahead, not
  fighting the dialog overlay — is invisible until it is missing, and a table of
  several hundred rows is where it matters. Asked and approved before install,
  per the dependency rule.
- **`task generate` was broken on main and nobody had run it.** `api/openapi.yaml`
  carried a duplicated `x-enum-varnames` key, which YAML rejects outright; the
  committed generated files predated it. Fixed here because it blocked the spec
  change, and worth noting: generated output being committed hid a broken
  generator for two releases.
- **Two bugs the tests could not have found, and running it did.** The table
  rendered two hundred rows into a container it could not scroll, so everything
  past the fold was unreachable — a missing `h-full`, invisible to a typecheck.
  And the select-all checkbox drew a tick for the indeterminate state, so one
  row selected out of two hundred looked exactly like all two hundred, on the
  control that arms a bulk deletion. Both were found by opening the page against
  both servers before handing it over, which is now simply how a slice finishes.

### 2026-09-05 — field documentation and boolean inputs (console slice 2)

- **The syntax picks the control; nothing else does.** An attribute declaring
  RFC 4517's Boolean syntax gets TRUE / FALSE / not set. One holding text that
  reads like a switch gets a text box with a suggestion. The two servers land on
  opposite sides of that line and neither is named: in `cn=config`, OpenLDAP has
  five real Booleans and no on/off strings, 389 DS has ninety-two on/off strings
  and no real Booleans at all. A conformance case now asserts the syntax lookup
  holds on both.
- **A Boolean with no value reads "not set", not TRUE.** The old control
  defaulted an empty value to TRUE, which showed a value the entry did not have
  and invited a change nobody made. Not set is also selectable, so an optional
  Boolean can be cleared — it produces `delete: <attr>`, which is what an absent
  attribute is. A value that is neither TRUE nor FALSE is displayed as it is,
  because a blank box over a real value is worse than an odd-looking one.
- **The pseudo-boolean control is a text box, not a select.** It is a datalist:
  the two words are offered, free text stays reachable, and what is in the box
  is what is sent. A control that corrected `on` to `TRUE` would be the same
  class of bug as the edit that dropped `SUBSTR` — a change nobody asked for,
  applied silently. The field says so in as many words.
- **`0` and `1` are not a boolean pair.** They read as one until the attribute
  is `nsslapd-auditlog-logrotationsyncmin`, whose value is `0` and whose unit is
  minutes. Forty fields on 389 DS's `cn=config` hold `0` or `1` and are genuine
  numbers. Nothing in the value distinguishes the two cases, so neither is
  guessed at; the vocabulary is on/off, yes/no, true/false, enabled/disabled.
- **The suggestion is seeded once, from what the server sent.** Recomputing it
  as the box is typed into made it vanish mid-edit, the moment the field was
  cleared to retype it.
- **A description repeated across an entry is a category, not a description.**
  389 DS answers "Netscape defined attribute type" for a hundred and
  forty-nine of the attributes on `cn=config`; only six distinct descriptions
  cover a hundred and ninety-three fields. Printing all of them buries the one
  line worth reading. So a description is shown beside the field when it belongs
  to exactly one attribute on screen, and stays on the name's tooltip otherwise.
  Nothing the server said is discarded.
- **No prose of our own.** Every description shown is the server's `DESC`.
  A hand-written gloss is one more thing to drift out of step with the directory
  in front of you, and the server has already answered.
- **A found bug: the editor knew nothing about any attribute being added.** It
  looked an attribute's schema up among the attributes the entry already had,
  which by definition never contains the one being added. So everything added
  through the picker arrived badged "not in the schema" with a plain text box —
  no description, no syntax, no single-valued flag, and a text box for something
  the schema calls a Boolean. Worst precisely when it matters most: you reached
  for the picker because you did not already know the attribute.
- **The fix sends the schema with the entry.** `EntryView.candidateKinds`
  carries the schema's opinion about every attribute the entry's classes permit
  but which it does not have, so the editor needs no second round trip and the
  control is right the moment the field appears. A conformance case asserts both
  servers can describe every attribute their suffix entry permits.

### 2026-09-05 — creation and membership (console slice 3)

- **A wizard has no finish button of its own.** Its last step hands a
  `ChangeRequest` to the same confirmation dialog every other write goes
  through, with the same LDIF, the same Ansible tab and the same Stage button.
  This was the invariant most likely to be eroded by somebody being helpful, so
  it is written down: a wizard composes a change record and nothing else.
- **Creation and editing now share one field implementation.** They had two, and
  the creation form had fallen a long way behind: a plain text box for every
  attribute, including the ones the editor gave a Boolean control, an entry
  picker, or the attribute's own description. `AttributeEditor` moved to
  `components/attribute-editor.tsx` and both use it, so the two cannot drift
  again.
- **`GET /schema/requirements` exists so creation is generated, not templated.**
  It answers what an entry of a set of classes must and may hold, with the
  schema's opinion about each attribute. Building the form from attribute names
  alone is exactly what produced the text boxes.
- **The wizard's first step is the object views.** The kind step offers what
  `/views` already derived — user, group, organizational unit — and narrows the
  class list to that view's structural classes. A server publishes hundreds of
  structural classes and a list of all of them is not a choice anybody can make;
  "Something else" still reaches the full list.
- **Only structural classes are offered for creation, and the servers disagree
  about which those are.** `posixGroup` is STRUCTURAL on OpenLDAP and AUXILIARY
  on 389 DS. It is a groups *anchor* on both, so posix groups are listed either
  way — but it is offered as something to *create* only where the server calls
  it structural, because an entry whose only class is auxiliary is refused.
  Nothing in Alder knows which server is which; asking the schema makes the
  divergence disappear. A conformance case records it.
- **Membership is one action, not an edit of a list.** Add member produces
  `add: member` with a single value and Remove produces `delete: member` with a
  single value. The alternative — reading the list, changing it, posting it back
  — silently removes anybody another administrator added in between, and group
  membership is the most concurrently edited attribute a directory has. Neither
  path reads the current list to build its change, so neither can overwrite it.
- **The membership controls come from the classes, not the values.** An empty
  group is still a group. Deciding from the values present would mean the
  control for adding the first member appears only after somebody has added the
  first member by some other route.
- **The membership attribute names are standards-track, like the view anchors.**
  member and uniqueMember from RFC 4519, memberUid from RFC 2307, memberURL from
  the dynamic-group draft — each used only where the server defines it and the
  entry's classes permit it. Where an entry permits more than one they are
  offered as a choice, with the warning that they are not interchangeable.
- **memberUid gets a text box, not the entry picker.** It holds a login name
  rather than a distinguished name, so there is nothing to browse to. The field
  says so rather than leaving it looking like a missing feature.

### 2026-09-05 — the URL, and a landing page (console slice 4)

- **The location is in the query string of one route, not in a path.** A DN
  carries commas, equals signs and non-ASCII text, and no two proxies agree
  about double-encoding one — which is the same reason the API takes a DN as a
  query parameter rather than a path segment. So the route tree is a single page
  and `?view=…&dn=…&filter=…` says where you are.
- **TanStack Router, which section 4 named and nothing had used.** It was a
  declared dependency that had never been imported; all navigation was
  `useState`. Hand-rolling the History API instead would have been substituting
  for a documented stack choice to avoid using the thing already installed.
- **A search is a link.** Base, scope, filter and limit live in the URL, so a
  search survives a reload, goes in a bug report, and can be sent to somebody.
  The boxes stay local while being typed into — putting every keystroke in the
  history would fill it with half-written filters — and the location is written
  when a search runs, which is when it becomes worth sharing.
- **Recent searches were cut, as planned.** They are a stored thing in a
  stateless application, and the URL is the durable artefact worth having.
- **The results table is the object views' table.** Its columns are the
  attributes that were asked for *and that something returned*: a search spans
  object classes, so a fixed set would show an email column over a page of
  organizational units.
- **The landing page counts nothing on load.** Counting a naming context is a
  subtree search, and doing three of them because somebody opened a page is
  what an administration tool must not do behind your back. It is a button per
  context, and the answer says "at least N" when it stopped at the limit rather
  than reporting a number that is merely the limit. `GET /count` asks for no
  attributes at all, so the cost is the search rather than the values.
- **The monitoring entry is probed, exactly as the configuration tree is.**
  `cn=monitor` is read at the conventional location and believed only if the
  server answers there and the answer is outside every naming context. 389 DS
  publishes one; OpenLDAP does not. A server with none gets no monitoring
  section rather than a row of dashes implying something failed.
- **What the monitor shows is whatever it publishes.** The attributes differ
  completely between the two servers, so a curated list of interesting counters
  would be a list of one server's counters.
- **A found bug: the probe worked and the answer never left the server.** The
  capability was resolved at connect time and stored, but `capabilitiesView`
  never mapped it, so both servers reported no monitoring entry. It is the third
  time in this programme that a correct back end has been invisible because the
  conversion to the API type was not updated with it.

### 2026-09-05 — schema provenance and the password scheme (console slice 5)

- **Provenance is reported, never inferred, and the two servers answer by
  different routes.** One keeps its schema in configuration entries and
  discards `X-ORIGIN` when it loads a schema file; the other keeps `X-ORIGIN`
  and has a single schema entry where the collection would say nothing. The
  measurements are exact and a conformance case records them: 186 definitions
  placed by collection and no `X-ORIGIN` on one, 177 carrying `X-ORIGIN` and no
  collection on the other.
- **So there is no "shipped or custom" column.** It was the original plan and it
  is not buildable honestly: on the server that discards `X-ORIGIN` such a flag
  would be a guess, and a column that guesses on one of two supported servers is
  worse than a column that is absent. "Which collection is this in" is the more
  useful question anyway, and it is the one an administrator actually asks.
- **The origin map costs nothing.** The config-style schema entries were already
  being read to count their definitions, so noting which collection each OID
  came from is one pass over values already in memory, at connect time.
- **The schema keeps both shapes.** A list beside a detail pane is right for
  reading one definition, having followed a cross-link. A table is right for
  "what does this directory define, where did it come from, and which of it is
  mine" — a thousand attribute types in a two-hundred-pixel column cannot answer
  that. The toggle preserves the section and the filter, which is why it is a
  toggle rather than two pages.
- **The password scheme crosses the wire; the hash never does.** The server
  already read `userPassword` and withheld it. It now also reads the `{SCHEME}`
  prefix off the front and sends only that. The parse stops at the first closing
  brace, refuses anything that is not valid UTF-8, and caps the label at
  thirty-two characters, so the hash cannot escape through it. Tested against
  every shape that could make it: no closing brace, a brace far past anything
  real, empty braces, a brace that is not at the start.
- **An unprefixed value is reported as stored in the clear, not as unknown.**
  RFC 2307 says an unprefixed `userPassword` *is* the cleartext password. The
  first implementation returned nothing for it, which hid the worst case behind
  the same blank the best case leaves — exactly backwards for the one fact this
  feature exists to surface. Found by running it: the harness's OpenLDAP has no
  default password hash configured and stores the seeded passwords verbatim,
  and Alder said nothing about it until this was changed.
- **The password policy form was cut, as planned.** Showing the scheme is the
  small honest half and answers the question that was actually asked.

### 2026-09-06 — the changeset had a bound nothing enforced

- **`maxItems: 500` was a description, not a check.** `api/openapi.yaml` declared
  it on `ChangesetRequest.changes`, with the note that a cap exists because the
  whole set is rendered and applied in one request — and neither changeset
  handler looked at the length. There is no request-validator middleware, so
  the spec's bound reached nothing.
- **It held by accident, and the accident was about to end.** The only route
  into the basket was one confirmation dialog at a time, so nobody could reach
  five hundred by clicking. Every feature that stages in bulk removes that: an
  imported document is bounded at eight megabytes, which is tens of thousands
  of records, and the changeset view previews automatically on any non-empty
  basket rather than on a button press. So the check goes in before anything
  that fills the basket, not alongside it.
- **The refusal names both numbers and what to do.** A bound that says only
  "too many" cannot be acted on: the operator needs the limit and the count.
- **`changesetSizeOK` returns a bool, and that is not the clumsier shape.**
  `badRequest` ends in `c.Status(400).JSON(...)`, and `JSON` returns nil when
  the write succeeds — so a helper returning that error hands its caller nil
  after refusing, and the handler writes a 400 and then carries on processing
  the request it just rejected. This was written the tidy way first and the
  test caught it immediately, which is the same reason `parseDNParam` returns
  `(value, ok)`.
- **Bulk staging is all-or-nothing.** `changeset.addMany` stages every change or
  none. Staging as many as fit is the tempting implementation and the wrong
  one: it leaves the basket holding part of a set that was asked for as a whole
  — a subtree missing its deepest entries, the first four hundred records of a
  document — which looks like it worked and then applies to something nobody
  chose.
- **The client's copy of the bound is a courtesy, not the enforcement.** It
  exists so a refusal happens before anything is staged rather than at preview
  time. The server decides; if the two disagree the failure is a refusal, not a
  wrong write.
- **The search results table can stage deletions.** It was one prop:
  `EntryTable` has carried the selection column and the staging bar since the
  object views shipped, and offered them only where `onStageDeletes` was
  passed. A search under an arbitrary OU could not reach what the three view
  pages could.
- **Tested at the boundary, which had never been tested at all.** No test in
  `internal/api` had ever built a fiber context; every one ran against pure
  functions. An unvalidated request is exactly the failure that shape of test
  cannot see.

### 2026-09-06 — which groups is this person in

- **The most-asked question about an account had no answer.** Membership was
  forward-only by construction: a group's members render as links, and there
  was no way back. `memberOf` appears nowhere in the codebase, and nothing in
  the API asks a reverse question.
- **It ships as a link, not a panel.** The search page already renders the
  answer as a table with row actions, and its filter is in the URL — so the
  answer is shareable, the query is visible and editable rather than hidden
  behind a button, and the whole feature is one string on `EntryView` plus a
  button. A panel with its own removal controls was the larger shape, and it is
  worth building only once the reading half has been used.
- **The filter is built in Go, not in the browser.** A DN is somebody else's
  data and the escaping rule lives in `internal/filter`; concatenating it in
  TypeScript would put that rule in a second place, next to a `esc()` helper
  whose own comment calls itself belt and braces. Tested with DNs carrying a
  comma, parentheses, an asterisk and a backslash.
- **Seven standards-track names, and only what the server defines.** member,
  uniqueMember, owner, manager, seeAlso, roleOccupant and secretary — RFC 4519
  and RFC 4524, resolved against the connected schema exactly as the view
  anchors and the membership attributes are. A sweep of every DN-syntax
  attribute would put forty terms in one filter, most of them operational, and
  ask a far more expensive question than anybody meant.
- **An attribute the directory owns is not asserted.** NO-USER-MODIFICATION or
  an operational usage means a reference nobody can remove, and a row offering
  to unpick it can only fail. `memberUid` is excluded for a different reason: it
  holds a login name, not a DN, so it cannot match a subject DN at all.
- **The seed only ever exercised one shape.** It held 307 `member` values and
  not one `uniqueMember`, `owner`, `manager` or `seeAlso` — so a conformance
  case asserting "every entry that names this person" would have passed while
  testing a single attribute. Two groups were added: one `groupOfUniqueNames`,
  which is structural on both servers and therefore keeps the seed byte
  identical, and one carrying an `owner`. The suite now asserts all three
  shapes on both servers.
- **A test that failed for the right reason.** The first conformance run
  expected `cn=platform` and got `cn=network`: the generator spreads users
  across teams by index, and user0001 lands in the second. The lookup had found
  all three references correctly; the expectation was wrong. Worth recording
  because a reverse-lookup test that asserts the wrong group would otherwise be
  fixed by loosening it.

### 2026-09-06 — a parsed document can be staged whole

- **The whole-set validation had no way of being reached from a document.**
  `changesetWarnings` reports a parent created after its child, an entry acted
  on after it is deleted, and the same DN changed twice — findings that exist
  only for a set — and the only routes into the basket were one confirmation
  dialog at a time and a table's selection. An imported document is the place
  multi-record work actually comes from, and it could reach none of it.
- **"Stage the remaining N", not "Stage all".** A record already applied from
  this panel, or already in the basket, is not staged again, and the button says
  the number it will act on rather than the number on screen.
- **A record is in exactly one of three states, and staged disables its own
  button.** This closes a hazard that predates the feature: the panel tracks
  applied records in local state, and a changeset applied from the changeset
  view never writes back into it. Two live routes to the directory for one
  record is a record you can apply twice — harmless for an add, which fails with
  entryAlreadyExists, and silent for a delete or a replace.
- **Staging is bounded by the same cap as everything else,** and refuses the
  whole document rather than a prefix. A document is a set: staging the first
  four hundred records of it and stopping produces something that looks like it
  worked.
- **The refusal is worded once.** `stageChanges` is shared with the table
  selection, because both refuse for the same reason and two callers explaining
  the same limit differently is how a bound stops reading as one rule.
- **The confirmation is moved, not skipped.** Every record's LDIF and warnings
  are already on screen before the button is reachable, and the changeset
  re-renders the combined document before a single Apply. That is the shape
  already blessed for a table's bulk selection.
- **Nothing about applying changed.** `/changeset/apply` still walks the set one
  record at a time through `Session.Apply`, and still halts at the first
  failure, which the view says in as many words.

### 2026-09-06 — deleting a container

- **Alder told the operator what to do and gave them no way to do it.** The
  result-code hint has said, since v0.6.1, that "the entry has children, and a
  directory removes entries one at a time from the bottom" — and there was no
  route to that anywhere in the product.
- **It counts before it walks.** `GET /count` is a bounded search asking for no
  attributes, so it is cheap, and it is the thing that makes the refusals
  possible. The number is shown before anything is staged.
- **Four refusals, and the default is to refuse.** No paging on the server; a
  count that came back truncated; a subtree larger than what is left in the
  changeset; and nothing underneath at all. Each prevents the same outcome — a
  partial subtree delete, which removes the leaves it reached and leaves every
  container standing, a state worse than not having started and one the
  operator cannot see coming.
- **That logic is a pure function with its own tests.** It was written inline in
  the dialog first, and verifying it meant driving a browser into a state where
  the basket was nearly full — which is the wrong way to test the safety logic
  of the most destructive action in the application. `checkSubtree` is now
  `lib/subtree-refusal.ts`.
- **Ordering is by depth, deepest first, and that is sufficient.** A child
  always has more RDNs than its parent, so descending depth puts every child
  ahead of every ancestor without building the tree. Entries at equal depth in
  different branches are unordered with respect to each other, which is correct:
  neither is the other's parent.
- **`splitDN` is no longer display-only, and its comment now says so.** Counting
  its components is what orders the deletions, so an RDN with an escaped comma
  counted naively would look a level deeper than it is and be staged ahead of
  its own children. The harness holds such an entry precisely because tools get
  this wrong, and there is now a test for it.
- **It stages DNs and nothing else.** The walk asks for `1.1`, so a subtree of
  three hundred entries does not pull three hundred entries' worth of values
  into the browser to be thrown away.
- **The copy says the run halts.** `/changeset/apply` stops at the first failure
  and leaves the rest staged; a subtree delete is exactly where somebody would
  otherwise assume all-or-nothing.
- **A test that failed on a locale, not on logic.** The first refusal test
  asserted "10,000" and got "10 000": the message is formatted with
  `toLocaleString`, which is right — a thousands mark belongs to the reader —
  and the assertion was wrong to hardcode one.

### 2026-09-06 — the columns you asked for, and the file they make

- **The candidate columns are computed on the server, and this is a correctness
  decision rather than a preference.** The set has to be walked *down* the class
  tree — over every class the view's filter matches — not along the anchors'
  superior chains. `person` permits no `mail` and `inetOrgPerson` does, so the
  superior walk drops the column people came for. That is the bug the object
  views were fixed for on 2026-09-05, and repeating the walk in the browser is
  exactly how a second copy of the rule would drift from the first.
- **There was no route to a site attribute at all.** `employeeNumber`, a
  cost-centre attribute, anything a directory owner added — none of it could
  appear in any table. The users view now offers 65 candidates on OpenLDAP and
  56 on 389 DS, against 5 shown by default.
- **The chosen columns are part of the search's cache key,** so choosing one
  re-runs the search asking for it. Without that a column added after the fact
  shows a dash in every row, which reads as "this directory holds no such value"
  rather than "nobody asked for it" — the two are indistinguishable on screen
  and only one of them is true.
- **The choice lives in the tab and nowhere else.** A stored column preference
  is server-side state in an application that has none, which the changeset
  decision already settled.
- **`GET /export/ldif` takes a filter,** parsed by `internal/filter` and never
  pasted, so a table can export what it is showing. Before this the only
  exports were one entry or a whole subtree, and "the thirty people I just
  searched for" — the thing that actually goes in a ticket — could only be
  assembled by hand.
- **The filter goes in the file's header.** A filtered export describes
  something narrower than its base, and a file that does not say so reads as
  the whole of it later, which is the same failure the truncation warning
  already guards against.
- **Nothing matched is not a 404.** Without a filter, no entries means the base
  is not there. With one it means the base exists and holds nothing matching —
  a different thing to be told, and not a missing entry.
- **`format=ansible` was deliberately left out.** It rides in on this
  parameter's coat-tails and does not deserve to: `community.general.ldap_entry`
  with `state: present` asserts existence and does not reconcile an entry that
  already exists, so a playbook rendered from live content is "recreate these if
  absent" rather than "enforce this". It needs its own design, its own
  server-side attribute sanitiser and parent-first ordering, and its own
  decision.

### 2026-09-06 — the referenced-by panel

- **A search says which entries matched, never which term matched.** The link
  shipped alongside "referenced by" answers "what names this entry", and that
  is the whole answer for reading. It is half of it for removing: unpicking a
  reference is a delete of one value of one *named* attribute on the
  *referring* entry, and `uid=user0001` in the harness is a `member` of one
  group, a `uniqueMember` of another and the `owner` of a third. Offering a
  Remove button without knowing which of those a row is would be guessing at a
  write, so `GET /references` does the matching and reports the attribute.
- **The matching happens on the server, where the values already are.** The
  alternative is sending every reference attribute of every matching entry to
  the browser to be compared and thrown away, and a group with five hundred
  members is five hundred DNs that never need to cross the wire.
- **References are compared as DNs, not as strings.** A directory may return a
  reference spelled differently from the entry's own DN — a different case in
  an attribute name, a different escaping of the same value — and a string
  comparison would report no reference where there is one. Under-reporting is
  the worst available answer to "what would break if I deleted this", so the
  comparison is the one the directory itself would make.
- **`uniqueMember` is trimmed at its `#uid` suffix, and not only when the DN
  parser refuses the value.** RFC 4517's Name and Optional UID is a DN with an
  optional bit string appended, and `#` is only special at the *start* of an
  RFC 4514 value — so `dc=test#'01'B` parses perfectly happily as a naming
  attribute whose value ends in a bit string. Retrying the comparison only on a
  parse error would therefore skip exactly the syntax the retry exists for.
  A test caught this; the first version of the code had it wrong.
- **The removal names the value the directory stores, not the subject's DN.**
  `uniqueMemberMatch` compares the UID part as well, so deleting the bare DN
  where the stored value carries a suffix matches nothing — and the directory
  reports the modification a success while the reference survives it. That is
  why `Reference` carries `value` and not just `dn` and `attribute`.
- **The panel keeps the link, as "Open as a search".** The link's own argument
  was that a filter in the URL is shareable and editable where a panel is not,
  which is still true and still worth having — and it is the answer when the
  bounded search truncates.
- **The bound is 200, and truncation is said out loud.** A list of references
  that quietly omits some is worse than no list at all, because it is read as
  "this is everything" by someone about to delete an entry.

### 2026-09-06 — bulk staging is in scope, and the line says where entries come from

- **The scope list said "bulk or CSV provisioning" and the software had grown
  three ways to stage many changes at once.** That is the shape the `cn=config`
  entry already records: a line in the list was contradicted by the code, and
  the fix was to correct the line rather than the software. Doing it before
  someone reads the list and closes a pull request over it is the cheap moment.
- **The distinction is where the entries come from, not how many there are.**
  Alder stages what the operator authored or what the directory already holds —
  a subtree's deletions, a parsed document, a table's selected rows. It does not
  generate entries from a spreadsheet or a feed, and that is what the excluded
  line means; it now says so instead of leaving a reader to infer it from a
  count.
### 2026-09-06 — the changeset cap is 2000, and the number was measured

- **500 blocked the case it cost least to allow.** A container delete of 600
  entries could not be staged at all, and a delete is the smallest record the
  changeset can hold — two lines of LDIF. The cap was refusing a legitimate,
  cheap operation while a 500-record bulk *modify*, which costs half again as
  much to render, was allowed.
- **The number came from measuring the thing the cap protects,** which is one
  `/changeset/preview` request rendering every record's LDIF, the combined
  document and the playbook. Against the harness: 500 deletes is 0.85 MiB and
  0.19s; 2000 deletes is 3.3 MiB and 0.14s; 2000 modifies — the heaviest shape
  — is 4.9 MiB and 0.18s. Server time is not the constraint at any of these
  sizes; response size is, and a few MiB to a browser on a LAN is not a reason
  to refuse a real container.
- **A cap is a guard against absurdity, not a review-quality mechanism.**
  Nobody reads 2000 LDIF records one at a time, and the changeset never asked
  anybody to: a subtree deletion is reviewed as a set — this subtree, this many
  entries, deepest first — which is why the count and the ordering are what the
  panel leads with. Choosing the number as "the most a human can review" would
  be choosing it for a review that does not happen that way.
- **One over the limit is still refused, and refused before any work.** 2001
  records returns 400 in 0.05s, naming both the limit and how far over the
  request is. That refusal is what makes the bound honest, and it is why the
  number can be raised again later without anything becoming unsafe.
- **Three files hold this number** — the Go constant, `maxItems` in the spec,
  and the SPA's `maxStagedChanges` — and two tests exist solely to fail when
  they drift apart. They were updated with it.
### 2026-09-06 — the search, as the command you would have typed

- **Rendered by the server, for the same reason the LDIF preview is.** The
  browser holds the filter as it was typed; the server holds the filter it
  parsed and actually sent, and normalising it is the entire point of parsing
  it. A command built from the typed text would describe a different search
  from the results sitting beside it — which is the one thing this product
  cannot do.
- **A field on `SearchResponse`, not a new endpoint.** The search already
  happened and the server already holds the base, scope, parsed filter, limit
  and attribute list; there is nothing to ask for. It also cannot drift from
  the search that produced it, because it is produced by the same request —
  the same argument as `referencedByFilter` on `EntryView`.
- **`-W`, never `-w`.** The password prompts. It is not in the string, and a
  test asserts that `-w`, the flag that takes one on the command line, never
  appears. This is a new surface that could have carried a credential out of
  the session, and it is the reason the renderer takes a `commandTarget` rather
  than the session itself.
- **The command reproduces what the session did, not what it should have
  done.** A session that skipped verification emits `LDAPTLS_REQCERT=never`,
  and one given a private CA gets a comment saying the bundle has to be
  installed or named — the bytes cannot go on a command line, and a command
  that omits them fails with a certificate error the reader then has to
  diagnose. StartTLS gets `-ZZ` rather than `-Z`, because the single form
  continues unencrypted when the upgrade fails, which is not what Alder did.
- **Arguments are shell-quoted, and that is the same class of bug as the rest
  of this codebase.** A DN carries commas and spaces, a filter carries
  parentheses and ampersands; pasted unquoted, one of them does something other
  than it appears to. `=` and `,` are not shell metacharacters, so ordinary DNs
  come out unquoted and readable, and everything else is single-quoted with the
  `'\''` dance for an apostrophe.
- **Verified by running it.** The emitted command, given to a real `ldapsearch`
  in the harness, returned the same twelve entries the API did.
### 2026-09-06 — three pieces of debt, cleared

- **`NewEntryDialog` is gone.** It was exported from `entry.tsx`, imported by
  nothing, superseded by `CreateEntryDialog`, and it was the last place in the
  SPA that built a DN by string concatenation and split an RDN on `=`. Dead code
  that breaks a hard rule is worse than dead code: it is a worked example of the
  wrong way, sitting in the file somebody copies from.
- **The filter builder validates the attribute rather than escaping it.** It
  escaped the value and interpolated the attribute name raw, into a free-text
  box. That was never a hole — the server parses and rebuilds every filter, so
  nothing malformed reaches the directory — but it let the builder write a
  filter that read as one query and described another, into a box the operator
  is invited to trust and edit. Escaping was the wrong fix: RFC 4515 escaping is
  for assertion values, and an attribute holding a parenthesis is not a name
  needing escaping, it is not a name. So a clause whose attribute cannot be an
  attribute description is marked in the row and left out of the filter.
- **The builder's logic moved to `web/src/lib/filter-builder.ts` with tests,**
  which is what made the above testable rather than a claim.
- **`capabilitiesView` now has a test that fails when it falls behind.** The
  mapping is hand-written and nothing kept it in step with the struct it maps;
  a field the driver fills and the wire drops is invisible in the worst way —
  correct back end, green tests, absent feature. That has cost three debugging
  sessions. Every `directory.Capabilities` field must now either change the
  rendered view or be named in `notOnTheWire` with a reason, and the allowlist
  is itself checked for names that no longer exist. Five fields are excused:
  the two vendor fields ride on `SessionInfo`, and `StartTLS`, `AllOperional`
  and `SupportedLDAPVersion` answer questions the browser does not ask.
- **The guard was verified by breaking it.** Dropping the `WhoAmI` mapping makes
  it fail and name the field and the file. A guard that has never failed is a
  guard nobody has checked.

### 2026-09-06 — the Ansible export of live content, settled

- **`ldap_entry` with `state: present` does not converge, so a playbook made of
  it alone is a lie.** It asserts an entry exists and creates it when it does
  not, then stops: run it against an entry that exists holding entirely
  different attributes and it reports "ok" and changes nothing. A file generated
  from live content, named like a playbook, that silently does not enforce what
  it lists is worse than no file — somebody runs it, sees green, and believes
  the directory matches it. Of the two options this decision was parked on, the
  converging one was taken rather than the disclaimer.
- **Each entry becomes two tasks:** `ldap_entry` to bring it into existence, and
  `ldap_attrs` with `state: exact` to bring the listed attributes to exactly
  those values. `exact` is the state `stateFor` already maps a replace onto, so
  this is the existing vocabulary used for the existing meaning.
- **`Task(ChangeRecord)` was deliberately left alone.** It renders a change the
  operator authored and has not applied, and its LDIF and Ansible renderings
  must describe the same change — an `add` is a create in both, and making the
  Ansible converge would have broken exactly the invariant that package exists
  to hold. Enforcement is a different job on different input: entries as they
  are, with no change record anywhere in sight. Hence a separate renderer,
  `EnforceTasks`, rather than a flag on the old one.
- **Ordered parent first, because a DN is a slice of RDNs and depth is its
  length.** A search returns the server's order, not the tree's, and
  `ldap_entry` cannot create a child under a parent that does not exist yet — an
  unordered playbook fails on its second task against an empty directory, which
  is the case somebody generates one for. Sorting is stable, so entries at one
  depth keep the order the directory gave them.
- **`/export/ansible`, not `format=ansible` on `/export/ldif`.** The obvious
  change was the parameter; it would have left the URL saying "ldif" while
  returning YAML. The two are also not one operation with two serialisations: an
  LDIF export transcribes entries, this asserts what they should be, which is
  why only one of them is ordered and only one of them drops attributes.
- **There is no "include sensitive" option, and that asymmetry is the point.**
  LDIF export offers one because recreating an entry elsewhere is a real reason
  to carry a hash. A playbook is a file destined for a repository, and the
  same argument does not transfer. Operational and `NO-USER-MODIFICATION`
  attributes are dropped too: a task enforcing one fails on every run against a
  server doing its job.
- **Verified with the real tool.** The generated file parses as YAML and passes
  `ansible-playbook --syntax-check` with `community.general` installed. Nothing
  was run against the harness.

### 2026-09-06 — the format choice reaches the tables

- **The playbook export shipped where it was least useful.** The entry page had
  the choice and the tables did not, which left "the thirty people I just
  searched for, as a playbook" unreachable — and that is the set worth
  enforcing. A single entry rarely is.
- **A menu, not the entry page's dialog.** There is one decision to make here.
  The base, scope and filter are not choices: they are the search already on
  screen, which is the whole reason for exporting from a table rather than from
  a subtree that happens to contain it. A dialog would ask for confirmation of
  things nobody chose.
- **One component for both tables, and `exportUrl` in `objects.tsx` went with
  it.** Two call sites building the same URL by hand is how they come to
  disagree about which parameters an export takes — and a third would have
  arrived the next time a table did.
- **The truncation warning was already right, and is what makes this safe.** The
  users view stops at 200; a playbook of those 200 says in its header that the
  result was truncated and does not describe the whole subtree. Without that,
  enforcing a partial export would silently describe a directory nobody has.

### 2026-09-07 — the HTTP layer gets tested, and the object views get a limit

- **Nothing had ever exercised a handler.** The conformance suite sits *below*
  them, driving the Driver against two real servers; the unit tests sit *beside*
  them, covering the pure functions they call. Between the two was the layer
  that decides what a browser actually receives — routing, parameter parsing,
  the guards, and the shape of the JSON. Of 37 functions on `*Server`, two were
  named by any test. That is the layer where a correct back end has been
  invisible three times.
- **A fake `directory.Session`, not a mock.** Handlers are judged by what they
  return, not by which methods they happened to call. The tests build a `Server`
  directly — same package — so no test-only door has to exist in production
  code, and they need no Docker, so they run on every `go test ./...` rather
  than only where a harness exists.
- **The guards were verified by breaking them.** Disabling the read-only check
  makes the test fail *and* print the write that reached the directory;
  disabling the sensitive-attribute branch makes the entry test fail with the
  hash in the browser. A guard that has never failed is a guard nobody has
  checked.
- **The password test asserts on the secret, not on field names.** An earlier
  draft matched `"password"` in the body and failed on `passwordModify`, which
  is a capability the browser needs. The session now carries a sentinel value
  and the test asserts that value never appears in any response.
- **The object views take the limit from the URL, like the search page.**
  `pageLimit = 200` was hardcoded with no control anywhere. Once the export and
  the playbook started taking the view's own limit, that silent 200 stopped
  being a cap on what you could see and became a cap on what you could take
  away: the Users view showed 200 of 305 accounts and exported 200. It is the
  same `limit` parameter the search page has always used, so a link to a bigger
  page is a link like any other.
- **The limit commits on blur or Enter, not on each keystroke.** It goes in the
  URL, and a half-typed number there is a history entry nobody wanted and a
  search nobody asked for.

### 2026-09-07 — import reconciles an entry that already exists

- **A content record is an add, and a directory refuses an add for an entry
  that exists.** That is correct, and it made the second half of an
  export/edit/import loop fail on every record — the loop whose first half
  Alder had just spent two releases building.
- **The document is not the whole truth about the entry.** Reconciling replaces
  the attributes the record names and leaves every other attribute exactly as
  it is. An export omits `userPassword` always and operational attributes by
  default; treating a file's silence as "remove it" would delete a password
  because nobody wrote it down. This is the one rule the whole feature rests on
  and it is asserted at all three levels of the tests.
- **An unchanged round trip produces no changes, not a page of no-op
  modifications.** A change that does nothing still has to be read and
  confirmed, and there is nothing there to confirm. The DNs that already match
  are reported instead.
- **Values are compared byte for byte, not by the attribute's matching rule.**
  A caseIgnoreMatch rule would call "Bob" and "bob" equal, and Alder would then
  decline to write a correction somebody deliberately made in the file. The
  case that has to be silent is the round trip, where values come back from the
  same server byte for byte — verified on both servers rather than assumed.
- **Attributes the directory owns are skipped and reported.** Enforcing one
  fails the whole record; dropping it silently would let the file mean
  something other than it says.
- **Only content records are reconciled.** A `changetype` record already says
  what it wants done, and rewriting it would be inventing an intent the document
  does not carry.
- **Three levels of test, on purpose.** The unit tests cover the decision
  logic; the HTTP tests cover the endpoint, using the harness added the same
  week; and a conformance test checks the three things about a *real* directory
  the design depends on — that written values come back byte-identical, that
  replacing an attribute with its own values is accepted, and that a modify
  naming only `mail` leaves `userPassword` intact. The last one is the safety
  claim, and it now holds on OpenLDAP and 389 DS rather than in principle.

### 2026-09-07 — expanding a group's membership

- **A member list answers the question only while no member is a group.**
  `cn=everyone` in the harness lists five members and contains no people —
  every one of the five is itself a group, and three hundred people are in it.
  The entry view was showing the five and calling it the membership.
- **Each member carries the chain of groups that reached it.** That is the part
  a flat list cannot give, and the part somebody needs in order to remove an
  unwanted member from the *right* group rather than the outermost one.
- **A cycle is a property of the path, not of everything seen.** The first
  version compared against every DN already recorded, which meant the root
  group — never in the result list — could not be recognised when a descendant
  named it, and a two-group loop went unreported. Reaching a group that is on
  the branch you came down is a cycle; reaching one already visited on a
  different branch is somebody in two teams, which is ordinary.
- **Everything unresolvable is reported rather than dropped:** a member DN whose
  entry cannot be read is the dangling reference the referenced-by panel exists
  to prevent, seen from the other side; `memberUid` holds a login name and
  `memberURL` a search, so neither can be followed to an entry, and saying so
  beats reporting the group as empty.
- **A member has to be read with the membership attributes.** Reading only
  `objectClass`, `cn` and `uid` made every nested group look empty and the walk
  stop one level short *while reporting success* — five groups found and nobody
  inside them. The live harness caught it; the unit tests could not, because the
  fake returned whole entries regardless of what was asked for. The fake now
  honours the attribute list, and reintroducing the bug fails two tests.

### 2026-09-09 — comparing two entries

- **A naive diff answers "why does this account work and that one not" badly in
  three specific ways,** and avoiding those is most of the feature. It compares
  what is *present*, so an attribute one entry's classes require and it does not
  hold is invisible — and that absence is frequently the whole answer. It
  compares bytes, so two DNs naming one entry in different case read as a
  difference the directory does not agree with. And its instinct is to show both
  sides of everything, which for `userPassword` is what rule 6 forbids.
- **Each side is annotated from its own object classes,** which the two entries
  need not share. `sn` is required on an inetOrgPerson and not permitted at all
  on an organizationalUnit, and reporting that as a plain "only on the left"
  hides the reason.
- **Presence is reported for a sensitive attribute; equality is not.** `/entry`
  already says a password is set, so saying so here reveals nothing new — but
  reporting whether two entries hold the *same* hash would make the product an
  oracle about password material it offers nowhere else. The row is marked
  `comparable: false`, and a test asserts the verdict does not change with hash
  equality. This was the one call the design flagged for a human; it is
  deliberately the conservative side and is additive to reverse.
- **DN-valued attributes are compared as DNs; everything else byte for byte.**
  The narrow exception `references.go` already justified twice. Checked by
  syntax OID rather than by `KindOf`, because Name-and-Optional-UID — which is
  what `uniqueMember` carries — maps to `KindString`, so a `Kind == KindDN` test
  misses exactly the attribute most likely to differ only in spelling.
- **The union is keyed on the attribute description, options included.**
  `foldName` folds `schema.BaseName` and drops options, so keying on it would
  merge `userCertificate;binary` with `userCertificate` into one row and call
  two distinct attributes "same". `foldDescription` sits beside it and keeps
  them.
- **Operational attributes are ordered last, not dropped.** `entryUUID` and
  `modifyTimestamp` always differ and are almost always noise, but
  `pwdAccountLockedTime` is operational and is sometimes the entire answer.
- **The fake session now answers Read per DN.** It returned the same entry for
  every DN, so a handler that read one entry twice — and confidently reported no
  differences — would have passed every test. Breaking the handler that way now
  fails, naming the DN it read twice.

### 2026-09-09 — the value inventory, and what it refuses to claim

- **It is a tally over a bounded search, so it never claims to describe the
  directory.** Rule 4 admits no unbounded search, so every number is scoped to
  the entries *examined* and `truncated` says the bound was reached. A tally
  that quietly summarises a truncated result set is not a weaker answer than
  the truth; it is a confident wrong one, and this feature is worth having only
  if it is trusted. The response deliberately has no field describing the
  directory, because a bounded search cannot produce one.
- **Sensitive attributes are refused before the search runs, not filtered out
  of the result.** That is the difference between never reading a password and
  reading every password and choosing not to say. `KindOf` marks an attribute
  sensitive even where the schema does not define it, so an unknown
  `sambaNTPassword` is refused too.
- **The attribute name goes through the filter builder's own RFC 4512 check**
  rather than a second validator written here, so `alderTeam)(uid=*` dies
  before anything is searched.
- **Singletons are the headline number.** A value one entry holds among three
  hundred is usually a misspelling of one that ninety hold, and that is the
  question this feature exists to answer.
- **Values are compared as bytes, and two spellings are two rows.** Folding
  them would invent an equality the directory never agreed to — and the whole
  point is to show that `Platform` and `platform` are both in there.
- **The tail is counted, not dropped.** Beyond the row cap, the remaining
  distinct values and the entries holding them are reported as a remainder;
  `distinctValues` stays exact regardless of how many rows are rendered.
- **A refused attribute is a 400, not a new error code.** The `Error.error`
  enum has no member for this and adding one would touch every client for a
  case the existing `bad_request` already describes correctly.

### 2026-09-09 — the jump palette parses, and never guesses

- **It parses and never searches.** `internal/dn` and `internal/filter` are the
  authorities on what these strings are, and a regex in the browser would drift
  from them the first time either grew a case — the same argument that keeps the
  LDIF preview server-side. But probing the directory to see whether an entry
  exists would turn every keystroke into a read, to answer a question the
  operator is about to answer by pressing enter. So `/resolve` touches no
  directory at all.
- **An ambiguous input returns both destinations rather than a guess.**
  `cn=platform` is a valid one-component DN *and* almost certainly a request to
  find something called platform. Choosing between those would be wrong about
  half the time, and offering both is also the only version of this that can be
  explained to somebody.
- **The search for a one-component DN is for what it names, not for its text.**
  The first version built `(cn=*cn=platform*)` — entries whose `cn` contains the
  literal string "cn=platform", which nobody has ever wanted. It now emits
  `(cn=platform)`, honouring the attribute the operator named as well as the
  value. The live harness caught this; the test did not, because it asserted
  only that both destination kinds came back. It asserts the filter now.
- **A multi-valued RDN is a DN and nothing else.** There is no single name in
  `cn=alice+ou=people` to search for, so only the entry destination is offered.
- **A half-typed filter offers nothing.** An input that opens a parenthesis and
  does not parse is a filter somebody is still typing; offering to search for
  the literal text "(objectClass=" would be nonsense.
- **The schema browser was deliberately left out of this feature.** Deep-linking
  to a definition means lifting the schema browser's internal section and
  selection state into the URL, which changes the application's single
  navigation primitive — a change worth making on its own terms, not as a side
  effect of a palette.

### 2026-09-09 — 1.0, and what it promises

- **1.0 is a promise about the output, not a claim that the work is finished.**
  The ten items in section 2 of `CLAUDE.md` all ship, and the tool writes to
  directories other systems authenticate against. `0.x` told an operator "this
  may break you", which had stopped being true of the write path around M3 and
  was actively discouraging the adoption the project wants. `docs/COMPATIBILITY.md`
  states what is covered and, as importantly, what is not.
- **The LDIF and the Ansible are the first surface named, ahead of the HTTP
  API.** The API is what the SPA talks to; the generated files are what somebody
  commits to their infrastructure repository and reviews as a diff six months
  later. A rendering change that reorders attributes breaks every one of those
  diffs at once without being wrong about anything, and nothing in the project
  guarded that until the golden files landed.
- **`bump-minor-pre-major` came out of the release-please configuration,** and
  that is a substantive part of what 1.0 means here rather than a tidy-up. Under
  it a breaking change and a new feature produced the same minor bump, so the
  version number could not say the thing a version number is for. It could not
  have carried the promise above while that setting stood.
- **`info.version` in `api/openapi.yaml` is the contract's version, not the
  binary's,** and the document now says so. It read `1.0.0` while the product
  shipped `0.12.x`, which was not a lie so much as an unanswered question; two
  fields called "version" in one repository need to say which is which.
- **A withheld comparison is its own status, not `same` with a flag beside it.**
  `/compare` reported `status: same` and `comparable: false` for an attribute it
  had deliberately not compared. Every client that read only the status — ours
  included — concluded the two passwords matched: the "only what differs" filter
  dropped the row entirely, so an operator asking what was different was told
  nothing about the one attribute the server could not answer for. `withheld` is
  now a status of its own, `comparable` is gone as redundant, and the counts
  gained a fifth bucket so they still account for every attribute. Done before
  1.0 precisely because an enum value and a removed field are free now and a
  major bump afterwards.
- **`/api/v1/source` is covered by the promise despite sitting outside
  `openapi.yaml`.** It carries the AGPL section 13 offer and must answer without
  a session, which is why it is registered directly. The compatibility document
  names it rather than letting the generated file's boundary silently decide
  what is binding.
- **Still out of scope, and still not built:** ACL or `cn=config` editing beyond
  the schema subtree, any multi-user concept, SSO, a persisted audit log, other
  drivers, bulk provisioning, self-service, a database. 1.0 does not widen
  section 2; it fixes the meaning of the number in front of it.

### 2026-09-10 — telling "denied" from "absent"

- **A read cannot report a one-sided attribute honestly, so the server is asked
  directly.** An attribute the access rules forbid is missing from a search
  result exactly as one the entry does not hold is; nothing on the wire marks
  the difference. `/compare` therefore reported an attribute it had been denied
  on one side as `leftOnly` — a statement about the directory, and the one an
  operator acts on, since "only on the left" is what somebody reads before
  copying a value across.
- **Compare is the operation that distinguishes them, and both target servers
  agree.** `no such attribute` for absent, `insufficient access` for denied,
  measured on OpenLDAP and 389 DS before any code was written and pinned in the
  suite. Had they disagreed, the fix would have been to weaken what `/compare`
  claims rather than to detect anything.
- **`Session.VisibilityOf` is a new method on the core abstraction.** Adding to
  section 6's interface is not free, and the alternative — a Compare reachable
  only through the LDAP driver — would have put a protocol detail in the
  handler and made the question unanswerable for any future driver. The
  question itself ("may this identity see this attribute here") is not
  LDAP-specific.
- **Only one-sided rows are probed, and at most fifty.** A row answered by
  values both sides returned needs nothing from the server, so two entries that
  differ only in their values cost no round trips at all. Two entries of
  different structural classes can differ in dozens of attributes, and past the
  cap the comparison says it stopped rather than spending unbounded time.
- **A failed probe leaves the row as it was.** The extra question is an
  improvement on the answer, not a precondition for it; a comparison that failed
  because one probe could not be sent would be worse than one that answers as it
  always did.
- **`undetermined` is a status, and the SPA's default case stopped saying
  "same".** The old default rendered any unrecognised status as agreement, so
  the enum value this change adds would have been displayed as "these match" by
  an older client. `docs/COMPATIBILITY.md` promises that new enum values are
  safe to fall through; falling through to the one claim that is never safe to
  invent is not what that means.
- **What this still cannot see.** An attribute denied on *both* sides is in
  neither read, so no row exists and nothing prompts a probe. `referenced-by`
  has the same shape and no equivalent fix: a search that returns fewer entries
  looks exactly like a directory with fewer entries, and "nothing points at this
  entry" is what somebody reads before deleting it. Neither is addressed here.

### 2026-09-10 — what a hidden entry is allowed to be called

- **"An entry deleted while a group went on naming it" was a verdict the
  product could not reach.** The membership walk lists members whose entry it
  could not read, and the panel rendered that list in red as deletions. A member
  the bind may not see answers a read exactly as a deleted one does — servers
  refuse access by saying "no such object", on purpose, so that refusing does
  not disclose what is there. Somebody auditing who can reach a system was being
  told a member was gone while they were still in the group.
- **Unlike the comparison, this one cannot be detected.** A Compare recovers the
  difference for an *attribute*; there is no equivalent for an *entry*, and the
  conformance suite now pins that both cases return the same result code. So the
  fix is to stop claiming: the panel names both causes, asserts neither, and is
  a warning rather than an error, because one of the two is benign.
- **The field keeps the name `dangling`.** Renaming a response field is a major
  bump under docs/COMPATIBILITY.md, and the name is defensible once the
  description says what it means — the reference dangles from this session's
  view, which is all the server told us.
- **The tally says whose directory it is counting.** An entry hidden from the
  bind is never examined and an attribute hidden from it lands under "do not
  hold", both indistinguishable from a smaller directory. That is lower stakes
  than the membership case — somebody hunting a typo finds fewer typos — so it
  is a sentence under the numbers rather than machinery.
- **The harness gained a member inside the hidden subtree.** cn=auditors names
  cn=svc-alder, which is in ou=services, so a delegated bind meets a member it
  cannot read without any test having to invent one. It was added to auditors
  rather than to a new group because the seeded entry count and the cn=everyone
  expansion are asserted elsewhere and should keep their numbers.

### 2026-09-10 — counting what cannot be seen

- **`numSubordinates` is the one hidden thing in the product that is directly
  countable.** Some servers publish it, they compute it themselves, and the
  access rules do not filter it — so the gap between it and the children a bind
  can enumerate is exactly what that bind may not see. Measured before it was
  built: 389 DS tells a delegated account there are three children while it can
  enumerate two, and OpenLDAP does not publish the attribute at all.
- **That difference is read from the entry, never from the server's name.** Rule
  8. Where the count is absent the tree says nothing rather than guessing, and
  the conformance case skips with the reason rather than asserting a vendor.
- **A truncated listing reports nothing.** The gap there is paging, and calling
  it access would turn "there is more here" into "somebody is hiding this" —
  which is a worse error than the one being fixed, because it invents a
  restriction that does not exist.
- **"Nothing names this entry, so deleting it would leave no reference
  dangling" was a conclusion the search could not support.** referenced-by
  returns the referring entries this session can see; one hidden by the access
  rules is missing from it exactly as one that does not exist is. It now says
  "nothing this session can see names this entry", which is the claim the search
  actually makes. There is no equivalent of the numSubordinates trick here — a
  search that returns fewer entries looks exactly like a directory with fewer
  entries.
- **The harness gained a reference out of the hidden subtree** rather than a new
  entry: cn=svc-alder names uid=user0001 as its manager, so a delegated bind is
  told three referrers where the administrator sees four, and the seeded entry
  count other tests assert stays at 320.

### 2026-09-10 — designing for a hundred thousand entries

- **The target is "works to 100,000 entries, and degrades honestly beyond".**
  Not "streams anything": bounded work with the bound stated is the instinct the
  rest of the product already runs on, and it covers the overwhelming majority
  of real directories.
- **The tally was the one feature the old caps made useless at that size.** It
  stopped at 10,000 entries because every entry was held at once, and a
  directory large enough for an attribute to have drifted is exactly the one too
  large to hold. Measured against 100,321 real entries: the whole directory
  tallies in 5.4 seconds and 5.8 MB, reporting truncated false. At the old cap
  the same question could only be answered about a tenth of it.
- **Counting incrementally is what made that possible, not a bigger number.**
  The fold keeps a map of distinct values rather than entries, so memory follows
  how many different values there are and not how many entries hold them. The
  pathological case is an attribute that identifies entries rather than grouping
  them: tallying `uid` across 85,000 entries cost 14 MB and the interface
  already says such a tally is a list rather than an answer.
- **The cap became a time bound and moved to 250,000.** Raising a maximum is
  additive under docs/COMPATIBILITY.md — a client that sends the old 10,000
  still works.
- **What a search costs was measured and left alone.** One search at the
  documented maximum of 10,000 entries takes a working set of roughly 300 MB,
  and repeating it plateaus around 330-380 MB rather than climbing, so it is the
  allocator holding freed spans and not a leak. It is disproportionate and worth
  fixing, but the default is 100, the maximum is a deliberate ceiling, and
  nobody reads ten thousand rows — so it is recorded here rather than rushed.
- **The bulk data is not in the harness.** It was generated, loaded, measured
  against, and discarded; the seeded 320 entries other tests assert are
  untouched. A permanent large fixture would slow every conformance run for a
  measurement taken rarely.

### 2026-09-11 — streaming the LDIF export

- **The export is rendered a page at a time and sent as it goes.** Building the
  document first meant holding every entry, every record and the rendered bytes
  at once, which is why it stopped at ten thousand entries — and "the output is
  code" is a promise about a directory, not about the first tenth of one.
  Measured: 39,338 entries render to a 14 MB document in 4.2 seconds using 18 MB
  of working set, where the old code spent 43 MB on ten thousand.
- **The entry count and the truncation warning moved to the end.** Neither is
  known until the search finishes, so a streamed export cannot put them at the
  top. This is a better answer than the one it replaces rather than a concession:
  a file that ends with its own summary is one you can tell arrived whole, and a
  count in the header of a download that died halfway is a lie the reader has no
  way to detect. docs/COMPATIBILITY.md now says the comment preamble may move
  while the records may not.
- **The Ansible export stays bounded, because its ordering is global.** It sorts
  parent first across the whole result, so it cannot emit anything until it has
  everything — `ldap_entry` will not create a child under a parent that does not
  exist yet. That is a real difference between the two exports and not an
  oversight; the limit stays at ten thousand and the reason is here.
- **The LDIF export never sorted, and now the suite says why that is safe.**
  Both target servers return a subtree parent first, which is what makes an
  unsorted export re-importable. It was an unstated assumption; it is now a
  conformance case, and the day a server stops honouring it the export will have
  to sort and give up streaming.
- **A streamed response cannot fail with a status code.** Once the first byte is
  out the status is fixed, so an error partway through is written into the
  document as a comment saying the file is incomplete and must not be restored
  from. The first page is fetched before anything is sent, so "no such entry"
  and "nothing matched" are still proper HTTP answers.
- **The context outlives the handler, and the fake now knows it.** A stream
  writer runs after its handler returns, so `defer cancel()` cut the export off
  at its first page. The unit tests passed anyway because the fake ignored the
  context it was given; it honours it now, and reintroducing the deferred cancel
  fails two of them. A real directory found this and a fake should have.

### 2026-09-11 — the schema answers the same question once

- **`Requirements` was recomputed for every entry in a search response**, with
  the same input each time. It walks the superclass chain, canonicalises every
  attribute name into two maps and sorts both — about two fifths of the
  allocation in building a response, measured, and a third of the time.
  A thousand people in one directory have the same object classes; they were
  paying a thousand times for one answer.
- **It is memoised on the schema rather than in the handler.** The editor, the
  object tables and the comparison all ask the same question, so caching where
  the answer lives fixes it once. A parsed schema is read-only and shared by
  every request on a session, so the cache is guarded by a mutex and the
  concurrency is a test rather than an assumption.
- **The key is folded and sorted**, because the answer depends on the set of
  classes and not on the order an entry happened to list them in or on how the
  server spelled them. Two entries differing only that way share one
  computation. The key space is the distinct class combinations in a directory,
  which is dozens.
- **Measured end to end**, same harness and same query, a search returning ten
  thousand entries: 2.46 s and 388 MB before, 1.22 s and 328 MB after. Twice as
  fast for sixty megabytes less.
- **What remains is the materialisation itself**, and it is left alone. The
  response holds every entry, converts each into the wire types and marshals the
  lot, so ten thousand entries still cost something like 265 MB. Streaming that
  would mean a JSON array written incrementally and a client that reads it that
  way — a change to the contract, for a ceiling the interface defaults to a
  hundredth of. The benchmark that found this is committed, so the next person
  starts from a number rather than a guess.

### 2026-09-12 — operational is not read-only

- **Reported by the first operator to test Alder against a real directory:**
  "a lot of operational attributes don't show on Alder for 389ds by default,
  they should be discovered and easy to set (for example nsAccountLock)", and
  "same for OpenLDAP and attributes linked to module ppolicy". Both were true,
  and the cause was one confusion.
- **USAGE says where an attribute lives; NO-USER-MODIFICATION says who owns
  it.** 389 DS declares nsAccountLock as `USAGE directoryOperation` with no
  NO-USER-MODIFICATION: the server keeps it and the server also expects an
  administrator to set it. The same is true of accountUnlockTime, of aci, and of
  an OpenLDAP ppolicy pwdAccountLockedTime. Alder's schema layer already had
  both flags and got them right; everything above it read only the first.
- **So an account could not be locked from Alder at all.** Operational
  attributes are in no object class, the editor's list of what could be added
  was `must` plus `may`, and an entry that had never been locked showed no sign
  that it could be. There was nothing to type into.
- **The offer is discovered, never listed.** `SettableOperational` is every
  attribute type the server declares as directoryOperation and does not flag,
  read from the published schema. No attribute name appears in Alder's source,
  so a directory with an overlay nobody here has heard of gets the same
  treatment as one we tested against.
- **dSAOperation is excluded, and that distinction earns its keep.**
  namingContexts and supportedControl are operational and unflagged too, but
  they belong to the server rather than to an entry; offering them on a person
  would be noise. Measured before choosing: OpenLDAP declares 12 dSAOperation
  attributes and 389 DS 10, and dropping them is what turns a useless list into
  a usable one.
- **The entry view had been asserting something false.** Every operational
  attribute sat under "Operational — the directory owns these". That is right
  for entryUUID and wrong for nsAccountLock, and it is the sentence that told an
  operator not to try. It splits on readOnly now.
- **The harness gained the ppolicy overlay**, because without it OpenLDAP
  declares no settable operational attribute that means anything on a person and
  half the report could not be tested. With it: pwdAccountLockedTime, pwdReset,
  pwdPolicySubentry, pwdStartTime, pwdEndTime. The conformance case names the
  lock attribute per server and asserts the same thing about both — that the
  schema's declaration predicts what the server accepts.

### 2026-09-12 — the outline, and why it is not LDIF

- **Asked for by the operator testing Alder:** "having LDIF is quite flat when
  plaintext, maybe have something that outputs the file as a hierarchy". A flat
  list of three hundred records does not tell you the shape of what you
  exported, which is often the thing you opened it to find out.
- **It could not have been indented LDIF.** RFC 2849 gives a leading space its
  own meaning — it continues the line above — so a tree drawn with indentation
  stops being a document anyone can import. The choice was between a format that
  is almost LDIF and quietly broken, and plainly something else. It is plainly
  something else, and the first line of every outline says so.
- **It shows where entries sit and never what they hold.** Only the DN, the
  structural class and a count of what is below. That makes the sensitive and
  operational questions that the other exports have to answer disappear rather
  than be answered: there is nothing in the file to withhold.
- **Bounded, where the LDIF export streams.** A record is complete on its own,
  so LDIF can be sent as it is read. A tree cannot be drawn until the last entry
  has arrived, because the entry that decides whether a node is a leaf may be
  the last one to come back. The same reason the Ansible export is bounded, and
  the limit stays at ten thousand.
- **Siblings are sorted rather than left in server order.** A shape is compared
  against the shape it had last week, so two outlines of an unchanged directory
  have to be the same bytes; a test reverses the input and asserts the output
  does not move.
- **Parents are found through the dn package, never by cutting at the first
  comma.** `cn=Liddell\, Alice` is one component, and the harness has that entry
  so shortcuts get caught. It is in the golden files.

### 2026-09-12 — YAML, and no YAML library

- **Asked for so a subtree can be opened in an editor and read.** LDIF is flat;
  YAML nests, folds and colours, which is most of what "view it in VS Code"
  means. It sits between the two exports that already existed: the outline has
  the shape and no values, LDIF has the values and no shape.
- **No dependency was added.** Section 10 says ask before adding one, and this
  did not need asking because it did not need a library: the Ansible renderer
  has emitted YAML by hand since M4. Its scalar encoder moved to
  `internal/yamlenc` so there is one implementation rather than two that agree
  today, and the twenty Ansible golden files are the proof the move changed
  nothing.
- **Every scalar is double-quoted, values and keys alike.** It is the only YAML
  style with a complete escape mechanism, and it is what stops `no` becoming
  false, `0755` becoming an octal number and a bare `y` becoming true. A test
  pins those three, and a real YAML parser reads the golden files back to check
  the values survive.
- **Every attribute is a list, even single-valued ones.** An LDAP attribute
  holds a set. A renderer that collapsed the common case would give the document
  a shape that depended on its data, so a reader would need both paths and a
  diff would show a type change the day a second value appeared.
- **A value that is not printable UTF-8 gets YAML's `!!binary` tag** rather than
  being passed off as a string, so an editor does not pretend it is readable.
- **Nothing reads it back, and the file says so.** A YAML document full of
  directory content looks like something you could apply. Alder imports LDIF,
  which has a specification and a changetype; adding a second import format
  would mean a second reconciler and a second set of ways to be subtly wrong.
- **It is bounded, not streamed.** Same reason as the outline: a tree cannot be
  nested until the last entry has arrived.

### 2026-09-12 — the last claim, and the one that cannot be detected

- **referenced-by answers "what would break if I deleted this" with a search,**
  and a search returns what this bind may see. A referring entry hidden by the
  access rules is absent from the result exactly as one that does not exist is.
  Measured on both servers: the administrator finds four referrers of
  uid=user0001 and the delegated account three, with no error, no truncation and
  no control to tell that from a directory which really has three.
- **There is nothing to detect, and that is the difference from the
  comparison.** A Compare recovers absent-from-denied for an attribute on an
  entry you can already read. No operation says "your search would have matched
  something you may not see". So this is pinned rather than fixed, and the
  interface says what it is scoped to.
- **The scoping applies to a list with entries in it, not only to an empty
  one.** The empty case was corrected when the tree work landed: "nothing this
  session can see names this entry". The non-empty case carries the same risk
  and was still presenting a bare count -- acting on three references you can
  see is no safer when there is a fourth you cannot -- so the count now carries
  the same qualification.
- **It is said always, because it is always true.** There is no cheap test for
  whether a session is restricted; numSubordinates answers that for the children
  of one container, which is not the question a subtree search asks. A line that
  appears only sometimes would be read as a warning about this entry rather than
  as a fact about the answer.

### 2026-09-12 — streaming the search response

- **The search response is written as it arrives, and the contract did not
  move.** The entries go out first and the fields that are only known once the
  search has finished — `truncated`, `cookie`, `took` — go out last. JSON gives
  an object's members no order, so a client decoding the body sees the same
  document it saw before; `api/openapi.yaml` is untouched. This was expected to
  need a contract change and did not, which is why it is worth recording.
  Measured at the documented maximum of 10,000 entries, over a real socket:
  peak live heap 120.5 MB before and 24.1 MB after, 158 MB allocated per request
  before and 34.6 MB after. That is the 300 MB working set the 2026-09-10 entry
  recorded and left alone.
- **The page loop moved out of the driver and into the handler.** Streaming the
  writing alone would have saved the marshalled document and nothing else: the
  driver fills whatever `Limit` it is given by accumulating pages, so asking it
  for ten thousand entries put ten thousand entries in memory one layer down.
  The handler now asks for one page and asks again. `internal/directory` is
  unchanged — the driver still fills a limit when something wants it filled, and
  the tally, the exports and the reference lookups all still rely on that.
- **A failure after the first byte leaves the document unterminated.** The
  status code is settled by then and `SearchResponse` has no field for an error,
  so the choices were a closed object holding half the entries — which no client
  could tell from a complete answer — or a body that fails to parse. It fails to
  parse, and carries a trailing comment naming the failure and the count, which
  is the same instinct as the LDIF export's "do not restore from this" footer.
  Both the first page and the schema are fetched before anything is sent, so
  every failure a search normally has is still a proper status code.
- **64 KB of write buffer, because the 4 KB one the server hands out costs 15%.**
  Every time it fills, the bytes cross a pipe to the connection goroutine and
  come back as a chunk of their own; a fifteen-megabyte answer crosses it nearly
  four thousand times. Measured back to back at ten thousand entries: 387 ms
  through the 4 KB buffer, 328 ms through the wider one.
- **It costs wall-clock time, and how much is not settled.** Ten thousand
  entries, materialised against streamed, measured back to back on the same
  laptop: 309 ms against 328 ms in one sitting, 366 ms against 483 ms in a
  noisier one. Chunked framing and ten calls into the driver instead of one both
  cost something real, and the benchmark's client de-chunks the body inside the
  same process, which no real client does. Somewhere between noise and a third
  slower, for a fifth of the memory, on a path whose default is a hundred
  entries and whose maximum nobody scrolls. Recorded rather than smoothed over:
  if it turns out to matter, the number to attack is the chunk framing.
- **The benchmark had to serve over a real socket to say anything true.** The
  in-memory transport the handler tests use collects the whole response before
  handing back any of it, so a streamed body measured through it looks exactly
  like a materialised one and the client's copy of the document lands in the
  heap being measured. Through it the change read as 2x; over a socket it is 5x.
  Peak live heap is sampled rather than read at the end, because by the time the
  request returns the collector has had the whole response back either way.
- **The fake gained `driverPaging`.** Its `pageSize` models a server, and a
  server sits below the `Session` a handler talks to: without a knob that puts
  the driver's own page loop back on top, a handler asking for ten thousand
  entries at once is handed one page of a thousand and measures as frugal as one
  that asks page by page. Two of the guards are worthless without it.
