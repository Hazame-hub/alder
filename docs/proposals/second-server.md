# A second server inside one session

**Status: proposed, for review. Nothing here is implemented, and nothing here
is a decision yet.** Backlog item B6. The recommendation below needs a scope
line in section 2 of the charter and a decision that revises one made in 1.33;
both are recorded in `docs/DECISIONS.md` when, and if, the review accepts them,
and code waits until then. This follows the way the project format was
proposed (`docs/PROJECT.md`): a specification first.

---

## The problem

The comparisons Alder is best at are between two servers: this replica
against its primary, staging against production, the 389 DS instance being
migrated to against the OpenLDAP one it replaces. A session is one directory.
The session store keys one `directory.Session` by one cookie, and since
2026-10-12 connecting again closes the session the cookie named. So every
question that needs two servers needs either a file carried between two
connections, or a disconnect in the middle of the work.

The 2026-09-28 UI audit (`docs/ui-audits/2026-09-28-indexes-on-a-replica`)
measured that at forty-six interactions before the drift was on screen,
against an ideal of five. 1.33 removed most of that for one case, and only
one.

## What exists today

- **`POST /snapshots/capture/from` (1.33).** Captures another server's
  *configuration*, in one request, with credentials that live for that
  request only, through the same `--allowed-targets` check and the same
  `ConnConfig.Validate` as a connection. The result is a document in the
  browser's memory, placed on the A or B slot of the configuration bench.
- **`POST /diff` with a `live` side.** Compares a snapshot with the
  session's own directory. At most one side may be live, because the only
  live directory there is is the session's.
- **`GET /compare`.** Two entries, one server.
- **The Replication card.** Reads one server's own record of its
  replication, and tells the operator to "open this page against the other
  server and compare" -- which means a disconnect.
- **The command line.** `alder snapshot` and `alder diff` compose two
  servers already: each run is its own session, and a snapshot is a file.
  Nothing here changes it.

### Why that falls short

1.33 chose its limits deliberately, and they are the right limits for what
it is: one read of one tree. They are also exactly where the remaining work
is.

1. **Configuration only.** Schema drift between a primary and a replica,
   and "is this entry the same on both", are the next two questions an
   operator asks, and both go back to the forty-six-interaction path. 1.33
   said a schema or data capture of somewhere else would be "a bigger
   promise" and that nothing asked for it yet. The audits now ask.
2. **One shot, so every refresh is a retyped password.** Compare, apply the
   three changes the comparison proposes, compare again to see nothing is
   left: the second comparison needs the reference server captured again,
   and the dialog, correctly, kept nothing.
3. **Side by side is not possible at all** for anything that is not a
   snapshot: the Replication card, an entry, the schema browser.

## What any design has to keep

From the charter and from decisions already on the record:

- **Stateless** (section 2). Nothing on disk, nothing that survives a
  restart.
- **Credentials never persist** (section 7, rule 5): in this process's
  memory, keyed by the httpOnly, Secure, SameSite=Strict cookie. Not in a
  token, not in browser storage -- and, by the rule's spirit, not held in
  page JavaScript to be resent.
- **One write path** (section 7, rule 1, and section 6). Every write is a
  `ChangeRecord` through `Session.Apply`.
- **Which server a write goes to is never in doubt.** 1.33: "Alder applies
  changes to the session's own directory and nowhere else."
- **`--allowed-targets`** is checked on the server before anything is
  dialled, for every outward connection (1.33).
- **`--max-sessions`** bounds directory connections, because every one is
  a connection opened with no prior authentication (2026-10-12).
- **`--read-only`** refuses every write, whoever asks.

## Option A: a reference server attached to the session

The session gains at most one second connection, the **reference**. It is
opened with the same `ConnectRequest` as a session, through the same
`connConfigFrom`, allowlist check and `Validate`, and held in the same
in-memory `Session`, beside `Conn`, under the same cookie.

It is read-only by type, not by convention. The session holds it as an
interface with `Capabilities`, `Schema`, `Search` and `Read` and no `Apply`,
so there is no handler that could write to it by mistake: the compiler
refuses. Rule 1 is kept by construction, and "which server does this write
go to" has one answer because only one connection in the session can be
written to.

Reads that take a side gain a way to name it. `POST /diff` gets a
`reference` side beside `snapshot` and `live`, the snapshot captures take
`on: reference`, and the three read-only views worth seeing twice --
replication, one entry, the schema -- take `?on=reference`.

