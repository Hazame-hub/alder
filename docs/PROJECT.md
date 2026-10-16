# The Alder project format

**Status: implemented** -- `alder project validate`, `alder project plan --env`
with its unmanaged-entries report, `alder project apply --env` and
`alder project fmt` (see [CLI.md](CLI.md)), and environments with a suffix of
their own (decision 2, amended). Confirmed as a scope change on
2026-10-10 as a specification and reviewed the same day; the decisions that
review made are at the end, and the sections above reflect them.

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
  first version. The LDIF a person reviews is the LDIF that is planned, with
  one stated exception: an environment with its own suffix, where names, and
  only names, are moved onto it, and the plan says so.
- **Not a place for secrets.** `alder.yaml` names where a password comes from,
  never the password, and the project's LDIF files may not carry one at all
  (see "No secrets in project files").

---

## `alder.yaml`

```yaml
version: 1

suffix: dc=example,dc=com

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
  staging:
    api-url: https://alder.staging.example.com
    host: ldap.staging.example.com
    bind-dn: cn=alder,ou=services,dc=staging,dc=example,dc=com
    bind-password-env: ALDER_STAGING_BIND_PASSWORD
    suffix: dc=staging,dc=example,dc=com
```

### `version`

Required, and `1`. A file with any other version is refused, not guessed at.

### `suffix`

Optional. The DN the project's files are written under. It is parsed as a DN,
never compared as text, and every managed `base` must lie at or below it. It
means nothing on its own: it is what an environment's own `suffix` replaces
(see "An environment with its own suffix").

### `managed`

The subtrees the project is responsible for, in order. Each has:

| Key | Meaning |
|---|---|
| `base` | The subtree's root DN. Parsed as a DN, never compared as text. |
| `files` | The LDIF files that describe it: paths or globs, relative to `alder.yaml`. A glob matches in sorted order, so the plan is the same on every machine. |

A pattern is a `filepath.Glob` pattern: `*`, `?` and `[...]`, within one
directory. **`**` is not supported, and a pattern containing it is refused**
rather than read as `*`, which would match one level and silently leave out
every deeper file. List one pattern per directory instead --
`people/*.ldif` and `people/contractors/*.ldif`. No glob library is added to
support it.

Rules, all checked by `validate`:

- Every file is **inside the project directory**, by its real path. A pattern
  with `..` or an absolute path is refused as written, and a symlink that
  resolves outside the project is refused once resolved, named but never read,
  so a repository cannot make `validate` or `plan` read a file it does not
  contain. A symlink that stays inside is an ordinary file of the project.
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

One more key is not a connection setting: `suffix`, the DN that replaces the
project's suffix in that environment (see "An environment with its own
suffix"). An environment `suffix` without a top-level one is refused.

A key called `bind-password`, or any key the format does not define, is
refused. An unknown key is far more likely to be a typo or a pasted secret than
an extension. It also keeps later additions backward compatible: a key added
in a later version is one no valid version-1 file can already be using.

### What `alder.yaml` decides, and who may change it

An environment says where a command connects *and* where its credentials go:
`plan` and `apply` send the environment's password, read from the variable or
file it names, to the `api-url` it names. Whoever can change `alder.yaml` can
therefore point a run at a server of their choosing, and the password goes
with it. That is true of any tool whose configuration names both a server and
its credentials, and the answer is the usual one for CI:

- **`validate` connects to nothing and reads no password**, so it is safe to
  run on any pull request, including one from a fork.
- **Run `plan` and `apply` with credentials only on changes you trust**: a
  protected branch, a schedule, a pull request from the same repository
  after review. Give the job a password variable named for that environment
  alone, so a run for one environment cannot reach another's.

Two things narrow what a changed `alder.yaml` can take with it:

- **A `ca-file` must be PEM certificates and nothing else.** Its content is
  sent to the Alder server with the connection, so a file holding any other
  block, or text outside the blocks, is refused before anything is sent, and
  only the parsed certificates are sent. Pointing `ca-file` at a runner's
  credentials file reads it and stops. (`--ca-file` on every other command
  follows the same rule.)
- **A password file contributes its first line only**, as everywhere else in
  the client.

Deliberately **not** settable in `alder.yaml`, for the reason they are not
settable from the environment today: anything that confirms or widens a write.
There is no `yes`, `allow-deletes`, `force` or `insecure-skip-verify` key.
Which environment a command runs against is chosen by `--env` on the command
line, and only there. The mechanism is the one `--yes` already uses: every
client flag reads `ALDER_<FLAG>` by default, and `--env` is listed in
`envflags.Exclude`, so an inherited `ALDER_ENV=prod` cannot choose production
for a command nobody pointed at it.

Every environment manages the same subtrees, with the same files. An
environment differs in *where* it connects, and at most in the suffix those
subtrees sit under, not in *what* it holds. Without a suffix of its own, a
plan for production reads exactly like a plan for development, which is why
the environment and host are printed above every plan (see `alder project
plan`).

