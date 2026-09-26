# UI audit — "a configuration setting drifted; find it and put it back"

**Date:** 2026-09-24 · **Build:** `e9c3313` (1.16 merged, 1.15.0 released) ·
**Target:** `http://127.0.0.1:8899`, single binary, embedded SPA ·
**Directory:** the `test/compose` harness, OpenLDAP, disposable data ·
**Viewport:** 1554×1022 · **Persona:** an infra engineer who owns this directory,
has read no docs, and has twenty minutes.

The drift was created outside Alder before the run — `olcIdleTimeout` 0 → 900 on
`cn=config`, applied with `ldapmodify` — as a real drift would be. The baseline
was a configuration snapshot captured from the same server earlier (88 settings,
`olcIdleTimeout: 0`), held as a file on disk, which is how a stateless tool has to
do it. A second run afterwards created and removed an overlay through the
interface, to reach the 1.16 capability the first task does not touch.

## Evidence, and what is missing from it

Findings are backed by the accessibility tree and the page text at each step,
quoted inline, and by reading the server directly before and after each apply.
**No screenshots**: capture against the hidden automation window times out in this
environment, so `shots/` is empty and nothing here rests on a picture. **Console
and network were not recorded during the first walkthrough** — tracking starts
when the tool is first called, which happened after that run. A clean reload
afterwards produced **zero console errors**, and every request in both runs
returned 200 (`/api/v1/session`, `/api/v1/source`, `/api/v1/tree`,
`/api/v1/entry`, `/api/v1/plan`). That is the only error data this audit can
honestly claim.

## Verdict

**The tool is not confusing. It is long, and it forgets.** Every screen says true
things in plain words, the guardrails are real, and nothing on the way to a
configuration write is decorative. Two things spoil it. The screen that *knows*
what changed cannot act on it — it hands you to a separate changeset, which then
asks for a plan and an apply as two more steps — while the ordinary entry editor,
two clicks from the Overview, applies the same change from one dialog with the
same warnings. And the comparison is thrown away the moment you follow the button
it gives you: come back from the changeset and both snapshot slots are empty. The
careful path is the slow one, the fast one is the one an operator finds first, and
the careful one does not survive being walked.

## Score

Ideal, stated in `claims.md` before the run: *capture → compare → tick → review →
apply.* **Five interactions**, connection excluded.

| Metric | Actual | Ideal | Delta |
|---|---|---|---|
| Interactions, task only (after Connect) | 8 | 5 | **+3** |
| Interactions, cold start included | 11 | 8 | +3 |
| Distinct screens visited | 4 (Connect → Overview → Snapshots → Changeset) | 2 | +2 |
| Backtracks | 0 | 0 | 0 |
| Dead ends | 0 | 0 | 0 |
| Time to first meaningful action | 3 interactions (two passwords, Connect) | 3 | 0 |
| Console errors / failed requests | 0 observed (see above) | 0 | 0 |

The log, one line per interaction:

```
#1  type bind password                     /connect    required
#2  type configuration password            /connect    second identity, cn=admin,cn=config
#3  click Connect                          /connect    lands on Overview
#4  click Snapshots                        nav         configuration is not named anywhere in the nav
#5  upload baseline file into slot A       Snapshots   below a tall capture form
#6  click Compare                          Snapshots   defaults were already right
#7  tick olcIdleTimeout                    Snapshots   2 rows differ, 6 filter controls above them
#8  click "Stage for review"               Snapshots
#9  click "Review the changeset"           → Changeset a screen change to see what was just selected
#10 click "Check against the directory"    Changeset
#11 click "Apply 1 of 1 in order"          Changeset   "All 1 changes applied."
```

Interactions #8–#11 are four steps to apply one change the screen at #7 had
already fully derived. The server was then read directly: `olcIdleTimeout: 0`. The
task succeeded, correctly, first time, with no wrong turns.

**Second run — create and remove an overlay (claim 9, 1.16).** Seven interactions
each way from the Snapshots screen with the document already at hand: upload →
Compare → tick → Stage → Review the changeset → Check → Apply. Both directions
worked and were verified on the server: created as
`olcOverlay={1}memberof,olcDatabase={1}mdb,cn=config`, the position assigned by
slapd and absent from the LDIF that was sent; removed by the live entry's own DN,
including its position. The object row said "Alder can create this" and later
"Alder can remove this", and the removal had to be ticked as a removal. This is
also the browser proof that 1.14, 1.15 and 1.16 all shipped without.

## What works

- **The review dialog is the same object everywhere it appears**: a `config`
  badge, "Recovery unavailable: schema and configuration changes are not
  recovered", the LDIF and the Ansible task on two tabs, and a line saying the
  LDIF is what the server itself rendered from the plan. Confirming it means
  something.
- **Compare's defaults were already the common case** — Source "The directory
  now", Target "Snapshot A" — so #6 needed no configuration at all.
- **"Recovery unavailable" is said twice** on the way to a configuration apply, in
  words, not as a colour or an icon.
