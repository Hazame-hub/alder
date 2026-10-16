# Access rules in recovery bundles

**Status: accepted (2026-10-16): option B, built in this PR.**
It settles the inconsistency the schema and configuration recovery entry in
`docs/DECISIONS.md` (2026-10-13) left open on purpose: "A data-tree `aci`
modification is still compensated as it has been since 1.9. That is
inconsistent and is left alone here [...] and deserves its own decision."

---

## What is true today

Read from the code on `main`, not from the documents, and not exercised on
a server for this proposal:

| Path | `aci` on a data entry | `olcAccess`, or `aci` in the configuration tree |
|---|---|---|
| Entry editor | Not offered (`internal/api/elsewhere.go`): shown read-only, with a sentence saying why | Not offered, the same way |
| A change Alder is sent as exact operations (`changetype:` LDIF, a changeset, `alder apply`) | Planned and applied like any attribute | Planned and applied, with the lockout warning (`internal/api/configwarn.go`: "None of these block") |
| Desired state (an LDIF of entries, a project) | Left alone: 389 DS defines `aci` as operational, and reconciling skips what the directory owns (`internal/plan/reconcile.go`, `directoryOwns`) | Not reached: desired state is for the data tree |
| A recovery bundle for such a change | Compensated value by value, `exact` (since 1.9) | Not compensated: `partial` or `unavailable`, reason `access_rule_not_written` (since 1.38) |

The charter (section 2) says access rules "are not evaluated, translated or
written: the editor shows `aci` and `olcAccess` read-only." What the code
does is narrower: **Alder never authors an access rule, but it applies one
that a reviewed change carries as an exact operation.** The editor is the only place Alder would
be writing a rule of its own; every other path writes what a person wrote
and reviewed, the same way it writes any other attribute.

The one place that departs from that is recovery in the configuration tree:
a reviewed change replaced `olcAccess`, Alder applied it, and the bundle
then says it cannot undo it, with a reason claiming Alder never writes access
rules -- which it just did.

## Why it matters

The change recovery bundles exist for is the one that went wrong. An access
rule is the change most likely to go wrong in a way that matters: a replace
drops every rule not listed, and first-match ordering means a correct-looking
edit changes what every later rule means. If that change still left the
bind able to write, the bundle is the fastest way back, and today it
declines exactly that step and leaves the operator to rebuild the old rules
by hand from a snapshot. If it locked the bind out, no option below helps:
the compensation fails like any write, and the fix is on the server.

## Options

**A. Never write an access rule, anywhere.** The change path refuses `aci`
and `olcAccess` (a new conflict reason), and recovery stops compensating
data-tree `aci` too. It matches the charter's sentence word for word. It
breaks two things people may rely on: an LDIF import or a changeset
carrying an `aci` modification is refused where it applies today, and a version 1 bundle that
recovers an `aci` change today comes back `partial`. Both are behaviour
changes under the 1.x compatibility promise.

**B. Write what a reviewed change says; never author one.** The charter
sentence becomes what the code does: the editor never offers access rules
and Alder never evaluates or translates them, but a change or a bundle that
carries one is planned, shown, warned about and applied like any other. The
configuration-tree compensation then follows the rule every other setting
already follows: replace the attribute with the values the server stored
before, positions (`{n}`) and spelling included, under an expectation of
what the change left. `access_rule_not_written` stays in the enum (1.x keeps
it) and is no longer produced. The compensation carries the same lockout
warning the forward change did, because it is the same kind of write.

**C. Leave it, and document it.** RECOVERY.md and the charter say that a
data-tree `aci` is compensated and a configuration-tree rule is not, and
why: the configuration tree's rules are ordered and server-wide, so a wrong
undo can matter more than a wrong do. Nothing changes for anyone. The cost is
a rule with an exception that a reader has to learn, and the case above
stays a manual rebuild.

## Recommendation: B

- It is what Alder already does on every path but one, so it is the smallest
  true sentence, not a new permission. The charter line is corrected, not
  widened: Alder still writes no access rule of its own.
- It gives the riskiest change an undo that is checked like every other
  undo: the expectation refuses it if anything touched the rules since, and
  the plan shows the rules that would come back before anything is written.
- A refuses things that work today and leaves people with `ldapmodify` for
  the same write, which is the outcome `configwarn.go` was written to avoid.
  C keeps a gap in the one place a recovery bundle is most wanted.

What B would change, when built: `internal/recovery/schemaconfig.go`
(the `accessRule` case, kept for the editor-only meaning or removed);
RECOVERY.md's table row; the charter's section 2 sentence; a conformance
case on both servers that replaces `olcAccess` (OpenLDAP) and a
configuration-tree `aci` (389 DS) with rules that still grant the test
bind, applies the bundle, and reads the rules back identical, positions
included; and a DECISIONS entry. No API shape changes.

## Open questions, decided

1. **Which option.** Decided (2026-10-16): B.
2. **If B, the charter wording.** Decided (2026-10-16), as proposed:
   "Access rules are read and shown against an entry, each server's in its
   own words. Alder does not evaluate, translate or author them: the editor
   shows `aci` and `olcAccess` read-only. A change or recovery bundle that
   carries one is applied like any other change, with a warning."
3. **If B, bundles already written.** Decided (2026-10-16), as recommended:
   a bundle made before the change says `access_rule_not_written` for that
   step and carries no values for it, so it stays `partial`. Nothing to
   migrate; only new bundles compensate.
