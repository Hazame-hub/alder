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
- **The wall-clock difference is not established, in either direction.** Ten
  thousand entries, materialised against streamed, measured back to back on the
  same laptop: 309 ms against 328 ms in one sitting, 366 ms against 483 ms in a
  noisier one, and 977 ms against 877 ms in a third -- streaming the faster of
  the two that time. The sign does not hold between sittings, so the difference
  is below what this machine can resolve and none of these figures should be
  quoted as a cost. Chunked framing and ten calls into the driver instead of one
  do cost something real, and the benchmark's client de-chunks the body inside
  the same process, which no real client does; if that ever shows up above the
  noise, the number to attack is the chunk framing. What is not in doubt is the
  memory: peak live heap of 17.6 MB against 123.5 MB, on a path whose default is
  a hundred entries and whose maximum nobody scrolls.
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

### 2026-09-12 — loading a hundred thousand entries offline

- **Bulk mode is a second harness state, not a bigger seed.** `task compose:up`
  still produces exactly the 320 entries the conformance suite counts;
  `task compose:bulk -- N` tears that down and builds a harness that also holds
  N generated filler entries under `ou=bulk,dc=alder,dc=test`. Keeping them in
  one harness was the obvious shape and the wrong one: the fixtures are
  deliberate and asserted on, the filler is volume, and a suite that could not
  tell them apart would start failing for reasons nobody chose.
- **The measurement it exists for.** 100,000 entries over LDAP took 863 s into
  OpenLDAP and 790 s into 389 DS. Offline, the same generated file takes 45 s
  and 58 s, and the whole of `task compose:bulk -- 100000` -- generate the file,
  rebuild both containers, load them, seed the 320 fixtures over LDAP as usual,
  count what arrived on each -- is 299 s. At 15,000 entries it is 125 s and 61 s
  against 3 s and 5 s. Scale work had been spending most of its wall clock
  waiting for `ldapadd`, which is a bad reason not to measure something.
- **OpenLDAP's load happens in the container entrypoint, because that is the
  only moment slapd is stopped.** `slapadd` writes into the mdb files directly
  and refuses to run against a live server; slapd is PID 1, so stopping it at
  any later point stops the container. That is also why `compose:bulk` recreates
  the containers rather than adding to a running harness — and it is honest
  about it, since a directory that has been bulk loaded is not the directory the
  suite runs against.
- **389 DS needed a backend of its own, because its import replaces rather than
  appends.** Both `dsctl ldif2db` and the online import task overwrite the whole
  contents of the backend they are pointed at, so importing filler into
  `userRoot` would have deleted the 320 fixtures — the one thing bulk mode must
  not do. The filler therefore lands in a second backend chained under the
  seeded suffix by `nsslapd-parent-suffix`, so a client still sees one tree and
  only the throwaway half is ever replaced. This is a genuine vendor divergence
  and it is now written down in the harness README's table with the others.
- **The 389 DS import is started by adding a task entry over LDAP, not by
  `dsconf`.** `dsconf` lives in the 389 DS container and the script driving the
  load does not; the entry under `cn=import,cn=tasks,cn=config` is what `dsconf`
  creates anyway. It also keeps the harness's existing habit of configuring 389
  DS over LDAP as `cn=Directory Manager`, which is how an operator without
  `dsconf` to hand would do it.
- **The mdb map size is raised for the load and only for the load.** Measured,
  `slapd.conf`'s 256 MB does hold 100,000 filler entries -- in 230 MB of it, so
  the next size anyone asks for fails partway with `MDB_MAP_FULL` and leaves a
  half-loaded directory that nothing announces. The entrypoint raises it offline
  in bulk mode to a sparse 8 GB, so the default harness keeps the map size its
  configuration actually states rather than inheriting a number chosen for a
  measurement.
- **The filler is generated on demand and never committed.** A hundred thousand
  entries is about 40 MB. `test/compose/seed/gen` grew a `-bulk` flag rather
  than gaining a second binary, and it writes fixtures or filler in one run,
  never both: `task seed` is the committed, CI-checked output, `-bulk` writes a
  gitignored file whose first line says it is not seed data.
- **The load is checked by counting both servers, not by trusting exit codes.**
  This earned itself immediately: the first working version rebuilt the harness
  image between the seed step and the bulk step, which left the running OpenLDAP
  container out of date against its own image, so the next `docker compose run`
  recreated it and silently discarded the database that had just been loaded.
  Every command had exited 0. What said otherwise was OpenLDAP reporting 100,006
  entries where 389 DS reported 100,321.
- **The fixtures are still loaded over LDAP in bulk mode.** Loading them offline
  too would have been faster and would have quietly changed what they are, since
  `slapadd` runs neither the overlays nor the access rules an LDAP write goes
  through. Bulk mode adds volume beside the fixtures; it does not produce a
  different set of them.
- **Seeding now waits for the credential, not for the port.** Dropping `--build`
  from the bulk task's seed step made a latent harness race reproducible: 389
  DS's container entrypoint starts `ns-slapd` and *then* connects to it over
  `ldapi` to replace `nsslapd-rootpw` with `DS_DM_PASSWORD`, so between those
  two moments the server answers searches, passes its own healthcheck, and
  rejects `cn=Directory Manager` with "Invalid credentials". `seed.sh` waited on
  an anonymous base search, which goes green inside that window. It now also
  waits for `ldapwhoami` to succeed with the credentials it is about to use.
  This bug predates bulk mode and could have hit `task compose:up` and CI at any
  time; what made it worth catching is that the window is short enough for the
  next run to pass and for nobody to look again.

### 2026-09-12 — what the tree exports were costing

- **Found by running the two previous changes against each other.** The bulk
  harness exists to measure at scale and the streamed search had just landed, so
  Alder was pointed at a real 100,321-entry directory. The streaming held up --
  a 44 MB search response at 82 MB of resident memory, the whole directory
  exported as 40 MB of LDIF for 23 MB -- which left the two bounded exports as
  the only materialising paths, and the YAML one holding 113 MB of live heap for
  an 8 MB document.
- **The bound stays. What went was everything held twice.** A tree cannot be
  nested until its last entry has arrived, so neither tree export can stream the
  way the LDIF export does; that is not the part that was expensive. Ten
  thousand entries were being indexed twice -- once into a map from folded DN to
  entry, again into a map from folded DN to node -- and every DN was parsed
  twice more while rendering, once to find the parent and once to find the RDN
  to print, when the caller had handed over a parsed DN to begin with. The RDN
  and the parent key are now taken from that parsed DN when the node is made,
  and the attributes ride on the node rather than in a second index.
- **The document is written as it is drawn.** `outline.WriteTo` and
  `outline.WriteYAML` take an `io.Writer`, and the handlers hand them the
  connection. `Render` and `RenderYAML` remain as the string forms, so the
  golden tests still compare exactly what a caller gets. Unlike the search and
  the LDIF export there is no window where a failure arrives after the status
  code: the tree is complete before the first byte goes out, and everything that
  can fail has already happened.
- **Measured, at ten thousand entries, with the LDIF export as the control.**
  YAML: 83.4 MB allocated and 1.79 M allocations became 17.3 MB and 380 k, and
  peak live heap above the fixture fell from 43.9 MB to 14.6 MB. The outline:
  25.3 MB and 890 k became 11.4 MB and 370 k. The streamed LDIF export, which
  this change does not touch, reads the same before and after, which is what
  says the numbers are the exports and not the bench.
- **`io.WriteString` to a wrapper without a `WriteString` method is a copy.**
  Writing in small fragments to avoid `Fprintf` made it *worse* at first --
  1.17 M allocations became 1.51 M -- because `io.WriteString` found only
  `Write` on the error-latching wrapper and converted every fragment with
  `[]byte(s)`. Giving the wrapper a `WriteString` method took it to 470 k. The
  same trap caught the values: `WriteScalar(w, string(v))` escapes to the heap
  once per value, which is why `yamlenc` grew `WriteScalarBytes`.
- **`yamlenc` has tests now.** Its package comment claimed "one implementation
  with one set of tests" while having none -- the escaping was only ever
  exercised through the Ansible and outline goldens. It now has three entry
  points sharing one `escapeOf`, which is a drift risk that did not exist
  before, and a test that pins all three against each other. Verified by
  breaking the bytes path alone: thirteen subtests fail.
- **Timing is not claimed.** The exports got faster in every run, by a third or
  so, but this machine put a 30% spread on the same benchmark across sittings
  while the allocation figures stayed put. The allocation and heap numbers are
  the ones worth quoting.

### 2026-09-12 — the allowlist, the ceiling, and the plan

- **Three documentation statements were wrong, and the fix is a test.**
  `SECURITY.md` said "Pre-1.0" through four 1.x releases; `docs/COMPATIBILITY.md`
  dated the search-streaming change to 1.4.0 when it shipped in 1.3.1; and the
  same document had promised since 1.0 that flags keep their
  "environment-variable equivalents" when there were none. All three are one
  failure -- a sentence true when written with nothing watching it -- so
  `cmd/alder` now reads the release manifest and fails when a document names a
  version this repository is not at. Prose cannot be checked automatically; the
  version number in it can, and that is what went wrong each time.
- **Every flag reads `ALDER_<FLAG_NAME>`, in twenty lines of stdlib.** The
  promise was the useful half, so it was made true rather than deleted.
  Environment and not a file: a container is how Alder is run. The flag wins
  over the environment, because something typed on a command line was typed on
  purpose. An unparseable value refuses to start rather than silently leaving a
  security setting at its default.
- **`--allowed-targets` turns "anyone who can reach the endpoint can point Alder
  at any host" from a property of the network into a decision.** It was always
  written down in `SECURITY.md`; now it is adjustable. Off by default, because
  Alder is normally run beside the directory by the person who owns both and a
  mandatory allowlist would be a configuration step before the first useful
  screen.
- **Enforced in `CreateSession`, before `Connect`.** The target arrives in a
  request body, so a check the browser makes is not one; a check after the dial
  is after the thing being restricted. A malformed target stays a `400` whether
  or not a list is configured, so turning the allowlist on changes which
  destinations are reachable rather than which inputs parse. `403` with a new
  `target_not_allowed` code, distinct from `forbidden` so a client can tell
  "this deployment will not go there" from "the directory said no".
- **Matching is equality of the normalised host and port, and there is no
  substring test anywhere in the package.** Case folded, trailing dot removed,
  IPv4-mapped addresses unmapped, zones refused, brackets handled, `ldap://`
  and `ldaps://` accepted with their default ports, and credentials in a URL
  refused outright rather than parsed and discarded -- by then they would have
  been written into a configuration file. Alder does not resolve before
  matching, so an entry naming a host permits whatever that name resolves to;
  said out loud in `SECURITY.md` rather than implied.
- **`--max-in-flight` is a ceiling, not a rate limiter.** Alder has no users of
  its own to account to, so a per-caller quota would be keyed on nothing. 64 by
  default, because the tree browser fires several requests per expansion and a
  low number would queue an ordinary session against itself.
- **A disconnected client does not stop the work it asked for, and that is now
  written down rather than assumed.** Measured: a streamed search stops, because
  the write fails; an abandoned tally served ninety more pages and stopped only
  when it ran out of entries. fasthttp does not cancel a request's context when
  the peer goes away -- tried, and it does not. What contains it is the
  per-operation timeout and the concurrency cap; a real fix needs connection
  state tracked outside the handler, and the test says so.

- **The plan is not a second diff engine, and the property that matters is that
  it hands back the records.** Alder could already preview one change exactly,
  because the LDIF in the dialog is rendered from the record `Apply` receives.
  What was missing was the question above it. `POST /api/v1/plan` answers it,
  and `items[].record` is the value `changeset/apply` takes -- not a description
  of it. A plan that described what would happen, and an apply that worked it
  out again from the same input, would agree right up until they did not.
- **`reconcile` moved out of `internal/api` into `internal/plan`, and both call
  it.** The import handler has reconciled content records against live entries
  since it was written; the planner needs the same answer. Two implementations
  of "what would have to change to make this entry match this record" is exactly
  how a plan and an apply come to disagree, so there is one.
- **Drift is a keyed fingerprint of what the decision depended on, not a
  snapshot and not a timestamp.** A snapshot would be large, would travel to the
  browser and back, and would carry attribute values the API withholds
  everywhere else. The key is random per process, so a baseline is meaningless
  to another Alder and does not survive a restart -- the same rule as the
  session store. A sensitive attribute contributes whether it is set and how
  many values it has, never the bytes.
- **The fingerprint covers only the attributes the change touches.** A baseline
  over the whole entry would refuse a change because somebody edited an
  unrelated attribute, and an operator refused for irrelevant reasons learns to
  bypass the check.
- **The dependency list travels inside the token, and the first version did
  not.** A reconciled change is planned from an add naming `cn` and `mail` and
  applied as a modify of `mail` alone, so a verifier that re-derived the list
  from the record it was handed fingerprinted a different set and called every
  reconcile stale -- caught by the end-to-end test, not by reasoning. The names
  are in the MAC as well as in front of it, so narrowing the list invalidates
  the token rather than narrowing what is checked.
- **Every baseline in a set is checked before the first change runs.** A
  changeset stops at its first failure, so finding at change twelve that the
  directory had moved would leave eleven applied against assumptions nobody
  rechecked. This is knowable in advance, which is the same reason the whole set
  is validated up front.
- **The test rig now builds its server through `NewServer`.** It used to be a
  struct literal, on the grounds that `NewServer` constructs an LDAP driver and
  a test has no directory. The cost was that every field `NewServer` set and the
  rig did not was invisible to tests: the concurrency gate and the planner were
  both nil in every test written to exercise them, and both suites passed.

### 2026-09-12 — the disconnect fix that does not exist, and the plan over HTTP

- **The mechanism named in the previous entry does not work, and now there is a
  test saying so.** That entry said a real fix for "an abandoned request keeps
  working" needed fasthttp's `ConnState`, a map from connection to cancel
  function, and the keep-alive distinction between idle and closed. All of that
  was built. It cancels nothing.
- **fasthttp serves a connection from one goroutine, so while a handler runs
  nothing is reading the socket.** `ConnState` does fire on close and the
  connection identity is stable -- both checked -- but the notice arrives after
  the handler has returned. Measured: the close was reported 537 ms after the
  hangup, by which time the abandoned tally had served 97 more pages and
  finished on its own. The two candidate signals are now both eliminated by
  measurement rather than by reading: `RequestCtx` implements `context.Context`
  and its `Done` is not closed on disconnect either.
- **What remains is a watchdog reading the socket underneath fasthttp,** which
  would take the bytes of a pipelined request from the server that needs them.
  For a signal whose absence is already covered by a thirty-second timeout and a
  concurrency ceiling, that is the wrong trade, so Alder does not do it and the
  code says why.
- **The finding is pinned as a test rather than only as prose.**
  `TestFasthttpReportsNoDisconnectWhileAHandlerRuns` asserts that the close
  notice arrives too late to be useful. If fasthttp ever changes, that test
  fails, and the failure message says the cancellation has become worth
  building. A limitation nobody re-checks is a limitation forever.
- **The plan is now driven over HTTP against both servers, not only through the
  planner.** The endpoint, the JSON, the record coming back and the apply
  reading a baseline out of it were covered by a fake, and a fake agrees with
  whatever it was written beside. The new cases start the real API server with
  the real driver, connect over LDAPS with the harness CA the way every other
  case does, plan, hand the plan's own record back to `/changeset/apply`, and
  check the directory. The drift case lets the suite's own session play the
  other administrator in between. Verified by dropping the baseline from what is
  handed back: the refusal test fails, which is what says it is testing the
  mechanism rather than something incidental.

### 2026-09-12 — the paths that were never measured

- **Three of them, and only one was fine.** The tree exports had just been
  measured and fixed; these are the rest. The inventory came out as designed:
  a hundred thousand entries tallied for 4.7 MB of peak heap above the fixture,
  ten thousand for 0.5 MB, which is the folding-as-it-arrives claim holding. The
  playbook export and the comparison did not.
- **The comparison parsed every DN six times.** Comparing two groups of a
  hundred thousand members took 1.73 s and 19.6 M allocations to produce a few
  hundred rows. Keying a value means parsing it as a DN, and the code reached
  the single-value case by wrapping the value in a slice and taking the first
  element back out; it then did that twice to build the membership maps, twice
  more in the emit loops, and twice again in the equality check that runs
  before all of it. Each side is now keyed once, positionally, and those keys
  are what both the equality check and the listing read: **0.36 s and 6.4 M**.
- **It also kept working after there was nothing left to emit.** The emit
  function stopped appending at the limit but the loops ran on to the end of a
  hundred thousand values, keying every one of them for a row that could not be
  produced. Both passes now stop when the limit is reached, and the left-hand
  membership set is built only if the first pass left room for a row that needs
  it.
- **`sameAttributeValues`, `sameDNSet` and `normalisedDNs` are gone,** replaced
  by `comparisonKeys` and `sameKeySets` over the shared keys. `sameKeySets`
  clones before sorting: the key slices are positional, and sorting them where
  they lie would have left the listing reporting values on the wrong side.
- **The playbook export was the worst of any path, and the fix was one line of
  the renderer.** `Playbook` indented the tasks by running `strings.Split` over
  the whole document -- a slice header for every line of thirty megabytes --
  and building a second indented copy before sending it. Walking the lines with
  `strings.Cut` holds one line at a time: **62.0 MB of peak heap becomes 49.5 MB
  and 211 ms becomes 141 ms**, better on both counts.
- **The playbook export is not streamed, and that is a measured choice.**
  Handing it to the connection the way the tree exports now are takes peak heap
  further, to 32.5 MB, and costs 300 ms against 141 -- a stable 2x, reproduced
  across three runs of ten each, unlike the search timing that would not hold
  its sign. The tree exports got faster because the change removed work; this
  one only changes delivery, so the chunked framing shows up net. Recorded here
  rather than taken quietly: 17 MB of peak heap is available whenever it is
  worth 160 ms.
- **What still materialises is `EnforceTasks`,** which builds the whole task
  document as one string before any of it can be indented. That is the deeper
  fix and it is not done; the number to attack is the 78 MB the streamed variant
  still allocates.
- **The benchmark cannot prove the inventory holds no entries.** The fake hands
  out slices of a fixture that is live for the whole run, so an endpoint that
  retained every entry would measure the same as one that dropped each page.
  What the figures do show is that nothing *derived* grows with the directory,
  which is the part that was in doubt.
- **A conflicting pull request now says so.** `pull_request` workflows run
  against a merge commit GitHub builds from the two branches; when they conflict
  there is no such commit, so none of them run -- ci included -- and the pull
  request sits with a short checks list, no failure, and nothing saying why.
  Branch protection does block the merge, so this was never a hole, but it cost
  two pull requests a round trip each. `mergeable.yml` runs on
  `pull_request_target`, which is scheduled from the base branch and therefore
  runs regardless, checks out nothing, and fails with the reason. It is not a
  required check: it reports a condition that already blocks the merge.

### 2026-09-13 — the plan becomes the operation

- **The plan is the source of the operations, and apply may not reconstruct
  them.** 1.4's baseline bound the state a change depended on and nothing about
  the change, so a client could plan one modification and apply another against
  the same attributes and be told the plan was current. The baseline is now two
  MACs: one over the exact operation — type, DN, every modification in order,
  every value — and one over the state. The apply recomputes both from what it
  was sent and what it reads. Rejected: storing plans server-side and applying by
  plan id. That is persistence, which v1 does not have, and it would make a
  restart lose every reviewed plan instead of merely invalidating its tokens.