- **The editor leads with its own contract**: "Editing. Nothing is sent until you
  review the LDIF and confirm it," above the form rather than beside the button.
- **The object rows earn their space**: "Alder can create this" / "Alder can
  remove this" is the whole 1.16 model in four words, and a removal is never
  ticked for you.

## Findings

### 1 · Major — the comparison can find the drift but cannot fix it

The compare result knows the setting, the old value, the new value, that Alder has
proved the setting writable, and exactly which `ChangeRecord` would put it back.
Its only offer is "Stage for review", after which the operator changes screen,
clicks "Check against the directory", then clicks "Apply 1 of 1 in order". Four
interactions and one screen for a change the page had already computed.

Two clicks away, the plain entry editor on `cn=config` reaches **the same apply**
through one dialog: Edit → change the field → "Review 1 change" → "Apply to the
directory". That dialog carries the config badge, the recovery warning, a "Read
this before applying" note, the plan's findings, the LDIF and the Ansible — every
guardrail the long path has.

The product already has the right component, and the drift screens are the ones
that do not use it: `ChangeDialog` (`web/src/components/change-dialog.tsx:42`) is
imported by nine features, and `config-diff-view.tsx`, `schema-diff-view.tsx` and
`snapshots.tsx` are not among them.

**Fix.** Give the comparison a primary "Review N changes" button that opens
`ChangeDialog` on the derived records, with "Add to changeset" demoted to the
secondary it already is everywhere else. The changeset stays what it is for — a
place to mix changes from several sources — instead of being the only road out of
a comparison. **Saves 3 interactions and one screen change** on the task this
release cycle was built for, and the same fix applies to the schema comparison.

### 2 · Major — leaving the comparison destroys it

Both snapshot slots are component state. Click "Review the changeset", then come
back to Snapshots: `Snapshot A (empty)`, `Snapshot B (empty)`, no comparison, no
result. Verified in the second run — the document loaded a minute earlier was
gone, and it had to be uploaded and compared again.

So the screen tells the operator to go to the changeset, and going there throws
away the evidence they would want beside it. Worse at 2am than it sounds: the
comparison is the only record of *what else* differed, and the way back to it is
to find the file again. The changeset is deliberately tab-local and unpersisted —
a staged password change carries the password — but a snapshot document already
sitting in the browser is not that, and clearing it buys no safety.

**Fix.** Keep the loaded documents and the last comparison in the Snapshots view's
own state for the life of the tab, exactly as the changeset is kept for the life
of the tab. If that state must be dropped on Disconnect, drop it there. Combined
with finding 1, the operator stops leaving the screen at all.

### 3 · Major — "0 Alder can change", next to a thing Alder can change

The comparison summary counts settings only. In the second run it read:

```
1 added   88 unchanged   0 Alder can change
```

directly above an object row reading "Added · overlay · memberof · **Alder can
create this**". An operator who reads the headline and stops — which is what a
headline is for — concludes Alder is useless here and goes back to `ldapmodify`.
The same undercount will hide every switchable 389 DS plugin.

**Fix.** Count objects in that summary: "1 Alder can change (1 object)". While
there, the "Only what Alder can change" filter should keep actionable objects
visible, and "1 settings" needs its plural.

### 4 · Major — nothing in the navigation says "configuration"

Nine top-level tabs: Overview, Directory, Search, Schema, Changeset, Import,
Snapshots, Packages, Preflight. An operator whose problem is *the configuration
drifted* has to guess "Snapshots", and that screen greets them with "Capture a
subtree or the schema as a versioned snapshot…"
(`web/src/features/snapshots.tsx:277`) — copy that predates 1.13 and omits the
third kind, the one they came for.

The Overview does better and then stops: its Configuration card reports the tree
and that it is readable as `cn=admin,cn=config`, and the DN is a link — to the raw
entry browser (`web/src/features/overview.tsx:128`), not to the comparison. Four
releases of configuration work sit behind a word that does not mention it.

**Fix.** Name configuration in the Snapshots intro; add a "Compare configuration"
action to the Overview's Configuration card beside the browse link; rename the tab
to admit what it is for ("Snapshots & drift"). Saves the guess, which never shows
up in a click count and always shows up at 2am.

### 5 · Major — two doors to a configuration write, with different rules behind them

The entry editor opens `cn=config` as an ordinary entry and offers all 48
permitted attributes as plain inputs — `olcTLSCertificateFile`, `olcConfigDir`,
`olcThreads` — with nothing saying which Alder has proved writable, which need a
restart, or which the server accepts and then fails to start on. The comparison
screen has all of that: it is what `writableOn`, `restartRequired` and the
round-trip conformance proof are for. The editor also offers "Delete" and "Delete
with contents" on `cn=config` itself.

To be fair to it, the review dialog it reaches is not naive — the config badge,
the recovery warning and the "cn=config is this server's own configuration, not
directory data" note are all there. The gap is upstream of the dialog, in the form
where the operator chooses what to touch.

