# UI audit — "this account cannot log in; why?"

**Date:** 2026-09-27 · **Build:** `d768e19` (v1.26.0, released) ·
**Target:** `http://127.0.0.1:8895`, single binary, embedded SPA ·
**Directory:** the `test/compose` harness, 389 DS on `11636`, disposable data ·
**Viewport:** 1554×1022 · **Persona:** an infra engineer who owns this
directory, has read no docs, and has twenty minutes.

The lock was created outside Alder before the run — `nsAccountLock: true` on
`uid=user0007,ou=people,dc=alder,dc=test`, applied with `ldapmodify` — as a real
lock would be. The claim sheet is [`claims.md`](claims.md) beside this file: 31
capabilities across the seven surfaces shipped since the [first
audit](../2026-09-24-config-drift/report.md), none of which had ever been seen
in a browser.

The walkthrough ran in a subagent that was forbidden to read any frontend
source, which is the only way discoverability failures stay visible.

## Evidence, and what is missing from it

Every finding below is backed by the accessibility tree, the page text and the
network log at the step that produced it, quoted inline. **No screenshots**:
capture against the automation window times out in this environment.

**One reported finding did not survive verification, and it is recorded here
rather than quietly dropped.** The walkthrough reported that six rows of the
effective-rights table rendered a rights gloss with no attribute name, and
inferred from their alphabetical positions that they were `cn`, `l`, `o`, `ou`,
`sn`, `st`. The inference about *which* six was exactly right. The conclusion
was not: querying the API directly returns all 61 attributes with names intact —
`short names present: [cn l o ou sn st]; empty names: 0; duplicates: []`. The
blank rows are an artifact of the accessibility-tree reader, which does not
surface one- and two-character text nodes inside `<code>`. **Nothing is wrong
with that table.** Future audits in this environment should treat very short
rendered strings as unreadable rather than absent.

## Verdict

**Alder diagnoses this perfectly and then cannot act on it.**

The account's locked state is the first thing on the screen — a red badge in the
entry header, before anything is opened. One click on **Policy** names the cause
and the attribute in a single sentence: *"the account is administratively locked
(nsAccountLock)"*. That is a better answer than any directory tool this auditor
has used, and it arrives in two interactions.

Then it stops. The entry viewer files `nsAccountLock` under **"OPERATIONAL —
KEPT BY THE DIRECTORY, YOURS TO SET"**, and no editor in the product will touch
it. It is not in the editor's 89 offered attributes. It is not in the Password
dialog. It is not in the Policy dialog, which has two controls and both of them
say Close. The only way to clear the lock is to hand-write an LDIF `changetype:
modify` into the Import screen — which is `ldapmodify` with extra steps, for the
most common account-recovery operation there is.

**Forty-two interactions against an ideal of six.** Seventeen of them sit
between knowing the answer and applying it, and every one is spent leaving the
screen that knew.

Second, and independent of the task: **nine failed API calls produced zero words
on screen and zero in the console.** Six `POST /api/v1/search` 502s rendered as
*"Run a search to see results."* — as though the button had not been pressed.
The auditor spent four minutes believing they had mistyped a filter.

## Score

| | Actual | Ideal | Delta |
|---|---|---|---|
| Interactions to complete (connection excluded) | **42** | **6** | **+36 (7×)** |
| …discounting the session wedge | 27 | 6 | +21 |
| Distinct screens visited | 6 | 1 screen + 1 dialog | +4 |
| Dialogs opened | 5 | 1 | +4 |
| Backtracks | 5 | 0 | +5 |
| Dead ends | **4** | 0 | +4 |
| From "the policy says locked" to "the lock is gone" | **17** | 3 | +14 |
| Time to first meaningful action | ~12 min of a 20-min window | <1 min | — |
| Console errors | 0 | — | *zero is the problem* |
| Failed requests | **9**, all invisible in the interface | 0 | +9 |
| Interactive controls in the entry header | 14 (15 on the suffix) | — | 3 were used |

The four dead ends: the search screen died mid-task and said nothing; the editor
does not offer `nsAccountLock`; the Password dialog has no unlock; the first
browser tab wedged and could not be recovered.

## What works

- The entry header shows a red **"account locked"** badge before anything is
  opened — the fact you came for is the first thing on screen. The best thing in
  the product.
- The Policy dialog's first line names the cause *and* the attribute, and
  separately says the policy in force is **"the server's default"**, written at
  `cn=config`, so you know the account did not choose it.
- The review dialog shows LDIF and Ansible side by side, says **"touches
  nsAccountLock"**, and warns **"Recovery unavailable: the server maintains this
  attribute"** — a warning the auditor wanted and did not expect.
- Reconnecting remembers host, port, bind DN and CA and asks only for the
  password: one field, not eight.
- The Access dialog prints the raw `aci` whole beneath its parsed reading, and
  says in its own words that it reads rather than decides. *"I trusted it
  immediately, which is rare."*

## Findings

### 1 · Critical — the one change this task needs cannot be made by any editor

The viewer promises **"YOURS TO SET"** over `nsAccountLock: true`. The editor's
picker says *"Only attributes this entry's object classes permit are offered. 89
available"* — and `nsAccountLock` is not among the 89, because it is operational
and in no objectClass's `MAY` list. The product already knows the attribute is
special: the review dialog it built from the hand-typed LDIF said *"Recovery
unavailable: the server maintains this attribute (nsAccountLock)"*. The
knowledge is in the codebase; it is on no path an operator can click.

**Fix.** An **"Unlock this account"** button in the Policy dialog beside the line
that names the lock, mirrored as **"Unlock"** in the entry header while the
badge is showing. It builds the same `ChangeRecord` the Import path built and
opens the same review dialog. Policy → Unlock → Apply: **three interactions
instead of seventeen.** Separately, admit `nsAccountLock` and the OpenLDAP
ppolicy equivalents into the editor as operational-but-writable, so the viewer's
"yours to set" stops being a lie.

