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
    ├── github-actions.yml copy to .github/workflows/directory.yml
    └── gitlab-ci.yml      copy to .gitlab-ci.yml, or include it
```

## Try it

```sh
alder project validate --project examples/project/directory
```

`validate` reads the files and connects to nothing: it checks that every
entry is whole, sits under its subtree, appears once and carries no secret.
Exit 0 is valid, 3 is not, with every problem listed.

To plan against a directory you have, change the bases, the DNs and the
environments to yours, set the password variable an environment names, and:

```sh
export ALDER_STAGING_BIND_PASSWORD=...   # from your secret store
alder project plan --project directory --env staging --detailed-exitcode --summary
```

`--detailed-exitcode` makes the exit status the answer: 0 the directory
matches the repository, 2 changes are pending, 3 some change cannot be
applied as written. A plan never writes; `alder project apply --env
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

## What is deliberately not here

- **Applying from CI.** `alder project apply --env NAME --yes` does it in one
  run, planning again against the directory as it is and applying exactly
  that plan. Whether a merge should write to production without a person is
  your call, not an example's; if you add it, give it its own job, a protected
  environment and `--recovery-out` so the run leaves a recovery bundle.
- **Deleting.** A project never deletes an entry its files do not mention.
- **Secrets.** `validate` refuses a `userPassword` or any other attribute
  Alder treats as sensitive, in any file.