**Fix.** When the entry is inside the configuration tree, decorate the form from
the same classifier the comparison uses: read-only settings shown as read-only,
restart-required marked in the field, and a banner pointing at the comparison.
Failing that, at minimum suppress "Delete with contents" on the configuration
root.

### 6 · Minor — the result is buried under the form that produced it

After Compare, the differing rows render below the full-height capture form, with
six filter controls above them. Six controls for two rows is furniture.

**Fix.** Collapse the capture form to a one-line summary once a comparison has run,
and show the filters only when the result runs past a screen — roughly 20 rows.

### 7 · Minor — A and B slots, plus Source and Target selects

Loading a document into "slot A" and then choosing "Snapshot A" as the Target is
two vocabularies for one decision, and the second can contradict the first.

**Fix.** Drop the selects. The slot a document was loaded into is the side; a single
"swap" control covers the rest.

### 8 · Minor — a row marked "Unknown" with no reason in it

`olcModuleLoad` appears as Unknown in both runs. Every other refusal in 1.16 has
words behind it — `module_not_loaded`, `parent_missing`, `not_creatable`, and the
`no_write_path` printed two rows below — and this one has none inline, which reads
as a bug in Alder rather than a fact about the setting.

**Fix.** Render the refusal text in the row, the way the object list does.

### 9 · Minor — a snapshot card reports its capture time as an upload time

The card reads "uploaded 2026-09-24T19:49:38Z" for a document loaded from disk
(`origin: "uploaded"`, `web/src/features/snapshots.tsx:225`), where the timestamp
is when the snapshot was *captured*. For a drift investigation the capture time is
the one that matters, and it is labelled as something else — in raw ISO, while the
entry viewer two tabs away formats dates for humans.

**Fix.** "captured 24 Sept 2026, 21:49 · loaded from file".

### 10 · Polish — a recovery bundle offered for a change that cannot be recovered

The apply screen offers "Prepare a recovery bundle" for a configuration change
that the same screen says twice is not recoverable.

**Fix.** Hide it for configuration targets, or disable it with the reason attached.

## The claim sheet, judged

| # | Capability | Verdict |
|---|---|---|
| 1 | Connect, with a second identity for the configuration tree | Works cleanly |
| 2 | See what the session can do, and where schema and configuration live | Works cleanly, dead-ends into the raw browser — finding 4 |
| 3 | Browse the DIT and read an entry | Works cleanly |
| 4 | Search with a filter builder or a raw filter | Not covered |
| 5 | Browse the schema and follow cross-links | Not covered |
| 6 | Capture a snapshot of data, schema or configuration | Works cleanly (upload path exercised; capture-to-file taken from the CLI) |
| 7 | Compare two snapshots, or one and the directory now | Works but painful — finding 2 |
| 8 | See which settings differ and which Alder can change | Works but painful — findings 1, 3, 6, 8 |
| 9 | See which objects differ, create or remove an overlay | Works cleanly — both directions applied and verified on the server |
| 10 | Stage differences and review them as LDIF | Works cleanly |
| 11 | Plan a change before applying it | Works cleanly |
| 12 | Apply, and see what a configuration change does not recover | Works cleanly — finding 10 is cosmetic |
| 13 | Preflight a package or snapshot | Not covered |
| 14 | See whether a document was signed, and by whom | Not covered in the browser |
| 15 | Build and validate a change package | Not covered |
| 16 | Import LDIF | Not covered |

**Not covered** means not attempted in this run, not missing. Five of sixteen were
left alone to keep the audit to real tasks; the configuration path, which is what
the last four releases added, was walked end to end three times and applied
against a live server each time.

## Since this audit

**Findings 1, 2 and 3** were fixed in the branch that carried this report: a
comparison offers "Review N changes" -- one opens the dialog every other screen
uses, several go to the changeset in one click -- the loaded documents and the
last comparison last as long as the tab, and the summary counts the objects
Alder can act on.

**Findings 4 and 6 to 10** followed. Configuration is named where an operator
looks for it: the tab is "Snapshots & drift", the intro says what it captures,
and the Overview's Configuration card links straight to the comparison. The
capture form folds away once a comparison is on the screen and the filters
appear only when a result is long enough to need them. With one document loaded
the two selects give way to "The directory now → Snapshot A" with Swap, and a
document loaded into a slot becomes the side compared. A setting Alder cannot
act on says why in a sentence. A snapshot card says "captured <time> · loaded
from file". A plan that recovers nothing offers no recovery bundle.

**Finding 5** was done in two parts. The editor first said what tree it was in
and stopped offering Rename, Delete and "Delete with contents" on the
configuration root; 1.18 then gave it the model's answer per attribute, through
a new `GET /config/entry?dn=`, so each field is marked writable, read-only,
restart-required or not-in-the-model. The marks mark and never block: the model
states what Alder changes, and the directory decides what it accepts.

Every finding in this report is now addressed.

## If only two things are changed

Findings 1 and 2. Together they are three interactions, one screen, and a
component that already exists — and they turn a flow that forgets what it just
showed you into one that does not need to.
