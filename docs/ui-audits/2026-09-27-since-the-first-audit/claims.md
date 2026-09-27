# Claim sheet: everything shipped since the first audit

For a black-box UI audit, per the `sybil-ui-audit` skill's Phase 1. Built
2026-09-27 from `api/openapi.yaml`, the README, `docs/*.md` and the route list
in `web/src/app.tsx` — **not** from the frontend source, which the auditor must
not read.

The [first audit](../2026-09-24-config-drift/report.md) covered eleven of sixteen
claims against one task. Seven surfaces have shipped since — access control read
(1.19), effective rights (1.20), password policy (1.21), refusal remedies (1.22),
indexes (1.23), replication visibility (1.25/1.26) and asking about another
identity (1.27) — every one of them on code review alone. That audit's own
conclusion was that its findings were invisible from the code.

**Anything on this sheet that the auditor cannot find in the interface is a
finding in itself.** Shipped and invisible is the same as not shipped.

---

## Tasks worth walking

Pick one. Each crosses feature boundaries, which is where the first audit found
all its friction. The ideal path is stated in advance, by somebody who knows what
the product can do; the delta against the actual walk is the headline of the
report.

### 1. "This account cannot log in. Why?" — svc-alder (or the seeded locked account) is being refused by the directory; find out what is stopping it and put it right.

**Reaches:** Connect screen, including the optional second identity for the configuration tree; Directory → tree / Users object view / jump palette (finding the account); Entry viewer header — the 'account locked' badge read from attributes already in hand; Password policy dialog (1.21): which policy applies, where it is written, what the server records — locked since, last changed, failed binds, expiry; Access dialog (1.19): the aci / olcAccess rules that bear on the entry, in the server's order; Effective rights (1.20): the directory's own per-attribute verdict, on 389 DS; the honest note in its place on OpenLDAP; Ask about another identity (1.27): the as= field, the DN picker, and the return-to-me control; Entry editor + ChangeDialog: removing the lock attribute or setting a password; Refusal remedies (1.22): if the unlock is refused, the error note's jump to the rule or the policy that explains it; Changeset, if the unlock is staged rather than applied from the dialog

**Ideal path.** Counting one click or one field entry as one interaction, connection excluded: (1) type the account into the jump palette, (2) pick it, (3) open one account-health answer that gathers the policy in force, what the server records about the account, and the server's own verdict on what may be written here, (4) act on what it names — clear the lock, (5) review the LDIF, (6) apply. **Six interactions, one screen and one dialog.** Alder has all six facts and spreads them over three sibling dialogs that must be opened and closed one at a time, with no action on any of them, so the walk should record: interactions to reach each of the three, whether anything says the three are related, how many interactions from 'the policy says locked' to 'the lock is gone', and whether the refusal (if any) carries a remedy button that lands on the right screen.

**Why this one.** It is the single task that crosses five of the seven surfaces shipped since the last audit (1.19, 1.20, 1.21, 1.22, 1.27), it is the question the decision log says those releases were written to answer, and it is the one an operator has at 2am. It is also the only walk that puts the read-only views next to a write, which is where the last audit found all its friction — the screen that knows the answer could not act on it. On OpenLDAP it additionally exercises the second identity (olcAccess is unreadable without it) and the 'this tree could not be read' path; run it on both servers, since 389 DS is the only one that produces an effective-rights verdict and therefore the only one that shows the as= control at all.

### 2. "I changed this entry on the primary ten minutes ago. Has it reached the replica?"

**Reaches:** Entry viewer + editor + ChangeDialog on the supplier (the write itself); Entry replication dialog (1.26): entryCSN / modifyTimestamp, the server that made the change, the entry's stable identity, whether the server marked it conflicting; Overview → Replication card (1.25): role, links, per-origin cursor, the 'open this page against the other server and compare' sentence; Conflicts search (1.26) below the suffix, and the 'an empty list from OpenLDAP is not good news' sentence; Disconnect / Connect — Alder holds one session per tab, so the second server needs a second connection, and disconnect clears the snapshot bench; The URL (view, dn) as the only thing that carries between the two sessions

