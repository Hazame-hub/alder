# The Alder project format

**Status: proposed, for review. Nothing here is implemented.** Confirmed as a
scope change on 2026-10-10 as a specification only (see `docs/DECISIONS.md`).
Code waits until this has been reviewed and the open questions at the end are
answered.

---

## What it is for

Today Alder can show what an LDIF document would do to a directory
(`alder plan --mode desired people.ldif`) and apply exactly that. What it cannot
do is hold a directory's intended state *as a project*: several files, more than
one environment, and a statement of which parts of the tree the project is
responsible for.

An Alder project is a directory in a repository, beside the rest of the
infrastructure code:

```
directory/
├── alder.yaml
├── people/
│   ├── engineering.ldif
│   └── operations.ldif
└── groups.ldif
```

`alder project plan --env dev` answers, for the dev directory: *if this
repository is the truth, what would have to change?* It answers with the plan
Alder already produces, made against the directory as it is now.

### What it is not

- **Not a state store.** There is no state file, lock or cache. Every plan is
  made against the live directory, as every plan is today. Alder stays
  stateless (charter, section 2).
- **Not a deleter.** An entry the project's files do not mention is never
  deleted, in any mode. Desired state already works this way
  ([PLAN.md](PLAN.md), "What absence means") and a project does not change it.
  What a managed subtree adds is a *report* of such entries, never an action
  (see "Unmanaged entries").
- **Not a templating language.** No variables, loops or conditionals in the
  first version. The LDIF a person reviews is the LDIF that is planned.
- **Not a place for secrets.** `alder.yaml` names where a password comes from,
  never the password.

---

## `alder.yaml`

```yaml
version: 1

managed:
  - base: ou=people,dc=example,dc=com
    files: [people/*.ldif]
  - base: ou=groups,dc=example,dc=com
    files: [groups.ldif]

environments:
  dev:
    api-url: https://alder.dev.example.com
    host: ldap.dev.example.com
    ca-file: certs/dev-ca.pem
    bind-dn: cn=alder,ou=services,dc=example,dc=com
    bind-password-env: ALDER_DEV_BIND_PASSWORD
  prod:
    api-url: https://alder.example.com
    host: ldap.example.com
    tls: starttls
    port: 389
    server-name: ldap.example.com
    bind-dn: cn=alder,ou=services,dc=example,dc=com
    bind-password-file: /run/secrets/alder-prod
```

### `version`

Required, and `1`. A file with any other version is refused, not guessed at.

### `managed`

The subtrees the project is responsible for, in order. Each has:

| Key | Meaning |
|---|---|
| `base` | The subtree's root DN. Parsed as a DN, never compared as text. |
| `files` | The LDIF files that describe it: paths or globs, relative to `alder.yaml`. A glob matches in sorted order, so the plan is the same on every machine. |

Rules, all checked by `validate`:

- Every file is **content records only**, the same rule as `--mode desired`. A
  `changetype` record is refused: a project describes state, it does not issue
  operations.
- Every entry lies **at or below** its subtree's `base`. An entry outside it is
  refused, because the project would be claiming something it says it does not
  manage.
- **Subtrees do not overlap.** Two bases where one is at or below the other is
  refused. Each entry belongs to exactly one subtree, so ownership has one
  answer.
- **No entry appears twice**, across all files.

Order matters only as it already does in a plan: within the plan, records keep
file order, and subtrees keep `managed` order. A parent should come before its
children, and the plan's existing ordering warnings say so when it does not.

### `environments`

A named set of connection settings. The keys are exactly the client's
connection flags ([CLI.md](CLI.md), "Connecting"), spelled the same way:
`api-url`, `api-ca-file`, `host`, `port`, `tls`, `ca-file`, `server-name`,
`bind-dn`, `config-bind-dn`, and so on.

A password is named, never written:

| Key | Meaning |
|---|---|
| `bind-password-env` | The name of an environment variable holding it |
| `bind-password-file` | A file whose first line is it |

