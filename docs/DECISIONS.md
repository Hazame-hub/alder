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

### 2026-09-05 — where the paid line falls, decided before there is one

- **Count the humans.** A feature that helps one person operate one directory is
  free, permanently, with no host limit and no nag. A feature that exists
  because there is a second person, an auditor, or a second server is paid. The
  rule is stated here rather than discovered feature by feature, because the
  alternative is deciding it under revenue pressure and getting it wrong.
- **The whole console programme is free, without exception.** Object-aware
  views, the table, wizards, search as a destination, inline documentation, the
  landing page, membership tasks, empty and error states, boolean inputs, the
  schema tables, password scheme visibility. Each one helps a single
  administrator do their job, and that is the entire case against the
  twenty-year-old PHP tool. Charging for any of it means somebody tries Alder,
  hits a wall on an ordinary task, and goes back to what they had.
- **Every write path, both drivers, `cn=config`, schema editing, the LDIF
  preview and the Ansible export stay free too** — and so does every security
  feature, always. A security tier costs more trust than it earns.
- **Paid, by the same test:** SSO, RBAC within Alder, approval workflow on a
  changeset, an audit log with SIEM export, shared connection profiles,
  multi-server diff, Git and CI export, the AD and Entra drivers, and the
  support relationship — hardened builds, LTS, an SLA.
- **Staging sits exactly on the line, and the split is the verb.** Staging a
  changeset for yourself to apply later is one administrator, and free.
  Staging it for somebody else to approve is a second pair of eyes, and paid.
  So `ChangeRecord` and the basket stay a primitive with no reviewer, no
  assignee and no state in them; a review layer is something built above,
  never a flag threaded through.

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
