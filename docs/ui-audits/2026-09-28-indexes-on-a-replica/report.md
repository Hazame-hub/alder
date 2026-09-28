# UI audit — "the replica's indexes do not match the primary"

**Date:** 2026-09-28 · **Build:** `e9a28a5` (v1.30.0, released) ·
**Target:** `http://127.0.0.1:8897`, single binary, embedded SPA ·
**Directory:** the `test/compose` harness, **OpenLDAP** on both ends
(primary `10636`, replica `20636`), disposable data ·
**Viewport:** ~1554×1022 · **Persona:** an infra engineer who owns this
directory, has read no docs, and has twenty minutes.

The claim sheet is [`claims.md`](claims.md). This is task 3 of the four on the
[first sheet](../2026-09-27-since-the-first-audit/claims.md), and the first
audit of any kind against OpenLDAP — the 2026-09-27 run was 389 DS only.

The walkthrough ran in a subagent forbidden to read any frontend or handler
source, which is the only way discoverability failures stay visible. Root
causes were attached afterwards, by me, from the source.

## Evidence, and one error in the setup

Every finding below is backed by page text, the accessibility tree or the
network log at the step that produced it, quoted inline. **No screenshots**:
capture against the automation window times out in this environment.

**The claim sheet was wrong on one row, and it was my error.** I wrote that
`l,st eq` was an index the replica had and the primary lacked, and built
claims 7 and 8 — index removal, and the `index_value_shared` refusal — on
top of it. It is on both servers. The comparison correctly reported no index
removals, so neither claim was exercised, and the auditor refused to pretend
otherwise. That is the right call and it is recorded here rather than
quietly dropped: **removal is still unaudited, two audits running.**

## Verdict

**Alder makes the change honestly and cannot get you the thing to compare
against.**

Adding the three missing indexes worked exactly as the product promises: tick
three rows, review the LDIF Alder derived for OpenLDAP itself, apply, and the
result is verifiable on screen two different ways. From the comparison to the
applied change is **six interactions**, and the write path is the best thing
in the product.

Getting to that comparison took **forty-six**. There is no way to look at a
second server from inside a session, so "compare this server against the
standard" means: connect to the primary, capture, notice a small Download
button, save a file, disconnect, connect to the replica, upload the file.
Miss the Download and you lose the capture — the auditor did, because nothing
warns you, and spent eighteen interactions finding out.

**Fifty-four interactions against an ideal of eleven.** Forty-four of them
happen before the operator sees the drift they came for.

## Score

| | Actual | Ideal | Delta |
|---|---|---|---|
| Interactions to complete | **54** | 11 | **+43 (5×)** |
| …of those, after the drift was on screen | 8 | 6 | +2 |
| …before it | **46** | 5 | +41 |
| Distinct screens visited | 7 + 2 dialogs | 2 | +5 |
| Backtracks | **10** | 0 | +10 |
| Dead ends | **3** hard, 1 soft | 0 | +4 |
| Time to first meaningful action | 46 interactions | 5 | — |
| Failed requests | `DELETE /session` → **503**, three times, silent | 0 | +3 |
| Console errors | 2, both deliberate, both surfaced correctly | — | fine |

The ideal: connect → Snapshots & drift → "capture from another server" →
Compare → tick three → Review → Apply.

## What works

- **The write path is honest end to end.** Plan first — *"3 changes examined
  / 3 modifications / Nothing has been applied."* — then the exact LDIF with
  *"This is exactly what applying will send, record by record"*, then
  *"Recovery unavailable: schema and configuration changes are not
  recovered"* rather than a fake undo.
- **Snapshots are tamper-evident.** A hand-edited capture was caught twice,
  first on counts, then on *"the file was corrupted or edited after
  capture"*. That is right for a document you will diff a production server
  against.
- **The config editor has three mutability states, not two** — "Alder changes
  this" / "not changed by Alder" / "not in Alder's model" — and `olcDbIndex`
  is in the right one.
- **The 2026-09-27 finding about the comparison is fixed:** "Review 3
  changes" no longer throws the comparison away.
- **1.30's access and schema work reads correctly on a configuration entry:**
  `olcAccess` and `olcAttributeTypes` both under *"Shown here, edited
  elsewhere"* with the sentence saying why.

## Findings

### 1 · Critical — Export writes the cleartext root password to a file, and the screen next to it tells you to

The entry viewer withholds the secret properly, then says: *"Sensitive
attributes are omitted; **use Export if you need them**."* Ticking "Include
sensitive attributes" in the Export dialog produced a file containing
`olcRootPW: alder-admin` — a value the viewer had itself labelled *"stored in
the clear"* — and the whole `olcSyncrepl` line including `credentials="…"`.

