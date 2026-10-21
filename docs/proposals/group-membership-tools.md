# Group membership tools: many members at once, and members nobody can find

**Status: proposed (2026-10-21), for review. No code until it is accepted.**
The third of the next open-source features. No charter change; one new
read-only endpoint.

---

## The problem

Two jobs every directory owner does, and Alder does one value at a time.

**Many members at once.** *"Add these twenty-five new starters to
`cn=engineering`"*, *"take the twelve contractors whose contracts ended out
of `cn=vpn-users`"*. Counted from the code, not from an audit: on the group's
entry, *Add member*, pick one, review, stage -- four interactions a member,
a hundred for twenty-five, and twenty-five separate `add: member` records in
the changeset for what is one change to one group.

**Members nobody can find.** A group goes on naming an entry long after the
entry was deleted, wherever referential integrity is not configured, and a
`groupOfNames` cannot simply have its last such value removed, because the
class requires a `member`. Today the *Members* panel lists, for one group,
the member values it could not read (`dangling` in `GET /members`), and
offers nothing to do about them. Across a directory there is no list at all.

## What is true today, read from the code

- **Adding and removing a member is one value.** `features/membership.tsx`
  stages `add: member` or `delete: member` with a single value, never a
  posted list, because a replacement of the list removes whoever another
  administrator added in between (decisions 2026-09-03 and 2026-09-05). The
  attribute is chosen among the standards-track membership attributes the
  entry's classes permit: `member`, `uniqueMember`, `memberUid`, `memberURL`.
- **A group's membership is expanded through nesting** by `GET /members`
  (`internal/api/expand.go`): at most ten levels and 1,000 reads, a result of
  at most 2,000 members, with cycles, `dangling` and `unresolvable` (a
  `memberUid` login name, a `memberURL` search) reported rather than dropped.
- **"Dangling" means "status unknown", by design.** `expand.go` and the
  `MemberList` schema say it in so many words: a server answers *no such
  object* both for an entry that was deleted and for one this bind may not
  see, so anything built on the list "must not say 'deleted'". Read from the
  code, the list is also wider than that: **any** failed read lands in it,
  a timeout or a refused read included, not only *no such object*.
- **The reverse question has a link.** *Which groups is this in* is a search
  over seven standards-track reference attributes (decision 2026-09-06), and
  `GET /references` says which attribute names an entry.
- **The plan already reports membership.** A plan item carries
  `PlanMembershipChange` -- per membership attribute, the members gained and
  removed, compared as DNs -- and a deletion's `PlanReferenceImpact` counts
  the references it would leave pointing at nothing.
- **The plan already refuses an empty `groupOfNames`.** Removing the last
  value of an attribute the entry's classes require is `invalid` with
  `missing_required_attribute` (`internal/plan/validate.go`). `groupOfNames`
  and `groupOfUniqueNames` require theirs; `posixGroup` (`memberUid`) and
  `groupOfURLs` do not, so those can be emptied without a word.
- **A plan does not check that a member it adds exists.** Nothing reads the
  DN a `member` value names.
- **Rows can be selected** on the search results and the object views
  (`components/entry-table.tsx`); the bulk-edit proposal adds an action there
  (`docs/proposals/bulk-attribute-edit.md`).

## What any design has to keep

- **Every write is a reviewed `ChangeRecord` through `Session.Apply`**
  (section 7, rule 1), staged and reviewed in the changeset.
- **Every search paged and bounded** (rule 4), and a bounded read says when
  it stopped.
- **DNs compared as DNs** (rule 2): `dn.Key`, as the project reader and the
  unmanaged report do since 2026-10-16.
- **Never say "deleted" when the truth is "not found by this bind".**
- **No write the directory would refuse halfway** if Alder can see it coming.

## Part 1: many members at once

### Options

- **A. One change per member**, as today, staged in a loop. No new shape,
  and each record is independent; but a hundred members is a hundred
  records in the review and a twentieth of the changeset's 2,000 for one
  group.
- **B. One `modify` per group, carrying every value**: `add: member` with
  the twenty-five values the group does not already hold, or `delete:
  member` with the ones it does. One record, read as one change.
- **C. A replacement of the member list.** Out, for the reason the membership
  actions were built: it silently removes whoever was added since the list
  was read.

### Recommendation: B

- **Two ways in, one record.** From the group: *Add members…* opens the entry
  picker allowing several picks from a search, and *Remove members…* lists
  the group's own values with checkboxes. From a selection: *Add to a group…*
  and *Remove from a group…* on the selection bar take one group. Either way
  the result is one `modify` of that group, in the membership attribute
  chosen as today.
