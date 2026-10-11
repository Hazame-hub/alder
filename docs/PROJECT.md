# The Alder project format

**Status: the first slice is implemented** -- `alder project validate` and
`alder project plan --env` (see [CLI.md](CLI.md)). Confirmed as a scope change
on 2026-10-10 as a specification and reviewed the same day; the decisions that
review made are at the end, and the sections above reflect them. The later
slices below are not built.

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
  never the password, and the project's LDIF files may not carry one at all
  (see "No secrets in project files").

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
- Every entry is **whole**, with its `objectClass`. The plan may have to
  create any entry, so the server reads each record as a possible add and
  refuses one without object classes, even for an entry that exists. Found
  by the first slice's conformance test, and checked by `validate` so a
  project that validates also plans.
- Every entry lies **at or below** its subtree's `base`. An entry outside it is
  refused, because the project would be claiming something it says it does not
  manage.
- **Subtrees do not overlap.** Two bases where one is at or below the other is
  refused. Each entry belongs to exactly one subtree, so ownership has one
  answer.
- **No entry appears twice**, across all files.
- **No secrets** in any file; see below.

### No secrets in project files

A project lives in a repository, and a repository is the one place a secret
must not be. `validate` refuses any record that carries an attribute Alder
treats as sensitive -- `userPassword`, `unicodePwd`, the configuration tree's
credentials and the rest -- naming the file, the line and the attribute, and
saying why. It is the same set (`schema.IsSensitive`) that keeps those values
out of the entry view, the entry's LDIF and an LDIF export, so a project
cannot hold what Alder would refuse to hand out. Unlike the export, there is
no option to include them: an export is a file a person chose to write, a
project is shared by design.

Setting a password stays what it is today, a deliberate change made through
Alder, never declared state.

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
an extension. It also keeps later additions backward compatible: a key added
in a later version is one no valid version-1 file can already be using.

Deliberately **not** settable in `alder.yaml`, for the reason they are not
settable from the environment today: anything that confirms or widens a write.
There is no `yes`, `allow-deletes`, `force` or `insecure-skip-verify` key.
Which environment a command runs against is chosen by `--env` on the command
line, and only there. The mechanism is the one `--yes` already uses: every
client flag reads `ALDER_<FLAG>` by default, and `--env` is listed in
`envflags.Exclude`, so an inherited `ALDER_ENV=prod` cannot choose production
for a command nobody pointed at it.

Every environment manages the same subtrees, with the same files, under the
same DNs. An environment differs in *where* it connects, not in *what* it
holds. Because of that, a plan for production reads exactly like a plan for
development, which is why the environment and host are printed above every
plan (see `alder project plan`).

**A value that genuinely differs between environments** -- a subtree that
only exists in one, or entries whose content differs -- is a separate project:
its own `alder.yaml`, managing that subtree, with only the environments it
applies to. Two small projects that each mean one thing are easier to review
than one project with conditions in it.

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
4. Prints, **above the plan**, the environment name and the directory host the
   plan was made against -- on standard error with the rest of the
   commentary, and as `environment` and `host` beside the plan with `--json`.
   Identical DNs make two environments' plans look identical; this is what
   tells them apart.
5. Prints the plan exactly as `alder plan` does, with the same `--json` output
   and the same exit codes.

That is the whole engine. Reconciliation, the schema checks, membership and
reference impact, recovery assessment and baselines are the existing plan's; the
project adds a file format and the decision of which records to send.

### Later slices, named so the first one does not paint them into a corner

- **`alder project apply --env NAME`**: plan, show, confirm, apply, with
  `alder apply`'s confirmation rules unchanged (`--yes`, `--allow-deletes`,
  refusing a plan with conflicts). A plan carries the environment it was made
  for, and `apply` refuses one made for a different `--env`.
- **`alder project fmt`**: rewrite the project's LDIF in a canonical form, so
  a Git diff shows what changed and not how it was wrapped. A tidier file, not
  a second format.