**Ideal path.** From the entry open on the supplier: (1) Edit, (2) change the field, (3) Review, (4) Apply — four interactions for the write. Then for the comparison, given the standing decision that Alder never contacts a peer: (5) open the entry's replication, (6) 'open this entry on the other server', (7) type the bind password, (8) Connect, (9) the same panel is already on the same entry. **Nine interactions total, one of them a second connection, and zero retyping of the DN.** The walk must record what the actual count is, how the DN gets to the second session (retyped by hand, pasted URL, or carried), how many interactions are spent getting back to the same entry after a disconnect, and whether the two change-sequence values can ever be on the screen at the same time — the product's own instruction is 'compare', and comparing two numbers that cannot be seen together is the thing to count.

**Why this one.** It is the maintainer's own candidate and it is the only task that tests the seam the 1.24 harness was built for: two servers, one single-session tool. It crosses the write path and both replication views, and its headline number — interactions to see two numbers that must be compared — cannot be obtained from code review. It also puts the 1.27 fix list under a black-box eye: opening olcDatabase={1}mdb,cn=config in the entry viewer and in the LDIF export on the way past is how an auditor confirms, without reading Go, that olcRootPW, olcSyncrepl and olcDbCryptKey no longer come back in the clear.

### 3. "Our standard says mail and givenName must be indexed on every server. This one is not. Fix it — and tell me whether Alder can do that, or whether I am back in slapd.conf."

**Reaches:** Connect screen, second identity for cn=config; Snapshots & drift → capture a configuration snapshot (1.13) and load the standard as a document; Configuration comparison → the settings list and the 'Configuration objects' block (1.16, 1.23); Index object rows: the backend/attribute name, what the index covers (eq/sub/pres), 'Alder can create this' / 'Alder can remove this'; Refusals with words: index_value_shared, system_index (1.23); ReviewActions → ChangeDialog → plan → apply, or the changeset for several at once; Preflight (1.23 sweep): whether it now says an index is a change Alder makes rather than manual work; The entry editor on the OpenLDAP database entry, where olcDbIndex should read 'not changed by Alder', not 'not in Alder's model' (1.18 + 1.23)

**Ideal path.** Counting from a connected session with the standard document on disk: (1) Snapshots & drift, (2) load the document into a slot, (3) Compare, (4) tick the two index objects, (5) Review N changes, (6) Apply. **Six interactions, one screen, no screen change** — which is exactly what the previous audit's findings 1 and 2 were fixed to make possible, so this walk is also the regression test for that fix. Record: whether the objects block is even found (its own copy says Alder creates 'one kind: an OpenLDAP overlay whose module the server has already loaded', which has been wrong since 1.23), whether the index rows say what they cover without a click, how a refused removal reads, and whether anything anywhere tells the operator that neither server reindexes on its own.

**Why this one.** 1.23 is the only shipped capability on this sheet that creates something on both servers, and it is the one that has never been seen in a browser — it went in on code review, and the sweep recorded in the decision log found it had been added to the comparison and nowhere else. It is also the task that re-walks the two findings the last audit called its headline, so the delta against the old 11-interaction count is directly comparable. The stale sentence in the objects block is a live prediction: if a black-box auditor reads it and concludes Alder cannot create an index on 389 DS, that is a finding produced by a walk and by nothing else.

### 4. "Just change this person's phone number." The smallest possible write, used as an instrument: count what the entry header puts between an operator and Edit.

**Reaches:** Entry viewer header — LDIF, Compare, Members (conditional), Referenced by (conditional), Access, Policy, Replication, Export, Membership actions, Delete subtree (conditional), Child, Copy, Password (conditional), Rename, Delete, Edit; The three diagnostics added in three consecutive releases (Access 1.19, Policy 1.21, Replication 1.26), all rendered unconditionally on every entry; Entry editor + ChangeDialog + apply; The same header on an entry inside cn=config, where some actions are suppressed

**Ideal path.** (1) Edit, (2) change the field, (3) Review 1 change, (4) Apply. **Four interactions, no screen change.** This task is deliberately trivial so that everything above four is measurement rather than opinion. The walk must return numbers, not adjectives: the count of interactive controls in the header on a plain user entry, on a group, and on a cn=config entry; how many of those are rendered regardless of whether they can say anything (Access and Replication are offered on every entry; Policy is offered on entries that are not accounts); how many rows the header wraps to at 1440×900, 1280×800 and 1024×768; the horizontal distance and ordinal position of Edit in each; and whether any control disappears below the fold. Then, for each of the three diagnostics, open it once on an entry where it has nothing to report and record the sentence it returns — a button that always returns 'nothing here' is a different cost from one that does not.