This contradicts a promise made in writing four days ago.
`docs/COMPATIBILITY.md` says of the 1.26 fix: *"The values can still be set;
they can no longer be read back."* They can.

**Root cause.** `internal/api/handlers.go:1371` — `includeSensitive=true`
switches the record renderer to `directory.EntryLDIFWithSecrets(e)`, which
releases everything in `schema.sensitiveAttrs`. That set was written for
password *hashes*, where an export you can restore from is a legitimate
need. The 1.26 security fix added the configuration secrets to the same set,
and so handed them to the same flag. A hash and a cleartext bind password are
not the same category of thing.

**Fix.** Split the set. `includeSensitive` continues to release hashes
(`userPassword`, `unicodePwd`); it never releases a value that is a usable
credential in the clear — `olcRootPW` when unhashed, `olcSyncrepl`,
`olcDbCryptKey`, `nsDS5ReplicaBindCredentials`, `nsMultiplexorCredentials`,
`nsslapd-keyPassword`. The export writes a comment naming what it withheld
and why. And delete *"use Export if you need them"* from the viewer: it is an
invitation to do the thing the fix exists to prevent.

### 2 · Critical — a capture is destroyed by disconnecting, which makes comparing two servers a file round-trip

Capture the primary, disconnect, connect to the replica: **"Snapshot A is
empty."** No warning at disconnect, no mention that the bench will be
cleared, nothing. The auditor lost the capture, hunted through Packages for
another route, and went back to the primary to do it again — eighteen
interactions, all of them waste.

**Root cause, and it is deliberate.** `web/src/app.tsx`, in `disconnect()`:
`bench.clear()`, with the comment *"Snapshot documents describe the server
just left, and the next session in this tab may be a different operator on a
different directory."* That reasoning is sound and the cost was not weighed:
a snapshot holds no credentials — secrets are withheld at capture — and
comparing one server against another is the whole point of the feature.

**Fix.** Keep the bench across a disconnect, and label each document with
where it came from (finding 4). If it must be cleared, the Disconnect button
asks first and names what will be lost. Saves ~18 interactions on the first
two-server comparison anybody attempts.

### 3 · Critical — you cannot disconnect while Alder is busy, and the screen does not say so

Five clicks on Disconnect did nothing visible. The network log:
`DELETE /api/v1/session → 503`, three times. A reload then showed the
connection screen — the session *had* gone. So the interface first implied
the disconnect had failed, then silently revealed it had not.

**Root cause, two of them.** `internal/api/limit.go` puts every route behind
the in-flight gate, including `DELETE /session` — which touches no directory
at all. It is an in-memory map delete queued behind the limiter that exists
to protect the directory, so exactly when Alder is busy or the directory is
slow, you cannot let go of it. Second, `web/src/app.tsx` calls
`api.DELETE("/session")` bare, outside the query and mutation caches, so the
`onError` handler added in 1.29 — *"Every failure is written down, once, in
one place"* — never sees it. The one place has a hole, and this is what fell
through it.

**Fix.** Exempt the routes that ask the directory nothing (`/session` DELETE
and GET, `/source`) from the gate. Route the disconnect through the mutation
cache so its failure reaches the badge and the console like every other. Make
`DELETE /session` idempotent so a torn-down connection still answers 204.

### 4 · Major — a snapshot never records which server it came from

Every card read *"server not identified"*. A file called
`alder-config-snapshot-20260928T061525Z.json` was uploaded into a session
bound to the replica and nothing on screen said it was a capture of the
primary.

The mistake this invites is the one that ruins the task: comparing a server
against a capture of itself, or against last week's other cluster, and seeing
"no drift". You would not find out until you applied the wrong thing.

**Fix.** Stamp host, port and bind DN into the capture; print it on the card
— *"from 127.0.0.1:10636, as cn=admin,cn=config"* — and say so loudly when
the loaded snapshot's origin matches the server you are connected to.

### 5 · Major — creating an index is reachable only by comparing against a server that already has one

The config entry editor marks `olcDbIndex` "not changed by Alder" and offers
no way in. The Directory screen has no index action. The only surface that
creates an index is a row in a drift comparison — so the feature is really
"copy an index another server already has", and the ordinary reason to add
one (a slow search you just diagnosed) has no path at all.

**Fix.** An "Indexes" panel on the `olcDatabase={1}mdb` entry, beside
Access / Policy / Replication: the list Alder already parses, with Add and
Remove producing the same `ChangeRecord` the comparison produces. The same
code behind a discoverable door.

### 6 · Major — the comparison goes stale after you apply and says nothing

