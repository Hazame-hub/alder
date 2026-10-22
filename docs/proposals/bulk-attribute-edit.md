# One modification over selected entries

**Status: accepted (2026-10-22), Option A, with every open question
answered as recommended. On membership attributes the dialog offers add and
remove only, and the value comes from the entry picker only.** The decision
is in `docs/DECISIONS.md` ("2026-10-22 — bulk attribute edit: accepted"),
and the planner fix ships first as its own change. The second of the next
open-source features. No charter change and no API
change; one planner fix is proposed alongside it, as its own change.

---

## The problem

*"Set `departmentNumber: 4200` on these forty people"*, *"remove the
`description` that says 'temp' from every account under `ou=contractors`"*,
*"add `cn=vpn-users` to the `seeAlso` of these twelve groups"*. One
modification, many entries the directory already holds.

Counted from the code, not from an audit, Alder offers two ways to do it
today:

- **Through the editor, one entry at a time**: open the entry, *Edit*, find
  the attribute, add the value, *Review*, *Stage* -- six interactions an
  entry, two hundred and forty for forty people, each reviewed alone.
- **By writing the LDIF by hand** and importing it: forty `changetype:
  modify` records, typed or generated outside Alder, which is exactly the
  "click here and hope" step the product exists to replace, with the DNs
  copied from a search result by hand.

Both end in the same place, a changeset of forty modifications planned and
applied as one review. What is missing is a way to *author* that changeset
from the rows already on screen.

## What is true today, read from the code

- **Tables already select rows.** `components/entry-table.tsx` has a
  selection on the search results and on the object views, and offers one
  action for it, *Stage deletions*. It is hidden on a read-only instance.
- **Staging many changes is one shared function.** `lib/stage-deletes.ts`
  `stageChanges(items)` adds a list of `ChangeRequest`s to the basket or
  refuses the whole list when it does not fit (the basket holds 2,000,
  decision 2026-09-06), with one wording for the refusal.
- **The changeset plans again before it applies**, and `POST
  /changeset/apply` refuses a change whose entry moved since its plan
  (`plan_stale`). A partial run is reported change by change; what did not
  apply stays staged (decision 2026-09-04).
- **The planner already holds the line this feature needs**
  (`internal/plan/plan.go`):
  - a modification of an entry that does not exist is a `conflict` with
    `entry_missing` -- it is never turned into an add;
  - a modification the entry already satisfies is `unchanged`;
  - the schema check (`internal/plan/validate.go`) makes an attribute the
    entry's classes do not permit `invalid` (`attribute_not_permitted`), as
    it does a second value on a single-valued attribute and the removal of a
    required one.
- **"Already satisfied" compares bytes** (`satisfied` and `SameValues`).
  Adding `Platform` to an entry holding `platform` in a case-insensitive
  attribute plans as a `modify`, and the directory then refuses it with
  `attributeOrValueExists`. In a changeset of forty that stops the run at
  that entry. It is rare when one person edits one entry; it is likely when
  one value goes to forty entries somebody else populated.
- **A naming value is not protected by the plan.** Removing or replacing the
  attribute an entry's RDN is built from (`uid` on `uid=ana,...`) is not
  checked by `validate`; the directory refuses it at apply
  (`notAllowedOnRDN`).
- **Membership is never posted as a list.** Adding and removing a member is
  one value, `add` or `delete`, because a replacement of `member` silently
  removes whoever another administrator added (decisions 2026-09-03 and
  2026-09-05).
- **The field for a value already exists.** `components/attribute-editor.tsx`
  renders a value input for an attribute's syntax, and creation and editing
  share it (decision 2026-09-05).

## Why this is not "bulk or CSV provisioning"

The charter excludes bulk or CSV provisioning, and CONTRIBUTING.md and the
2026-09-06 decision say where the line is: *"Alder stages what the operator
authored or what the directory already holds ... and does not generate
entries from an external file. The distinction is where the entries come
from, not how many there are."* This feature stays on the allowed side by
construction:

1. **The entries come from the directory.** The targets are DNs a search or
   a view returned, selected on screen. No DN comes from a file, a column or
   a feed.
2. **Nothing is created.** Every staged change is a `modify`. An entry that
   is gone by the time the changeset is planned is a `conflict`
   (`entry_missing`), never an add.