- **Only values that change anything.** The group is read when the dialog
  opens, and the record holds only the values it does not hold (for an add)
  or does hold (for a remove), compared as DNs. An LDAP modification is
  atomic per entry, and an `add` naming a value already present is refused
  whole by both servers; leaving those out is what makes the record apply.
  The dialog says how many were left out and why (*3 are already members*).
- **Members come from the directory.** The picker searches; there is no
  paste box for DNs. A member added is an entry the directory returned,
  which is what keeps this inside the 2026-09-06 line and keeps the tool from
  creating the dangling values Part 2 exists to clean up.
- **Nesting is respected.** When a picked member is itself a group, the
  dialog expands it with `GET /members` (bounded as today) and refuses a
  member that would make a cycle, naming the chain: *`cn=platform` already
  contains `cn=engineering` through `cn=backend`*. A group that already
  reaches a person through nesting is told so (*ana is already in
  `cn=engineering` through `cn=backend`*) and the add is still allowed, since
  a direct and an indirect membership are different facts.
- **Concurrency.** The record is planned like any other, and its baseline
  binds the membership attribute it touches. A member added or removed by
  someone else between the plan and the apply makes it `plan_stale`, refused
  with nothing written, and planning again shows the new list. That is
  stricter than the single-value actions, which succeed for both
  administrators; it is the honest price of one record, and it fails safe.
- **Bounds.** At most 1,000 values in one record. Both servers accept far
  larger modifications; the limit is the review, and the plan's
  `membership.gained` is what the changeset shows instead of a thousand
  lines of LDIF it would otherwise lead with.

## Part 2: members nobody can find, and groups that would be left empty

### Options for finding them

- **A. A read per member value**, as `GET /members` does. Exact, and bounded
  at 1,000 reads, which one large group exhausts.
- **B. One paged search of what exists, then a comparison.** Under each
  naming context the RootDSE announces (never the configuration or schema
  trees), a paged search for every entry's name only (`1.1`), at most 50,000
  entries, the ceiling a snapshot capture has; then each group's DN-valued
  membership values are looked up by `dn.Key`. A value under no naming
  context that was read is reported as *not checked*, not as missing.
- **C. Ask the server.** Neither server offers a "dangling references"
  query, and OpenLDAP's `refint` and 389 DS's Referential Integrity plugin
  act on deletes going forward; they do not report what is already there.

### Recommendation: B

It costs one bounded search per naming context however many groups there
are, it is whole or refused like a capture (a directory past 50,000 entries
is told so, and the report is not made rather than made from part of the
tree), and it uses the comparison the unmanaged report already uses.

### The report

On the Groups view, *Check membership* runs `POST /members/audit` over the
groups below the view's base, at most 2,000 groups, and shows:

- **Members not found**, per group and attribute: values naming an entry
  that the search did not return. Headed, always, by the identity that ran
  it: *Not found by `cn=alder,ou=services,…`. An entry this bind may not see
  is reported the same way as one that was deleted.* Never the word
  "deleted".
- **Values not checked**: values outside every naming context read, and
  values that are not DNs (`memberUid`, `memberURL`), counted and listed.
- **Groups that would be left empty** if every value not found were removed,
  counting all their membership attributes. For each it says whether its
  classes **require** a member (`groupOfNames`, `groupOfUniqueNames`: the
  plan would refuse the removal) or merely permit one (`posixGroup`,
  `groupOfURLs`: the removal would leave it empty).
- **Groups left with no person through nesting**: a group whose remaining
  members are only groups that would themselves be empty. Computed from the
  same read, not from a fresh walk, and marked as such.
- **Cycles** among the groups read, as `GET /members` reports them.

### The clean-up

- **Chosen, then staged, then reviewed.** Nothing is selected to begin with;
  *Select all not found* is one click, under the line naming the bind. The
  choice becomes one `delete` of the chosen values per group and attribute,
  staged through `stageChanges`, so an over-full basket refuses the whole
  set.
- **Checked again before it is staged.** The chosen values are looked up once
  more, by DN, and any now found is dropped and named, so a member re-created
  since the report is not removed. A value found between staging and apply is
  not caught: the report says when it was made, and the changeset says when
  its plan was.
- **A group that would be left empty is left out of the clean-up** and listed
  with the reason, rather than staged to be refused. Its last value can be
  removed with the group's deletion, which is the existing *Stage deletion*,
  reached from the list, with the plan's reference impact as usual. Alder
  never adds a placeholder member to keep a group valid: that is a value
  nobody authored.