Lifetime: the reference never outlives its session. Disconnecting,
reconnecting, the idle timeout and the maximum lifetime all close both; the
reference can also be closed alone. Logged as its own `session opened` and
`session closed`, with `role=reference`, so an operator watching the log
can still account for every directory connection.

- **For:** small blast radius -- one field on `Session`, one sub-resource,
  one new side kind; every existing handler keeps reading `sess.Conn` and
  is unaffected. Write ambiguity is removed structurally. The credentials
  are where rule 5 already says credentials live.
- **Against:** it revises 1.33's "the credentials live for one request" for
  this path: a second server's bind password is now held for the life of a
  session. It also holds two connections per browser, which changes what
  `--max-sessions` counts.

## Option B: several sessions per cookie, with a selector

The cookie names a set of sessions; each request names which one it means,
by header or path, and the SPA has a server switcher.

- **For:** fully symmetric. Either server can be browsed, edited, compared.
- **Against:** every handler in `internal/api` becomes a question about
  which session it means, and a write is only unambiguous if every write
  request names its target and the UI never gets that wrong. A changeset
  staged against one server and applied with the selector on the other is
  the exact accident this product exists to prevent. It undoes the
  2026-10-12 rule that reconnecting replaces the session, TanStack Query
  keys gain a server dimension everywhere, and the session model -- a
  section 7 rule 5 sentence -- changes shape. It is the largest change and
  buys symmetry nobody has asked for: the audits ask to *compare*, not to
  edit two servers from one tab. A second browser profile already does
  that.

## Option C: a stateless compare taking two connection specs

Generalise 1.33: `POST /diff` accepts a `remote` side carrying a full
`ConnectRequest`, for every kind, and `/compare` and `/replication` grow
the same. Nothing is held between requests.

- **For:** no change to the session model or to `--max-sessions`; nothing
  outlives a request; 1.33's promise stands as written.
- **Against:** the password has to come from somewhere on every request.
  Either the operator retypes it for every refresh -- the shortfall in
  item 2 above, made general -- or the SPA keeps it in page memory to
  resend, which is a credential held in JavaScript, readable by any script
  on the page, and is what rule 5 exists to forbid in spirit if not in
  letter. A data capture of arbitrary size with someone else's credentials
  also runs inside one request's timeout every time it is refreshed.

## Recommendation: Option A, read-only, one reference

It is the only one of the three that keeps every rule in the list above
without a qualification on any of them, and the one change it makes to a
promise -- how long the reference credentials live -- moves them to where
the charter already says credentials live.

### API sketch

```yaml
/session/reference:
  get:
    operationId: getReference
    summary: Describe the reference server, if one is attached
    responses:
      "200": { SessionInfo, with readOnly always true }
      "401": not connected
      "404": no reference attached
  post:
    operationId: attachReference
    summary: Connect to a second, read-only server for comparison
    requestBody: ConnectRequest            # the same schema as POST /session
    responses:
      "201": SessionInfo                   # readOnly: true, role: reference
      "400": settings not usable           # connConfigFrom, Validate
      "401": not connected                 # a reference needs a session
      "403": target_not_allowed            # --allowed-targets, before dialling
      "409": a reference is already attached; detach it first
      "502": the directory refused the connection or the bind
      "503": sessions_full                 # --max-sessions
  delete:
    operationId: detachReference
    responses: { "204": closed, or there was none }

/diff:
  # DiffSide: exactly one of snapshot, live, reference.
  # live and reference together are allowed; reference on both sides is not.

/snapshots/capture:       # body gains  on: session | reference  (default session)
/compare:                 # query gains rightOn: session | reference
/replication:             # query gains on: session | reference
/schema:                  # query gains on: session | reference
```

`/snapshots/capture/from` stays as it is. It is still the right tool for a
one-off look at a server the operator does not want to hold open, and it
holds nothing.

**Proposals from a comparison go to the session's server, always.** A diff
whose live side is the session proposes changes, as it does today; a diff
with only a reference and a snapshot proposes none and says why, the same
way a comparison of two snapshots does. Nothing in a `/plan`,
`/changes/*` or `/changeset/*` body can name the reference, so there is no
request shape that writes to it.

### UI sketch

- **Header.** Beside the connection: `ldap-primary:636 as cn=admin` and, when
  attached, a second, quieter chip -- `reference: ldap-replica:636, read-only`
  -- with Detach. The chip is the only place to attach from, so the
  connection screen does not grow a second form.