A key called `bind-password`, or any key the format does not define, is
refused. An unknown key is far more likely to be a typo or a pasted secret than
an extension.

Deliberately **not** settable in `alder.yaml`, for the reason they are not
settable from the environment today: anything that confirms or widens a write.
There is no `yes`, `allow-deletes`, `force` or `insecure-skip-verify` key.
Which environment a command runs against is chosen by `--env` on the command
line, and only there.

Every environment manages the same subtrees, with the same files. An
environment differs in *where* it connects, not in *what* it holds; see the open
questions for environments whose suffixes differ.

---

## Commands

### `alder project validate`

Checks the project without connecting to anything:

- `alder.yaml` parses, has `version: 1`, and has no unknown keys;
- every glob matches at least one file, and every file is readable;
- every file is LDIF of content records only;
- every entry lies in its subtree, subtrees do not overlap, and no DN repeats;
- every environment names a password source and no password.

It does not check the schema, because the schema belongs to a directory. That is
what `plan` is for.

Exit codes follow the existing table: 0 valid; 3 for a project that is not
valid, which is that code's existing meaning ("cannot be applied as written;
nothing was written"); 7 for a wrong command line; 8 for a file that cannot be
read.

### `alder project plan --env NAME`

1. Validates, as above; an invalid project is not planned.
2. Connects with the environment's settings, as any client command does.
3. Sends the project's records to `POST /api/v1/plan` as one desired-state LDIF
   document, in the order described above.
4. Prints the plan exactly as `alder plan` does, with the same `--json` output
   and the same exit codes.

That is the whole engine. Reconciliation, the schema checks, membership and
reference impact, recovery assessment and baselines are the existing plan's; the
project adds a file format and the decision of which records to send.

### Later slices, named so the first one does not paint them into a corner

- **`alder project apply --env NAME`**: plan, show, confirm, apply, with
  `alder apply`'s confirmation rules unchanged (`--yes`, `--allow-deletes`,
  refusing a plan with conflicts).
- **Unmanaged entries.** For each managed subtree, the entries the directory
  holds that no file mentions, listed beside the plan as *unmanaged*. Read with
  the existing snapshot capture of the subtree; reported, never deleted. A
  project that wants one removed says so with an explicit delete, which the
  project format does not express, so it goes through `alder apply` like any
  other deliberate deletion.
- **The web interface.** Opening a project in the browser is not in this
  specification.

---

## Open questions for review

1. **Where `validate` reads LDIF.** The client commands have no LDIF code by
   design: "It does not parse LDIF" ([CLI.md](CLI.md)). An offline `validate`
   needs to parse it. Options:
   - (a) parse locally with `internal/ldif`, the same package the server uses,
     so it is the same code and not a second implementation, and amend the CLI
     rule to say "no LDAP code, no second LDIF parser";
   - (b) make `validate` ask a running Alder server, which keeps the rule but
     means validating a file needs a server.

   **Recommendation: (a).** A validator that needs a server will not run in a
   pre-commit hook, and the rule exists to prevent two implementations
   disagreeing, which reusing the server's own package does not risk.

2. **Environments with different suffixes**, `dc=dev,dc=example,dc=com` against
   `dc=example,dc=com`. Supporting it means rewriting every DN in every file per
   environment: done with the `dn` package it is safe, but it is the first
   feature in which what is planned is not byte-for-byte what was reviewed.
   **Recommendation: not in version 1.** Same DNs in every environment; revisit
   with a real user who needs it.

3. **Attributes owned by one environment**, such as a value that differs
   between dev and prod. Same trade-off as 2, and the same recommendation:
   not in version 1.

4. **Files other than LDIF.** YAML entries would be friendlier to write and
   review. **Recommendation: LDIF only.** It is the format every directory tool
   already reads, the one Alder previews and exports, and adding a second one is
   a decision for when somebody asks.

5. **Name and location.** `alder.yaml` at the project root, found from the
   current directory or given with `--project PATH`. Searching parent
   directories, as git does, is easy to add and hard to take back; the first
   version does not.