### An environment with its own suffix

When development is `dc=dev,dc=example,dc=com` and production
`dc=example,dc=com`, the files are written once, under the project's
`suffix`, and the development environment names its own:

```yaml
suffix: dc=example,dc=com
environments:
  dev:
    suffix: dc=dev,dc=example,dc=com
    # ...
```

Planning or applying for that environment moves names, and only names, from
below the project's suffix to below the environment's, with the `dn` package
(charter, rule 2), before anything is sent:

- **Every entry's DN and every managed base.** Only the suffix is replaced:
  the RDNs above it keep their types and values as written, so
  `uid=alice,ou=people,dc=example,dc=com` becomes
  `uid=alice,ou=people,dc=dev,dc=example,dc=com`. An RDN value is never
  rewritten, even one that spells a DN.
- **Every value of an attribute type whose syntax is DN or Name And Optional
  UID** (`member`, `uniqueMember`, `manager`, `owner`, `seeAlso` and every
  other type declared with those syntaxes), when the value is at or below the
  project's suffix. A Name And Optional UID value keeps its `#'...'B` part. A
  moved value is written as Alder writes DNs. A value of those syntaxes
  outside the suffix, or one that does not parse as a DN, is sent as written,
  for the directory to judge.
- **Nothing else.** A value of any other syntax is sent byte for byte as
  written, however much it looks like a DN. One that reads as a DN below the
  suffix -- a `description` that quotes one, say -- is named on standard
  error and in the JSON (`leftAsWritten`), because it may be a name the
  schema does not know is one.

**Which types hold DNs is the directory's answer.** The client reads the
schema of the directory it is planning against (`GET /schema`, which reports
each type's effective syntax, inherited from its superior where it names
none) and goes by syntax, never by attribute name: a type a directory adds
with DN syntax is rebased like `member`, and nothing is rebased because of
what it is called. The schema is read only for an environment that rebases.
A project that uses an attribute type the schema does not define is not
planned for that environment (exit 3): whether its values are names cannot
be told, and a guess either way would be a silent wrong answer.

Two more refusals, because rebasing would contradict an entry's own name:

- **The suffix entry itself, onto a suffix with another RDN.** Moving
  `dc=example,dc=com` to `dc=dev,dc=example,dc=com` would rename it to
  `dc=dev` while its `dc: example` stayed as the file wrote it. `validate`
  refuses it. Onto a suffix with the same RDN (`dc=example,dc=org`) it is
  moved like any other entry. Manage the entries below the suffix instead.
- **An entry named by a DN-syntax attribute** whose value is below the
  suffix: its name and its value cannot both be kept. Refused when planned.

What is planned is then not byte for byte what was reviewed, so it is said
everywhere: on standard error before anything is sent, on the line under the
environment above the plan -- `Suffix dc=example,dc=com ->
dc=dev,dc=example,dc=com: 12 entries and 30 DN values rebased from the
files` -- in `validate`'s output, and as `rebase` (`from`, `to`, `entries`,
`values`, `leftAsWritten`) beside the plan with `--json`. The plan itself, the
unmanaged-entries report and `--detailed-exitcode` are made against the
rebased names, so they speak the environment's language.

Connection settings are not rebased: an environment's `bind-dn` is written in
its own names already.

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
- every environment names a password source and no password;
- `suffix` and every environment's `suffix` are DNs, an environment has one
  only when the project does, every managed base lies below the project's,
  and no environment would rename the suffix entry the project describes.

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
   tells them apart. For an environment with its own suffix, the project is
   rebased onto it after connecting, and the line under the environment says
   so (see "An environment with its own suffix").
5. Prints the plan exactly as `alder plan` does, with the same `--json` output
   and the same exit codes.

That is the whole engine. Reconciliation, the schema checks, membership and
reference impact, recovery assessment and baselines are the existing plan's; the
project adds a file format and the decision of which records to send.

**`--detailed-exitcode`** answers "does this environment match the project?" in
the exit status alone: 0 nothing to change, 2 changes pending, 3 some change
cannot be applied as written (it wins over 2), the numbers `terraform plan
-detailed-exitcode` uses. Without it, 0 means planned,
as for every other plan; the default is part of the 1.x compatibility promise,
so the stricter meaning is asked for, never assumed.

### Later slices, named so the first one does not paint them into a corner

- **`alder project apply --env NAME`** (built): plan, show, confirm, apply,
  through `alder apply`'s own path, so its rules are unchanged (`--yes`,
  refusing a plan with conflicts or one gone stale, `--recovery-out`).
  `--allow-deletes` has nothing to permit: desired state never deletes.

  *Amended when it was built:* this said a plan "carries the environment it
  was made for, and `apply` refuses one made for a different `--env`" --
  which assumed a plan saved by `project plan` and applied later. A baseline
  is a MAC under a key that exists only in the Alder process that issued it,
  so such a file would stop applying at the first server restart. `apply`
  therefore plans and applies in one run against one environment, where a
  plan for another environment cannot reach it by construction, and its
  question names the environment and the directory.