- **Mismatch and staleness are different answers.** A request that is not the
  planned operation is the client's mistake and gets `400 plan_mismatch`; a
  planned operation the directory has moved away from is nobody's mistake and
  gets `409`. A token that verifies neither way is reported stale, because the
  honest remedy for both is the same: plan again and look.
- **Stale stays `error: conflict`, with `cause: plan_stale` added.** The first
  cut changed the code to `plan_stale`; the 1.4 HTTP test pinning `conflict`
  failed, which is what it was for. Error identifiers are API.
- **Never replan and apply on the caller's behalf.** The interface disables Apply
  on a stale plan and offers Recompute plan, and applying is possible again only
  once the new plan has been rendered. Rejected: an automatic retry with a fresh
  plan when nothing in the new plan "looks different" — deciding that is the
  operator's review, done by the program.
- **LDIF is read in a mode the caller names, and a mixed document is refused.**
  `changes` makes every record the operation it states; `desired` makes every
  record a statement of state and accepts no `changetype`. Rejected: inferring
  the mode per record, which is exactly the silent reinterpretation a desired-
  state input must not do — a `changetype: add` that became a modification is
  a different change from the one written. That was 1.4's import behaviour with
  reconcile on, and it is fixed: only records with no `changetype` reconcile.
- **Absence never means deletion,** in any mode, and no flag is offered to make
  it. A document that omits an entry is not a document about that entry.
- **Schema validation judges only what the schema states and the operation
  would produce.** An undefined class or attribute, an attribute no class
  permits, a second value in a single-valued attribute, a missing required
  attribute on an add. Anything that depends on server behaviour is left for the
  server: the RDN attribute counts as present, operational attributes are exempt
  from class permission, and when no object class is visible the class rules are
  skipped entirely. A false `invalid` withholds a change the directory would have
  accepted, which is worse than letting the directory refuse it.
- **Impact is facts, not a risk score.** Target kind from the locations the
  server announces; membership gained and removed by replaying modifications
  over the live values; inbound references and how many the plan would leave
  dangling; deletions grouped into subtrees. A score would be a claim about the
  operator's directory that Alder cannot back.
- **Reference search runs as the session's bind and says so.** Entries the ACLs
  hide are indistinguishable from entries that do not exist, and no LDAP
  operation reports the difference. Above 2,000 deleted or renamed entries the
  search is not run at all and the plan says it was not analysed, rather than
  reporting a partial count as if it were the answer.
- **Sensitive values are bound by shape and withheld everywhere a change is
  rendered.** A token binds a `userPassword` modification by name, operation
  and count — the rule `set_password` always had — so no token is a function of
  a password. Plan records carry `{size}` instead of the value, previews
  `withheld (n bytes)`. Doing this found a real leak: `/changes/apply` and the
  change preview echoed a directly-set `userPassword` back to the browser. A
  size-only value is refused on the way in rather than written as empty.
- **A latent 1.4 bug, found by the design rather than by a test.** Verifying a
  reconciled plan read back only the attributes of the narrower modification,
  while its token covered every attribute the document named, so a reconciled
  plan looked stale against any real server. The fake directory returned whole
  entries and hid it. Verification now reads the token's attributes too, and a
  test uses a fake that returns only what it is asked for; the conformance suite
  applies a reconciled desired-state plan on both servers.
- **The equivalence invariant is a test of executed operations, not end state.**
  It plans a mixed set over HTTP, applies it as a client would, and compares the
  recording session's operations one for one and in order with the plan's
  records, using a comparison written in the test rather than the production
  token. Three deliberate breaks — an inverted `deleteOldRDN` on the apply side,
  reordered modifications on the apply side, an inverted `deleteOldRDN` in the
  plan's rendering — each fail it.
- **Membership impact had to be made cheap before it could ship.** As first
  written it keyed every member as a parsed DN on both sides, three times over:
  planning an add of 100 members to a 100,000-member group went from 25 ms to
  about a second. A byte-equal pre-pass, recognising an append positionally,
  and a scan that keys plain DNs without parsing (held to the parser's answer
  by a test) bring it to roughly 75–110 ms and 33 MB; framing token fields
  without `fmt` took classification alone to 34 ms for the add and 24 ms for a
  replace, against 25 and 38 in 1.4.

### 2026-09-13 — one plan for every write

- **A single change is a plan of one.** The confirmation dialog that every
  single write in the interface goes through -- entry edits, creation, rename
  and move, deletion, passwords, memberships, schema definitions, configuration
  settings, and each record applied from the import panel -- now plans the
  change with `POST /plan` and applies it to `POST /changes/apply` with the
  plan's token. Rejected: a separate single-entry planner, or a dedicated
  "plan one change" endpoint. The planner already takes a list; a second
  representation of "what will happen" is exactly what 1.5 set out to remove.
- **Preview is something a plan contains, not a second model.** The dialog shows
  the LDIF and Ansible rendered inside the plan item. `POST /changes/preview`,
  which renders without reading the directory, is kept and marked deprecated
  rather than removed: it is a documented 1.x endpoint, and deleting it buys
  nothing the interface still needs. It is no longer called by the interface.
- **The dialog applies the change it reviewed, not the plan's record.** A single
  change is always planned as exactly itself, so the two describe one operation
  -- and only the staged copy still holds a password or a withheld sensitive
  value. The token holds the server to that equivalence.
- **A plan under review is never replaced silently.** The dialog's plan query
  does not refetch on focus or reconnect and is discarded when the dialog closes.
  A refused apply leaves the dialog showing the refusal with Apply disabled; the
  operator asks for a new plan, sees it, and only then can apply.
- **Nothing to do offers nothing to do.** An `unchanged` plan disables both Apply
  and staging. A `conflict` or `invalid` plan is shown and not offered for
  applying, but can still be staged, because a later change in a set may be what
  makes it apply.
- **Secrets are bound by a session-scoped keyed MAC.** 1.5 bound a password
  change by its type and DN and a sensitive value by its count, so a different
  password of the same length verified. The operation MAC now includes an HMAC
  of each secret under a key derived from the process's random fingerprint key
  and the session ID. Rejected: an unkeyed digest (SHA-256 of a password in a
  token is an offline guessing oracle); a stable server secret (a new
  configuration value to generate, protect and rotate, for a guarantee the
  per-process key already gives); binding under the process key alone (any
  session on the server could then test guesses by planning them and comparing
  tokens). The cost is that a token carrying a secret only verifies in the
  session that planned it. The state half still binds stored secrets by count.
- **Configuration is where the server keeps it, announced or found.** 389 DS
  announces no `configContext`, so its `cn=config` was being classified as data.
  A change is `config` if it is under the announced context or under the
  configuration tree Alder found answering at connect time -- both locations the
  server itself established, neither a guess from the DN's spelling.
- **The equivalence proof covers every family the dialog builds.** Create,
  multi-attribute modify, rename, move, delete, a schema write, a configuration
  write and a password are each planned and applied over HTTP and compared
  operation for operation with what the plan showed. Eight deliberate breaks
  on the apply path -- one or more per family -- each fail it.

### 2026-09-13 — snapshots, comparisons, and the one write path

- **A snapshot is its own format, not LDIF.** It has to say things LDIF cannot:
  that a value was withheld and how many there were, what was excluded, what
  the schema said about each attribute, whether the capture was whole, and
  where it came from. Written into LDIF, those would be comments nothing reads
  back, and an LDIF content record is also something the import panel would
  happily apply. A JSON document with `format`, `version` and `kind` cannot be
  mistaken for a change. LDIF export is unchanged and remains the format for
  moving entries.
- **Canonical, and checksummed without the time.** Entries parent-first, attributes
  `objectClass`-first then by name, values by their matching-rule key. SHA-256
  over everything but `createdAt`, so an unchanged directory produces the same
  checksum twice. A change to the captured content or the covered metadata
  invalidates it; a change to `createdAt` does not, by design. It is an integrity
  check against corruption, described everywhere as neither authentication nor
  a signature. Rejected: signing, which needs a key to
  manage and would suggest an authenticity nobody can check.
- **Sensitive attributes are a count, never a digest.** A hash of a password in
  a file people share is an offline guessing oracle. The consequence, stated in
  the documentation: a password changed to another password is invisible to a
  comparison, and a restore from a snapshot never writes one.
- **Never partial.** A size limit, a referral or more than 50,000 entries makes
  capture fail with `snapshot_too_large`. `completeness` exists as a field with a
  single value, so a future partial kind would be a new value an old reader
  refuses, not a document it misreads.
- **Unknown fields are refused.** A 1.x reader that skipped a field a later
  writer relied on would compare wrongly without saying so. Snapshot format
  version 1 was introduced in 1.7, and every later 1.x release will continue to
  read it.
- **Direction is explicit, and only a live source proposes changes.** `added` means
  in the target and not in the source, always. Candidates exist only when the
  source is the directory, because that is the only side a change can be made
  to, and they move it toward the target. Rejected: proposing changes for two
  snapshots "to apply somewhere", which is a plan without a directory to plan
  against.
- **A candidate is a list of change requests, and nothing more.** The interface
  stages the selected ones into the changeset, which plans them against the
  directory as it is then. There is no "apply this diff" endpoint. The mapping
  is proven the way 1.6's dialog was: capture, drift, compare, select everything
  including an explicit delete, plan, apply, compare again, and find nothing.
  Four deliberate breaks of the mapping each fail it: removed values not
  deleted, an attribute dropped from an add, an absent attribute not deleted, a
  delete not produced.
- **Deletion is never inferred.** A partial comparison produces no delete at all
  (`incomplete_comparison`). A complete one produces deletes marked
  `destructive`, which "select all" skips and which each need their own
  checkbox. "Could not read" is `unknown` and never `removed`: a truncated live
  read, a scope that differs, or an attribute the live directory says the bind
  DN cannot read. Checking access costs one request per attribute, capped at 500
  per comparison, after which the rest are `access_not_verified`.
- **Renames only by a shared identity.** `entryUUID` or `nsUniqueId`, recorded as
  the entry's `id`. Rejected: matching entries by similar attributes. A wrong
  guess turns into a `modrdn` of the wrong entry.
- **Equality follows the matching rule, conservatively.** A rule Alder implements,
  named the same on both sides, decides equality. Anything else compares bytes
  and is listed as such. Byte comparison can over-report a difference; it
  cannot hide one.
- **Cross-vendor means the announced vendors differ, including one that announces
  none.** OpenLDAP announces no vendor, so a rule requiring both to be named would
  never have fired for the pairing the harness exists to test.
- **Data only.** A base in the schema or configuration tree is refused with
  `snapshot_scope_unsupported`. Their identity and value ordering are different
  problems, and 1.7 does not pretend to solve them.
- **The request limit stays at 16 MB.** Measured: 10,000 people and a group of all
  of them come to 5.0 MB compact (12.4 MB indented). That covers a comparison of
  one ~30,000-entry snapshot with the directory or two ~15,000-entry snapshots.
  Raising the limit globally would raise it for every endpoint. Instead the
  interface measures what it would send and refuses with the reason, and a
  larger capture can still be downloaded. Build, decode and diff each grow
  about 12× from 1,000 to 10,000 entries (0.20 s, 0.40 s and 0.31 s at 10,000),
  so none of them is quadratic in entries or in group size.
- **Stateless, still.** Snapshots are downloaded and uploaded. The server keeps
  no copy, no history and no schedule.

### 2026-09-14 — the command line is a client

- **Subcommands of `alder`, not a second binary.** The root command took no flags
  and ran nothing, so `alder snapshot`, `diff`, `plan`, `apply` and `version` sit
  beside `alder serve` without changing a single existing invocation, flag or
  `ALDER_*` variable, and the container still starts `serve` by default. Rejected:
  `alderctl`, which would double every release archive and SBOM to avoid a
  compatibility problem that does not exist.
- **This reverses a stated position, deliberately.** `cmd/alder` said there was
  "deliberately nothing else" because a second interface to the same operations
  is a liability. The condition under which the client exists is that it is not
  one: it implements none of the operations. The package comment now says that
  instead.
- **A client of the HTTP API, not an in-process engine.** The commands talk to a
  running Alder through a Go client generated from `api/openapi.yaml`, beside the
  server interface and models. Rejected: linking the planner and driver into the
  command, which would mean a second place that holds credentials, opens LDAP
  connections and issues plan tokens -- under a different per-process key, so its
  plans would bind secrets and state differently from the server's. One Alder
  process issues and checks tokens; the web interface and the command line both
  ask it.
- **JSON is the server's bytes.** `diff --json` and `plan --json` are the API's
  response as it came, and a snapshot is sent to `/diff` as the file's own bytes.
  Re-encoding either would drop fields the client's generated types do not know:
  on the way out that loses information, and on the way in it would hide exactly
  the unknown field a version 1 snapshot reader must refuse. The client checks
  only that a file is one JSON object, so a crafted file cannot close the
  request's object and write the other side of the comparison itself.
- **Exit codes are a small stable table.** 0 done or no differences, 1 differences,
  2 incomplete (winning over 1), 3 not applicable as written, 4 the plan no longer
  holds (`plan_stale` or `plan_mismatch`), 5 not confirmed, 6 stopped partway,
  7 usage, 8 anything else. Differences are not a failure, and a partial
  comparison is never reported as "no differences".
- **Apply always plans, and applies that plan.** What is sent is the web
  interface's rule from `web/src/lib/plan.ts`, ported and tested case for case: an
  exact change from the client's own copy (for LDIF, the document as the server
  parses it), a desired-state change from the plan's record, each with the plan's
  baseline. A stale or mismatched plan stops with nothing written and is never
  planned again and applied. A plan with a conflict or an invalid item is not
  applied at all -- stricter than the changeset view, which can apply the
  applicable subset, because a script should not quietly do part of what it was
  asked.
- **Confirmation is explicit.** At a terminal the answer defaults to no. With no
  terminal to ask -- a pipe, CI, input on standard input -- `--yes` is required,
  and the command refuses before connecting. `--yes` answers the question and
  nothing else. A plan with deletions also needs `--allow-deletes` when `--yes`
  answers.
- **No secret is a flag.** Passwords come from `ALDER_BIND_PASSWORD`, a file, or
  standard input; an empty one is refused rather than becoming an anonymous bind.
  There is no hidden interactive prompt: it needs `golang.org/x/term` as a direct
  dependency, and dependencies are not added without asking.
- **The environment fills in connection flags, never safety flags.**
  `internal/envflags` is `alder serve`'s mechanism, moved so both can use it, with
  an annotation that keeps a flag command-line only. `--yes`, `--allow-deletes`,
  `--force`, `--insecure-skip-verify`, `--bind-password-stdin`, and the flags
  choosing what is operated on do not read variables.
- **Streams and files are predictable.** `-` is standard input (or output for
  `snapshot --output`), standard input has one owner, standard output carries only
  the data asked for, and everything for a person goes to standard error. Files
  are written to a temporary name, synced, checked when they are snapshots, and
  then named with a hard link that fails if the name exists -- or replaced by
  rename with `--force`.
- **Directory text is escaped for terminals.** Control characters and
  bidirectional overrides in values, DNs and messages are printed as escapes. No
  colour at all, so no rules about when it is safe.
- **Deletions from a comparison need two explicit acts.** `diff --stage-deletion`
  selects one by DN (never `--stage`, never a wildcard), and `apply --yes` needs
  `--allow-deletes` to send it. Only change requests the server derived are
  written; the client derives nothing.
- **Tested at two layers, and broken on purpose.** A stub Alder holds the client to
  its contract -- streams, codes, files, confirmation, what is sent back. The
  conformance suite runs the real server against both directories for the whole
  snapshot, diff, plan and apply workflow, and asks the client and the API the
  same questions: the snapshot matches apart from `createdAt`, the comparison
  matches exactly, and the plan matches apart from its session-bound baselines.
  Six deliberate breaks of the client each fail a test. The client's tests also
  run on Windows in CI.

### 2026-09-14 — comparing two snapshots needs no directory

- **`POST /diff` asks for a session only when a side is live.** The handler
  decodes the request's shape, and calls the session check only if either side
  is `live` -- before either snapshot is read, so an unauthenticated live
  comparison is refused as unauthorised and never parses a snapshot. Two
  snapshots go through the same decoding and the same `diff.Compare`, with no
  attribute-access probe, because there is no directory to ask. Rejected: a
  second handler or endpoint for snapshots, which would be a second place for
  the comparison to drift from.
- **A sessionless comparison is still bounded untrusted input.** It stays under
  the `/api/v1` in-flight gate, `alder serve`'s 16 MB body limit and the request
  timeout, and its snapshots are decoded as strictly: malformed JSON, unknown
  fields, a checksum mismatch, an unsupported version, and more than 50,000
  entries are all refused. No new route, no new limit, no new exception.
- **The client sends nothing it does not need.** `alder diff a.json b.json` checks
  only `--api-url`, opens no session, and reads no password. Directory flags
  inherited from the environment are ignored rather than refused. A server
  before 1.8 answers such a request with `401`; with directory flags present the
  client makes the comparison again with a session, and without them it says
  what to add.
- **Proven with a directory that cannot be reached.** The API tests run the server
  with a driver that counts connection attempts and connects to nothing, and no
  session in its store. Two snapshots compare with zero attempts and no cookie
  issued, and the answer is byte-identical to the same request made with a
  session. A live side without a session is `401`, also with zero attempts. The
  client's test does the same end to end against the real server and its real
  LDAP driver with no directory anywhere, and a deliberate break -- asking for a
  session before decoding -- fails it.
- **The command line's equivalence with the web interface is stated precisely.**
  It uses the same server-side Snapshot, Diff, Plan and Apply. It adds a
  client-side safety policy: confirmation, `--allow-deletes`, and refusing a plan
  that already holds a conflict, where the changeset view can apply the
  applicable part. The policy decides whether a request is sent; what is sent
  goes to the same endpoint with the same plan tokens.

### 2026-09-14 — recovery bundles (1.9)

- **Recovery is compensation, derived before an apply and handed over after.**
  A bundle holds the compensating changes Alder can derive from each entry as
  it was read immediately before its change, only for the changes that applied,
  to run in reverse order. It is not a transaction, a rollback, a backup or a
  restore, and the documentation never calls it one.
- **There is no second write path.** `POST /recovery/inspect` validates a bundle
  and returns ordinary change requests. Those are planned, reviewed and applied
  through `/plan` and `/changeset/apply` like any other change.
  - Rejected: an endpoint that applies a bundle, and any server-side storage of
    bundles. The server keeps nothing.
- **Drift is a general precondition, not a recovery feature.** `ChangeRequest`
  gained `expect`: attributes that must hold exactly these values, optionally
  nothing else.
  - The planner reports a change whose entry does not hold it as `conflict` with
    `expected_state_differs`.
  - Its attribute names are folded into the baseline, and it is checked again at
    apply, because a token cannot see an attribute added after planning.
  - A change with `expect` and no baseline is refused.

  Rejected: a recovery-specific planner, and relying on the token alone.
- **An expectation is the whole after-state of each touched attribute.** Values
  are compared by the attribute's equality rule. A compensating delete
  expects the entry's user attributes exhaustively, not counting
  `objectClass`, operational, identity or sensitive ones.
  - This is what makes a later change to any value visible as drift, rather
    than a compensation that quietly succeeds around it.
  - The cost is size. A modification of a 5,000-member group carries 5,000
    values: a 270 KB bundle, and an in-process apply of 15 ms instead of 1.6 ms.
  - Rejected for now: expecting only the touched values, which would hide
    drift in the rest; and a digest form, which would be a second wire shape.