Returning to Snapshots after a successful apply, the panel still read *"11
added … 3 removed"* with the three indexes still offered as creatable. Only
pressing Compare again told the truth. An operator re-ticks and re-applies,
or reports the drift as unfixed.

**Fix.** Mark the panel stale when a change is applied from it — *"Applied 3
changes; this comparison predates them. Compare again"* — with the button in
the message.

### 7 · Major — expanding `cn=schema,cn=config` wedged the tab for over three minutes

No reads, no clicks, no recovery by navigation; the server answered `curl` in
2.9 ms throughout, so this is the SPA. It did not reproduce on a second
attempt, so it is intermittent — but the configuration tree is where this
whole feature lives, and losing the tab loses the changeset and the bench
with it.

**Fix.** Virtualise the children list, and render `cn={0}core`'s ~40
multi-kilobyte `olcAttributeTypes` values behind a disclosure rather than all
at once.

### 8 · Major — the same drift is reported twice, and the second one contradicts the first

Near the top: *"index / alderTeam / eq, sub / Alder can create this"*. Lower
down, the same fact again: *"olcDbIndex / performance / alderTeam / Reported
only / compared as text: nothing here parses this value"*. Alder plainly did
parse it — it split the value into an attribute and a coverage list and wrote
the LDIF from it. With 21 setting rows in this comparison, this is the main
source of noise, and it invites the reader to doubt the actionable row.

**Fix.** Fold a setting that is fully accounted for by a resource row into
that row as its detail; at minimum, suppress "nothing here parses this value"
for settings whose resource *is* parsed.

### 9 · Minor — three smaller ones

- **Switching servers costs eight interactions** (Disconnect, port, two
  passwords, Connect, renavigate). A saved-connections list in the header
  that asks only for the passwords would make it two.
- **The change label lowercases the attribute**: *"create index
  dc=alder,dc=test/alderteam"* while the LDIF correctly says `alderTeam
  eq,sub`. Showing the wrong case in the thing you are asked to confirm is
  needless doubt. Display the definition's spelling; keep the folded form as
  the key.
- **A snapshot that failed its checksum stays in the slot**, distinguishable
  from a good one only by the *absence* of a "checksum verified" chip, with
  the error far down the page. Mark the card rejected, or refuse the load.

### 10 · Polish

- The ppolicy overlay sits in the same tick-list as the indexes, also marked
  "Alder can create this". One stray tick installs an overlay while you
  thought you were fixing indexes. Group the rows by kind and let the action
  bar say *"Review 3 changes: 3 indexes"*.
- *"2 withheld"* and *"9 machine details"* are unexplained chips on the
  snapshot card.

## Claim-sheet verdicts

Of the 16 claims, this task went near 13.

| Verdict | Claims |
|---|---|
| **Works cleanly** | 2, 3, 5, 6, 9, 10, 13, 14, 16 — capture, compare, create an index, the review path, the corrected intro text, the three mutability states, and 1.30's access and schema handling |
| **Works but painful** | 1 (reconnect remembers everything but the passwords, and silently bins your captures), 4 (coverage is visible; the backend/suffix is only in an accessible name, and every index is listed twice) |
| **Broken** | 12 — the export releases cleartext credentials |
| **Not found** | 11 — Preflight wants an artifact the UI gives you no way to author for an index |
| **Not covered** | 7, 8 (removal — the sheet's premise was wrong), 15 (nothing took four seconds) |

Claim 9 is worth naming: the intro that had been wrong since 1.23 now reads
*"Alder creates and removes two kinds: an OpenLDAP overlay whose module the
server has already loaded, and an index, on either server."* It was fixed
between the two audits.

## Not covered

- **Index removal, and the `index_value_shared` refusal** (claims 7, 8) —
  the drift the sheet described did not exist, so no removal row was ever
  produced in the browser.

  Not to be read as "untested", which is how I first read it myself.
  `test/conformance/configindex_test.go` creates an index through the HTTP
  plan-and-apply path on both servers, removes it again, and checks the
  configuration checksum comes back to where it started; the two refusals
  have a case each, and each runs on the server whose concept it is —
  `index_value_shared` against OpenLDAP's `olcDbIndex: l,st eq`,
  `system_index` against 389 DS. What went unexercised is the path through
  the interface, which calls those same endpoints.
- **Preflight against an index change** (claim 11) — no way to author the
  artifact it wants.
- **The "still waiting" badge** (claim 15) — nothing ran long enough.
- **The Ansible tab** on the changeset — seen, not opened.
- **389 DS** — this run was OpenLDAP at both ends, deliberately.
- **Screenshots** — capture times out in this environment; all evidence is
  page text, the accessibility tree and the network log.