- **Unmanaged entries.** For each managed subtree, the entries the directory
  holds that no file mentions, listed beside the plan as *unmanaged*. Read with
  the existing snapshot capture of the subtree; reported, never deleted. A
  project that wants one removed says so with an explicit delete, which the
  project format does not express, so it goes through `alder apply` like any
  other deliberate deletion.
- **The web interface.** Opening a project in the browser is not in this
  specification.

---

## Decisions from review (2026-10-10)

Each was put as an open question with a recommendation. The review accepted
all five, amended the first and fifth as written here, and added two things
that matter more than any of them, now in the sections above: project files
hold no secrets, and every plan names its environment and host.

1. **Where `validate` reads LDIF.** The client commands have no LDIF code by
   design: "It does not parse LDIF" ([CLI.md](CLI.md)). An offline `validate`
   needs to parse it. Options:
   - (a) parse locally with `internal/ldif`, the same package the server uses,
     so it is the same code and not a second implementation, and amend the CLI
     rule to say "no LDAP code, no second LDIF parser";
   - (b) make `validate` ask a running Alder server, which keeps the rule but
     means validating a file needs a server.

   **Decided: (a).** A validator that needs a server will not run in a
   pre-commit hook, and the rule exists to prevent two implementations
   disagreeing, which reusing the server's own packages does not risk.
   `validate` needs three of them: `internal/ldif` for the records,
   `internal/dn` for the subtree checks, and `internal/schema` for the
   sensitive-attribute set. The CLI rule becomes: *no LDAP code; LDIF, DNs and
   the sensitive-attribute list only through the server's own packages.*

   **Enforced by a test, not a comment.** A `go list -deps` check that
   `internal/cli` reaches neither go-ldap, the LDAP driver nor the server
   package. The client used to import `internal/api` for the generated
   request and response types, and that package also holds the HTTP server,
   which builds the driver; so the first slice began by generating the wire
   types into `internal/apiclient`, which the client imports instead, and
   added the test in the same change.

   *Amended when it was built:* the review worded the rule as "never
   `internal/directory` or go-ldap". `internal/directory` turned out to hold
   no protocol code -- it is Alder's driver-independent model, the
   `ChangeRecord` and the interfaces -- and recovery bundles and the session
   cookie names, which the client already uses, are expressed in it. Banning
   it would have meant rewriting both for no safety gain, so the test forbids
   what "no LDAP code" means: go-ldap, the driver built on it, and the server
   that builds the driver. `internal/ldif`, `internal/dn` and
   `internal/schema` depend on none of those.

2. **Environments with different suffixes**, `dc=dev,dc=example,dc=com` against
   `dc=example,dc=com`. Supporting it means rewriting every DN in every file per
   environment: done with the `dn` package it is safe, but it is the first
   feature in which what is planned is not byte-for-byte what was reviewed.
   **Decided: not in version 1.** Same DNs in every environment, made safe
   by the environment and host printed above every plan; revisit with a real
   user who needs it. Adding it later is backward compatible, because version
   1 refuses unknown keys.

3. **Attributes owned by one environment**, such as a value that differs
   between dev and prod. **Decided: not in version 1.** The common real case
   is a password, which project files cannot hold anyway. For anything else,
   the workaround is a separate project for that subtree, as "environments"
   describes.

4. **Files other than LDIF.** YAML entries would be friendlier to write and
   review. **Decided: LDIF only.** It is the format every directory tool
   already reads and the one Alder previews and exports; a YAML rendering
   exists for reading, and nothing reads it back. If Git diffs get noisy,
   `alder project fmt` fixes that without a second format.

5. **Name and location.** `alder.yaml` at the project root, found from the
   current directory or given with `--project PATH`, where the path may be the
   directory or the file. **Decided as proposed.** Searching parent
   directories, as git does, is easy to add and hard to take back; the first
   version does not.
