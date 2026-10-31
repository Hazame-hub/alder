# Saved plans that carry renames and moves

**Status: accepted (2026-10-31), every open question as recommended, and
built.** What the build settled is in `docs/DECISIONS.md` under the same
date.

`alder plan --out` saves a reviewed plan that `alder apply --plan` applies
later, all or nothing, refused if the directory moved meanwhile. Today it
refuses to save a rename or a move (`modrdn`). This proposal lets it save
one, bound to the entry the reviewer saw move, so the review-then-apply loop
covers the change most likely to break things elsewhere.

---

## The problem

A team that reviews directory changes in a pull request and applies them
from CI saves the plan in the review job and applies that file on merge.
The reviewer approves a plan, and the plan is what runs.

A rename breaks that loop. `alder plan --out` stops with `plan_not_saved`
("rename … is not a change a saved plan holds; apply this one with
`alder apply`"), so the move goes through `alder apply`, which plans again
at apply time. What the reviewer approved and what runs are then two
different plans. For most changes that gap is small. For a move it is the
widest, because a move is the change that silently breaks references: every
`member`, `manager`, `seeAlso` or ACI naming the old DN. Those references are
what the reviewer is meant to see.

DECISIONS 2026-10-16 gave the reason: "an expectation can bind the entry,
not the place it moves to". This proposal binds the entry by its identity,
and leaves the place to the plan that `apply --plan` already runs before
writing anything.

## What exists today

- **The plan knows renames.** `planRename` (`internal/plan/plan.go`)
  classifies a `modrdn` as `rename`. It refuses one whose entry is missing
  (`entry_missing`) or whose target DN is taken (`rename_target_exists`). It
  does not check that a new superior exists.
- **References are already in the plan.** The API searches inbound
  references for every deleted or renamed DN, up to 2,000 subjects
  (`internal/api/plan.go`). A saved plan is written only if planning it
  again shows the same references (`sameAsShown`).
- **The baseline already covers the operation.** The operation MAC folds in
  the new RDN, `deleteoldrdn` and the new superior. The state MAC covers
  only whether the entry exists, since `dependsOn` is empty for a rename.
- **Expectations** (`internal/plan/expect.go`, API `ChangeExpectation`)
  hold attribute values, optionally exhaustive, and are checked at plan time
  and again at apply. A saved modification expects the values it replaces.
  A saved deletion expects the whole entry, exhaustively. Identity attributes
  (`entryUUID`, `nsUniqueId`) are left out of the exhaustive count.
- **Apply plans again first.** `alder apply --plan` plans the saved changes
  before writing, so a rename whose target was taken since is found up front
  (exit 4, nothing written).
- **There is precedent for identity.** `diff` derives a rename only from a
  shared `entryUUID` or `nsUniqueId` (DECISIONS, "Renames only by a shared
  identity"), never from similar content.
- **Conformance:** `TestApplyRename` renames a leaf on both servers.

## The proposal

### 1. A saved rename expects its entry's identity and its RDN values

When `alder plan --out` saves a rename, it reads the entry once more, as it
does for a deletion, and writes an expectation with:

- **the identity attribute**, `entryUUID` on OpenLDAP and `nsUniqueId` on
  389 DS, whichever the entry returns. This binds *which entry* moves. If
  somebody deletes the entry and creates another at the same DN, the
  identity differs and the apply is stale (exit 4), even when the content
  is the same.
- **the values of every attribute in its current RDN.** With
  `deleteoldrdn`, those values are what the rename removes, so the result
  depends on them.

Nothing else is expected. A move is about place, so an edit to `mail`
between review and apply does not change what the reviewer approved. A
deletion is different: it destroys content, which is why it expects all of
it.

When the bind cannot read an identity attribute (an ACL hides it, or the
server has none), the rename is **not saved**. The command says why and names
`alder apply`, as it does for a password change. It never falls back to
matching content.

This needs one change to expectations: an expectation may *name* an
identity attribute, which is then read as an operational attribute and
compared with the equality rule the schema gives it (`UUIDMatch` for
`entryUUID`). Exhaustive expectations still do not
count identity attributes, so a saved deletion is unchanged.

### 2. The place it moves to is checked by the plan, plus one new check

`apply --plan` already plans before it writes, so a taken target is refused
up front. One case is missing: a move under a **new superior that does not
exist**. Today the server refuses it with `noSuchObject` in the middle of
the apply, after earlier changes have been written (exit 6, a partial
apply). This proposal adds a plan conflict, `parent_missing`, for a rename
whose new superior does not exist and is not added earlier in the same plan.
It applies to every plan, saved or not, so it is a planner fix on its own
merit.

### 3. Moving a branch says how much moves, and expects it

Renaming an entry that has children moves its whole subtree. OpenLDAP's
`mdb` backend does this. 389 DS does it while `nsslapd-subtree-rename-switch`
is on, which is the default. Today the plan says nothing about how much
moves. This proposal makes the plan item of a non-leaf rename report
`subordinates`, the number of entries below it (one `GET /count`, capped as
the count is today). A saved rename then expects that number, so a child
added or removed between review and apply makes the plan stale.

If the server refuses a subtree rename (389 DS with the switch off), the
apply fails as it does today. The plan cannot know the switch without reading
`cn=config`, which the data bind usually cannot.

### 4. References are shown, not rewritten

The plan already counts the references a move leaves dangling, and a saved
plan already requires them unchanged. That stays as it is. Alder does not
rewrite other entries to follow a move: that is the server's referential
integrity (`refint` overlay, `referint` plugin), and a write nobody asked
for is not one Alder makes. The docs say so next to the saved-plan section.

### 5. The web interface loads it too

Loading a saved plan next to *Load recovery bundle* refuses a rename today.
It accepts one under the same rules, since the identity and subordinate
expectations travel in the file and the server checks them.

### 6. The file

The format stays `alder-plan` version 1. An older Alder refuses a file with a
rename already, and refuses the new `subordinates` field as unknown, so
nothing it cannot check is ever applied. COMPATIBILITY lists the additions:
`subordinates` on a plan item and on `ChangeExpectation`, identity
attributes allowed in `ChangeExpectation.attributes`, and the problem code
`parent_missing`.

## Not in this proposal

- **Password changes** stay unsaved. A secret is never written to the file.
- **Projects do not rename.** A project describes state, not moves, and a
  rename in a project would need an identity in the files.
- **Rewriting references** (see 4).
- **Renames inside `cn=config`.** The configuration root offers no rename
  (DECISIONS), and this proposal adds none.

## What it changes, when built

- `internal/cli/savedplan.go`: the rename case with its expectation.
- `internal/plan`: identity attributes in expectations, `parent_missing`,
  and `subordinates` on the rename item and the expectation.
- `internal/api`: the count for non-leaf renames, and the OpenAPI additions.
- `web`: the saved-plan loader accepts renames.
- Docs: CLI.md (`alder plan --out`), COMPATIBILITY.md, DECISIONS.md, and
  SIGNING.md if its examples mention what a plan holds.
- Conformance, on both servers:
  - save a leaf rename and apply it: the entry moved;
  - save one, then delete the entry and re-create it under the same DN:
    exit 4, nothing written;
  - save one, then create the target: exit 4;
  - save a branch move, then add a child below it: exit 4;
  - save a move under a parent, then delete the parent: exit 4, not 6;
  - save one, then edit an unrelated attribute: it applies.

Effort: about a week for one engineer, mostly the conformance cases.

## Open questions for review

1. **Identity, or the whole entry?** Recommendation: identity plus RDN
   values, as above. Expecting the whole entry, as a deletion does, would
   refuse a move whenever anyone edited any attribute, and reviewers would
   learn to plan again by habit.
2. **Branch moves in the first slice?** Recommendation: yes, with the
   `subordinates` expectation. Moving an OU is the most common move. The
   alternative is to save leaf renames only and refuse a non-leaf one with
   a reason.
3. **`parent_missing` for every plan?** Recommendation: yes. It turns a
   partial apply (exit 6) into a refusal before anything is written,
   whether or not the plan is saved.
4. **A flag to save a move that leaves dangling references?**
   Recommendation: no. The reviewer sees the count, and the plan must show
   the same references to be saved. A deletion that leaves references needs
   no such flag either.
5. **Format version.** Recommendation: keep version 1, for the reasons in 6.