- **The pre-state comes from the plan check's read where that is safe.**
  - With recovery asked for, the verification read is widened to what recovery
    needs, and reused.
  - The entry is read again once a change other than a modification has run.
    Adds, deletes and renames are what servers answer with changes elsewhere,
    such as referential integrity.
  - It is also read again when an earlier change in the request touched the
    entry or one above it.

  Measured in-process:
  - 1 modification: 1 read either way, 69 µs to 103 µs;
  - 100 modifications: 100 reads either way, 2.5 ms to 5.1 ms.

  A stale read cannot write the wrong thing: its expectation fails and the
  plan reports drift.
- **Compensations of one entry are merged when a bundle becomes changes.** A
  plan reads every change against the directory as it is. Two compensating
  modifications of one entry would otherwise plan as one that applies and one
  that reports false drift.
  - Modifications of one entry within a run of modifications become one, their
    mods in execution order.
  - A modification followed by the delete of that entry becomes the delete.

  Still in rounds, and documented: a parent and child both added (the parent
  is `has_children` until the child is gone), and an entry deleted and added
  again.
- **The operation matrix.**
  - Attribute modifications are exact.
  - An add is compensated by a guarded delete, never a subtree delete, and is
    exact.
  - A delete is always partial: a new identity, unreadable attributes unknown,
    sensitive values not restored.
  - A rename or move is compensated by a rename back, and is exact.
  - A password change is unavailable.
  - A sensitive or server-owned attribute in a modification makes it partial.
  - Schema and configuration are unavailable in 1.9.
- **The format is its own.** It is `alder-recovery` version 1: not a snapshot,
  not LDIF, not a plan.
  - It is decoded strictly: unknown fields, trailing content, a sensitive
    attribute with values, a password change as a compensation, inconsistent
    recoverability and more than 2,000 steps are all refused.
  - The checksum covers everything but `createdAt` and `checksum`, and detects
    corruption only.
  - Recovery format version 1 was introduced in Alder 1.9, and later 1.x
    releases will continue to read it.
- **The origin is announced, not proven.** A bundle records vendor, vendor
  version and naming contexts, and no host, port or bind DN. When they differ
  from the session's, the interface asks for confirmation before staging, and
  `alder apply --recovery` needs `--allow-origin-mismatch`.
- **The response carries the bundle as the recovery package encoded it.** The
  result field is `json.RawMessage` in Go (`x-go-type`), and TypeScript keeps
  the full schema. Rejected: passing the bundle through the generated types,
  which re-encoded it and cost a third of the time on a large bundle.
- **Command line.**
  - `apply --recovery-out FILE` writes the bundle only after reading it back and
    verifying it, with mode 0600, never over an existing file without
    `--force`, and never to `-`.
  - A partial apply writes it and keeps exit 6. Nothing applied writes nothing.
  - `plan` and `apply` take `--recovery FILE` as a third input and send the file
    unchanged.
  - None of these flags reads the environment.
- **Interface.**
  - Preparing a bundle is opt-in per apply.
  - The change dialog stays open after applying so the bundle can be
    downloaded, and holds `onApplied` back until it closes.
  - The Changeset view loads a bundle into a preview: the applied changes and
    their limitations, the origin, the compensations and their drift.
    "Review plan" stages them into an empty changeset and checks them, and
    Apply stays disabled until a plan is shown.
- **Proven by breaking it.** HTTP tests take a changing in-memory directory
  from S0 through apply, inspect, plan and apply, and back to S0. Seven
  deliberate breaks each fail tests:
  - a forgotten removed value;
  - the wrong RDN;
  - forward order;
  - a compensation for a failed change;
  - an attribute restored that S0 lacked;
  - derivation from the post-apply state;
  - a password in a recreated entry.

  The same round trips run against OpenLDAP and 389 DS.

### 2026-09-14 — 1.9 finalisation: what exact means, and directory text on screen

- **`exact` is about ordinary directory data, and now says so everywhere.**
  Exact recovery means Alder can derive compensating changes that restore the
  ordinary directory state it captured before the change: the user attributes a
  modification touched, an added entry's absence, a renamed entry's former name
  and parent. All of it is subject to the plan's drift checks. It never meant
  byte-identical server state, and RECOVERY.md, PLAN.md, the README and the API
  description now list what is outside it: operational attributes such as
  `modifyTimestamp` and `createTimestamp`, server-generated identifiers such as
  `entryUUID` and `nsUniqueId`, replication metadata, and attributes the bind
  could not read. The model is unchanged: modify, add and rename are exact,
  delete is partial, password and schema or configuration changes are
  unavailable.
- **One display rule for directory strings, in one helper.** The recovery
  proof showed an entry whose RDN held U+202E rendering reordered in the tree,
  though the recovery preview and the command line escaped it. The recovery
  helper moved to `web/src/lib/display.ts` as `safeText`, and every place the
  interface renders an LDAP-controlled string calls it: the tree, the entry
  header and dialogs, values, the search table, members, references,
  membership, plans and changesets, comparisons and snapshots, the overview and
  monitor, the schema browser, imports, error messages and the LDIF and Ansible
  preview.
  - **The set.** C0 controls except tab and line feed; DEL; C1 controls; LRM,
    RLM and ALM; the embeddings, overrides and isolates U+202A–U+202E and
    U+2066–U+2069.
  - **Why tab and line feed stay.** They lay text out and cannot reorder it, so
    multi-line values keep their lines.
  - **The command line.** It gained the three marks, so both escape the same
    set, and it still escapes tab and line feed, which a terminal cannot show
    otherwise.
- **Escaped where rendered, never where kept.** `displayText` still returns the
  value itself, because membership removal and table sorting use it. The
  helper is applied in JSX and tooltips only, so navigation, copy buttons,
  keys and API requests carry the original string.
  - Rejected: escaping in `displayText` or `rdnOf`, which would have sent
    escaped text to the directory.
  - Rejected: a CSS isolation rule, which cannot neutralise an explicit
    override inside the text.

### 2026-09-14 — 1.10: schema snapshots and schema comparison

- **A new kind in format version 1, not a version 2.** COMPATIBILITY.md already
  allowed a later 1.x to add "a new kind", and a schema is a different document
  rather than a new shape of the data one. `kind: schema` has its own strict
  field set; the data reader refuses it by kind and the schema reader refuses a
  data snapshot the same way. A release before 1.10 refuses it as
  `snapshot_invalid`, which is the promised behaviour for a kind it does not
  know.
- **The OID is the identity; names are aliases.** A renamed definition is a
  modification of `names`. A NAME claimed by two OIDs makes both `unknown`
  (`ambiguous_name`). Descriptor-style OIDs, which 389 DS publishes for a few
  definitions, are compared by exact text and flagged `non_numeric_oid`, never
  acted on.
  - Rejected: pairing by NAME when OIDs differ. It is the fuzzy identity the
    product refuses everywhere else.
- **Each definition is stored twice, parsed and as text, and the two must
  agree.** The comparison reads fields; people read text. Decoding re-parses
  every definition and refuses a document whose fields and text disagree, so an
  edited field cannot pass as the published definition.
- **Semantics versus metadata.** Every field that changes what a definition
  does is `core`; `DESC` is its own category and still a modification; `X-`
  extensions alone make `metadata_only`, which is reported and never proposed.
  `X-ORIGIN` and `X-SCHEMA-FILE` are left out of what Alder writes; the server
  records its own.
- **What is actionable is what the server states.** Configuration collections,
  read fresh at comparison time, say which collection holds a definition;
  `X-ORIGIN 'user defined'` says a directly writable subschema's definition was
  added by an administrator. Anything else is `server_defined`. An addition
  needs a target, chosen explicitly where there are several.
  - Rejected: classifying "custom" by OID arc or by name prefix. `origin`
    unknown is better than a confident wrong answer.
  - The collection map is read at comparison time rather than taken from
    connect time, so a definition added in the same session can be changed
    again without reconnecting.
- **Order is computed once, on the server, and returned.** `order` is a
  topological order (dependencies before dependents, referrers before a removed
  definition, removals last), ties broken by element kind then OID. The
  interface and the command line stage in it; a selection missing a dependency
  is refused with `dependency_required`, never completed automatically. A cycle
  gets no order and no change.
- **The plan checks schema sets in order too.** A post-pass over a computed
  plan reads schema-entry changes in sequence against the live schema and turns
  a definition named before it exists, or removed while still named, into a
  conflict. It is the same check whether the set came from a comparison or was
  written by hand.
- **Usage is never assumed to be zero.** A removal's impact is
  `used_by_entries` only on positive evidence from a one-entry search;
  otherwise `usage_unknown`. At most 50 searches run per comparison.
- **A live schema capture reads the schema fresh.** The session cached the
  parsed schema and invalidated it only after its own writes, so a comparison
  with the live schema would have missed a change made elsewhere. `Session`
  gained `RefreshSchema`; data captures keep using the cache.
- **No new command family or endpoint.** `kind` on the capture request and on a
  live diff side; a live side without one takes the other side's kind; mixed
  kinds are refused. `alder snapshot --kind schema`, `alder diff`, stage by
  OID. The data-shaped `counts` stay filled, with `metadata_only` counted as
  `unchanged`, so an older client's exit status still means "core differences".
- **`dITStructureRules` are not captured.** They are identified by integer rule
  IDs rather than OIDs, the parser does not read them, and the document says so
  in `coverage.notCaptured` rather than omitting them silently.
- **Proof.** Conformance on both servers: capture twice with identical content,
  zero differences against live, an attribute type and a dependent class added
  and compared, refused out of order, planned and applied in order, modified,
  removed in reverse order, zero differences after each. Cross-server: the
  harness schema, installed identically on OpenLDAP and 389 DS, has zero core
  differences, and one deliberate `SINGLE-VALUE` change makes exactly one. Seven
  deliberate breaks — identity by NAME, a forgotten `MUST`, `SYNTAX` treated as
  equal, a class ordered before its attribute type, an unselected deletion
  included (command line and interface), `SINGLE-VALUE` normalised away — each
  fail the tests.

### 2026-09-16 — 1.11: change packages, and what may not travel

- **A package is intent; a plan is what one directory would do about it now.**
  The package holds the changes an operator means to make. Every target
  validates it against its own state and makes its own plan. There is no path
  from a package to an apply that skips the plan, in the interface, on the
  command line or in the API, and there is deliberately no `alder package
  apply`: `plan --package` and `apply --package` are inputs to the commands that
  already show a plan.
- **Plan tokens never appear in a package.** A baseline is a MAC under a key
  that exists only in one server process, scoped to a session; it is
  uncheckable anywhere else, so carrying one could only ever be theatre.
  `expect` is left out for the same reason in reverse: it describes the source
  environment's current values, and promoting it would refuse every target that
  legitimately differs. The strict reader refuses both fields along with every
  other it does not know, so neither can be smuggled in.
- **Schema changes travel as intent, not as modifications.** Installing an
  attribute type is a modify of `attributeTypes` on `cn=schema` on 389 DS and of
  `olcAttributeTypes` on a configuration collection on OpenLDAP. A package
  records the element, the operation, the OID and the definition; each target
  builds its own modification through the existing schema write path. The
  conformance proof shows one package becoming a change to
  `cn={4}alder,cn=schema,cn=config` on one server and to `cn=schema` on the
  other.
  - Rejected: packaging the modification and rewriting it on arrival. That is a
    provider transformation, which this milestone does not have and 1.x may
    never want.
- **Promotion is revalidation, never replay.** The same bytes go to each
  environment. Test accepting a package says nothing about production: an entry
  that differs is a conflict there, not an overwrite, and one that already holds
  what the package describes is `already_satisfied` rather than a failure.
  Neither outcome changes the package.
- **An add whose entry already matches is satisfied intent.** The first
  conformance run reported it as a conflict, which is right for a plan and wrong
  for a promoted change somebody applied here last week. It is
  `already_satisfied` when the entry holds everything the item describes, and a
  conflict only when it is there and different.
- **Dependencies are explicit, and derived once.** Array order carries no
  meaning. Alder works the graph out from the changes when a package is built --
  a class after the attribute types it names, an entry after its class and its
  parent, a child's deletion before its parent's -- and writes it into the
  document, where it can be edited. A graph with a missing identifier, a
  self-dependency or a cycle is refused rather than silently repaired.
- **Secrets are forbidden, and omissions are recorded.** A password change
  cannot be packaged: it is refused when the package is built and listed in
  `omitted` with a machine-readable reason, so nothing is dropped silently. A
  change naming a sensitive attribute is refused outright, including one written
  into a document by hand. No escrow, no placeholder, no claim that packages can
  promote provisioning that involves a password.
- **Recovery belongs to the apply, not to the intent.** A bundle is derived from
  the entries as they were in that directory immediately before the change, so
  the same package applied to two directories produces two different bundles --
  proved against both servers. Packages never contain bundles.
- **Validation is separate from the plan, and is not kept.** It answers "can
  this be interpreted here", prepares ordinary change requests for what is
  ready, and stops. It is never persisted as a reusable plan, and the statuses
  are machine-readable: ready, already_satisfied, no_op, conflict,
  dependency_missing, unsupported, target_incompatible, unknown.
- **Reading a package needs no directory.** `POST /packages/inspect` and
  `alder package inspect` check format, graph, definitions, secrets and checksum
  with no session, which is what a pipeline can run before it has credentials.
  Target compatibility still needs a target.
- **Identity is provenance, not authority.** A package carries a generated UUID,
  independent of the filename, and it is part of what the checksum covers. It
  grants nothing, and Alder keeps no record of it: no catalogue, no deployment
  history, no environment registry. The artifact is the operator's.
- **No DN translation in 1.11.** Promoting between `dc=dev,dc=example,dc=com`
  and `dc=example,dc=com` is a real need and a real hazard: an implicit rewrite
  that caught some references and not others would be a silent corruption, and
  an explicit one needs a preview and a model for every reference -- members,
  superiors, DN-syntax attribute values. It is left out deliberately, and a
  package is portable only where the DNs are.
- **Portability is reported, never transformed.** A change that cannot travel is
  omitted with a reason; a target that cannot perform one says `unsupported`.
  There is no OpenLDAP-to-389 DS rewriting, and no provider abstraction: that
  milestone can start from a clean statement of what is portable rather than
  from a half-built engine.
- **A plan follows the schema through the set.** Packages made schema-first sets
  ordinary: install a definition, then add the entry that uses it. Judging every
  change against the schema as it is now refused the entry the directory would
  have accepted, which is the one failure mode schema validation must not have.
  The planner now carries a view of the schema as the set would leave it and
  judges each change against that view. Only a change that would actually run
  updates the view, so the dependency check moved into the same walk: a schema
  change refused for its dependencies adds and removes nothing, and the changes
  after it still see the schema they were written against.

## 2026-09-17 — 1.12: migration preflight, and what it will not do

- **Preflight is read-only analysis.** It reads the target, never writes, never
  changes the artifact, and produces nothing a plan or an apply reads. The
  session reaches it as a type without Apply, the HTTP tests drive every mode
  against a directory that records writes, and the conformance suite compares
  both servers' data and schema before and after.
- **One artifact, one live target.** The source is client-held -- a change
  package, a schema snapshot or a data snapshot -- and the target is the session's
  directory. Alder does not connect to two directories at once.
- **Package validation is reused, not duplicated.** A package preflight runs
  validation first and carries each item's status on its findings; what
  preflight adds is what validation does not ask.
- **Compatibility is semantic, not vendor-name based.** Definitions are compared
  by OID and meaning through the schema comparison; syntaxes and matching rules
  by what the target publishes; `crossVendor` is metadata and decides no finding.
  Which snapshot definitions a server supplied itself is read from what the
  snapshot recorded -- collections, or `X-ORIGIN 'user defined'` -- never from the
  vendor.
- **Unknown visibility never becomes absence.** An entry the server admits
  exists and the bind cannot read is unknown. Where a server conceals an entry
  exactly as a missing one, nothing can tell them apart, and the finding says it
  is the server's account to this bind. Unknown keeps a report from being
  compatible. The presence probe compares `cn`, not `objectClass`: OpenLDAP
  refuses a compare of an OID-syntax attribute with a non-OID value before it
  looks at the entry, which made every absent entry unknown -- found by the
  conformance suite, not by review.
- **Naming-context mismatch is reported, never rewritten.** No DN, attribute or
  object class is mapped. Mappings, if they ever exist, will be explicit and
  reviewed transformations of the source.
- **Operational and server-generated attributes are not ordinary portable
  state.** Usage and `NO-USER-MODIFICATION` decide, from the target's schema and
  what the source recorded; they are excluded from a snapshot's migration, and a
  package that writes one is unsupported. Identities are the target's own.
- **Capability checks are contextual.** A capability is listed only when the
  artifact needs it: schema write for definitions to add, paged results for a
  data snapshot's comparison, password modify when a source withheld passwords.
- **The overall result is derived.** Incompatible beats unknown, unknown beats
  prerequisites, and compatible means none of them. No scores, no percentages;
  an empty artifact is incomplete.
- **Config and ACL compatibility are not evaluated in 1.12, and every report
  says so.** So are secret values.
- **Preflight never produces execution state.** No baseline, no plan token, no
  prepared change is in a report.
- **Excluded is a classification.** A withheld secret, a server-generated value
  and a server's own definition are neither portable nor incompatible; calling
  them either would make the report say something untrue.
- **A preflight's schema comparison skips dependency ordering.** It reads items
  only, and the ordering was the one quadratic step (483 ms to 62 ms for 1000
  definitions). `POST /diff` still orders.
- **Provider abstraction remains deferred.**

## 2026-09-17 — 1.13: configuration snapshots, and the model that is not shared

- **Configuration is provider-specific unless equivalence is explicitly
  proven.** There is one model per provider — `internal/config/openldap.go` and
  `internal/config/ds389.go` — and a configuration is only ever compared with
  another capture of the same one. No portable configuration model exists here,
  and adding one is not deferred work: it is work that would have to prove each
  equivalence, one setting at a time, and nothing in 1.13 proves any.
- **Two providers produce a mismatch, not a diff.** Lining up the harness
  servers would have produced roughly 1,500 differences, every one an artefact
  of the comparison rather than a fact about either server. A cross-provider
  comparison reports `providerMismatch`, no items, `complete: false`, and a
  summary of what each side holds. This is the one place in Alder where the
  answer to "compare these" is "these are not comparable".
- **The provider is decided by the tree, and the vendor name only breaks a
  tie.** Only OpenLDAP writes configuration in `olc*` attributes and only 389 DS
  in `nsslapd-*`, which is a fact about the server rather than about its name.
  The vendor name stays what section 3 of CLAUDE.md says it is: display, plus a
  last resort when the tree says nothing.
- **Identity is what the provider names a thing; never a position.** A database
  is its suffix, an overlay its name under its database, a plugin its name, a
  backend's index its path below the backend. The `{n}` in an OpenLDAP DN is
  where something sat and changes when something else is removed. A setting is
  `section / resource / key`, lower-cased, inside a document that names its
  provider. The conformance suite asserts no resource name contains `{` and no
  two share an identity.
- **A secret is present and counted, never a value and never a digest.** No
  hash, no salt, no truncation, no fingerprint: a configuration document must
  not be an offline guessing oracle, and a "safe" digest would make one. The
  sensitivity rule is deliberately narrow — a credential, a secret, a key, or a
  name ending in the word password — because a configuration is full of
  settings that describe passwords without holding one, and withholding those
  would hide ordinary configuration while protecting nothing.