3. **The modification is authored once, by the operator.** One attribute,
   one operation, the same value or values for every entry. There is no
   per-row value, so there is nothing a spreadsheet could supply: "set each
   person's `mail` from column C" is provisioning, and it stays out.
4. **The set is fixed when it is staged.** The changeset holds the forty
   DNs, not the filter that found them. Nothing re-runs the search at apply
   time, so what is reviewed is the list that is written.

## What any design has to keep

- **No write outside `Session.Apply`** (section 7, rule 1): the changes go
  through the changeset as every staged change does.
- **Every write is reviewed as LDIF** (section 2, item 5): the changeset
  shows the forty records and their plan before anything is sent.
- **DNs are never strings** (rule 2): the targets are the DNs the server
  returned, sent back as it rendered them, and parsed by the server.
- **Sensitive values never reach the browser or the log** (rule 6).
- **Read-only refuses every write**, and staging is not offered there.

## Options

### Option A: the browser composes one change per entry, through the existing staging

The selection bar gains *Edit an attribute…* beside *Stage deletions*. A
dialog takes one attribute, one operation and the value or values, and
stages one `ChangeRequest` per selected DN with that one modification,
through `stageChanges`.

- **For:** no new endpoint and no new write path; every rule the planner
  and the changeset already enforce applies unchanged. A partial failure
  leaves the remaining changes staged, as for any changeset.
- **Against:** the browser builds the records. That is already true of the
  editor's modifications and of the membership actions; what reaches the
  directory is still decided by the server's plan.

### Option B: a server endpoint that derives the changes

`POST /changes/bulk` taking a DN list and one modification, returning the
`ChangeRequest`s, as `POST /config/indexes/candidate` derives a change.

- **For:** the composition is tested in Go.
- **Against:** an endpoint for a loop. The index candidate is on the server
  because the two servers want different writes for the same intention;
  here both servers want the same `modify`. It would be a second way to ask
  for exactly what `/plan` already accepts, and an addition to the API that
  can never be taken back.

### Option C: "apply to every entry matching this filter"

- **Against, and out:** the set of entries would be decided when the write
  runs, not when it is reviewed. An entry added between review and apply
  would be written without anybody seeing its DN. That breaks "every write a
  reviewed `ChangeRecord`" in substance even if each record passed through
  `Session.Apply`.

## Recommendation: Option A, with one planner fix first

### The dialog

- **Opened from the selection**, on the search results and on the object
  views, only when the instance is not read-only. It names the count and the
  first few DNs: *40 entries selected from `ou=people,dc=example,dc=com`*.
- **One attribute.** Offered: the attributes every selected entry's classes
  permit, from `GET /schema/requirements` over the union of the rows'
  `objectClass` values (the rows already carry them). *Show attributes only
  some entries permit* widens the list, and those entries are then planned
  `invalid` and named. Not offered at all: sensitive attributes (a password
  set on forty accounts is the "password reset" the charter excludes, and
  `setpassword` is not a modification), operational and
  `NO-USER-MODIFICATION` attributes, and `objectClass`.
- **One operation**, each with what it means in words:

  | Operation | Record | Planned `unchanged` where |
  |---|---|---|
  | Add values | `add: attr` with the values | the entry already holds them all |
  | Remove values | `delete: attr` with the values | the entry holds none of them |
  | Remove the attribute | `delete: attr` | the entry has no value |
  | Replace with | `replace: attr` with the values | the entry already holds exactly them |

- **Replacement is refused on membership attributes** (`member`,
  `uniqueMember`, `memberUid`, `memberURL`), for the reason the membership
  actions exist: it removes whoever was added since the table was read. Add
  and remove are offered there, one value each, and *(accepted 2026-10-22)*
  the value comes from the entry picker only, never typed: a member added is
  an entry the directory returned. `memberUid` holds a login name and
  `memberURL` a search, so neither can be picked and neither is offered in
  this dialog. On any other multi-valued
  attribute, replacement says above the values *every other value of this
  attribute is removed from each entry*.
- **Naming attributes**: an entry whose RDN uses the attribute is left out of
  a removal or a replacement and named in the dialog (*3 entries are named
  by `uid`; they are not changed*), since the directory would refuse them
  and stop the run.
- **Values** use `AttributeEditor`'s field for the attribute's syntax, so a
  Boolean is a choice and a DN is the entry picker.
- **Stage, never apply.** The dialog's only action is *Stage 40 changes*,
  through `stageChanges`, so an over-full basket refuses the whole set. The
  person then reviews the changeset as always.

