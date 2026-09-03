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