- **Operational metadata stays, marked and documented.** Paths, ports, host
  names and file names are not secrets and do describe the machine. They are
  captured with `operational: true`, counted, and SECURITY.md says plainly that
  a configuration snapshot may carry operational infrastructure metadata even
  when secrets are withheld.
- **Runtime state is not configuration.** `cn=monitor` at any depth, `cn=tasks`,
  and the attributes a server maintains for itself are excluded, and the
  document says so in `coverage.excluded`. Schema definitions are excluded too —
  they have a kind of their own, and two answers to one question is worse than
  one. Which schema entries are *loaded* is configuration, so OpenLDAP records
  their names and nothing else.
- **An unrecognised entry becomes an area, not a hole.** A model that does not
  know what something is still captures it, in the `other` section, so a
  difference in it is still seen.
- **Diff does not create a new config write capability.** A difference is
  actionable only where the provider's model already lists the setting as one
  Alder writes through the ordinary plan, both sides agree, it is not a secret,
  and it exists on both sides. A forged document cannot make a setting writable,
  because the live side's own model has to agree — proved against both servers.
  Adding or removing a configuration entry is reported and never derived.
- **The change goes to the live side's own entry.** Two servers may keep the
  same setting in differently named entries, so the DN comes from the live
  side's snapshot, never from the other side's.
- **Absence in a partial capture is `unknown`, never `removed`.** A side that
  was not read in full cannot be used to say a setting is not there, and such a
  comparison is never complete. Hidden configuration is the same answer.
- **Recovery stays unavailable for configuration**, as for schema. Compensation
  is derived from ordinary directory state, and a configuration entry is not
  that. A configuration change is put back the way it was made: by planning the
  opposite change.
- **Order is kept only where the provider says it means something.** Access
  rules, `syncrepl` statements and module loads keep the server's order and are
  compared in it; the `{n}` prefix is stripped, because a configuration that
  differs only in renumbering is the same configuration. Everything else is a
  set.
- **A value nothing parses is compared as text, and says so.** `olcAccess`,
  `aci`, index definitions: `comparison: raw`, `not_comparable` on the
  difference, never actionable. This is also as close to ACL analysis as 1.13
  goes, deliberately.
- **A value that is not text is captured as base64**, rather than dropped, so a
  snapshot is never quietly smaller than the configuration it claims to hold.
  389 DS's replication state is the case that found this.
- **A configuration capture takes no base, scope or filter.** There is nothing
  to narrow; a request that carries them was written for another kind and is
  refused rather than ignored. Kind `schema` goes on ignoring them, because
  1.10 clients already send them.
- **Preflight is unchanged.** Configuration compatibility is still not
  evaluated, cross-provider or otherwise, and every report still says so.

## 2026-09-22 — 1.14: the settings Alder changes, and configuration in preflight

- **A setting is writable only once it has been changed.** The conformance
  suite writes, reads back and restores every setting a fresh capture calls
  writable, through a comparison, a plan and an apply, on both servers; the
  test takes its list from the capture, so a model cannot list a setting the
  suite has not proved. 28 settings on OpenLDAP and 36 on 389 DS in 1.14.
- **The proof found four defects that 1.13 shipped.** Booleans were
  lower-cased and written back that way, which OpenLDAP refuses; the plan called
  changes to 389 DS configuration attributes invalid because its published
  schema does not define them; read-only mode was offered where it would lock
  the configuration against the write that undoes it; and settings needing a
  restart were offered. The 1.13 conformance case changed one integer, which is
  why none of this surfaced. A proof per setting is what finds per-setting
  defects.
- **A boolean keeps the server's spelling.** Normalisation is for comparing,
  not for writing: booleans compare without case, and a change writes the value
  in the live server's own spelling, so a 1.13 document still restores.
- **An attribute the entry already holds is the server's vocabulary.** The
  plan's schema check leaves a modification of one to the directory when the
  published schema does not define it. A misspelt attribute, and any undefined
  attribute on a new entry, are still refused. This follows the rule the check
  was written under: a false positive withholds a change the directory would
  accept, which is worse than a miss.
- **What needs a restart is the server's statement.** 389 DS lists it in
  `nsslapd-requiresrestart`; Alder reads that at capture, matches by attribute
  alone (the cautious reading), and offers nothing it names. OpenLDAP publishes
  no such list; its model lists only settings `cn=config` applies at once.
- **Read-only mode is offered only on an ordinary database or backend.** On a
  global entry, the frontend or the configuration database it would also stop
  the write that switches it back.
- **A value compared as text is never written back**, even on a writable
  setting. `olcDbIndex` came off the list for this reason.
- **Left out, with reasons:** lockout risks (TLS, SASL, root DN, access,
  `olcLocalSSF`, socket buffers), settings read only at start, index-shaping
  settings, the password policy, security and schema checking, and
  `nsslapd-cachememsize`, which 389 DS refuses while cache autosizing is on.
- **A configuration snapshot is a preflight artifact, within one provider.**
  The comparison is `POST /diff`'s, with the target as the live source. Paths,
  hosts and ports are excluded as environment-specific; a missing database,
  overlay or plugin is its own finding and the cause of its settings' findings;
  a setting only the target has is not a finding, as for data snapshots.
  Against other software the report judges nothing and keeps server
  configuration in the not-evaluated list, worded as before.
- **Decided today, built later: creating or removing configuration entries,
  on a narrow list.** OpenLDAP overlays whose module is already loaded, and
  switching 389 DS plugins on or off (a modification of an existing entry, not a
  new one). Moved into scope deliberately, on the record, as schema editing was;
  CLAUDE.md section 2 says so. Removal is never pre-selected, the plan shows what
  depends on it, and recovery stays unavailable.
- **Decided today, built later: signed snapshots and packages, keyed by the
  person.** `alder sign --key` signs on the operator's machine with Ed25519 from
  the standard library; the server only verifies, against public keys named at
  `alder serve --trusted-keys`. The private key never reaches Alder, which stays
  stateless and holds no secret on disk. Staged: signing in 1.15, configuration
  entries after it.

## 2026-09-23 — 1.15: signing, and where a key lives

- **The key belongs to the person.** `alder sign` runs on the operator's
  machine; the server verifies against public keys named at startup and holds no
  private key. Chosen over server-side signing deliberately: a server that signs
  holds a secret on disk, which is the one thing section 7 of the charter says
  Alder does not do. The Config type has somewhere to put public keys and
  nowhere to put a private one, and a test says so.
- **A signature wraps a document; it never enters one.** `alder-signed` version
  1 carries the payload unchanged. Putting a `signatures` field inside the
  snapshot and package formats would have made every signed document unreadable
  to an older Alder and changed what those formats mean; the envelope leaves
  both alone, and an older release refuses it as a document it does not
  recognise rather than half-reading one.
- **What is signed is the payload's compact form**, under the context line
  `alder-signed/1`. Found by a test: writing an envelope indents its payload, so
  signing the exact bytes made a signature that could not survive being written
  to a file. Compacting is whitespace-only -- the token sequence, the key order
  and every escape are signed as they stand -- and the payload's own checksum
  still covers its content. The context line stops a signature being replayed
  onto another kind of document.
- **Ed25519 only, from the standard library.** No dependency, no parameter to
  get wrong. A second algorithm would be a second thing to get right, and a
  signature naming one is invalid rather than ignored.
- **Trust is a list, not a chain.** No certificates, no expiry, no revocation:
  `--trusted-keys` names files or a directory, and removing a key is how trust
  ends. A key id is the SHA-256 of the public key, which says which key and not
  whose; the signer label is a label and decides nothing.
- **Invalid is refused, untrusted is reported.** A signature that does not match
  means the document changed after signing, so nothing downstream sees the
  payload -- that is not policy. A valid signature by a key nobody named leaves
  the document intact and its provenance unestablished, which is the operator's
  question, so it is read and labelled. `--require-signature` is how a
  deployment turns that into a refusal, and it will not start without keys.
- **Unsigned stays ordinary.** Every document written before 1.15 is read
  exactly as before, nothing is signed by default, and the interface shows no
  badge for an unsigned document.
- **A signature authorises nothing.** A verified document goes through the same
  plan, review and apply, and no directory permission follows from it.

## 2026-09-24 — 1.16: one kind of configuration object, and the plugins a server can spare

- **A comparison lists objects as well as settings.** "The target has no
  memberof overlay" is one fact; reporting it as fourteen missing settings, or
  not at all, was the gap 1.13 left. Listing is all it does for almost
  everything.
- **Exactly one kind is created or removed: an OpenLDAP overlay whose module is
  loaded.** Scoped that way before any code, and confirmed as a change to
  section 2 of the charter. A database is where data lives, a module is a
  library the server must find, a 389 DS plugin is a fixed set the server
  ships: each would need its own proof, and none is in this release.
- **The precondition is checked before the write.** OpenLDAP answers an overlay
  whose module is missing with "handler exited with 1" -- after Alder has sent
  something. Reading the loaded module list from the capture means the operator
  is told what is actually wrong, first. Found by trying it against the harness
  rather than by reading documentation.
- **The new entry carries no position.** `olcOverlay=memberof,<database>` is
  what is sent; slapd assigns `{n}` and stores it under the name it chose, which
  Alder has reported as the resting DN since 1.4. Naming a position would be
  inventing an ordering the server owns.
- **A removal is derived only when it is named as one.** `--stage-deletion` for
  the command line, a separate tick in the interface, nothing in bulk selection.
  An overlay takes its settings with it, and recovery is unavailable for
  configuration.
- **389 DS plugin switching is decided by the tree, not by a list.** The server
  accepted disabling its own `ldbm database` plugin with no error; the damage
  would have appeared at the next start. So a plugin whose type the server needs
  -- syntax, matching rule, database, password scheme -- and any plugin another
  plugin names in `nsslapd-plugin-depends-on-named` are never offered. 26
  plugins remain switchable on the harness, and the round-trip proof covers
  every one of them.
- **The harness loads memberof and leaves the overlay unconfigured.** A proof of
  creation needs something creatable; loading a module is not configuring an
  overlay, which is exactly the distinction the feature rests on.

## 2026-09-26 — 1.17 and 1.18: an audit of the running product, and the editor reading the model

The 1.17 changes were not planned; they came from auditing the running product
against a real task -- a configuration setting drifted, find it and put it back
-- and counting. The audit is `docs/ui-audits/2026-09-24-config-drift/`.

- **The task took 11 interactions across 4 screens against a stated ideal of 5.**
  What the count found was not a confusing product but a long one: the screen
  that knew what had changed could only stage it, and the plain entry editor
  two clicks away applied the same change from one dialog with the same
  guardrails. A comparison now offers "Review N changes": one opens that same
  dialog, several go to the changeset in one click. The rule underneath is
  untouched -- a plan is made, seen, and only then applied.
- **A comparison outlives the screen that made it.** Following the comparison's
  own "Review the changeset" used to empty both snapshot slots, and the
  comparison is the only record of what else differed. The documents and the
  last comparison now live as long as the tab, like the changeset, in memory
  and never in browser storage, and are dropped on Disconnect.
- **The summary counts what Alder can act on, objects included.** A comparison
  whose one actionable difference was an overlay read "0 Alder can change"
  directly above a row offering to create it.
- **Auditing the product is worth doing from outside it.** Every finding above
  is invisible from the code: each screen is correct on its own, and the cost
  only appears when one task crosses four of them. The report's ten findings
  are all fixed bar one, and that one is 1.18.

1.18 is the other half of the audit's fifth finding: the entry editor treated
`cn=config` like directory data.

- **The editor marks fields from the same model a comparison reads.**
  `GET /config/entry?dn=` answers per attribute -- writable, read-only or
  unknown, and whether the server said it takes effect only at the next start.
  A second implementation written for the editor would have drifted from the
  one a comparison uses, and the two would have disagreed on screen; the
  conformance suite asserts they agree, setting by setting, on both servers.
- **It reads the tree, because the answer is in the tree.** Which settings need
  a restart is what the server says about itself, which plugins it can run
  without is read from its plugin entries, and an overlay is named after the
  database above it. Answering from the one entry would mean answering from a
  list of vendor facts, which is what this package exists not to do.
- **The marks mark and never block.** The model is deliberately conservative:
  it states what Alder changes, not what the directory accepts. A disabled
  field would make the editor less capable than the server it is editing, so an
  unmarked or read-only attribute is still editable and the change goes through
  the same plan, preview and apply as any other.
- **It says nothing about this session's rights.** What a bind may write is the
  directory's answer, and Alder does not guess it.
- **The configuration root offers no Rename, Delete or "Delete with contents".**
  Taking away `cn=config` takes the server's configuration with it, and no
  confirmation dialog makes that a thing to offer beside "Copy".

- **1.16 and 1.17 are milestone numbers, not tags.** Nothing had been released
  since 1.15.0, so release-please had configuration objects, the UI audit and
  the entry model queued as one 1.16.0. The documentation, the decisions above,
  the OpenAPI descriptions and the compatibility notes each name the milestone
  they were written for, and every one of those statements is true of the code;
  shipping the lot as 1.16.0 would have made all three untrue at once. The
  release is 1.18.0, and 1.16 and 1.17 are numbers a reader will find in the
  notes and not in the tags, which costs them nothing.

## 2026-09-26 — 1.19: reading access control, and why it stops there

Section 2 of CLAUDE.md puts ACL editing out of scope for v1, and it stays out.
What moved, on the record, is reading: the rules a server holds are now shown
against an entry.

- **The question is "why can't I write this?", and Alder had no answer at all.**
  The preflight report listed access control among the things it neither reads
  nor translates, which was true and unhelpful. Reading the rules is most of
  what an operator needs: which ones name this entry, where each is written, and
  in what order the server consults them.
- **Reading is not evaluating, and the difference is stated on every surface.**
  Evaluation depends on group membership, filters, connection security, rule
  order across databases, and rules Alder may not be able to read. A verdict
  would be believed; a wrong verdict about access control is worse than silence.
  One sentence says so in the API response, in the interface and in the
  document, from one constant, so the three cannot drift apart.
- **Writing stays out, and not for lack of appetite.** Access control is the one
  thing in a directory that can lock every administrator out of it, including
  the one making the change. The ordering semantics that make a rule effective
  -- first match wins in OpenLDAP, the deny-wins precedence of an aci -- mean a
  correct-looking single-rule edit can change the meaning of every rule after
  it, and Alder's whole claim is that what it applies is what the operator read.
- **No shared model, again.** An ordered list in a configuration tree and an
  attribute on an entry are different mechanisms with different evaluation
  rules. Each is reported in its own words: an aci allows or denies, an
  olcAccess by-clause grants a level and the first match wins. Nothing is
  translated into the other, for the same reason configuration is not.
- **Both places are looked in, on every server.** Nothing branches on a vendor
  name: aci attributes up the tree and olcAccess in the configuration tree are
  both read, and what answers is what is reported.
- **"maybe" is an answer.** A regular expression target, a filter, a group
  membership: the report says the rule may bear on the entry and says what could
  not be decided, rather than guessing either way.
- **A half-read rule is reported whole.** `parsed: false` means the raw value is
  all Alder will say about it. A set specification read halfway invites a
  decision made on the half that was understood.
- **An unreadable place is said out loud.** OpenLDAP keeps its rules in the
  configuration tree, which usually needs its own identity; a session without
  one gets "this tree could not be read", never a confident report of no rules.
- **Effective rights are the next piece, deliberately separate.** 389 Directory
  Server publishes the Get Effective Rights control, which answers "what may
  this bind do here" from the server itself; OpenLDAP has no equivalent. Asking
  a server that can answer is worth doing, and it is a different feature from
  reading the rules: one is the directory's verdict, the other is the text.

## 2026-09-26 — 1.20: asking the server what an identity may do

1.19 read the rules. This asks the directory.

- **Where a server answers the question, Alder asks it rather than reasoning.**
  389 Directory Server publishes the Get Effective Rights control; the answer
  is computed by the same code that will refuse the operation, which no amount
  of rule reading can be. It sits above the rules in the report and is marked
  as the server's.
- **The control value is the authorization identity as text, not a BER
  structure around it.** Both readings of the specification were tried against
  the harness: the plain form answers, the wrapped form makes 389 DS reply with
  a numeric code in place of the rights. The kind of thing only a real server
  tells you, and the reason the harness exists.
- **Never critical.** A server that publishes the control and declines this
  request should still return the entry; the rights are an addition to the
  answer, not the answer.
- **The letters are kept beside the gloss.** `v`, `rsc`, `none` are what the
  server said. A letter this release has never seen is shown as it came rather
  than dropped: a right nobody here recognises still exists on the server.
- **Three different silences, three different notes.** A server that cannot
  answer, a server that declined, and a question that failed are distinct
  facts, and none of them is "no rights". Turning any of them into a verdict is
  the failure this whole feature is written to avoid.
- **An optional interface, not a new method on Session.** A driver that cannot
  ask should not have to pretend it can, so `internal/access` type-asserts for
  it and the report says which it got. Section 6's `Driver` and `Session` are
  untouched.
- **The default subject is the session's own bind.** "Why can't I write this?"
  is asked about oneself; `as=` is the administrator's version, and whether it
  is allowed is the server's decision, reported as the server's.
- **Proved by contradiction on the harness.** The acis take `alderTeam` and
  `userPassword` away from svc-alder, and the server's own answer says `none`
  for exactly those two among 61 attributes. The rules said it; the directory
  confirms it. On OpenLDAP the same test asserts the honest absence of a
  verdict.

## 2026-09-26 — 1.21: the password policy in force, and a DN that could not be read

The sibling of 1.19's access rules: the other question a directory answers
badly is "why can't this user log in?", and the facts that answer it are
spread across three places nobody looks by accident.

- **Report the policy and the state; decide nothing.** Which policy applies,
  where it is written, and what the server records on the account — locked,
  must change, last changed, failures. Alder does not work out whether a bind
  would succeed, and no server offers to answer that one the way 389 DS
  answers effective rights.
- **Where the policy is written is half the answer.** A policy an operator
  cannot find is a policy they cannot change, so every report names the entry
  it came from, and says whether the account chose it or inherited the
  server's default.
- **A setting Alder does not recognise is still reported.** The label is
  absent, the value is not: a policy attribute nobody here has heard of is
  still in force. A duration is glossed beside the value -- 7776000 *(90
  days)* -- and never instead of it.
- **The lock is shown before anything is opened.** An account the server holds
  locked is badged in the entry header, read from attributes already in hand,
  because that is the fact worth not having to hunt for at 2am.
- **The harness grew a policy on each side.** OpenLDAP gets two policy entries
  and a `pwdPolicySubentry` on one account; 389 DS gets local policies switched
  on and a subentry for the same account. Two mechanisms, one intent, which is what
  makes the conformance suite's assertions worth anything. Password policy is
  now the third confined vendor-specific step in the seed, beside the schema
  install and the aci file.
- **The conformance suite locks an account and puts it back.** A seeded
  account records nothing, so the state half would have been tested against an
  empty answer; the test makes the one thing happen that an operator most
  often has to diagnose.

And the find that mattered more than the feature:

- **A DN value containing `=` is now rendered escaped.** RFC 4514 leaves that
  optional and Alder left it bare, which is legal and unreachable: 389
  Directory Server stores such a DN with `\3D` and answers "no such object" to
  the bare form. Any entry whose RDN value contains `=` -- a 389 DS per-entry
  password policy subentry, among others -- could not be read by Alder at all.
  Escaping costs nothing, the standard permits escaping any character, and
  both servers accept the escaped form. It changes the checksum of a snapshot
  holding such a DN, which is a price worth paying for entries that were
  invisible. Found by the harness, not by reading the standard: the standard
  says Alder was right.

## 2026-09-27 — 1.22: a refusal says where to look

The last three releases taught Alder to answer "why can't I write this?" and
"why can't this user log in?". This connects them to the moment those
questions are actually asked, which is when the directory says no.

- **The hint says what; the remedy says where.** A result code and a sentence
  were all a refusal carried. Now it also names the screen that answers it --
  the access rules on that entry, the password policy in force, the schema
  definition, the parent that is missing -- and the interface offers the way
  in.
- **A pointer, not a diagnosis.** Alder is not claiming the access rules are
  the reason a write failed; it is saying they are where somebody would look
  next. The distinction is the same one the access report itself makes, and
  the wording keeps it.
- **Silence where there is nothing useful to say.** A constraint violation on
  an attribute that is not a password could be any rule the server keeps, so
  no remedy is offered: pointing at the password policy would be a guess
  dressed as help. The tests pin the silence as firmly as the pointers.
- **The refusal that needs no screen.** No rights in the configuration tree
  is answered by connecting with a configuration identity, not by reading
  anything, so that remedy is a sentence with no button behind it.
- **It reaches an operator through both shapes of failure.** A single change
  fails as an error body; a changeset run reports each change's outcome
  inside a success. The remedy rides on both, because a changeset is where an
  unexplained refusal costs most: the run has stopped and part of it applied.
- **The parent comes from the DN type.** Cutting a DN at the first comma names
  a parent that does not exist whenever an RDN contains one, and sends
  somebody hunting for it.
- **One module-level navigator, deliberately.** The error note is rendered in
  a dozen places, several of them three levels inside a dialog, and threading
  a navigation callback through all of them for a button that appears on a
  fraction of a percent of renders was the wrong trade. It is not a general
  escape hatch: anything that navigates as part of its ordinary job still
  takes a prop.

### 2026-09-27 — a correction to the entry above

The 1.21 notes said a `ppolicy_default` crashes this slapd build. That was
written after the crash appeared with one configured; it happened again after
the default was removed, so the default is not the cause. What is known: this
build sometimes aborts in the mdb backend when a bind arrives immediately
after a password modify on the same entry, it has done so twice on one
developer machine and never in CI, and a clean harness runs the whole suite
green. The harness still configures no default -- it proves nothing the
per-entry pointer does not -- and `slapd.conf` now says why in those terms
rather than the wrong ones.

A crash also leaves the suite's test bases behind, which makes the *next* run
fail with "entry already exists" on five unrelated tests. That is worth
knowing before chasing them: `task compose:down && task compose:up` is the
fix, and the failures are debris rather than a regression.

### 2026-09-27 — 1.23, indexes

The first object Alder creates on **both** servers, and the first where the
two write nothing alike.

- **The same kind, two writes.** OpenLDAP holds an index as a value of
  `olcDbIndex` on the database entry; 389 DS holds it as an entry beneath the
  backend. Neither is translated into the other. A comparison reports both as
  an index on an attribute of a backend, because "this server indexes `mail`
  and that one does not" is the question an operator has, and derives each
  server's own change from that. Reporting them as what each server calls them
  would have made the comparison useless across the two, which is the whole
  point of the harness.
- **Named by backend and attribute, never by position.** `index:dc=alder,dc=test/mail`
  on both. The alternative on OpenLDAP -- "the third value of `olcDbIndex`" --
  is not something anyone can act on, and it changes when a different index is
  removed.
- **The types travel with the object.** An index for equality and an index for
  substrings are not the same index, so a comparison that omitted them would
  offer to create the wrong thing. They are on the row rather than a click
  away. One with nothing recorded is created for equality, which is what both
  servers' own tooling does.
- **The setting keeps the value as written, not the types alone.** On OpenLDAP
  the value is the unit -- `uid,cn eq,sub` is one value and two indexes -- and
  what can be done to `cn`'s index depends on `uid` sharing it. Splitting the
  types out at capture time would have thrown away the fact the refusal needs.
- **Two removals are refused rather than attempted.** `index_value_shared`
  where an OpenLDAP value names other attributes, because the write would be a
  rewrite of somebody else's index rather than a deletion; `system_index`
  where 389 DS marks it as its own. The first cannot arise from a
  `slapd.conf` -- `slaptest` splits those lines one attribute per value -- so
  the harness writes such a value offline, on purpose, and the refusal is
  proved against a real server rather than only in a unit test.
- **The removal deletes the value the server holds, character for character,**
  rather than one rebuilt from its parts. slapd matches on the value, and a
  rebuilt one differing by a space deletes nothing while reporting success.
- **`olcDbIndex` is read-only, and now says so.** It used to be *unknown*,
  which means "Alder has no answer". Alder does have one: the index is created
  and removed as an object, and the attribute is never replaced, because a
  replace rewrites every index on that database at once and leaves the ones it
  did not mean to touch stale on disk. The entry editor says "not changed by
  Alder" instead of "not in Alder's model", and those are different sentences.
- **Reindexing is not Alder's.** Neither server rebuilds an index on its own
  when one is added; that is `reindex` on 389 DS and `slapindex` on OpenLDAP,
  and both are operations on a server rather than changes to a directory.

### 2026-09-27 — 1.24, the harness grows two replicas

Replication visibility was asked for. Before any of it could be written, the
harness had to be able to answer the questions: two lone servers can only ever
say "no replication here", and a feature proved against that is a feature
proved against nothing.

- **A replica of each server, not a cross-vendor pair.** OpenLDAP does not
  replicate with 389 DS and never will; the harness runs `openldap-replica`
  consuming from `openldap` and `ds389-replica` consuming from `ds389`. Four
  containers. The cost is real and it is the same cost the harness has always
  been worth paying: this is the expensive part that makes "works on both"
  mean something.
- **The two servers configure replication from opposite ends.** OpenLDAP puts
  a `syncrepl` directive in the *consumer's* configuration, so the consumer
  configures itself at build time and needs nothing at run time. 389 DS
  enables replication on each instance and creates the agreement on the
  *supplier*, over LDAP, after both are up -- hence `scripts/replicate.sh`,
  and hence its place after the seed.
- **The consumer binds as the rootdn.** A delegated replication account is the
  shape of a real deployment and was tried: it has to be seeded over LDAP
  after the server is up, so the consumer spends the first seconds of every
  harness start failing to bind, and a suite that asserts convergence then
  depends on a retry timer. What Alder reports is whichever identity it finds
  in the agreement, so nothing in the feature rests on this.
- **The total update is asked for from one condition: the consumer has no
  data.** Not from `repl-agmt create --init`, which starts one that has not
  finished by the time the condition is next evaluated, so a fresh harness ran
  two overlapping ones. Asking from the condition also fixes the case that
  actually bites -- a recreated consumer container, where the agreement
  already exists, nothing initialises the empty consumer, and the supplier
  reports "consumer (Unavailable)" for ever.
- **`moduleload ppolicy` on the consumer, with no overlay.** Loading it is
  what registers the `pwdPolicy` schema, and the supplier's seed holds two
  entries that use it. Without it the consumer's mdb backend cannot store an
  attribute it has no type for: syncrepl logs `be_modify failed (80)` and
  retries for ever, the consumer sits exactly two entries short, and every
  later change is stuck behind those two. Nothing outside the log says so,
  which is precisely why the harness asserts the entry counts match.
- **certgen is idempotent per file now, not per run.** It used to exit early
  if the CA existed, so adding a server to the harness would never have issued
  it a certificate. Related: `compose:up` force-recreates the certs container,
  because a container that exited successfully satisfies
  `service_completed_successfully` for ever and an edited `certgen.sh` would
  otherwise never run again.
- **`replicahar_test.go` tests the harness, not Alder.** Entry counts match, a
  change on a supplier reaches its consumer, and a consumer refuses a write.
  Both servers refuse it the same way, with result code 10, which is the sort
  of agreement between them that is worth pinning. A replication view proved
  against a harness whose replication had quietly stopped would be a view
  proving nothing, and the failure would read as a product bug for as long as
  it took someone to check.

### 2026-09-27 — 1.25, replication visibility

The third of the read-only views that answer "why is the directory like
this", after access (1.19) and password policy (1.21), and held to the same
rule: it says what this server records, and where.

- **Alder never contacts a peer.** A replication report that dialled out to
  whatever host a configuration value happened to name would be a report that
  can be pointed at anything, by anyone who can write to `cn=config`. So
  everything comes from the server in hand, and the disclaimer says so.
- **How far along a server is is the one thing both can be compared on.**
  OpenLDAP writes `contextCSN` on the suffix, 389 DS a replica update vector
  on the replica entry, and both mean "I have everything from server N up to
  this moment". Reading them into the same pair -- origin and time -- is what
  makes "open this against the other server and compare" a method rather than
  a suggestion. It is also the only answer to "is replication working" that a
  single server can give.
- **The setting keeps the value as the server wrote it.** For the same reason
  the indexes do: an operator pastes a change sequence at somebody, so the raw
  form is shown beside the parsed time.
- **OpenLDAP's "no status recorded" is printed, not left blank.** It keeps no
  outcome for a syncrepl link anywhere a client can read. An empty status
  column reads as "nothing wrong", which is the opposite of what is known.
- **The credential is dropped where the value is parsed.** An `olcSyncrepl`
  value carries the consumer's bind password in the clear, in an ordinary
  configuration attribute. Stripping it at the parse means nothing downstream
  -- renderer, logger, API view -- has to remember that this particular string
  is different from every other one. The conformance suite asserts no harness
  password and no credential attribute name appears in the response, on all
  four servers.
- **The role is phrased as "is set up to serve", not "sends changes to".** On
  OpenLDAP the only evidence a server supplies anything is that it carries the
  `syncprov` overlay; it does not record who has asked, or whether anybody
  ever has. The stronger sentence would be claiming to know something no
  single OpenLDAP can be asked.
- **A link is named by its rid or its agreement name, never by the `{0}` in
  front of it.** slapd writes an ordered value with its position first, and
  the first version of the parser read `{0}rid` as a key it did not recognise
  and lost the rid entirely. A unit test caught it; the conformance suite now
  refuses any link name containing a brace.
- **Read on request, not when the overview opens.** It is a search of the
  configuration tree, and the overview is the page that costs nothing to open.
  The entry counts on the same page already work this way.

### 2026-09-27 — 1.26, one entry and its conflicts

- **"Has this change arrived" is asked about one entry, so it is answered
  about one entry.** The change sequence at entry scale is the same number the
  suffix cursor is made of, and the method is the same: open the entry on the
  other server and compare. The conformance suite is that method -- write on
  a supplier, wait, and assert the two sides carry the same value.
- **389 DS's modification time is labelled as weaker evidence.** It keeps no
  per-entry change sequence a client can read. Presenting `modifyTimestamp` as
  if it were one would be presenting "two changes in the same second are
  indistinguishable" as a guarantee.
- **An empty conflict list from OpenLDAP is said to mean nothing.** OpenLDAP
  discards the losing change and leaves no trace, so there is nothing to find
  -- which is not the same as nothing having collided, and a bare empty list
  reads as good news. The report carries the sentence either way.
- **The search is sent to both servers regardless of the model.** Skipping it
  on OpenLDAP would make the answer depend on Alder being right about the
  server rather than on the directory's answer. A filter naming an attribute
  OpenLDAP has never heard of is answered, not refused.
- **The conflict fixture is written by the test, not seeded.** A genuine
  conflict needs two suppliers changing the same entry in the same instant,
  which is a race no test should depend on. What can be pinned is that an
  entry the server has marked is found, read and explained; the entry is put
  back afterwards either way, and the harness seed stays byte-identical
  between the two servers.

### 2026-09-27 — what an adversarial review of the above found

Every finding below was produced by a review of the unmerged stack, put to a
second agent told to refute it, and confirmed against the running harness.
Recorded because three of them are the kind of mistake that recurs.

- **The entry viewer and the LDIF export were serving cleartext passwords.**
  `schema.sensitiveAttrs` -- the one list every generic value path gates on --
  held `nsslapd-rootpw` and `nsds5ReplicaCredentials` but not `olcRootPW`,
  `olcSyncrepl` or `olcDbCryptKey`. So `internal/config` withheld them from a
  snapshot, `internal/replication` stripped them from a report, and
  `GET /entry` handed them to the browser: two features each knowing a fact
  the shared path did not. Confirmed live -- `/entry` and `/export/ldif` on
  `olcDatabase={1}mdb,cn=config` returned `alder-admin` and a whole
  `credentials="..."`. Pre-existing for `olcRootPW`; the replica harness is
  what made the syncrepl half reachable. The lesson is the general one: a
  feature that discovers a secret must put it in the shared list, not in its
  own.
- **An OpenLDAP change sequence's server id is hexadecimal.** slapd writes it
  `%03x`; `olcServerID` is decimal. Reading it as text made server 16 report
  as 10 and server 10 report as the letter `a` -- on the one screen an
  operator is told to compare against another server. Invisible in the
  harness, which uses ids 1 and 2.
- **Server id 0 is legal and was trimmed to nothing.** It is what a provider
  with no `olcServerID` stamps. The card read "from server :".
- **"...and receives none itself" was a claim no server had made.** A 389 DS
  read-write replica is type 3 whether it is the only supplier or one of
  several, so a multi-supplier reported itself as sending only -- while
  listing, three lines below, a change it had received from the other one.
  The word "supplier" is supportable; the negative was not. Receiving is now
  read from the replica update vector, which is evidence the server holds.
- **A syncrepl link on the configuration database was dropped silently.**
  `olcDatabase={0}config` has no `olcSuffix`, so the branch `continue`d --
  and a server replicating its own configuration was told it "replicates
  nothing". It is named after the configuration root instead.
- **The harness index write was not idempotent, and would have killed a
  restarted container.** `add: olcDbIndex` under `set -eu`, on a cn=config
  that lives in the container's writable layer: any `docker compose restart`
  or host reboot re-applied it, slapmodify failed with "Type or value
  exists", and slapd was never exec'd. `bulk_load` two blocks below already
  guarded itself against exactly this. Now guarded the same way, and proved
  by restarting both OpenLDAP containers.

### 2026-09-27 — 1.23 tells the rest of the product about itself

A sweep for deferred work found that indexes had been added to the comparison
and nowhere else, which is the ordinary way a capability becomes invisible.

- **Preflight was saying "Alder does not create configuration objects"** about
  an object Alder creates, and marking the finding `manualAction` and
  `blocksPortability`. Preflight is exactly where somebody decides whether a
  migration can be automated, so that is a wrong answer rather than stale
  wording. It now asks `config.CreatableKinds` for the target's provider —
  the list, not a second copy of it, because the answer changed in 1.22 and
  again in 1.23 and a copy would already be behind.
- **`config.CreatableKinds` stopped being dead code.** Its own doc comment
  said it existed "for a report and for the documentation to be written
  from", and neither had been. It is now what preflight asks and what the
  README sentence is written from. An exported helper nobody calls is a claim
  about the code that is not true.
- **The README said Alder creates one kind of configuration object.** Two,
  since 1.23, and the second is the one that works on both servers.

### 2026-09-27 — 1.27, asking about another identity, and the bundle

**The `as=` control.** The backend had answered this question since 1.19 and
the interface had never asked it. What made the UI work more than plumbing is
one fact about the backend: the subject reaches exactly one thing, the
effective-rights control. The rules, and Alder's marks on them, come from the
target DN alone.

- **Only the verdict changes, and the screen says so.** A relabelled verdict
  above an unchanged rules list reads as an answer about that identity. It is
  not: access rules are about an entry. The sentence above the rules is not
  decoration — without it this feature would be the exact failure the access
  view was written to avoid.
- **No control where the server does not answer.** On OpenLDAP the response is
  byte-identical whatever identity is named. A control that changed nothing
  would make Alder's reading of rule text look like a server's answer about a
  person. Not a disabled field with a tooltip either: the report already
  carries the server's own note in the verdict's place, and a second paragraph
  saying the same thing is one more line on a screen the last audit called
  long.
- **The verdict is labelled by what the UI asked, not by what the server
  echoed.** The handler fills a blank `as` with the session's own bind DN
  before it asks, so `effective.subject` is non-empty for every bound session
  and cannot tell "me" from "somebody else". Reading it that way is why the
  panel already said "this identity" to a person asking about themselves —
  dead code since 1.19, found while writing this.
- **Somebody else's verdict loses the success tint.** Neutral, not warning:
  nothing is wrong, the panel is simply about a different person. Green says
  "you are fine" and it would be saying it about the wrong person.
- **A subject that is not a DN is refused with a 400.** The only authzID the
  driver builds is `dn: <DN>`. 389 DS answers a malformed one with an error
  code where the rights letters go, which reaches the reader as "the server
  declined to say" — a sentence about access, describing a typo.
- **Not done, and it needs a decision:** asking about *anonymous*. The driver
  already sends `dn:` with no DN for an empty subject, which is how the control
  names an anonymous requester, but the handler turns blank into the session's
  own bind. Reaching it needs a sentinel and therefore a wire-contract change.

**The bundle is not split, and the warning limit is raised instead.** Measured
on this branch with a sourcemap build attributing every emitted byte: 832 kB
raw, 241 kB gzip, ~200 kB brotli, and Fiber compresses the response. 61% is
vendor — react-dom alone is 182 kB, TanStack ~119 kB, radix ~80 kB, lucide
33 kB already tree-shaken; there is no highlighter, no diff library and no date
library. A *complete* route-level split of all ten views moves at most 260 kB of
app code and leaves an entry chunk of ~546 kB, which trips the 500 kB warning
anyway. A split that ends in "and then raise the limit" is not an answer to the
warning, and it would trade instant view switches for a Suspense spinner in a
tool that is offline-first and loaded once from an internal host.

`chunkSizeWarningLimit` is 1000: a ceiling with about 170 kB of headroom, not a
silencer. Whoever trips it should measure again rather than raise it again.
`embed.FS`, the GoReleaser assertion and the CSP were all checked and none of
them would have blocked a split, so if this is ever revisited the blocking work
is known to be small.

**A missing asset is a 404 now, not the page.** Found while investigating the
split, and it stands on its own. `NotFoundFile` answered every unrecognised
path with index.html, which is right for a client-side route and wrong for
`/assets/index-ABC123.js`: a hashed filename is a content claim, and answering
it with an HTML document and a 200 turns "that file is gone" into a syntax
error at the point of use. The case is real — a tab left open across a deploy
asks for the previous build's chunks by name — and it is what would have made a
split hard to do safely.

### 2026-09-27 — a second review, and what it found in the first

Six findings survived refutation. Two were found by *mutation* rather than by
reading — a test was copied into an isolated module, the code under it was
broken, and the suite was watched to see whether it noticed. Both times it did
not. That is worth adopting as a habit: a green suite is evidence only if
something has checked that it can go red.

- **`errors.Unwrap` on an `errors.Join` value returns nil.** The access and
  policy handlers both did `s.fail(c, errors.Unwrap(err))` on the
  entry-unreadable branch, so `s.fail` matched nothing and every unreadable DN
  answered "Something went wrong" with a 500 — on the two screens where the
  difference between a typo and a permissions problem is the thing the reader
  came for. Joined errors implement `Unwrap() []error`, not `Unwrap() error`;
  `errors.As` walks them correctly, so the fix is to pass the error itself.
  Pre-existing since 1.19/1.21, and reproduced against the harness.
