# Saved plans

**Status: accepted 2026-10-15** -- expectations rather than a persistent
key (question 1), and an apply refuses project files that changed since
the plan (question 3); the other questions as recommended. **The first
slice is built**: `project plan --out` and `project apply --plan`. What the
build settled is recorded in `docs/DECISIONS.md` under the same date, and
marked *Settled when built* below where it differs from what was proposed.

`docs/PROJECT.md` said "a saved-plan workflow waits until baselines can
outlive a process"; this proposal argued that the wait was unnecessary: a
saved plan does not need baselines that outlive a process, and making them
do so would break the charter. It followed the way the project format, the
second server and the web projects were proposed: a specification first,
accepted or amended before any code.

---

## The problem

`alder project plan --env prod` shows what would change, and
`alder project apply --env prod` plans again and applies what it just
showed. Between the two there is nothing to carry: a review of the first
plan approves "whatever the directory needs at merge", not "these
operations". That is the decision recorded on 2026-10-11 (one run, not a
saved plan), and it holds for a person at a terminal.

It does not hold for the workflow teams ask for:

1. a job plans `prod` and stores the plan;
2. somebody reviews that plan -- a GitHub environment's required reviewer,
   a GitLab manual job, a change board;
3. a later job applies **exactly what was reviewed**, or refuses.

Today step 3 can only plan again. If the directory moved between 1 and 3,
the apply shows a different plan to a job nobody is watching (`--yes`), and
the reviewed plan was a description of a change that did not happen. That
is the gap the "no write without a confirmed ChangeRecord" rule exists to
close.

## Why not make baselines outlive a process

A baseline is two MACs under a 32-byte key that exists only in the server
process's memory (`internal/plan/baseline.go`). Making a saved plan carry
baselines means giving that key a life beyond the process:

- **A key on disk, or in an environment variable** (`--baseline-key-file`).
  The charter (section 2) says Alder is stateless, and SIGNING.md says the
  server "holds no secret on disk". This would be the first. Every replica
  behind a load balancer would need the same key, and rotating it would
  invalidate every saved plan at once, which is the restart problem again,
  scheduled.
- **The key is also what keeps a token opaque.** A secret's binding is an
  HMAC under a key derived from it, scoped to the session. A key that lives
  longer, and is readable by whoever runs the server, turns every saved
  token that binds a password into an offline guessing target. Session
  scoping would also make any such token fail in the applying session, so
  the feature would not work for exactly the changes it binds most
  tightly.
- **A token proves nothing a reviewer can read.** It is opaque by design.
  What a reviewer needs is a file that says what will run and what it
  assumes, which a token is not.

Rejected for the same reasons: storing plans on the server and applying by
plan id (persistence, rejected already on 2026-09-13), and signing tokens
with a key pair (a key the server holds is still a secret on disk).

## Recommendation: a saved plan is exact changes with expectations

Alder already has a document that is planned in one place, carried as a
file, survives any number of restarts, and applies later only if the
directory is still in the state it was written against: the **recovery
bundle**. Its compensating changes are exact operations, each with an
`expect` (1.9) -- "this entry must hold exactly these values" -- which a
plan checks (`conflict`, `expected_state_differs`) and then binds into the
fresh plan's baseline, so a change made between that plan and the apply is
`plan_stale` as well.

A saved plan is the same idea pointed forwards:

- **What is saved is the plan's own records**, the operations the planner
  produced (a desired-state record reconciled into an `add`, or a `modify`
  that replaces the attributes it names), not the project's LDIF. Applying
  it plans these **exact** changes; an exact change is never rewritten, so
  what runs is literally what the file says.
- **Each change carries an expectation of the state it was planned
  against**: for a `modify`, the attributes its baseline depended on, with
  the values the directory held, an attribute with no values meaning
  absent. An `add` needs none: planning an `add` of an entry that now
  exists is already a conflict (`entry_exists`). The expectation is
  compared by each attribute's equality rule, as recovery bundles' are.

  *Settled when built:* the expectation names the attributes the
  modification replaces, read from the entry once the plan is made. Those
  are what make the reviewed change "these values become those"; an
  attribute the project names and the plan leaves alone is drift for the
  next plan to show, not a reason to refuse the reviewed change. The saved
  changes are then planned once more, exactly as an apply will plan them,
  and the file is written only if every one applies, which closes the gap
  between the plan and the reads behind the expectations (exit 4 when it
  does not).

  *Fixed 2026-10-16:* "every one applies" did not close that gap: an edit
  between the plan and the read was written into the expectation and
  overwritten at apply, unseen. The saved changes' plan must now also be
  the one shown, item by item: the same action, the same effect on group
  membership and the same references found, or the file is not written
  (4).