### 2 · Critical — nine failed requests, no words anywhere

`POST /api/v1/search` 502 ×6, `GET /api/v1/entry` 502 ×2, `DELETE
/api/v1/session` 503 ×1. The interface showed: *"Run a search to see results."*;
Users frozen on *"Searching dc=alder,dc=test"* with no timeout; Overview frozen
on *"Reading it…"*; the header still reporting *"Bound as cn=Directory Manager"*
and *"Certificate verified"*. `read_console_messages` returned nothing at all.
The failed disconnect rendered as a clean one.

This contradicts the product's own posture everywhere else — *"could not be
read, so this is not the whole answer"* is exactly the right sentence, and it is
simply not wired to HTTP failures.

**Fix.** One shared error surface on every data-fetching view: on any non-2xx
from `/api/v1/*`, replace the placeholder or spinner with a bordered note
carrying the route, the status and the server's message — the treatment the
Access dialog already gives an unreadable tree. Every spinner gets a timeout
that resolves to that note instead of spinning. And `console.error`
unconditionally, so the next person finds it in ten seconds rather than four
minutes.

### 3 · Major — the session wedged while reporting itself healthy

From #20 to #25 every LDAP-backed call 502'd while `GET /api/v1/session` kept
returning 200 and the Overview kept claiming a healthy bind. `GET /api/v1/tree`
kept returning 200 throughout while `GET /api/v1/entry` 502'd. Disconnect and
reconnect cured it completely — the identical filter returned *"1 entry in
2ms"*. **The trigger was not reproduced**; two post-task probes failed to
recreate it. Symptom and evidence only, not a diagnosis.

**Fix.** (a) Mark the session degraded in the header on a transport-level
failure, with a **Reconnect** button that reuses the remembered host and DN. (b)
Re-dial the LDAP connection on a transport failure before returning 502, so the
common case self-heals.

### 4 · Major — the jump palette resolves a DN but not a name

`Ctrl+K` with `user0007` offers exactly one thing: *"Search for user0007"*.
Clicking it lands on the Search screen with the filter pre-filled **and does not
run it**. `Ctrl+K` with the full DN does offer *"Open uid=user0007"* and works.
Nobody arrives at 2am holding a DN; they hold a uid from a ticket.

**Fix.** Run the search inside the palette and list matching entries with their
DNs. Failing that, a screen navigated to with a filter in its URL must run it on
arrival — making the operator press the button is a bug wearing a design
decision.

### 5 · Major — the effective-rights verdict omits the attribute in question

The verdict lists 61 attributes, eight at a time behind *"Show all 61
attributes"*. `nsAccountLock` — named by the sibling dialog as the cause — is not
among them, because the list is the object classes' permitted attributes rather
than the attributes the entry holds. There is no "not covered" line; just
absence, which reads as "no such attribute".

**Fix.** Order the verdict by the entry's own populated attributes first, the
rest below the fold, and include operational attributes the entry actually holds.
On a locked account `nsAccountLock` would then be the second or third row.

### 6 · Minor — the Users view is 200 unfiltered rows with no lock column

*"200 entries in 80ms / stopped at 200 — there are more than this"*, with no text
filter of any kind, and no column for the one piece of account state the entry
header considers important enough to show unconditionally.

**Fix.** A filter box that narrows the already-fetched rows as you type, and a
State column carrying the locked badge — so the view that lists accounts can
answer "which accounts are locked?".

### 7 · Minor — policy rows print a label and an attribute with nothing between

Eight of twenty-one rows — `pwdMinLength`, `pwdMinAge`, `pwdInHistory`,
`pwdMaxFailure`, `pwdGraceAuthNLimit` and four `passwordMin*` — render the label
and the attribute name with an empty value position. On a dialog whose job is to
say what the server records, an empty cell is the one thing it must not print:
absent, zero and unread are three different facts.

**Fix.** Render `not set` in muted text where the server holds no value, and say
so with the Access dialog's treatment where Alder could not read one.

### 8 · Polish — Ctrl+K is the fastest thing here and nothing advertises it

Found by guessing. No search icon, no nav item, no hint in the header.

**Fix.** A search affordance in the header showing `⌘K` / `Ctrl K`.

## Claim-sheet verdicts

Of the 31 claims, this task went near 15.

| Verdict | Claims |
|---|---|
| **Works cleanly** | 1, 2, 3, 5, 7, 12, 14, 15 — the access rules and their reading, the policy in force and where it is written, the locked badge, glossed durations |
| **Works but painful** | 6 (verdict omits the attribute in question, 53 of 61 rows behind a click), 13 (one state row and no way to tell absent from unread) |
| **Present, not exercised** | 9 — the "ask about another identity" row is there and was read; no query was submitted |
| **Not found** | none |
| **Not covered** | 4, 8, 10, 11, 16–31 — OpenLDAP entirely, refusal remedies, indexes, replication, `cn=config` credential redaction |

Nothing on the sheet turned out to be shipped-and-invisible, which is the result
that matters most: the previous audit's lesson was that features can ship
without ever being reachable, and none of these had.

## Not covered

- **OpenLDAP entirely.** Claims 4, 8 and the second-identity path for
  `olcAccess` are untested.
- **Claims 16–31** — refusal remedies, indexes, replication, conflicts,
  credential redaction. They belong to the other three tasks on the sheet.
- **The session-wedge trigger.** Symptom, network evidence and cure recorded;
  cause unknown.
- **Header wrap behaviour at 1440×900 / 1280×800 / 1024×768** (sheet task 4).
  Control counts only — 14 on a user entry, 15 on the suffix — not layout.