- **A storage scheme is a password's, not every secret's.** Adding the
  configuration secrets to `schema.sensitiveAttrs` had a tail: the entry view
  reports the RFC 2307 `{scheme}` beside a withheld value, and an unprefixed
  value as "stored in the clear". `olcSyncrepl` begins with slapd's ordering
  prefix, so it reported a hash scheme called "0"; `olcDbCryptKey` has no
  brace, so it reported a secret stored in the clear. Both are sentences about
  a value the rule does not cover. `valueSchemes` is now asked only of the
  attributes that really are passwords.
- **The scope sentence pointed at a verdict that was not there.** It said
  "only the verdict above is about cn=X" whenever a subject was named,
  including when 389 DS declined the question — which is not an edge case:
  every non-root bind asking about another identity gets a numeric error code
  where the rights letters go. The sentence now takes whether a verdict exists
  and words the other case for itself.
- **`dnEquals` compared two strings written by different hands.** The bind DN
  is what the operator typed on the connect screen, kept verbatim; the subject
  is what the picker returned in the server's own spelling.
  `cn=admin, dc=alder, dc=test` against `cn=admin,dc=alder,dc=test` printed
  "— not you" over a person's own rights. Now folded around the separators.
  Not RFC 4517 matching, which needs the schema and belongs on the server.
- **`TestTheRealAssetIsStillServed` asserted only the status code.** A handler
  that answered every path with index.html and a 200 passed all four web
  tests while the application could not boot. It now asserts the content type
  per extension, and that mutation fails it.
- **Nothing exercised the `as=` control end to end.** The query parameter
  could be deleted from the client and fourteen tests stayed green: what was
  pinned was the pure functions and two presentational components, not the
  line that connects them. The request and its cache key are now built by
  `accessQuery` / `accessQueryKey` — a function a test can hold to account,
  where a literal buried in a `queryFn` could not be.

### 2026-09-27 — replication's running state is not configuration

CI caught this the first time the replication branch was tested against a
main that already had the four-server harness in it: two captures of an
unchanged 389 DS configuration produced different checksums. It passed
locally and failed in CI, which is the signature of a race, and the race was
real — whether an exchange happened to land between the two reads.

A replica entry and its agreements sit in `cn=config` and carry counters and
timestamps the server rewrites every time it replicates anything. Measured
against the running harness rather than guessed: `nsState`,
`nsds5ReplicaChangeCount`, `nsds5replicaChangesSentSinceStartup`,
`nsds5replicaLastUpdateStart` and `...End` all moved across a single write.
`nsDS5ReplicaName` goes with them for a different reason — it is generated
per instance, so two servers never agree on it and a comparison would report
a difference nobody can act on.

They are skipped now, through the `skipped` set the classifier already had
and neither model used. Nothing is lost: `internal/replication` reports every
one of them, as state, which is what they are.

The wider point is the one worth keeping. **This was not a bug the replication
feature introduced; it was a bug the replication harness exposed.** Any 389 DS
that actually replicates — which is most of them — had an unstable
configuration snapshot, and snapshot-and-compare is a headline feature. Two
servers alone could never have shown it. That is the harness doing the job it
was doubled for, on the first run after the merge.

The conformance case that found it depended on timing, so there is now one
that does not: three writes between the two captures, so the supplier really
does exchange something with its consumer while we look.

### 2026-09-27 — the release notes were wrong, and why

A check of what v1.26.0 actually shipped, run independently of anyone's account
of it, found that commit `6d74230` (#139) carried **eight distinct changes**
under one `feat(replication):` subject with no `BEGIN_COMMIT_OVERRIDE` block.
release-please therefore emitted exactly one changelog line for roughly 7,100
added lines across 39 files.

Unannounced, among them: a **credential-disclosure fix** — the entry viewer and
every LDIF export had been serving `olcRootPW` and a whole `olcSyncrepl` value,
which carries the consumer's bind password in the clear. `SECURITY.md` promises
the changelog will say so plainly, and it did not. Also unannounced: a
user-facing access-control feature, an input validation narrowing on an existing
endpoint, the 500-instead-of-404 fix, the hexadecimal change-sequence fix, and
the `/assets/` 404.

The changelog and `docs/COMPATIBILITY.md` are corrected by hand, marked as
added after the release, with the reason stated at the top of the section
rather than quietly. The release itself is untouched.

**The rule this leaves.** The override block is not decorative and it is not
only for the version number. A squash whose body is a list is a squash that
needs `BEGIN_COMMIT_OVERRIDE`, with one conventional-commit line per change
inside it — the three PRs merged before this one each carried the block and
each produced three to five changelog lines. A stacked branch is exactly where
this goes wrong, because the branch accumulates changes that have nothing to do
with its title.

Two smaller things from the same check, both correct and recorded so they are
not rediscovered: `Release-As` worked this time (nothing follows it in the
message, which is what broke 1.18.0), and the version stamping is right —
`.goreleaser.yaml` passes `-X main.version={{ .Version }}` and the workflow
checks out the release tag, so a release build reports 1.26.0.

### 2026-09-27 — 1.29, the two things the audit earned

Both come from the black-box walk of "this account cannot log in, why", and
both were measured before they were fixed.

**Clearing a lock.** The report already names the attribute that holds the
lock — it has to, to say the account is locked — and that is where it stopped.
The entry viewer filed the attribute under "operational, kept by the
directory, **yours to set**", no editor offered it, the password dialog did
not mention it, and the only way out was a hand-written LDIF modify in the
import screen. Seventeen interactions between knowing the answer and applying
it, against three.

- **The change is derived by the server**, carried on the report beside the
  state it came from, for the reason every other change is: one code path
  builds what gets sent, and the LDIF the operator confirms is rendered from
  that record. The browser builds no LDIF.
- **What is removed depends on what the entry holds.** `pwdAccountLockedTime`
  takes `pwdFailureTime` with it, because the overlay re-locks at
  `pwdMaxFailure` and an operator who unlocked an account does not expect the
  next single failure to lock it again. `nsAccountLock` stands alone, and is
  deleted rather than set to `false`, which would leave a value that reads as
  though somebody meant something by it.
- **An attribute the entry does not hold is never named.** A delete of an
  absent attribute is an error on both servers, so naming it would turn a
  working unlock into a refusal.
- **A lock Alder does not recognise gets no change**, and the dialog says so.
  A change invented for an attribute Alder has never seen would be a write
  nobody asked for, on the screen whose whole point is that it reports.

**Making a failure visible.** The audit reported nine failed requests with
"zero words on screen and zero in the console", and proposed adding error
surfaces. Reproducing it first showed the diagnosis was wrong in a useful way:
the error surfaces exist and do render. What was wrong was the timing and the
header.

Measured, with the harness 389 DS paused: `POST /search` took **thirty
seconds** to return 502, and the retry policy spent two more of them — ninety
seconds before any screen was allowed to say something had gone wrong. And
`GET /session` answered **200 in 1.6 milliseconds** reporting a healthy bind
throughout, which is why the header went on saying "Bound as cn=Directory
Manager" to a directory that had stopped answering.

- **An answer the server gave is never retried.** An `ApiFailure` means Alder
  answered; retrying costs another full operation timeout and changes
  nothing. A failure that never reached Alder is worth one retry.
- **Every failure is written to the console, unconditionally**, from the query
  and mutation caches rather than from each view. The first place anybody
  looks said nothing at all.
- **The header watches what the requests are doing**, because it cannot ask
  the session: a session is an object in memory. It shows nothing until
  something fails upstream, and then names it and offers Reconnect — the cure
  the interface had never suggested. A 403 or a 404 does not count: an
  indicator that cries wolf over the ordinary business of the day gets
  ignored, which is worse than not having one.

The server-side thirty-second timeout is unchanged and deliberate: a real
search can legitimately take that long. What changed is that Alder no longer
waits three times over, and says so when it gives up.

### 2026-09-27 — what the review of 1.29 found

Eight findings survived refutation. Three mattered, and one of them made the
feature useless in the only case it was built for.

- **`pwdFailureTime` is `NO-USER-MODIFICATION`, and the unlock was deleting
  it.** The reasoning had been: the overlay re-locks at `pwdMaxFailure`, so
  clear the counted failures too. The directory owns that attribute, so the
  server refused the modify with a constraint violation — and a modify is
  atomic, so the lock was not cleared either. An account only carries
  `pwdFailureTime` when the overlay is what locked it, so **every real
  OpenLDAP lockout was unfixable**. It was also unnecessary: deleting
  `pwdAccountLockedTime` alone unlocks, and the overlay discards the failure
  times itself.
- **The conformance case could not see it, because it faked a lock shape the
  server never produces.** Writing the attribute as an administrator gives
  `pwdAccountLockedTime` with no `pwdFailureTime`. There is now a case that
  makes the *server* do the locking — five bad binds against the one account
  the harness holds to a strict policy — and it fails on the old code with
  `Constraint Violation (code 19)`. The lesson generalises: a fixture built by
  writing the state a server would have written is not the same as the state,
  and the difference is exactly where the defects live.
- **389 DS's automatic lockout was reported as no lock at all.** The state
  reader only looked at `nsAccountLock`, so the commonest lock on that server
  — failed binds reaching `passwordMaxFailure`, after which the server sets
  `accountUnlockTime` — left the dialog with nothing to say, and the table
  entry meant to clear it could never be reached. It is a lock while that
  time is still in the future.
- **A consumer refuses replicated password-policy operations.** Found because
  the new test locks an account by failing binds, which makes the supplier
  write lockout state and try to replicate it. 389 DS rejects that on the
  consumer unless `passwordIsGlobalPolicy` is on — and the agreement then
  falls into "Error (16) … connection error. Backing off", which stalls the
  **whole** agreement. One locked-out account silently stops replication for
  everything. The harness sets it on the consumer, in `replicate.sh` rather
  than in the policy seed, because `cn=config` does not replicate: it has to
  go on the server doing the rejecting. Any replicated 389 DS where accounts
  are ever locked out needs the same.
- **Any success cleared the health badge, including from requests that never
  touch the directory.** `/session`, `/schema` and `/source` all answer 200
  from memory in single-digit milliseconds while the directory is down —
  `/session` refetches on window focus, so looking away and back restored
  exactly the false claim the indicator was built to remove. They are
  excluded by name, which is the weak point and is written down as such; the
  honest fix is for the server to say which responses involved directory I/O.
- **The new console line logged `error.detail`, which can quote a password.**
  An LDIF parse error echoes the offending source line, and a line can be
  `userPassword: …`. Verified against the running server. It logs the status,
  the code and the message now — the dialog still shows `detail` on screen,
  where it is not written down. Rule 6 does not stop being true because the
  log is in a browser.

The re-walk of the audited task measured **2 interactions**, not the 3 the
change claimed, and the same 2 on both servers.
### 2026-09-27 — 1.28, asking about anyone unauthenticated

The piece 1.27 left open, and it needed a wire-contract change, which is why
it waited for a decision.

- **A reserved word, not an authzID and not a second parameter.** The
  effective-rights control identifies an unauthenticated requester by an
  authorization identity carrying no DN at all, so the question genuinely has
  no DN to put in `as`. The options were exposing the authzID forms (`dn:`,
  `u:`) in the API for the sake of one case, a second boolean parameter that
  can contradict the first, or one reserved value. `as=anonymous` is the
  reserved value, and it is safe to reserve because a DN always contains an
  equals sign — nothing a person could legitimately type collides with it.
- **`askedAbout` on the report.** Alder describing its own request, which is
  unambiguous where `effective.subject` is not: a server answering the
  anonymous question echoes no subject, because there is none, and so does a
  server that declines. Without it a client could not tell the anonymous
  answer from a declined one.
- **It is worth having, and that was checked before it was built.** 389 DS
  answers the question meaningfully — on the harness, entry rights `none` and
  all 61 attributes denied for an unauthenticated client. That is the seeded
  access rules being confirmed rather than assumed, and it is the one question
  on this screen an operator asks about an entry they did not expect to be
  readable.
- **One click, not a field to type into.** The identity has no name, so
  there is nothing to type; the button sits where the reset sits when a
  subject is in force.

### 2026-09-27 — what the review of 1.28 found, and one thing it found in 1.20

- **`effective.subject` is Alder's own request value, not the server's.** The
  Get Effective Rights response carries only the rights; there is no subject
  in it to echo. The 1.28 prose said the opposite — in `api/openapi.yaml`,
  which is the contract third parties read — and the documentation and a test
  comment repeated it. Corrected, and the schema's own description now says
  plainly not to compare the field against what you asked for.
- **The consequence, which is older than 1.28:** the warning in the verdict
  panel that says "the server answered about X, which is not what was asked"
  can never fire. Both sides of the comparison come from the same request
  string, unnormalised on either side. It shipped with effective rights in
  1.20 and has been dead ever since. Deleted rather than repaired: a guard
  that cannot fire is worse than none, because it reads as a check that has
  been done. `askedAbout` is the honest version of the same idea.
- **`strings.EqualFold` and `toLowerCase` do not agree.** EqualFold applies
  Unicode simple folding and matches U+017F LATIN SMALL LETTER LONG S against
  "s", so `anonymouſ` reached the server as the reserved word while the
  browser read it as an identity of that name — and the screen headed a
  genuinely anonymous verdict with a name nobody had asked about. The match is
  ASCII-only now, on both sides, and a conformance case holds the server to
  it.
- **"Strictly narrower than the administrator's" was not enough.** Proved by
  mutation: replacing the empty subject with a real service account's DN kept
  the test green while the dialog printed "what an unauthenticated client may
  do" over a bound account's rights. The assertions are now the two things
  only the real anonymous answer has — no subject at all, and entry rights
  `none`.

### 2026-09-27 — 1.30, the last three audit findings, and what closing them exposed

The audit's two critical findings were closed in 1.29. These are the three
that were left, plus a scope violation found while closing the second one.

- **The unlock is offered where the badge is.** The red "account locked" badge
  is read from attributes already in hand, so it costs nothing and appears
  before anything is opened; the change that clears the lock is derived by the
  server, so the header asks for it. One extra request on a locked entry and
  none on any other. It renders nothing until the report says there is a lock
  it knows how to clear — a button that appears and then fails is worse than
  no button, because the Policy dialog is where an unrecognised lock gets
  explained in words. Locked entry to cleared lock: **two interactions**,
  against the seventeen the audit measured.

- **Operational is not the question the editor was asking.** Whether the
  server will accept a write is answered by NO-USER-MODIFICATION and nothing
  else. The editor excluded every operational attribute instead, so the
  viewer's heading — "operational, kept by the directory, **yours to set**" —
  was contradicted by the editor one click away, and a locked account could
  not be unlocked from the screen that named the attribute. The exclusion is
  now `readOnly`, a withheld secret, or Alder's own decision; a settable
  operational field carries a "kept by the directory" badge, because the
  server may change it back and an operator should know that before typing.

- **Two attributes were writable that the decisions log says Alder does not
  write.** Found while doing the above, and older. `aci` is operational and
  settable, so the attribute picker offered it on every 389 DS entry that did
  not already carry one; `olcAccess` is an *ordinary* attribute of an OpenLDAP
  database entry, so nothing filtered it anywhere and `olcDatabase={1}mdb`
  opened as four editable text boxes of access rules, `{0}` ordering prefixes
  included. Reading access rules is 1.19; writing them is out of scope, and
  the reason is that an access rule is the one change that can lock every
  administrator out, with first-match-wins ordering that makes a
  correct-looking single edit change the meaning of every rule after it. The
  new `elsewhere` field on an attribute's kind carries *why* an attribute is
  shown and not editable, so the viewer prints a sentence instead of leaving
  an absence to be discovered — which is the same dead end the audit found
  around `nsAccountLock`, from the other direction. The subschema definition
  attributes are held back the same way, and for a related reason: a schema
  change is an add or a delete of one definition, and a text box holding a
  thousand of them can only express a replacement of the lot.

  By name, which section 3 otherwise forbids. That rule is about detecting
  what a server can do; this is a statement about what Alder chooses to write,
  and both attributes are named in the charter itself.

- **"Still waiting" is a status; the failure badge was a post-mortem.** An
  LDAP operation gets thirty seconds, so the honest version of the badge has
  to appear while the request is outstanding rather than after it has given
  up. The query cache reports what is in flight, the header names the oldest
  such request after four seconds and counts the seconds after that. Per
  request rather than by a count of them: a count never reaches zero during
  ordinary navigation, which would make every busy moment look like a stall.
  A failure outranks a wait — "not answering" is established, "still waiting"
  is being established, and letting a retry downgrade the first would walk the
  indicator backwards while things got worse.

- **`(code 200)` was not a result code.** go-ldap numbers its own client-side
  conditions from 200 in the same field the protocol's result codes live in: a
  dead socket is 200 "Network Error". The audit read it as an HTTP 200 in a
  message about a failure, which is a fair reading. Those numbers are no
  longer printed as result codes and no longer sent as `ldapCode`, and the
  message for one says the directory did not answer rather than that it
  returned an error — two different things to go and look at.

- **The policy attributes are asked for the way each server spells them.** A
  search returns an attribute name as it was requested, and the unlock is
  built from the name the entry came back with, so asking in lower case
  produced an LDIF preview reading `delete: nsaccountlock` under a viewer that
  had just said `nsAccountLock`. Matching is still on the folded name, as it
  has to be.

### 2026-09-27 — what the review of 1.30 found

Five lenses over the diff, each finding refuted by a separate agent. Five
survived, and all five were real. Two of them were defects this work
introduced; two were older, and one of those -- a whole-schema replace
reachable from the entry editor on OpenLDAP -- is the same mistake as the
access rules, in the attribute one tree away from the one that was fixed.

- **An access rule was rendered twice, in two contradictory groups.** The
  viewer's five headings are five independent filters, and `olcAccess` is an
  *ordinary* attribute of an OpenLDAP database entry — not operational — so
  holding it back from the operational groups alone left it under Optional as
  well: once with the sentence saying there is no field for it, once without.
  The property worth testing is not which heading an attribute gets but that
  the headings partition the entry, so that is what the test asserts, and the
  mutation that removes the new filter turns it red.

- **The in-flight count could leak, and one leaked entry pins the badge on for
  the session.** Counting `fetch` events up and `success`/`error` events down
  is only correct if every fetch ends in one of those two, and a query removed
  mid-flight or paused because the browser went offline does not. It is now
  reconciled: every cache event hands over the whole list of what is running,
  and whatever is no longer in the answer is no longer in flight. That cannot
  drift from the cache, because it *is* the cache.

  The same change fixed the counter walking backwards. The badge counts the
  wait, not the request: a wait begins at the oldest request running when it
  began and only moves earlier, so one request of several returning no longer
  makes the number jump down, which reads as a broken clock rather than as
  progress.

- **The list of attributes Alder will not write named the wrong half of the
  schema.** `objectClasses` and `attributeTypes` are the *subschema's*
  spellings, and on OpenLDAP the subschema is a generated view whose
  attributes are NO-USER-MODIFICATION -- so covering them covered the half
  that was never reachable. The schema slapd actually writes is
  `cn={0}core,cn=schema,cn=config`, carried by `olcAttributeTypes` and
  `olcObjectClasses`, which slapd's own configuration schema declares as
  ordinary user attributes: no operational usage, no NO-USER-MODIFICATION.
  Exactly the `olcAccess` shape, one entry away, and measured on the harness
  before the fix that entry opened as fifty-two text boxes holding the core
  attribute definitions. One character changed in one of them would have sent
  a replace of the whole set. Older than this release, and closed by the same
  rule that closed the access rules; the conformance case now reads the
  writable location as well as the generated one.

- **The overwrite warning started firing on attributes the server moves by
  itself.** The banner says "applying them will overwrite the newer values",
  which is a sentence about a colleague. Once the editor offered the settable
  operational attributes, they entered the baseline the banner is computed
  from -- and a failed bind bumps `passwordRetryCount` and
  `retryCountResetTime` on 389 DS with nobody touching anything. Somebody
  editing a description while the account's owner mistyped their password
  twice would have been warned about overwriting an attribute the pending
  change does not mention. Operational attributes now count only when the
  draft touches one too, which is a genuine collision and is exactly what the
  banner is for.

Refuted, and changed anyway, because each was cheap and left the thing more
consistent than it found it: the changeset error path now withholds a
client-side code exactly as the single-change path does; `RequirementsView`
describes only the attributes it actually offers, rather than documenting ones
the same response declined; `IsClientSide` is the library's own numbers (200
to 206) rather than everything above 199, because swallowing a result code the
directory really sent is the more expensive mistake; and `resetHealth()` runs
after the disconnect requests rather than before them, so it is not undone by
what they record.