- **Applying a saved plan** sends those changes to `POST /plan` as
  `changes`, refuses if any comes back as a conflict (an expectation
  that no longer holds, an entry that now exists) or `invalid`, shows it,
  and applies with the **fresh** baselines. The window between that plan
  and the apply is covered as it is today.

What this keeps:

- **No new server state, key or endpoint.** The server sees a plan of exact
  changes with expectations, which it has handled since 1.9. A restart, a
  second replica or an upgrade between plan and apply changes nothing.
- **The rule in PLAN.md still holds.** "A stale or mismatched plan is never
  replanned and applied on the caller's behalf": the apply plans the saved
  operations themselves, and refuses rather than applies when that plan is
  not the saved one. Nothing the reviewer did not see runs.
- **Secrets cannot reach the file.** A project refuses sensitive attributes
  already, and an expectation refuses them as well. A plan carrying a value
  the server withheld is refused when saving, as staging one in the browser
  is, with the same pointer to `project apply`.
- **The file is readable.** A reviewer, or a pull request comment, can show
  it as LDIF beside what each change assumes.

What it changes about the guarantee, said plainly: a token covers exactly
the attributes a change depended on, and so does the expectation, with one
difference. A baseline also covers the operation through a MAC, which made
"the request is not the plan" detectable by the server; here the
operations are the file. Anyone who can edit the file between plan and
apply can change what runs. In CI that is whoever can change the workflow
run's artifacts, who could equally change the workflow. See open question
3 for binding it to the project.

*Fixed 2026-10-16:* the digest bound the project's files and nothing bound
the changes to them, so editing `changes` alone still changed what ran. A
project's saved plan is now applied only when every change is one the
project's desired state makes for the environment: an add of an entry the
files describe, exactly as they describe it, or a `replace` of an attribute
they name on that entry, to the values they give it, compared under the
environment's suffix and the schema's names for each attribute. Every
modification, of either kind of plan, must also expect each attribute it
changes. A change set's saved plan has nothing to be bound to: the file is
the input, and what vouches for it is who could write it, or a signature.

## The commands

```console
$ alder project plan --env prod --out prod.plan.json
$ alder project apply --env prod --plan prod.plan.json [--yes]
```

- `--out FILE` writes the saved plan after printing the plan as usual. The
  exit status is unchanged, `--detailed-exitcode` included, so a job can
  save the plan and know whether there is anything in it. A plan with a
  conflict or an `invalid` item is still printed and is not saved: there is
  nothing an apply could do with it (exit 3, as today).
- `--plan FILE` on `project apply` reads the file instead of planning the
  project. The project is still read and validated, for `alder.yaml`, the
  environment and its credentials; its LDIF is not planned.
