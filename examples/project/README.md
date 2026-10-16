# An Alder project, checked in CI

A small, working Alder project and the CI that goes with it: validate every
change offline, and check on a schedule that each directory still matches
the repository. [`docs/PROJECT.md`](../../docs/PROJECT.md) is the format's
reference and [`docs/CLI.md`](../../docs/CLI.md) the command line's.

```
examples/project/
├── directory/             the project: copy this into your repository
│   ├── alder.yaml
│   ├── groups.ldif
│   └── people/
│       ├── engineering.ldif
│       └── operations.ldif
└── ci/
    ├── github-actions.yml        copy to .github/workflows/directory.yml
    ├── github-actions-apply.yml  optional: copy to .github/workflows/directory-apply.yml
    ├── gitlab-ci.yml             copy to .gitlab-ci.yml, or include it
    └── gitlab-ci-apply.yml       optional: include it beside gitlab-ci.yml
```

## Try it

```sh
alder project validate --project examples/project/directory
```

`validate` reads the files and connects to nothing: it checks that every
entry is whole, sits under its subtree, appears once and carries no secret.
Exit 0 is valid, 3 is not, with every problem listed.

`alder project fmt` rewrites the LDIF in the form Alder's own exports take,
so a diff shows what changed rather than how it was wrapped; `fmt --check`
writes nothing and exits 1 if a file is not canonical. These files already
are. Once the release you pin has `fmt`, add `alder project fmt --check
--project directory` to the validate job.

To plan against a directory you have, change the bases, the DNs and the
environments to yours, set the password variable an environment names, and:

```sh
export ALDER_STAGING_BIND_PASSWORD=...   # from your secret store
alder project plan --project directory --env staging --detailed-exitcode --summary
```

`--detailed-exitcode` makes the exit status the answer: 0 the directory
matches the repository, 2 changes are pending, 3 some change cannot be
applied as written. Entries the directory holds under a managed subtree that
no file mentions are listed beside the plan as unmanaged: reported, never
deleted. A plan never writes; `alder project apply --env
staging` shows the same plan, asks, and applies it.

## What the CI does

| Job | Runs on | Needs | Fails when |
|---|---|---|---|
| validate | every pull or merge request, and the default branch | nothing: no server, no password | the project is not valid |
| plan | the default branch, and a schedule | a runner that reaches your Alder server, and one password per environment | an environment differs from the repository (2), or cannot be planned |

**Why plan never runs on a pull request.** An environment in `alder.yaml`
names both the Alder server and the password variable, so whoever can change
`alder.yaml` can choose where that password is sent. `validate` holds no
password and is safe on any pull request, a fork's included. `plan` holds
one, and runs only on changes that have already been reviewed and merged.
Keep it that way when you adapt these files.

**One password per environment.** The GitHub workflow hands each matrix job
only its own environment's secret. In GitLab, store each password as a
masked, protected variable, so only protected branches see it; both
variables are visible to both plan jobs there, so scope them to GitLab
environments if that matters to you.

**Pinned client.** Both files install a fixed release from GitHub and check
it against the release's `checksums.txt`. Update `ALDER_VERSION` on purpose,
not by accident.

## Applying, with a reviewed plan

The two `-apply` files are optional and apply the repository to an
environment, with a person between the plan and the write. A first job saves
the plan with `alder project plan --out`, shows it, and keeps the file. A
second job applies **that file** with `alder project apply --plan --yes
--recovery-out`, only once someone approves it:

| File | Starts | Approval |
|---|---|---|
| `github-actions-apply.yml` | by hand: Actions > directory-apply > Run workflow, choosing the environment | the GitHub environment of the same name: give it required reviewers |
| `gitlab-ci-apply.yml` | on the default branch: one saved plan per environment, then a manual apply job each | the GitLab protected environment of the same name |

What is approved is the saved file, and the apply holds to it. It is refused
before connecting (exit 3) when the project files, the host, the bind or the
suffix are not the ones it was saved for, and refused whole (exit 4) when any
change no longer holds because the directory moved since the plan. Nothing
is written then: plan again and review that. A partial apply exits 6 and
the job keeps the recovery bundle as an artifact.

Both pin 1.39.0, the first release with saved plans. On GitHub, without
required reviewers on the environment, the apply runs as soon as the plan is
saved: set them up before the first run. These jobs hold a password and
never run on a pull or merge request, for the reason given above.

**Signed plans.** The approval says a person looked; a signature says
which file they looked at, wherever it travels next. To use one, the
reviewer downloads the saved plan, reads it, signs it with `alder sign
saved-plan.json --key reviewer.pem --signer "$NAME" --output
saved-plan.signed.json`, and hands that file to the apply job, which runs
`alder project apply ... --plan saved-plan.signed.json --trusted-keys
/etc/alder/reviewers/`. A plan nobody signed, or signed by a key not in that
directory, is then refused (exit 3), and one changed after it was signed
(exit 8), before connecting. Keep the reviewers' public keys on the runner
or somewhere only they can change, not in files a pull request can edit.
`github-actions-apply.yml` marks where the flag goes; how the signed file
reaches the apply job (a release asset, a protected bucket, an artifact the
reviewer uploads) depends on your setup, so the example leaves it out. See
[`docs/SIGNING.md`](../../docs/SIGNING.md#signing-a-saved-plan).

## What is deliberately not here

- **Applying without a person.** The apply files below always wait for
  someone: a protected environment's reviewers on GitHub, a manual job on
  GitLab. `alder project apply --env NAME --yes` plans and applies in one
  run with nobody in between; whether a merge should do that to production is
  your call, not an example's.
- **Deleting.** A project never deletes an entry its files do not mention.
- **Secrets.** `validate` refuses a `userPassword` or any other attribute
  Alder treats as sensitive, in any file.