One finding was refuted and stayed refuted: the compact tooltip does not need
`safeText`, because the attribute name in it is adopted from the entry only
when it folds equal to one of five ASCII constants, and no character
`safeText` strips can survive that. The call was added regardless — escaping
every rendered directory string is a habit worth keeping unconditional, and
an exception that needs a paragraph to justify is an exception somebody will
get wrong later.

### 2026-09-27 — releasing 1.30.0, and why the changelog nearly said three lines

- **`BEGIN_COMMIT_OVERRIDE` is ignored when the squash subject is itself a
  conventional commit.** Three for three each way, in this repository's own
  history: `A refusal says where to look (#136)`, `Password policy and
  account state, read (#135)` and `Effective rights: asking the server
  (#133)` all carried an override block under a subject release-please
  cannot parse, and every line of every block reached the 1.26.0 changelog.
  `feat(access): … (#140)`, `feat(policy): … (#142)` and `feat(entry): …
  (#144)` carried blocks of five, seven and twelve lines under subjects it
  can parse, and all three were dropped for the subject alone — twenty-four
  changes rendered as three lines.

  So the rule for this project is: **a squash whose body carries an override
  block must have a subject that is not a conventional commit.** The subject
  is what the tool reaches for first, and a parseable one stops it looking
  further. The note in the 1.26.0 changelog blamed a missing block for that
  release's single line; this is the other way to get the same result, and it
  is the easier mistake to make, because a conventional subject is what every
  other rule in this repository asks for.

  `Release-As:` is unaffected — it is read from the raw message whatever the
  subject looks like, which is how `chore: release as 1.18.0 (#128)` worked.

- **Released as 1.30.0.** release-please proposed 1.27.0, being one minor bump
  from 1.26.0, while the documentation names 1.28, 1.29 and 1.30 as the
  milestones since. Forced to the highest of them, exactly as 1.18.0 was, so
  that no statement already written becomes untrue; 1.28 and 1.29 are
  milestone numbers with no tag of their own.

  1.27 is *not* one of them, and the compatibility notes said it was. Asking
  `GET /access` about another identity shipped inside 1.26.0 — it was part of
  commit `6d74230`, which is where that release's eight-changes-one-line
  problem came from. Corrected in `docs/COMPATIBILITY.md`.

- **The changelog for this release was written before the tag, not after it.**
  1.26.0 was corrected afterwards, with a note in the changelog explaining
  why the entries appeared late. Doing that twice would make it a habit
  rather than an accident.

### 2026-09-28 — the correction to yesterday's release note, from the tool's own log

The entry above claimed `Release-As:` is read whatever the subject looks
like. It is not, and the run that was supposed to act on it says so:

```
commit could not be parsed: af47513 Release 1.30.0, and the reason its
  changelog nearly said three lines (#149)
error message: Error: unexpected token ' ' at 1:2
...
PR #143 remained the same
```

An unparseable subject does not make release-please look harder; it makes
release-please **drop the commit entirely**, footer and all. So the version
stayed at 1.27.0 and the release did not move.

The complete rule, which the two halves only make sense together:

- A commit whose subject is a conventional commit is read from the subject,
  and a `BEGIN_COMMIT_OVERRIDE` block in its body is ignored.
- A commit whose subject is not is dropped, **unless** it carries an override
  block, which then supplies the conventional lines in its place. That is how
  `A refusal says where to look (#136)` put two entries in the 1.26.0
  changelog.
- `Release-As:` is only honoured on a commit that survives one of those two
  paths. `chore: release as 1.18.0 (#128)` did; this one did not.

So an override block needs an unparseable subject, and a `Release-As:` footer
needs either a parseable one or an override block to travel with. Getting
that backwards costs a release cycle and is invisible until the version does
not change.

### 2026-09-28 — the UI refresh, and the status light in it

Eight pull requests restyled the screens: a fade-in shell, an eyebrow label
above each title, larger headings, a gradient behind the connection form.
Merged, with three things put right afterwards.

- **A green dot labelled "Live directory session", hard-coded.** It would
  have stayed green while the directory was unreachable and the header two
  inches away said "the directory is not answering". That is the fault 1.29
  was built to remove -- a session object in Alder's own memory answering for
  a directory that had stopped answering -- and a decorative version is
  worse, because it does not even read the session. Replaced with the one
  thing the overview can state from what it holds: whether the session is
  read-only.
- **The access dialog announced its own name twice**, as eyebrow and as
  title, which a screen reader reads out in full both times.
- **The fade-in ignored `prefers-reduced-motion`.** For some readers that
  setting is the difference between using a page and feeling ill, and an
  administration tool has no business spending it on a 240ms flourish.

The eyebrow wording is left as it arrived -- "Directory intelligence",
"Portable intent", "Safe migration planning" -- because it is taste rather
than a claim, and it is not mine to overrule. It is worth saying that it
reads like a brochure beside the rest of the product's voice, which is plain
to the point of flatness on purpose.

### 2026-09-28 — the three critical findings of the second UI audit

An audit of "bring the replica's indexes into line with the primary", run
against OpenLDAP at both ends — the first audit of any kind on that server.
The report is `docs/ui-audits/2026-09-28-indexes-on-a-replica/`. Fifty-four
interactions against an ideal of eleven, and forty-six of them before the
operator saw the drift they came for.

- **An export released cleartext credentials, and the screen beside it said
  to use one.** The entry viewer withholds `olcRootPW`, then said *"Sensitive
  attributes are omitted; use Export if you need them"* — and Export with
  "include sensitive attributes" wrote the root password in plain text and
  the replication bind password inside an `olcSyncrepl` value. The 1.26 fix
  had added the configuration tree's secrets to the set that option releases;
  that set was written for password *digests*, where an export you can
  restore from is a real need.

  The rule is now per value, not per attribute, because the attribute cannot
  tell you: `olcRootPW` holds `{SSHA}…` on one server and the password itself
  on the next. A value carrying an RFC 2307 `{scheme}` prefix is a digest and
  may travel; a value without one *is* the password and never does. That is
  the same reading the entry viewer already shows an operator — it prints the
  scheme as a badge, or the words "stored in the clear" — applied where it
  has consequences. A short list is refused whatever it looks like:
  `olcSyncrepl` and friends carry a credential inside a longer string, and
  key material is not a digest of anything.

  Consequence worth stating plainly: on a server that stores passwords
  unhashed, an export now carries no password at all. The harness's OpenLDAP
  is such a server, so the conformance case proves both halves at once —
  389 DS stores `{PBKDF2-SHA512}` and the digest travels, OpenLDAP stores the
  password and the export names it and leaves it.

- **A capture was destroyed by disconnecting, which made the feature's own
  headline task impossible.** Alder can only capture the server it is
  connected to, so comparing two servers *requires* holding one across a
  disconnect. `bench.clear()` on disconnect had a stated reason — the next
  session in this tab may be a different operator on a different server — and
  the reason was real while the cost was never weighed: the audit lost its
  capture and spent eighteen interactions recovering by round-tripping a file
  through the filesystem.

  The bench survives a disconnect now, and each document says which server it
  came from instead. The worry was never that the document is present; it was
  that nobody could tell whose it was. Recorded client-side, not in the
  document, because the document's bytes are checksummed and comparable
  across servers.

- **Disconnect answered 503 and said nothing, three ways at once.** The
  request was queued behind the in-flight gate that exists to protect the
  *directory*, though letting go of a session is a delete from a map in
  Alder's own memory — so exactly when Alder was busy, an operator could not
  let go. It then awaited `invalidateQueries()`, which refetches every live
  query against the directory being abandoned, so the screen sat there for
  the full operation timeout. And it called the API outside the query and
  mutation caches, so 1.29's "every failure is written down, once, in one
  place" never saw it.

  Three fixes: the routes that ask the directory nothing bypass the gate;
  nothing is refetched on the way out, the cache is dropped; and the
  disconnect is a keyed mutation like every other write. One click now, and a
  failure reaches the badge and the console.

### 2026-09-28 — the release-note rule from 2026-09-27 was wrong, again

Yesterday's entry said release-please ignores an override block when the
squash subject is itself a conventional commit, and offered a three-for-three
table. Merging #155 refuted it: a **non**-conventional subject, a correctly
formed block of seven `fix:` lines, and the run said

```
commit could not be parsed: d30657b An export that cannot leak a credential…
✔ Considering: 0 commits
✔ No commits for path: ., skipping
```

so no release pull request was opened at all. The rule as written would have
predicted the block being used.

What the ten merges since 1.19 actually show, as shapes rather than as a
theory:

| shape | outcome |
|---|---|
| block, nothing after `END_…`, unparseable subject | the block is used — every line reaches the changelog |
| block, git trailers after `END_…`, conventional subject | the **subject** is used; the block is silently ignored |
| no block, the marker only mentioned in prose | the commit is dropped entirely |
| block, git trailers after `END_…`, unparseable subject | the commit is dropped entirely |

Two things follow that are worth acting on, and one that is not yet known.