- The environment is part of the file: its name, its `host:port`, the bind
  DN, the configuration bind DN when it has one, and any suffix rebase
  (#233). An apply for another `--env`, or for one whose `host`, `port`,
  bind or configuration bind no longer match, is refused before
  connecting (exit 3). This restores what PROJECT.md's first draft said ("a
  plan carries the environment it was made for, and `apply` refuses one
  made for a different `--env`"), which the 2026-10-11 decision had to
  drop.
- A saved plan that no longer holds -- a change is now a conflict because
  its expectation fails or its entry now exists -- exits **4**, the code
  `plan_stale` already has ("the directory changed after it was made"),
  rather than the 3 a fresh conflict gets, and prints the changes that
  moved. Nothing is written. The way forward is a new plan and a new
  review.
- An entry the saved plan found `unchanged`, and somebody has edited since,
  is not part of the apply: the apply runs the reviewed changes and nothing
  else. The next `project plan` shows the drift, as it does today.
- An empty saved plan applies nothing and exits 0.

## The file

JSON, versioned the way the recovery bundle is, and read strictly: a field
the reader does not know is refused rather than half-read, and so is a key
in another case than the format's or a key given twice in one object
(*fixed 2026-10-16*: Go's decoder accepted both, the last of two winning).

```json
{
  "format": "alder-plan",
  "version": 1,
  "createdAt": "2026-10-15T08:00:00Z",
  "alderVersion": "1.39.0",
  "project": { "digest": "sha256:…" },
  "environment": {
    "name": "prod",
    "host": "ldap.example.org:636",
    "bindDn": "cn=alder,ou=service,dc=example,dc=org",
    "configBindDn": "cn=Directory Manager",
    "rebase": { "from": "dc=example,dc=test", "to": "dc=example,dc=org" }
  },
  "changes": [
    {
      "dn": "uid=alice,ou=people,dc=example,dc=org",
      "type": "modify",
      "mods": [{ "op": "replace", "name": "mail", "values": [{ "text": "alice@example.org" }] }],
      "expect": { "attributes": [{ "name": "mail", "values": [{ "text": "a@old.example.org" }] }] }
    }
  ],
  "unchanged": 41
}
```

*Settled when built:* `project` carries the digest alone. A name would be
the path the plan job happened to run in, which the apply job does not
share. The digest is the SHA-256 of the project's entries rendered as one
LDIF document, as the files write them, before any environment's suffix;
the suffix is compared on its own, in `environment.rebase`.

*Settled with #243:* `environment.configBindDn` is the configuration bind
DN (`config-bind-dn` in `alder.yaml`, `--config-bind-dn` for `alder plan`)
the plan was made with, and is absent when it was made with none. A plan
can hold `cn=config` and schema changes, which were read and reviewed as
that identity's; an apply whose configuration bind differs, including one
given where the file has none or none given where it has one, is refused
before connecting (3). It is compared as `bindDn` is, without regard to
case. The field is optional, so a file without it is the file of a plan
made with no configuration bind, and the reader stays strict otherwise.

`changes` are `ChangeRequest`s, the object `POST /plan` and
`/changeset/apply` already take. `unchanged` is a count, for the summary
line the apply prints; nothing is checked against it. Baselines, previews and the
unmanaged-entries report are not saved: the first is meaningless outside
the process, the others are recomputed.

## CI

`examples/project/` gains an optional third job, documented and off by
default: on `workflow_dispatch` or a push to `main`, `plan` writes
`prod.plan.json` as an artifact; an `apply` job in a GitHub environment
with required reviewers downloads it and runs `project apply --plan
--yes`. The reviewer approves the plan shown in the first job's log and
summary, and the second job applies that or fails with exit 4. GitLab gets
the same with a manual job. The `plan` job still never runs on a pull
request, for the reason in the example's header: it holds a password and a
pull request can change `alder.yaml`.

## Scope

**First slice:** `project plan --out`, `project apply --plan`, the file
format and its strict reader, the environment check, exit 4 on a plan that
no longer holds, and a conformance case on both servers: plan, save, edit
an entry behind the plan's back, apply and see it refused; plan, save,
restart the Alder server, apply and see it applied.

**Later, if asked for:** `alder plan --out` / `alder apply --plan` for any
change set, not only projects (*built 2026-10-16*: an LDIF document or
`--changes`; a deletion carries an exhaustive expectation; renames, password
changes, `--recovery` and `--package` are not saved); loading a saved plan in the web interface
next to *Load recovery bundle*; a signed plan, wrapped with `alder sign`
as SIGNING.md wraps any document.

**Out:** a persistent baseline key; plans stored on the server; applying a
saved plan that partly holds (it is all or nothing, as every apply's
up-front check is).

## Open questions for review

*Decided 2026-10-15: 1 expectations, 3 refuse; 2, 4 and 5 as recommended.*

1. **Persistent key or expectations.** Recommendation: expectations, for
   the reasons above. Choosing the key instead means amending the charter
   (section 2), and this proposal would be rewritten.
2. **What an `add` expects.** An `add` relies on `entry_exists` and carries
   no expectation. Should it also expect the parent to exist? Recommendation:
   no; a missing parent is already a conflict when planned.
3. **Binding the plan to the project.** Should `apply --plan` refuse when
   the project's LDIF in `--project` no longer renders the digest saved in
   the file? It stops an apply from a checkout other than the one planned,
   and makes editing the artifact alone not enough to change what runs. It
   also refuses the case where the plan job ran on one commit and the
   apply job checks out a later one. Recommendation: yes, refuse, and say
   which; a workflow that wants the later commit plans it again.
   *Completed 2026-10-16:* the digest alone bound the files, not the
   changes; an apply now also refuses any saved change the project would
   not make.
4. **How long a saved plan stays valid.** Recommendation: no expiry. The
   expectations already say whether it still holds, and a date would refuse
   plans that are still exactly right.
5. **What a reviewer reads.** Recommendation: the job log and the step
   summary print the plan as `project plan` does; the file itself is for
   the apply. `alder project show-plan FILE` is not added unless asked for.