- **Snapshots & drift.** The A and B slots each offer *this server*, *the
  reference*, *a file* or *capture another server* (1.33's dialog, unchanged).
  With a reference attached, the audit's ideal is reached: compare, tick,
  review, apply, compare again -- with no password typed after the first.
- **Entry.** "Compare with this entry on the reference" opens the existing
  comparison view, the right column labelled with the reference's host.
- **Replication and Schema.** A *Show the reference beside it* toggle, two
  columns, each headed by its host.
- **Every write dialog names its server.** `ChangeDialog` and the changeset
  today show the LDIF and not where it goes, because there was only one
  place. With two servers on the screen they say, above the LDIF, *Applies
  to ldap-primary:636*. That is a requirement of this proposal, not polish.
- Anything on screen read from the reference carries its host. Two columns
  that do not say which is which are a write to the wrong server waiting
  to happen, even when only one of them can be written.

### Security

- **Credentials.** Held in the same in-memory `Session`, cleared with it
  exactly as the primary's are (`remove` blanks `BindPassword`), never
  returned, never logged at any level -- the conformance suite's
  grep-the-log check from 1.33 is extended to cover attach.
- **The allowlist.** Checked on the server before dialling, the same call
  as `CreateSession` and 1.33 make, with a conformance case holding attach
  to it on both servers: otherwise attach is a way around it.
- **Outward connections need a session.** Like 1.33, attach requires one,
  so an unauthenticated caller cannot make this Alder dial hosts it names.
- **`--max-sessions` counts connections.** A reference takes a slot, is
  refused with the same 503 when the store is full, and is checked before
  dialling. The flag's help and `limit.go`'s comment already say "each is
  a connection to a directory"; the count becomes connections rather than
  map entries, so the ceiling on abuse is not halved by an attacker who
  attaches a reference to each session.
- **`--read-only`.** Changes nothing: the reference is read-only whether or
  not the instance is, and the session's own server is governed by the
  flag as today.
- **Sensitive values.** The reference is read through the same handlers, so
  rule 6 filtering, the `olcSyncrepl` credential stripping and the deny
  list apply to it unchanged.
- **The reference's own access rules.** Binding to a production server as
  an account that can read its configuration is the operator's choice, and
  the attach form says, as 1.33's does, what is kept and for how long,
  above the password field.

### Does it need a charter change?

**Section 2: yes, one line**, under "Added since": *a second, read-only
connection within a session, for comparison.* Scope grows only on the
record, and holding a second directory's credentials for the life of a
session is a new capability, not a refinement of drift comparison -- 1.33
said as much when it declined to do it.

**Section 7: no.** Rule 5 says credentials live in an in-memory session
keyed by the cookie; the reference's do. Rule 1 holds by construction.

**A decision revising 1.33** is needed alongside: its "the credentials live
for one request" stays true of `capture/from`, and stops being the only
model for a second server.

## Scope boundary

In: one reference per session; reading it through capture (all three
kinds), diff, entry comparison, replication and schema; attaching,
detaching, and its lifetime tied to the session's.

Out, and not scaffolded:

- Writing to the reference, by any path, including "apply this comparison
  to the other side". Swap the two instead, by connecting to it.
- Promoting the reference to the session ("swap"). It would make the
  writable server change under a screen full of state.
- More than one reference.
- Browsing the reference's tree, search, or the entry editor against it.
- Any change to the command line, which already composes two servers.
- Remembering the reference across a reconnect. Its non-secret settings
  may be remembered the way `recent-connection.ts` remembers the primary's;
  its password never is.

## Open questions for review

1. **Does `--max-sessions` count connections or cookies?** Recommendation:
   connections, with the default left at 256 and the help text saying so.
   A team's use is still far below it.
2. **The reference's lifetime.** Its own idle timer, or the session's?
   Recommendation: the session's activity keeps both alive, and the
   reference is also closed when the session's maximum lifetime ends. A
   reference that times out separately is one more state the header has to
   explain.
3. **Data snapshots of the reference.** The same bounds and paging as a
   capture of the session's own directory (rule 4), or tighter?
   Recommendation: the same; the bounds are already the reason a capture
   is safe.
4. **Does attach accept a separate configuration identity** (`configBindDn`),
   as a session does? Recommendation: yes; a configuration comparison is
   the main use, and on OpenLDAP that usually needs it.
5. **Is "Applies to host:port" in every write dialog worth shipping first,
   on its own?** Recommendation: yes. It costs little, it is true today, and
   it makes the rest of this safer to review.