- **Saved plans** (built). `alder project plan --env NAME --out FILE` saves
  the plan, and `alder project apply --env NAME --plan FILE` applies exactly
  it later, through any Alder, or refuses. The file holds no baseline: each
  change is the plan's own record, a modification with the values it
  replaces as an `expect`, the precondition recovery bundles use, so any
  Alder can check it and a restart between review and apply changes
  nothing. The file names the environment, its `host:port`, the bind, the
  configuration bind (`config-bind-dn`) when it has one, and any suffix, and a digest of the project's entries; an apply for anything
  else is refused before connecting (3), and one whose changes no longer
  hold is refused whole (4). Specified, and accepted, in
  [proposals/saved-plans.md](proposals/saved-plans.md).
- **`alder project fmt`** (built): rewrite the project's LDIF in a canonical
  form, so a Git diff shows what changed and not how it was wrapped. A tidier
  file, not a second format.

  *Settled when it was built:* the canonical form is the one Alder's own LDIF
  writer produces, so an export is already formatted and formatting it changes
  nothing, and the form is held still by the writer's golden files rather than
  by a second set of rules. Each line keeps its place -- records, attributes and
  values in the order written, attribute names as spelled -- and only how it is
  written down changes: refolded at 76 columns, one space after the colon,
  base64 exactly where `NeedsBase64` says, the DN as the `dn` package renders
  it, one blank line between records, LF endings. Comments are kept verbatim,
  where they were: the reader drops them, so a formatter built on parse and
  write would have deleted every one, and refolding them would break prose
  mid-word. A project that does not validate is not touched, every file is
  formatted before any is written, and each new form is read back and must
  hold exactly the records the old one did before it replaces the file.
  `--check` writes nothing and exits 1 when a file is not canonical: the
  number `diff` uses for "they differ".
- **Unmanaged entries** (built). For each managed subtree, the entries the
  directory holds that no file mentions, listed beside the plan as
  *unmanaged*. Read with the existing snapshot capture of the subtree;
  reported, never deleted. A project that wants one removed says so with an
  explicit delete, which the project format does not express, so it goes
  through `alder apply` like any other deliberate deletion.

  *Settled when it was built:* `project plan` makes the report on every run,
  after the plan, by capturing each managed subtree and comparing names with
  `dn.Equal`, as the directory would. A capture is whole or refused, so a
  subtree too large to capture (more than a snapshot holds) or one the bind
  cannot read is said to be unreadable on standard error, never half listed,
  and the plan beside it stands. A base the directory does not hold has
  nothing unmanaged below it. The report never changes the exit status:
  unmanaged entries are not changes, and `--detailed-exitcode` still answers
  only "does the directory match what the project describes?". With `--json`
  it is `unmanaged`, one object per subtree with its `base`, its `entries`
  and, when it could not be read, an `error`. `project apply` does not make
  it: it applies what the project describes, and the report is about what it
  does not.
- **The web interface** (first slice built). Opening a project in the
  browser is specified, and accepted, in
  [proposals/web-projects.md](proposals/web-projects.md). *Open project*, on
  the Import screen, takes a folder: the page reads `alder.yaml` at its top
  and every `*.ldif` below it outside dot-directories, and sends them to
  `POST /project/plan`. The server validates them with this package -- the
  same rules, globs and secrets check, in the same words as `validate` --
  plans them as `project plan` does and reports the unmanaged entries, in
  one response, and keeps nothing. The plan is the session's: the
  environments in `alder.yaml` are validated and never used (no `api-url`
  followed, no host dialled, no password variable or file read, no
  environment `suffix` applied), and the heading above the plan names the
  project and then the session's own directory and bind. *Stage for
  review* puts the plan's changes into an empty changeset, which plans them
  again and applies them as it applies everything; a changeset that already
  holds something is refused with its count and a *Clear changeset* action.
  `fmt` is not offered in the browser.

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

   *Amended when it was built (2026-10-13):* the project's owner asked for
   it, and it is in version 1 as two optional keys, a top-level `suffix` and
   an environment's `suffix`; a file without them means what it always did,
   and no valid file could already be using them. The rewrite is the `dn`
   package's, never text, and it is said wherever the plan is shown, so a
   reviewer is never left to infer it. The question the review left open --
   which values are DNs -- is answered by the schema of the directory being
   planned, by syntax, not by a list of attribute names in Alder: a list
   would miss every type a directory adds, and a value that merely looks like
   a DN would be rewritten by any rule that reads the value. Where the
   schema has no answer, nothing is planned. See "An environment with its
   own suffix".

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