- **Never write the marker in prose.** `Release 1.30.0, …` (#149) and
  `fix(ui): a status light…` (#154) both merely *described* the mechanism in
  their commit bodies, and both were dropped — taking a `Release-As:` footer
  with them, which is why 1.30.0 had to be driven by hand.
- **Put nothing after `END_COMMIT_OVERRIDE`.** The three commits whose blocks
  reached the changelog end there. The ones that carry `Co-Authored-By:` and
  `Claude-Session:` after it do not, unless the subject rescues them.
- **Not established:** why `docs: say what v1.26.0 actually shipped (#141)`
  was dropped when `feat(access): … (#140)` in the same shape was not. I have
  guessed at this mechanism twice now and been wrong twice; it is written
  down here as unexplained rather than explained away.

The reliable path does not depend on any of it: **drive the release pull
request by hand** — set `.release-please-manifest.json`, rewrite the
`CHANGELOG.md` section, retitle, merge — which is how 1.30.0 shipped with
twenty-four accurate entries. Check the release run's log after every merge;
`gh run view <id> --log | grep "could not be parsed"` is the whole check.

### 2026-09-28 — indexes get a door (1.32)

The second UI audit's M4: creating an index was possible from 1.23 and only
from inside a configuration comparison, where a row appeared if this server
differed from a snapshot of one that already had the index you wanted. So
the feature was really "copy an index another server has", and the ordinary
reason to add one — a slow search you have just diagnosed — had no path at
all. The auditor looked on the database entry, in the editor and on the
directory screen, found nothing, and wrote down that you were back in
`ldapmodify`.

- **Two endpoints, and no second way to write.** `GET /config/indexes` lists
  each backend and what it indexes; `POST /config/indexes/candidate` derives
  the change for one attribute. Both hand back the record the comparison
  already derived, and it goes through the same plan, the same LDIF and the
  same confirmation. Two ways to ask, one way to write, which is the rule
  that keeps `ChangeRecord` meaningful.

- **A backend is one that serves a naming context.** The configuration model
  calls a great many things a backend: on the harness 389 DS reports
  thirty-nine, of which one holds directory data and the rest are the ldbm
  machinery and one entry per default-index template. A panel listing all of
  them is a panel nobody reads. The filter is the server's own answer — the
  naming contexts the RootDSE advertises — rather than a list of names Alder
  carries, which is section 3 applied to a place that does not obviously look
  like a capability question. A backend that already holds indexes is kept
  whatever its name, so nothing the server is really indexing can vanish
  because Alder disagreed about what it is.

- **The refusal is a sentence, not a code.** `index_value_shared` is what the
  comparison put on the screen, and the audit read it there. The report
  carries both: the code to branch on, and the reason in words for the
  operator — an OpenLDAP value naming several attributes is a rewrite rather
  than a deletion, and an index 389 DS maintains for itself is not Alder's to
  take away. Proved on both servers: `l` and `st` on OpenLDAP share
  `olcDbIndex: l,st eq`, and nine of 389 DS's thirty-one are its own.

- **"Already indexed" is an answer, not a refusal.** Asking for an attribute
  the backend already indexes returns `exists`, so the screen can say so
  rather than showing an empty result that reads as a failure.

Proved end to end in a browser as well as in the suite: `title` indexed and
unindexed through the panel on the live OpenLDAP, twelve indexes before and
twelve after.

### 2026-09-28 — capturing the server you are not connected to (1.33)

The second UI audit's M1, and the other half of the index work. A
configuration snapshot exists to be compared against another server, and
Alder could only capture the one it was connected to — so the comparison an
operator actually wants, this replica against its primary, meant capturing
one, disconnecting, connecting to the other, and comparing. The audit
measured forty-six interactions before the drift was on screen, and lost its
first capture to the disconnect on the way. (1.31 stopped the bench being
cleared, which removed the loss; this removes the second connection.)

`POST /snapshots/capture/from` opens a connection to a directory the caller
names, reads its configuration, and closes it.

- **The allowlist applies, and that is the point worth testing.** An
  endpoint that dials outward without consulting `--allowed-targets` is a
  way around it: "capture from" would reach hosts "connect to" cannot. The
  check is the same one the connection screen goes through, before anything
  is dialled, and a conformance case holds an instance to it on both servers.
- **The credentials live for one request.** No session is opened on that
  server, nothing is stored, nothing is logged — verified by grepping the
  server's log after a capture — and the connection is closed however the
  request ends. The dialog says so above the password field rather than
  after it, because the person typing a production bind password is entitled
  to know first.
- **Reading only, and only the configuration.** Alder applies changes to the
  session's own directory and nowhere else. A schema or data snapshot of
  somewhere else would be a bigger promise — a subtree of arbitrary size read
  with someone else's credentials — and nothing asks for it yet.
- **The connection settings are read in one place.** `connConfigFrom` is
  shared with the connection handler, because two readings of the same
  settings is two places for the TLS rules to drift apart, and the rule that
  matters — a plaintext target is refused unless this Alder was started to
  permit it — lives in `ConnConfig.Validate`, which both go through.
- **The form does not pre-fill the host.** Everything usually shared between
  two servers in a pair is carried over — how they are reached, who you bind
  as — but a form pre-filled with the server you are on is one that captures
  the wrong thing when somebody hurries.

Measured after: connect, choose Configuration, capture the other server, five
fields, Compare. Against forty-six interactions and two connections.

### 2026-09-28 — release-please, the fourth wrong answer, and what to do instead

Four hypotheses about why release-please drops Alder's squash commits, four
refutations, each by the next release run. Written down in full because the
pattern of being confidently wrong is the useful part.

| # | The rule I wrote | What refuted it |
|---|---|---|
| 1 | An override block is ignored when the subject is a conventional commit | #155: non-conventional subject, correct block, dropped |
| 2 | `Release-As:` is read whatever the subject looks like | #149: the footer was ignored and the version never moved |
| 3 | A commit with git trailers and no override block is dropped | #160 and #161: no trailers, a block, dropped |
| 4 | Carriage returns in the message break the parse | Every commit in the repository has them, parsing or not |

The observations themselves are solid; only the explanations were wrong.
Across ten squashes the log says `could not be parsed` for six and nothing
for four, and the four that parsed are: `docs: … (#158)` with no block and no
trailers, and `A refusal says where to look (#136)`, `Password policy and
account state, read (#135)` and `Effective rights: asking the server (#133)`
— each a non-conventional subject, an override block, and **at least one line
of ordinary text after the closing marker**. Every commit lacking that last
property has been dropped.

So the convention, stated as a convention and not as a mechanism: **a squash
whose body carries an override block ends with a line of prose after
`END_COMMIT_OVERRIDE`, not with the marker and not with a git trailer.** It
has held four times out of four and failed none. I do not know why, I have
stopped guessing, and the next person should not treat it as understood.

What does not depend on any of it: **the release pull request is driven by
hand whenever the log shows a dropped commit.** Set
`.release-please-manifest.json`, write the `CHANGELOG.md` section from the
dropped commits' own override blocks, retitle the pull request, keep the
`autorelease: pending` label that release-please keys on, and merge. That is
how 1.30.0, 1.31.0 and 1.33.0 shipped, each with an accurate changelog, and
it takes about five minutes. The one-line check after every merge is
`gh run view <id> --log | grep "could not be parsed"`.

1.32 is a milestone number with no tag: the index door and the remote capture
reached a release together in 1.33.0.

### 2026-09-28 — the actual mechanism, read from release-please's source

Four hypotheses, four refutations, and then the obvious step: install the
tool and read it. `release-please/build/src/commit.js`:

```js
function preprocessCommitMessage(commit) {
    // look for 'BEGIN_COMMIT_OVERRIDE' section of pull request body
    if (commit.pullRequest) {
        const overrideMessage = (commit.pullRequest.body.split('BEGIN_COMMIT_OVERRIDE')[1] || '')
            .split('END_COMMIT_OVERRIDE')[0].trim();
        if (overrideMessage) return overrideMessage;
    }
    return commit.message;
}
```

Two things were backwards, and between them they account for every one of
the ten squashes since 1.19 — six dropped, four parsed, no exceptions.

- **The override block is read from the pull request body, not the commit
  message.** This repository's notes said the opposite and told the next
  person to pass the block with `--body-file` at merge time, which puts it
  somewhere the tool never looks. #140, #142 and #144 each carried a
  correct block in the squash message and got one changelog line from their
  subject, because the subject was a conventional commit and the fallback
  took it. #155, #160 and #161 did the same with a subject that is *not* a
  conventional commit, and were dropped entirely.

- **A bare mention of the marker in a pull request body hijacks the parse.**
  The split takes everything after the first occurrence, to the closing
  marker or to the end of the body, and returns it as the commit message.
  #141, #149, #154 and #156 each *described* the mechanism in prose, so
  their bodies were parsed as though the prose were a conventional commit,
  and all four were dropped — including the pull requests whose whole
  purpose was documenting this. Writing about the marker broke the thing
  being written about, four times, and each failure was read as new evidence
  for the wrong theory.

Verified against the record rather than asserted: the pull request bodies of
#133 and #136 contain a real block and both had every line reach the 1.26.0
changelog; #140, #158, #160 and #161 contain no marker at all; #141, #149,
#154 and #156 contain a mention with no closing marker. Predicted outcome
matches actual in all ten.

**So the rule is:** put the block in the pull request body. Never write the
marker in a body except as a real block — when a body must discuss it, break
the word. The commit message is free to contain anything, because only the
body is searched.

The four "shapes" recorded earlier today were correlations with no mechanism
behind them, and the convention derived from them — a line of prose after
the closing marker — was noise. It happened to correlate because the commits
that had it were the ones whose *pull requests* carried real blocks. Both
entries above it stand as a record of being wrong, which is the point of an
append-only log, but this is the entry to act on.

Reading the tool took about ten minutes and I should have done it four
hypotheses ago. The rule that produced those hypotheses — "check the log, do
not predict" — was right and insufficient: the logs said which commits were
dropped, never why, and no amount of staring at outcomes recovers a
one-function mechanism that is sitting in a file on disk.

### 2026-09-28 — the index panel asked every entry for the configuration

Found while measuring the audit's M6, and introduced by me in 1.32. The
index report is read by a query with no `enabled` gate, mounted in the entry
header, so **every entry view captured the whole configuration tree** —
including every ordinary person. Measured against the harness: 115 to 175ms
per view on 389 Directory Server, a directory round trip for an answer that
is "this is not a backend" on all but one entry in the tree.

Worse on a session with no configuration identity, which is the ordinary way
to run Alder against a directory whose `cn=config` you cannot read: the
request answers 400 every time, and 1.29's console logging — put there so
the next person finds a failure in ten seconds rather than four minutes —
fills with expected refusals. A log that cries wolf stops being read, which
is the same fault in a different place.

The comment above the query said it was "asked for only once a screen is
open on a configuration entry". It was not. A comment that describes what
the code was meant to do is worse than no comment: it is the thing a reader
checks instead of the code, and I wrote it while writing the code that
contradicts it.

Gated on `inConfigTree`, the same reading the editor uses to decide whether
an entry is configuration at all. Verified in a browser: zero requests on
`uid=user0001`, one on `olcDatabase={1}mdb,cn=config`, panel unchanged.

**M6 itself is not explained and remains open.** The audit's proposed cause —
forty multi-kilobyte values rendered at once — does not survive measurement:
`cn={0}core,cn=schema,cn=config` is 38KB over the wire in 7ms, 79 values
averaging 145 bytes, and expanding `cn=schema,cn=config` in the tree is 781
bytes in 36ms. Nothing there wedges a browser for three minutes. A render
loop would fit the symptoms better — a blocked main thread explains reads,
clicks and navigation all failing at once — but I have not reproduced it, so
that is a hypothesis and is written as one.

### 2026-09-28 — a comparison that the directory has moved past

The audit's M2. A comparison whose source is "the directory now" stops being
true the moment anything is applied, and the panel said nothing: the auditor
came back from a successful apply to a result still offering the three
indexes it had just created, and only found out by pressing Compare again on
a hunch. Re-applying would have been the obvious next move.

- **The result is kept, not thrown away.** It is the only record of what
  *else* differed, which is exactly what somebody halfway through fixing a
  drift wants beside the changeset — the same reason the bench survives a
  trip to another screen at all. It gains a line saying it predates the
  change, and a Compare again button in that line.
- **Marked from two places, because there are two ways to apply.** The
  changeset's `removeApplied` is where every staged change lands, and the
  review dialog's success is where every single change does — an unlock, an
  index, an ordinary edit. The snapshots screen never hears about either
  otherwise.
- **Conservative on purpose.** Any write makes "the directory now" older
  than it looks, so any applied change marks any comparison. A data edit
  cannot really change a configuration comparison, and saying so anyway
  costs one button; the opposite mistake costs an operator a duplicate
  write.

Proved in a browser on the audit's own path: compare, leave, index an
attribute through the panel, come back — the banner is there with its
button, and the harness went back to twelve indexes afterwards.

### 2026-09-28 — the same drift, reported twice, in two voices

The audit's M5. A configuration comparison reports the objects that differ
and the settings that differ, and an object that is wholly added produces
both. The auditor read

    index / alderTeam / eq, sub / 1 setting / Alder can create this

near the top and, lower down, about the same drift:

    olcDbIndex / performance / alderTeam / Reported only
    compared as text: nothing here parses this value

Alder plainly did parse it: it split the value into an attribute and a
coverage list and wrote the change from it. The second row is the same fact
in a voice that contradicts the first, and it invites the reader to doubt
the actionable one.

The rule is narrow on purpose: a setting is folded away only when its object
is **wholly added or wholly removed**, because then the object row already
says what is happening and carries the change. An object that exists on both
sides keeps its setting rows — there they are the only place the particular
difference is legible, and hiding them would hide the answer.

Not silently shorter, either: the count beside the list says how many
settings belong to an object above. A number that drops without explanation
is a reader wondering what was kept from them.

Measured on a real replica-against-primary comparison rather than a fixture:
six of seventeen setting rows were duplicates — the ppolicy overlay's, not
an index at all, so the shape is general.

### 2026-09-28 — the verdict did not cover the attribute it was opened about

A UI audit opened the effective-rights verdict on a locked account to find
out who could clear the lock. The verdict listed sixty-one attributes and
`nsAccountLock` was not one of them. The audit's diagnosis was that the list
came from the object classes' permitted attributes; that was wrong, and the
wrong diagnosis is worth recording because it would have sent the fix into
the schema code.

The list is whatever the rights search asks the server about, and it asked
for `*`. In LDAP `*` is *user* attributes. An account lock is operational,
so 389 DS was never asked and correctly never answered. Asking for `+` as
well takes the answer from 61 attributes to 141, `nsAccountLock` among them.

One character of cause, and a second decision behind it: 141 rows in
alphabetical order starting at `aci` is not an answer a person can read, so
the attributes the entry **actually holds** are listed first. That costs
nothing to know — the rights response carries the entry as well as the
rights, so the same reply says which of the 141 are populated. It travels
as `present` on each attribute right, driver through API to the UI, because
the server is the one that knows and the browser must not guess by
re-reading the entry.

Proved on the harness rather than in a unit test. A unit test here asserts
against the answer this code invents; the assertion that matters is against
389 DS's own, and it was shown to fail when the `+` is taken back out.

### 2026-09-28 — "which of these accounts cannot log in", in one interaction

The same audit found the Users view was two hundred rows with no filter and
no account state, on the screen whose entire job is listing accounts.

Three choices worth recording.

The filter narrows **what has been fetched** and never re-runs the search.
A box that re-queried would be a different and slower feature, and it would
inherit the page limit anyway. What it must not do is let that go unsaid:
the count reads "1 of 200 entries", and an empty result says *"None of the
200 entries on this page matches …"* with the caveat about the limit, rather
than the view's ordinary "no users under this suffix". A filter that
silently searches less than the directory holds is how somebody concludes
an account does not exist.

The lock indicator is a badge beside the name, not the State column the
audit asked for. A column is off the right edge of a 1280px laptop once
four attributes are chosen, and "this account cannot log in" is not a fact
to put behind a horizontal scrollbar.

The badge is searchable as the word `locked`. The attribute values behind it
are `true` and a generalised timestamp, which nobody would think to type, so
without this the badge answers the question only for rows already on screen.
With it, the task the audit measured is: type six letters.

`looksLocked` moved from `features/policy` to `lib/locked` for this — a
component must not import a feature — and the lock attributes joined the
Users search's `alwaysFetch`, so the badge costs no extra request. Both
spellings, `pwdAccountLockedTime` and `nsAccountLock`, because a view does
not know which server it is looking at.

### 2026-09-28 — two of the audit's eight findings were not real

Checked before being fixed, which is the only order that works.

Finding 7 said eight policy rows printed a label and an attribute name with
nothing between them. `/policy` returns all twenty-one settings with their
values, and the rendered DOM reads `minimum length 8 pwdminlength`. It is
the same accessibility-tree artifact the audit itself had already documented
for six "unnamed" rows: the reader dropped the value node. No code change.

Finding 4 said the jump palette resolved a DN but would not run a search for
a name. Retried against the same harness, the palette's suggestion lands on
the search screen with `filter`, `base` and `scope` in the URL and the search
**runs** — "1 entry in 37ms". Either it was fixed between the run and now
without being recorded, or the audit saw a stale build.

Both are recorded in the report rather than deleted from it. A finding that
quietly disappears is indistinguishable from one that was never real, and
the next audit would find them again. The rule they produce is already in
that report's evidence section and is worth repeating here: a claim about
what is on screen is read off the DOM, not off the accessibility tree.

### 2026-09-28 — the fastest thing in the application, finally advertised

The last open finding of the 2026-09-27 audit. The jump palette answers
"open this DN" and "find this name" in one keystroke, and the auditor found
it **by guessing** — after clicking down the tree to reach an entry whose DN
was already on their clipboard. A keystroke with no affordance is a feature
only its author has.

It is a button shaped like the field it opens, not an icon. An icon teaches
nothing and has to be hovered to find out what it is; a box reading "Jump
to…" teaches the gesture, which is *type what you are looking for*. The key
is printed on it, because the point of advertising a shortcut is that the
second visit costs no click.

`⌘K` on Apple platforms and `Ctrl K` everywhere else, read from the
platform rather than picked as a house style: a hint naming the wrong key
teaches a gesture that does nothing, and the reader concludes the feature
is broken rather than the label. An unreadable platform falls through to
`Ctrl`, which is right everywhere except a Mac that reports nothing.

The palette's open state moved to the caller, because there are now two
ways in. The shortcut stays inside the palette — it belongs to the thing it
opens — and takes `open` in its dependency list, without which the button
and the keystroke disagree about what is open after the first toggle.

**A bug worth recording, because the tooling should have caught it and
cannot.** The lifted state went in below the two early returns that render
the connection screen and the loading spinner, so the hook count changed
between renders and React threw #310 on a session that was already
connected. `web/` has **no ESLint** — no config, no script, and CI runs only
`typecheck` and `test`. `react-hooks/rules-of-hooks` would have caught this
in a second. There is even an `// eslint-disable-next-line
react-hooks/exhaustive-deps` in `app.tsx` suppressing a rule that never
runs. Adding ESLint is a dependency decision and has not been taken.

### 2026-09-28 — the overview redesign, submitted twice

A pull request implementing an overview redesign was opened against a merge
base from the day before, and closed rather than merged: #145 had already
landed that redesign, and what remained of the diff against `main` was
mostly a revert of #154, which fixed faults found in it.

It re-added the hard-coded green dot labelled "Live directory session" —
deleting, to make room, the comment explaining why there is no status light
there. The dot reads nothing: not the health store, not the session. It
cannot say anything but "live", including while the header two inches away
says the directory is not answering. It also replaced the read-only versus
read-write line, which is the one thing this panel can honestly know from
what it holds.

Also in it: `Capabilities 3/4`, a score over four capabilities picked by
hand, on a page whose whole argument is that capabilities are branched on
individually and never ranked.

Three things from it were worth keeping and were taken: `flex-wrap` on the
hero, which does not wrap at narrow widths today; `screen-kicker` in place
of the same utilities written out longhand, the overview being the last
screen not using the shared class; and headings over the two groups of
cards, which is a real improvement on a page this long.

### 2026-09-29 — the schema definition that came back

A UI-only pull request failed the conformance suite. The diff touched
`web/` and `docs/` and nothing else; `main` had passed the same job forty
minutes earlier on byte-identical Go with digest-pinned images. So the
failure was worth understanding rather than re-running.

`TestOnePackageIsPromotedToBothServers` seeds one of its three changes on
the second server and expects to be told "one already satisfied, two
ready". It was told two were already satisfied. The definition it had not
seeded — `alderPackProofClass` — was in 389 DS's schema before the test
began, left by an earlier test whose cleanup had deleted it.

**The cleanup had worked.** That was the part that took the longest to
believe. Alder deleted the class, read the schema back on a live search,
and counted 1026 attribute types with the definition absent — correctly.
An `ldapsearch` against the same container moments later counted 1027 with
the definition present.

It is not a caching bug and not an Alder bug. Reproduced with no Alder in
the picture at all:

    delete the class with ldapmodify   -> present: 0
    wait sixty seconds                  -> present: 0
    add one unrelated entry             -> present: 1

**389 DS reconciles schema at the start of a replication session, and the
supplier adopts definitions its consumer has and it lacks.** Schema is
pushed supplier to consumer and deletions are not part of that push, so
the consumer keeps every definition any test ever created. The next write
to a replicated suffix — any write, by anything — hands them back to the
supplier. The harness has run four servers since replication went in, and
this has been latent ever since: whether a run passed depended on whether
a replicated write happened between one test's cleanup and the next test's
read.

The fix is `purgeSchemaDefinitions`, which deletes on the consumer as well
as the supplier and then **reads both back**. Four cleanup helpers, in
four files, now go through it.

Order is the trick, and the first version had it backwards. Deleting on
the consumer first leaves the supplier holding the definition, and a
session in that window hands it straight back — CI said so, in the words
the new check exists to produce: *"389ds-replica: objectClass … survived
its delete on cn=schema"*. Supplier first is stable, because the push
only ever adds: with the supplier clean there is nothing to restore the
consumer's copy and nothing to learn back. A session can still interleave
anywhere, so the purge retries until both sides read clean and gives up
loudly rather than looping.

Two smaller decisions inside it. A cleanup that cannot fail is a cleanup
nobody can trust, so anything other than "the definition is not there"
fails the test that leaked, at the point it leaked, instead of surfacing
three tests later as an inexplicable "already satisfied". And the server
that needs this is named by a capability, `learnsSchemaFromConsumer`, not
by vendor: OpenLDAP keeps schema in `cn=config`, which this harness does
not replicate, so it has nothing to learn back.

Proved both ways. With the consumer cleanup on, the suite is green and
both 389 DS instances hold none of the disposable definitions afterwards.
With it off, the suite fails and both hold all of them.

**Correction, same day.** The paragraph above says anything other than
"the definition is not there" fails the test. That was too strict and it
failed CI on the next pull request. 389 DS answers Unwilling To Perform
for a definition still in use, and in a replicated topology "in use" is a
moving target: between deleting the class on the supplier and deleting
the attribute type it names, a replication session can re-learn that
class from the consumer, and the attribute is in use again. The next pass
— class now gone from both sides, nothing left to learn it back from —
succeeds.

So the read is the arbiter and a refusal is only evidence. A pass that
ends with both sides clean has done its job whatever it was told along
the way; a definition still present after three passes fails the test,
and the last refusal is quoted in the message because it is usually the
reason. The rule the original paragraph was reaching for survives intact:
a cleanup still cannot pass while the state it was meant to remove is
still there.

### 2026-09-29 — ESLint in web/, and what it is allowed to say

Added on request, after its absence cost a blank page. A `useState` went
in below the two early returns that render the connection screen and the
loading spinner; the hook count changed between renders and React threw
#310 at anyone with a live session. `tsc` was happy with it, the unit
tests render components that never reach that path, and the browser was
the first thing to notice. The rule that catches it in a second —
`react-hooks/rules-of-hooks` — had no linter to run in. There was even an
`eslint-disable-next-line` in `app.tsx` suppressing a rule that did not
exist.

Four packages: `eslint`, `@eslint/js`, `typescript-eslint`,
`eslint-plugin-react-hooks`. Not `eslint-plugin-react-refresh`, which
guards a development-server convenience rather than anything that can
reach a user.

**The rules are named one at a time rather than spread from the plugin's
recommended set.** Version 7 of the hooks plugin also ships the React
Compiler rules, and they report fourteen further sites here — thirteen
`set-state-in-effect` and one `static-components`. Those are not noise
and are not dismissed: several are the kind of effect that can drive a
render loop, and a render loop is still an open question in this product,
because the wedge finding 3 of the 2026-09-27 audit describes has never
been reproduced. Reviewing fourteen effects is its own piece of work, and
doing it under a linter that fails the build meanwhile is how it would
get done badly. They are recorded here so the next session finds them
rather than rediscovers them.

Not type-aware linting either. The type-checked configurations want a
program per run, roughly doubling what CI spends on the frontend, to find
what `tsc --noEmit` already finds in a step that already exists.

`--max-warnings 0`, because a warning nobody has to fix is a warning that
accumulates until the number is the point and the findings are not. That
means the repository had to start at zero, and it did not quite: two
things needed settling first. `no-control-regex` fires on
`lib/display.ts`, where matching control characters is the entire purpose
of the module, so it carries an inline exemption with the reason written
next to it. And `exhaustive-deps` found a real one in `create-entry.tsx`
on day one: `must` and `may` were rebuilt with `?? []` on every render, so
the effect below them ran every render and the `useMemo` beneath memoised
nothing.

Style rules were not added. This repository has a voice, formatting
arguments are noise, and the value of a linter here is the handful of
rules that catch what neither the compiler nor the tests can.

Proved rather than assumed: with the original bug put back exactly where
it was, `tsc --noEmit` says nothing and `eslint` says *"React Hook
'useState' is called conditionally … Did you accidentally call a React
Hook after an early return?"*

### 2026-09-29 — the fourteen React Compiler findings, reviewed

The follow-up the ESLint entry above promised. The question behind it was
whether any of them explained the wedge in finding 3 of the 2026-09-27
audit, which has never been reproduced.

**They do not, and the reasoning is short enough to check.** A
`setState` in an effect can only loop if it changes something the effect
depends on. Three of the thirteen do, and all three are guarded:

- `changeset.tsx` sets `pendingCheck`, which is in its own dependency
  list — to `false`, behind `if (pendingCheck)`. One pass.
- `create-entry.tsx` sets `rdnAttr`, which is in its own dependency list
  — behind `if (rdnAttr === "")`, to `must[0]`, a required attribute's
  name. It would loop only if an attribute were named the empty string.
- `objects.tsx` sets `base`, which is in its own dependency list — behind
  `if (!namingContexts.includes(base))`, to the first naming context.
  That value is then in the list, so the guard is false next time. On a
  server publishing no naming context it sets `""` to `""`, and React
  stops at a state that has not changed.

The other ten set state that does not appear in their own dependencies,
so they cannot re-trigger themselves at all. **The wedge is still
unexplained**, and this rules out the most promising place it was not.

Two were worth fixing anyway, and both are real:

- `changeset.tsx` depended on the whole object `useMutation` returns,
  which is rebuilt on every render — so that effect ran on every render.
  The guard made it harmless, which is why nobody noticed, but a
  dependency list naming something rebuilt every time says nothing. It
  depends on `mutate` now, destructured, which is the form
  `exhaustive-deps` can see is stable.
- `tree.tsx` returned a new `Set` from its updater every time, so
  selecting an entry that was already revealed re-rendered the whole tree
  to arrive at the state it was already in. It returns the set it was
  given when there is nothing to add. That behaviour is an identity, which
  no rendered output shows, so the logic moved into `revealing()` where a
  test can assert it — and the test was shown to fail when the identity
  check is removed.

The remaining twelve stay as they are. Eleven are the same two patterns:
reset this dialog's fields when it opens, and copy a URL parameter into
the box that edits it. React would rather the first were a `key` and the
second were derived during render, and rewriting twelve working
components to satisfy that is a large change with real regression risk
and nothing a user would see. The twelfth, `static-components` in
`tree.tsx`, is **a false positive**: `iconFor` returns one of five
existing lucide components, and never creates one.

So the rules stay off, with this entry as the reason rather than an
omission. If they are ever turned on, the twelve above are the work, and
eleven of them are mechanical.