**Why this one.** The hypothesis was raised and never tested, and it is precisely the kind of claim that dissolves into taste unless a walk produces counts. Three releases each added one unconditional button to the same strip, which is the classic way a header degrades: no single release is wrong. Framing it around the most ordinary write in the product means the number produced — controls offered versus controls used — is directly interpretable, and the wrap-row measurement at three widths is evidence a code reviewer cannot obtain at all. Last of the four because it is the cheapest to run and the easiest to fold into the other three walks if time runs short.

---

## The claim sheet

| # | Claimed capability | Where it should live | Since |
|---|---|---|---|
| 1 | See the access control rules the server holds that bear on this entry, in the server's own order and its own words | Directory → select an entry → the "Access" button in the entry header → "Access control" dialog. Also opened by a refusal's remedy button. | 1.19 |
| 2 | Tell a rule that definitely bears on this entry from one that only may, and see the count of each | Access dialog — the header line "N rules, M bearing on this entry", and a badge on every rule row reading "bears on this entry" / "may bear on this entry" / "elsewhere" | 1.19 |
| 3 | See a rule Alder could only half-read printed whole, rather than a confident summary of the half it understood | Access dialog — rule rows carry the raw value as the server holds it; a regex target, set spec or unknown syntax shows the raw value with no parsed structure | 1.19 |
| 4 | Be told when a place where rules live could not be read, instead of being shown an empty list | Access dialog — a bordered note naming the tree and the reason ("<dn> could not be read, so this is not the whole answer — …"). Reproduce by connecting to OpenLDAP without the optional configuration identity. | 1.19 |
| 5 | Be told, every time, that Alder is reading the rules and not deciding what is allowed | Access dialog — a tinted warning paragraph, from one shared constant so the API, the UI and the docs cannot drift apart | 1.19 |
| 6 | See the directory's own verdict on what may be done to this entry and each of its attributes, above the rule text, marked as the server's answer rather than Alder's | Access dialog — the "Verdict" block above the rules list. Present on 389 DS (11636/21636), which publishes Get Effective Rights; absent on OpenLDAP. | 1.20 |
| 7 | See the raw rights letters the server sent (v, rsc, none, and any letter Alder has never seen) beside the plain-language gloss | Access dialog → verdict block, per attribute | 1.20 |
| 8 | Tell apart three different silences — a server that cannot answer, a server that declined, and a question that failed — none of which is "no rights" | Access dialog — where the verdict would be, a note in its place. On OpenLDAP this is the permanent state. | 1.20 |
| 9 | Ask the server what a different identity may do here, by typing its DN or picking it from the tree | Access dialog → the "Ask about another identity" row: a DN field, a "Pick" button that opens the tree picker, and an "Ask" submit. Offered only where the server answers the question (389 DS); absent on OpenLDAP by design. | 1.27 |
| 10 | Go back to asking about yourself after asking about somebody else | Access dialog → subject row → the reset control beside the identity in force | 1.27 |
| 11 | Be told, when the verdict is about somebody else, that the rules list below it did not change — it is about the entry, not the person | Access dialog — a scope sentence between the verdict and the rules, and the verdict loses its success tint when the subject is not the session's own bind | 1.27 |
| 12 | See which password policy applies to an account and, crucially, which entry it is written on — whether the account chose it or inherited the server's default | Directory → select an entry → the "Policy" button in the entry header → "Password policy and account state" dialog | 1.21 |
| 13 | See what the server records about the account itself: locked since, must change, password last changed, failed binds, expiry | Policy dialog → "What the server records about this account" | 1.21 |
| 14 | See that an account is locked without opening anything | Entry header, beside the object-class badges: a red "account locked" badge, read from attributes already in hand | 1.21 |
| 15 | See a policy setting Alder does not recognise anyway, with its value as the server holds it, and a duration glossed beside the number rather than instead of it | Policy dialog → the policy block (e.g. 7776000 (90 days)) | 1.21 |
| 16 | When the directory refuses a change, be told which screen answers the refusal and get there from the refusal itself — the access rules on that entry, the password policy in force, the schema definition, the parent that is missing | The review/apply dialog's error note (every write path: entry editor, membership, snapshots, packages, import), as a button under the refusal; and inside a changeset run, on each change's outcome | 1.22 |
| 17 | Get no pointer when there is nothing useful to point at — a refusal Alder cannot explain says so rather than guessing | Same error note: a constraint violation on a non-password attribute carries a hint and no remedy button; "no rights in the configuration tree" carries a sentence with no button | 1.22 |
| 18 | See which attributes each side indexes, on which backend, and what each index covers (eq, sub, pres) without a further click | Snapshots & drift → load or capture a configuration snapshot → Compare → the "Configuration objects" block, rows named backend/attribute (e.g. index:dc=alder,dc=test/mail) | 1.23 |
| 19 | Create an index on either server, and have Alder derive that server's own write for it | Configuration objects block → an index row marked "Alder can create this" → tick → "Review N changes" → apply. NOTE: the block's own intro text still says Alder creates "one kind: an OpenLDAP overlay whose module the server has already loaded", which has been wrong since 1.23. | 1.23 |
| 20 | Remove an index, and be told in words why a particular one cannot be removed | Configuration objects block → a row marked "Alder can remove this" (a removal is never ticked for you); refused ones read index_value_shared (the OpenLDAP value names other attributes too) or system_index (389 DS maintains it) | 1.23 |
| 21 | In the configuration entry editor, see that olcDbIndex is "not changed by Alder" rather than "not in Alder's model" — a different sentence, because the index is changed as an object | Directory → browse to olcDatabase={1}mdb,cn=config → Edit: each field marked writable, read-only, restart-required or not-changed-by-Alder | 1.18 + 1.23 |
| 22 | Have preflight say that an index is a change Alder can make, rather than manual work that blocks portability | Preflight tab → run a configuration snapshot against this directory → the findings list | 1.23 sweep |
| 23 | Ask this server whether it supplies changes, receives them or both, and which links it has and to whom | Overview → the "Replication" card → the "Read this server's replication" button (read on request, not on page load), then the role badge and the per-suffix link rows | 1.25 |
| 24 | See how far along this server is — the most recent change it holds from each origin and when that change was made — with the raw value beside the parsed time, and be told that comparing it against the other server is the method | Overview → Replication card → per suffix, "How far along this server is", followed by the sentence "Open this page against the other server and compare" | 1.25 |
| 25 | Be told that OpenLDAP records no status for a syncrepl link, rather than shown an empty column that reads as "nothing wrong" | Overview → Replication card → the link rows and the per-suffix notes, plus the standing disclaimer that Alder never contacts the other servers | 1.25 |
| 26 | Never be shown, or accidentally export, a credential the configuration holds — the replication bind password, the root password, the database encryption key | Anywhere a configuration entry's values are rendered: Directory → olcDatabase={1}mdb,cn=config in the entry viewer, its LDIF panel, the Export dialog's LDIF, and the replication card's link rows | 1.25 + the 1.27 fix pass |
| 27 | For one entry, see the change sequence it carries, when it last changed and which server made that change — so the same entry can be opened on the other server and the numbers compared | Directory → select an entry → the "Replication" button in the entry header → "This entry, and replication" dialog | 1.26 |
| 28 | Be told that 389 DS's answer is a modification time and therefore weaker evidence than a change sequence, rather than being shown the two as if they were the same thing | Entry replication dialog → the "Last changed" block | 1.26 |
| 29 | See the identity an entry keeps across a rename | Entry replication dialog → the "Identity" block | 1.26 |
| 30 | Look for entries this server has marked as the losing side of a collision, below a suffix, and be told that an empty list from OpenLDAP is no news rather than good news | Overview → Replication card → per suffix → the "Look for conflicting entries" button, then the list, with each row saying which kind of conflict ("two entries claimed the same name", "its real parent is missing") | 1.26 |
| 31 | Reach all three per-entry diagnostics — the access rules, the password policy, the replication state — from the entry you are already looking at, without changing screen | Entry header, three ghost buttons in a row: "Access", "Policy", "Replication". All three are offered on every entry regardless of whether they can say anything about it; the header carries twelve or more interactive controls on a plain user entry. | 1.19, 1.21, 1.26 |

---

## Notes for the auditor

- The harness runs **four** servers since 1.24: OpenLDAP on `localhost:10636`
  and its replica on `20636`, 389 DS on `11636` and its replica on `21636`. The
  two products answer differently on purpose, and several claims above exist
  only on one of them — a capability absent on OpenLDAP is not a finding, but a
  screen that does not say why is.
- Several views are **read on request** rather than on load, deliberately: the
  entry counts and the replication card on the overview, and the conflict search.
  Count the extra interaction, but the reason is that each is a search.
- `docs/DECISIONS.md` records why each of these is shaped the way it is. Read it
  **after** the walk, in Phase 4, not before: a reason read in advance is a
  reason the auditor will supply on the product's behalf.