### In the changeset

Each change is labelled *Add departmentNumber to uid=ana,…*. The plan
summary already counts by action, so the review reads *37 modify, 2
unchanged, 1 conflict*, and each `unchanged` and `conflict` says why. A
changeset that holds only one bulk edit gains a heading saying so (*One
modification over 40 entries: add `departmentNumber: 4200`*), dropped the
moment anything else changes the basket, as a staged project's heading is.

### The planner fix, as its own change first

`satisfied` and `SameValues` compare bytes. For the add and delete cases,
compare values under the attribute's equality rule where Alder implements
that rule already: DN syntax as `dn.Equal` (which `references.go` and the
comparison already do), and `caseIgnoreMatch`, `caseIgnoreIA5Match` and
`caseIgnoreListMatch` with a fold of case and insignificant spaces, in one
function in `internal/plan` with its own tests. It errs towards "different":
a pair it is unsure of (anything RFC 4518 would map that a simple fold does
not) is left to the directory, as every pair is today.
Anything else stays byte for byte, and the directory stays the authority.

- It changes one thing a plan says: an add of a value the entry holds in
  another case is `unchanged` instead of a `modify` the directory would
  refuse. That is a correctness fix in the sense of COMPATIBILITY.md, not a
  new behaviour, and it helps every plan, not only this feature.
- It goes first because a bulk edit is where the byte comparison stops a
  run halfway.

### API delta

**None.** The dialog composes `ChangeRequest`s, which `/plan`,
`/changeset/preview` and `/changeset/apply` already take. The planner fix
changes no shape: `unchanged` is an existing `PlanAction`.

### Tests

- **Conformance, on both servers** (the composition is the browser's, so the
  case posts what it composes): one `add` of a value to five entries, where
  one already holds it in another case (`unchanged`, after the fix), one was
  deleted after it was selected (`conflict`, `entry_missing`), one's classes
  do not permit the attribute (`invalid`), and two are written; read back
  through the driver. A `replace` on a single-valued attribute and a
  `delete` of a value one entry does not hold.
- **Unit**: the composer (one record per DN, the naming-attribute exclusion,
  membership replacement refused, sensitive attributes never offered), and
  the planner's equality comparison per matching rule.
- **Playwright journey**: select three people in a search, add a
  `description` value, review the changeset, apply, and check each entry
  with `ldapsearch` in each server's container.

## What it will not do

- **No per-entry values**, no import of values from a file or a column. That
  is the excluded provisioning.
- **No creation and no deletion of entries.** Deletion already has its own
  action, *Stage deletions*.
- **No filter-driven writes.** The set of DNs is fixed when it is staged.
- **No more than one modification per dialog.** Two edits are two dialogs and
  two sets in one changeset, each reviewed.
- **No selection across pages the search did not return.** The rows are the
  ones on screen, bounded by the search's limit, and a truncated result
  says so above the table, as today.
- **No password, sensitive or server-owned attribute**, and no `objectClass`.
- **No configuration or schema entries.** A selection holding an entry at or
  below the configuration or schema naming context the RootDSE announces is
  not offered the action; those trees have their own, narrower write rules
  (charter, section 2).

## Does it need a charter change?

**No.** It is section 2, item 9 (changesets: "stage several changes, review
them as one LDIF document ... apply them in order") and item 4 (the editor),
inside the line the 2026-09-06 decision drew. The decision entry, when
accepted, should restate the four reasons above, so the next reader of the
excluded line does not have to infer them.

## Open questions for review

1. **The planner's equality fix: ship it first and separately?**
   Recommendation: yes, as a `fix(plan)` with its own conformance case,
   before the dialog.
2. **Attributes permitted by every entry, or by any?** Recommendation:
   every, with the toggle to widen; a list where most choices plan
   `invalid` for most rows is a list that teaches people to ignore
   `invalid`.
3. **The bulk heading in the changeset.** Recommendation: yes, dropped on any
   other basket change, as a project's is.
4. **Membership attributes in this dialog at all,** given that the group
   tools (`docs/proposals/group-membership-tools.md`) cover them?
   Recommendation: allowed for add and remove only, so "add this group to
   the `seeAlso` of twelve entries" and "add `cn=vpn` to the `member` of
   these groups" do not need two tools. *Accepted, with the value from the
   entry picker only.*