- **Read-only** shows the report and offers no clean-up.

### API delta (1.x: only additions)

```yaml
/members/audit:
  post:
    operationId: auditMembers
    summary: Membership values that name no entry this bind can find, and the groups a clean-up would empty
    requestBody:
      base: string            # where the groups are; a DN
      limit: integer          # groups, 1..2000, default 500
    responses:
      "200": MemberAudit
      "400": bad base; a base under the configuration or schema tree
      "401": not connected
      # a naming context holding more than 50,000 entries is refused with
      # the status and code a data capture uses for the same ceiling, and
      # no report is made: whole or not at all

MemberAudit:
  required: [bind, readAt, groups, truncated, contexts]
  properties:
    bind: string                     # the identity that ran it
    readAt: string (date-time)
    contexts: [ { dn, entries } ]    # what was read to check against
    truncated: boolean               # more groups than `limit`
    groups:
      - dn: string
        classesRequireMember: boolean
        notFound: [ { attribute, value } ]
        notChecked: [ { attribute, value, reason } ]   # outside_contexts | not_a_dn
        remaining: integer          # values left if every notFound is removed
        noPersonThroughNesting: boolean
    cycles: [ string ]
```

Nothing else changes. Adding and removing many members, and the clean-up,
are `ChangeRequest`s the changeset already takes, and the re-check before
staging uses `GET /entry` (a `404` is *not found*, anything else keeps the
value and says so). `GET /members` is unchanged.

### Tests

- **Conformance, on both servers**, with fixtures the case creates and
  removes below its own `ou`, so the 320-entry seed stays identical: a
  `groupOfNames` with one member that exists and one DN that never existed
  (both servers accept an unresolved DN-syntax value); a `groupOfNames`
  whose only member never existed; a `posixGroup`. The audit reports the
  missing values and the two empties with the right `classesRequireMember`;
  the clean-up of the first group applies and is read back through the
  driver; the second is left out; the plan of a removal of its last value is
  `invalid` with `missing_required_attribute`. A many-member add where one
  member is already present plans and applies with that one left out.
- **Unit**: the comparison by `dn.Key` (case, spacing, a multi-valued RDN,
  `uniqueMember`'s `#'…'B` suffix), the bounds, the cycle refusal on add, and
  the HTTP layer's refusals.
- **Playwright journeys**: add five people to a group from a search
  selection, and remove a value not found from the Groups view; both checked
  with `ldapsearch` in each server's container.

## What it will not do

- **Say a member was deleted.** It says the bind did not find it.
- **Resolve `memberUid` login names or evaluate `memberURL`.** Both are
  listed as not checked. A `memberUid` audit means a search per name and a
  guess at which attribute holds it; it can be its own proposal.
- **Clean up automatically, on a schedule, or on delete.** That is
  referential integrity, which both servers implement and Alder does not
  configure.
- **Add placeholder members**, or remove a group's last required member
  except by deleting the group, explicitly, through the existing path.
- **Accept member DNs from pasted text or a file.** Members come from a
  search of the directory.
- **Replace a member list**, in any dialog.
- **Touch the configuration or schema trees.**

## Does it need a charter change?

**No.** Reading membership is the entry viewer and search (section 2, items
4 and 8, and the expansion of 2026-09-07); every write is a staged,
reviewed modification (item 9) of entries the directory already holds,
inside the 2026-09-06 line. The decision entry, when accepted, should record
the "not found by this bind" wording as a rule, so no later screen turns the
report into "deleted members".

## Open questions for review

1. **Should `GET /members`'s `dangling` stop collecting every failed read?**
   Today a timeout or a refused read is listed as dangling. Recommendation:
   a separate `fix(api)`: only *no such object* is dangling, anything else
   is reported as an error on the walk, because the audit will reuse the
   wording and should not inherit the gap.
2. **One record per group for many members (B), or one per member (A)?**
   Recommendation: B, for the review; A only if the stricter concurrency of
   B turns out to bite in practice.
3. **The 50,000-entry ceiling.** Recommendation: the capture's, so one number
   means "too large to read whole" everywhere. A directory beyond it can run
   the audit on a smaller base once the base narrows what is read, which is
   a later slice.
4. **Report referential integrity's state** (OpenLDAP `refint`, 389 DS's
   plugin) above the audit? Recommendation: not in this slice; it is a
   configuration read with its own vendor-model question (charter, section
   3), and the audit's answer stands either way.
