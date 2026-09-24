# The command line

The `alder` binary that runs the web interface (`alder serve`) also has five
client commands:

| Command | What it does |
|---|---|
| `alder snapshot` | Capture a subtree as an Alder snapshot file |
| `alder diff` | Compare two snapshots, or a snapshot and the live directory |
| `alder plan` | Show what an LDIF document or a set of change requests would do |
| `alder apply` | Plan, show the plan, and apply exactly that plan |
| `alder version` | Print this binary's version, and a server's |

They exist so that the workflow the web interface offers, from snapshot through
diff and plan to apply, can be run from a shell and from automation.

---

## How it works

The client commands are clients of a **running Alder server**. They are not a
second implementation of anything:

```
alder snapshot | diff | plan | apply
        │   HTTPS, the same API the web interface uses
        ▼
alder serve  ──  Snapshot, Diff, Plan, Apply  ──  LDAP  ──  directory
```

A command that reads or writes a directory opens a session the way the
connection screen does, calls the endpoints the web interface calls, and closes
the session when it finishes, even when it is interrupted. Comparing two
snapshot files reads no directory, so it opens no session and needs no
directory credentials.

The client has no LDAP code. It does not parse LDIF, compare values, derive
changes or write to a directory. **It uses the same server-side Snapshot, Diff,
Plan and Apply as the web interface**, and with `--json` it writes the server's
own response as it came.

**It adds its own safety policy around them.** That policy is client-side, and
it is stricter than the web interface in three places:

- **Confirmation:** `apply` asks at a terminal, and elsewhere needs `--yes`.
- **Deletion:** `--yes` alone never deletes; a plan with deletions also needs
  `--allow-deletes`.
- **Pre-existing conflicts:** `apply` refuses a plan that already contains a
  conflict or an invalid change. The web interface's changeset can apply the
  applicable part of such a plan.

None of this is another way of executing changes. What `apply` does send goes to
the same endpoint, `POST /api/v1/changeset/apply`, with the same plan tokens the
web interface sends.

What the client adds is what a terminal needs:

- reading files and standard input;
- writing files atomically;
- asking before a write;
- exit codes a script can branch on.

`alder serve` is unchanged. The client commands are new subcommands beside it,
so every existing invocation, flag and `ALDER_*` variable of the server means
what it meant before. The container image still starts `alder serve` by
default.

---

## Installing

The client is part of the `alder` binary in every release archive: Linux, macOS
and Windows, on amd64 and arm64. There is nothing separate to install. The
container image has it too:

```sh
docker run --rm -e ALDER_BIND_PASSWORD ghcr.io/hazame-hub/alder:<version> version
```

---

## Connecting

Two things are named: the Alder server to ask, and the directory it should
connect to. The directory flags are exactly the connection screen's fields.

| Flag | Variable | Meaning |
|---|---|---|
| `--api-url` | `ALDER_API_URL` | The Alder server, such as `https://alder.example.com`. Required. `/api/v1` is added if it is not there. It must not contain a user name, password, query or fragment. |
| `--api-ca-file` | `ALDER_API_CA_FILE` | PEM certificates to verify the Alder server's HTTPS certificate with, instead of the system roots |
| `--timeout` | `ALDER_TIMEOUT` | Give up on the whole command after this long. Default `10m`; `0` waits indefinitely. |
| `--host` | `ALDER_HOST` | The directory's host, as the Alder server reaches it. Required for everything but comparing two snapshot files. |
| `--port` | `ALDER_PORT` | The directory's port. Default 636 for `ldaps`, 389 otherwise. |
| `--tls` | `ALDER_TLS` | `ldaps` (default), `starttls` or `plaintext`. The server refuses plaintext unless it was started with `--i-know-this-is-insecure`. |
| `--ca-file` | `ALDER_CA_FILE` | PEM certificates to verify the directory's certificate with |
| `--server-name` | `ALDER_SERVER_NAME` | The name to check the directory's certificate against, when it is not `--host` |
| `--insecure-skip-verify` | — | Do not verify the directory's certificate. The session is marked unverified. |
| `--bind-dn` | `ALDER_BIND_DN` | The DN to bind as. Omit it for an anonymous bind. |
| `--bind-password-file` | `ALDER_BIND_PASSWORD_FILE` | See [Passwords](#passwords) |
| `--bind-password-stdin` | — | See [Passwords](#passwords) |
| `--config-bind-dn` | `ALDER_CONFIG_BIND_DN` | A second identity, used only for the server's configuration tree |
| `--config-bind-password-file` | `ALDER_CONFIG_BIND_PASSWORD_FILE` | Its password |

**Precedence is flag, then environment, then default**, the same rule as
`alder serve`. A variable is the flag name upper-cased, with dashes as
underscores, behind `ALDER_`.

Only the flags in this table read the environment. **These are deliberately
command-line only:**

- anything that confirms or widens a write: `--yes`, `--allow-deletes`,
  `--force`;
- anything that decides what a command operates on: `--base`, `--output`,
  `--mode`, `--stage`;
- `--insecure-skip-verify` and `--bind-password-stdin`.

An inherited environment must never confirm an apply nobody typed.

There is no configuration file, and nothing is stored between runs: every
command connects, works and disconnects.

If `--api-url` is plain `http://` to another machine, the client warns on
standard error, because the bind password would cross the network unencrypted.
It never follows a redirect, because a redirected request could carry the
password somewhere else.

---

## Passwords

**A password is never a flag value.** A flag's value ends up in shell history,
in the process list every user on the machine can read, and in the log of any
CI job that echoes its commands. There is no `--password`.

A bind password comes from exactly one of these, in this order:

1. `--bind-password-file PATH`: the first line of the file, without its line
   ending, so a file saved on Windows works. Keep the file readable only by you.
2. `--bind-password-stdin`: the first line of standard input. It cannot be
   combined with a document read from standard input (`-`); that is refused
   before anything is read.
3. `ALDER_BIND_PASSWORD`: the usual choice for CI, from the CI system's secret
   store.

An empty password is refused: many servers treat a simple bind with an empty
password as an anonymous bind that succeeds. There is no hidden interactive
prompt in this release. The configuration identity reads
`--config-bind-password-file` or `ALDER_CONFIG_BIND_PASSWORD`.

The password goes to the Alder server in the session request, as it does from
the connection screen. It is never printed, never written to a file and never
logged. The client has no verbose or tracing mode that could print a request
body.

---

## Standard input, standard output, files

- **`-` means standard input** for a document, and standard output for
  `snapshot --output`. It means the same thing everywhere.
- **Standard input can be read once.** Two uses of it in one command, such as
  `diff - -` or `plan - --bind-password-stdin`, are refused before anything is
  read.
- **Standard output carries data only.** That means the snapshot, the JSON
  document, or the human report the command exists to print. Warnings, progress,
  questions and errors go to standard error, so `alder diff ... --json > diff.json`
  holds exactly one JSON document.
- **Files are written atomically.** Content goes to a temporary `.partial` file
  beside the destination, is synced, and takes the destination's name only when
  complete. A snapshot is also checked to be one whole JSON document before it
  is named. An interrupted command leaves no file under the name you asked for.
- **An existing file is never replaced without `--force`.** With `--force` it is
  replaced atomically. New files are created readable only by their owner on
  Unix. Directory data is not something to share by default.

---

## Exit codes

The client commands exit with one of these. They are part of the 1.x
compatibility promise.

| Code | Meaning |
|---|---|
| 0 | Done. For `diff`, complete with no differences. For `apply`, applied, or nothing to apply. |
| 1 | `diff`: complete, and there are differences. `preflight`: compatible once the listed prerequisites are done |
| 2 | `diff`: incomplete, because something could not be seen. This wins over 1. `preflight`: incomplete |
| 3 | A change cannot be applied as written (a conflict or a schema violation), or a selected difference offers no change. `preflight`: incompatible. Nothing was written. |
| 4 | The plan no longer holds: the directory changed after it was made (`plan_stale`), or the operations sent are not the ones planned (`plan_mismatch`). Nothing was written. |
| 5 | Not confirmed: declined, or no confirmation could be given. Nothing was written. |
| 6 | `apply` stopped after writing some of its changes |
| 7 | The command line was wrong. Nothing was sent. |
| 8 | Anything else. Alder or the directory refused, a server could not be reached, or a file could not be read or written. |

A mistake before a command is chosen, such as an unknown command name, exits 1
as it always has.

---

## JSON output

`diff`, `plan`, `apply` and `version` take `--json`. **JSON is the automation
contract; the human output is not.** The human output may be reworded in any
release.

| Command | Standard output with `--json` |
|---|---|
| `diff` | Alder's comparison, exactly as `POST /api/v1/diff` returns it |
| `plan` | Alder's plan, exactly as `POST /api/v1/plan` returns it |
| `apply` | `{"plan": <the plan>, "result": <POST /api/v1/changeset/apply's result>, "error": …, "recoveryFile": …}`, each present when there is one |
| `version` | `{"client": {"version": …}, "server": <GET /api/v1/source>}` |

The shapes are the API's, documented in `api/openapi.yaml`. When a command fails
after its command line was accepted, standard output holds `{"error": …}`
instead:

- **Alder's own error object, verbatim**, when Alder refused. Its `error` code
  (`conflict`, `plan_mismatch`, `snapshot_checksum_mismatch`, …), `cause`,
  `detail` and `affected` are all kept.
- **`{"error": <code>, "message": …, "origin": "cli"}`** when the client refused
  by itself. The client's codes are:
  - usage and input: `usage`, `input`, `input_too_large`, `snapshot_not_json`,
    `changes_invalid`, `request_too_large`;
  - files: `output`, `output_exists`, `incomplete_snapshot`, `recovery_missing`,
    `recovery_invalid`, `recovery_not_written`;
  - reaching the server: `unreachable`, `interrupted`, `timeout`,
    `unsupported_server`, `busy`, `unexpected_response`, `no_session`;
  - confirming and applying: `confirmation_required`, `declined`,
    `deletes_not_allowed`, `plan_not_applicable`, `partially_applied`,
    `apply_refused`, `outcome_unknown`, `origin_mismatch`;
  - selecting differences: `selection_not_found`, `selection_not_applicable`,
    `selection_blocked`, `deletion_not_selected`, `not_a_deletion`.

JSON output never contains terminal formatting. The client prints no colour at
all.

---

## `alder snapshot`

```sh
alder snapshot --base ou=people,dc=example,dc=com --output people.snapshot.json
alder snapshot --base dc=example,dc=com --scope one --filter '(objectClass=organizationalUnit)' --output - > ous.json
```

The output is the Alder snapshot, version 1, exactly as the server produced it:
the same document the web interface downloads, byte for byte. See
[SNAPSHOTS.md](SNAPSHOTS.md) for the format.

- `--scope` is `sub` (the default), `one` or `base`.
- `--filter` narrows the entries; `--operational` also captures operational
  attributes.
- **Sensitive attributes are recorded as a count of values**, never a value or a
  hash.
- The server's limits apply: at most 50,000 entries, a base under the schema or
  configuration tree is refused, and a capture that cannot read the whole
  subtree fails rather than writing part of it.

`--output` is required: a path, or `-` for standard output. On success the
client prints one line to standard error, with the entry count and checksum.

### The schema

```sh
alder snapshot --kind schema --output schema.json
```

`--kind schema` (1.10) captures the schema the server publishes instead of a
subtree: attribute types and object classes for comparison, syntaxes, matching
rules and the rest as context. It takes no `--base`, `--scope`, `--filter` or
`--operational`. The line on standard error gives the subschema entry, the
counts, whether the capture is complete, and the checksum. See
[SCHEMA-SNAPSHOTS.md](SCHEMA-SNAPSHOTS.md).

### The configuration

```sh
alder snapshot --kind config --output config.json
```

`--kind config` (1.13) captures the server's own configuration: its settings,
the databases, overlays, backends and plugins they belong to, and whether each
is one Alder can change. Secrets are withheld -- recorded as present and
counted, never as a value -- runtime counters are not configuration and are left
out, and schema definitions have their own kind.

It takes no `--base`, `--scope`, `--filter` or `--operational`: a configuration
is one tree, the server's own, and there is nothing to narrow. The line on
standard error names the provider, the root, the counts, how many values were
withheld, whether the capture is complete, and the checksum:

```
Captured the openldap configuration at cn=config (88 settings in 6 resources, 2 withheld; complete) to config.json, sha256:...
```

A session that cannot read the configuration tree, or a server Alder has no
configuration model for, is told so (`config_model_unavailable`) rather than
given an empty configuration. See
[CONFIG-SNAPSHOTS.md](CONFIG-SNAPSHOTS.md).

---

## `alder diff`

```sh
alder diff before.json after.json
alder diff baseline.json @live
alder diff @live baseline.json --json > drift.json
cat baseline.json | alder diff - @live
```

Each side is a snapshot file, `-` for a snapshot on standard input, or `@live`
for the directory as it is now. A file really named `@live` is `./@live`.

**The direction is always SOURCE → TARGET.**

- `added` means in TARGET and not in SOURCE.
- `removed` means in SOURCE and not in TARGET.

`alder diff baseline.json @live` answers "what has happened since the
baseline". `alder diff @live baseline.json` answers "what would bring the
directory back to the baseline", and is the direction from which changes can
be derived.

- **The live side matches the snapshot by default.** It uses the snapshot's
  base, scope, filter and operational setting unless `--base`, `--scope`,
  `--filter` or `--operational` say otherwise.
- **Snapshots are sent as their exact bytes.** The server, not the client,
  decides whether a file is a valid snapshot, so a snapshot edited after
  capture, or carrying a field version 1 does not know, is refused just as the
  web interface refuses it. A file that is not a single JSON object is refused
  before it is sent.
- **The comparison must fit in one 16 MB request**, which is the server's limit
  and is checked before sending.
- **Two snapshot files need only `--api-url`.** The server compares them without
  a directory session, so no directory flags, bind DN or password are needed;
  any given are ignored. A comparison with `@live` needs the directory flags.
  A server before 1.8 needs a session even for two snapshots. Against one, give
  the directory flags and the comparison is made again with a session.

The human report prints:

1. the two sides;
2. the counts;
3. any reason the comparison is incomplete;
4. every difference, with removed values as `-` and added values as `+`.

Unchanged entries appear only with `--include-unchanged`; `--summary` prints
the counts alone. Sensitive attributes show how many values changed, never a
value. Directory text that could act on a terminal (escape sequences,
bidirectional overrides) is shown escaped.

### From a difference to a change

With `@live` as SOURCE, each difference Alder can undo carries the change
requests it derived. Select them by DN, as the report prints it:

```sh
alder diff @live baseline.json \
  --stage uid=alice,ou=people,dc=example,dc=com \
  --stage-deletion uid=mallory,ou=people,dc=example,dc=com \
  --changes-out restore.json

alder plan  --changes restore.json
alder apply --changes restore.json --yes --allow-deletes
```

- **Nothing is selected by default,** and there is no wildcard.
- **A deletion can only be selected with `--stage-deletion`.** The same DN given
  to `--stage` is refused.
- **A difference Alder offers no change for is refused**, not skipped, whether
  it is unknown or blocked. That includes any deletion in an incomplete
  comparison, which the server never derives.
- **Selected changes are ordinary change requests.** They go through plan and
  apply like any other, and the directory is re-read when they are planned.

### Comparing schema

```sh
alder diff openldap-schema.json 389ds-schema.json --summary
alder diff @live schema.json
alder diff @live schema.json \
  --stage 1.3.6.1.4.1.99999.1.9 --stage objectClass:1.3.6.1.4.1.99999.2.9 \
  --changes-out schema-changes.json
```

A schema snapshot compares with another schema snapshot, or with `@live`, which
then reads the published schema whole. `--base`, `--scope`, `--filter` and
`--operational` do not apply, and a schema snapshot is never compared with a
data snapshot. Two schema files need only `--api-url`, as for data.

- **The report is by definition:** counts for attribute types and object
  classes, then each difference by OID and NAME, with each changed field as
  `-` and `+`, its category (`core`, `description` or `extension`) and any
  problem.
- **A difference only in `X-` extensions is `metadata_only`.** It is reported
  and never counts toward exit status 1.
- **Differences are selected by OID,** or as `attributeType:OID` or
  `objectClass:OID` where the same OID names both. A NAME is not accepted: the
  OID is the identity.
- **A removal is selected only with `--stage-deletion`.**
- **A change that needs another is refused** (`dependency_required`) unless the
  other is selected too. Nothing is added for you.
- **Changes are written in dependency order,** the order Alder returned, not the
  order they were named in.
- `--schema-target DN` names the schema entry an added definition is written to,
  where the server keeps schema in several; without it such additions are not
  offered.

### Comparing configuration

```sh
alder diff before-config.json after-config.json
alder diff @live config.json
alder diff @live config.json --stage olcIdleTimeout --changes-out config-changes.json
```

A configuration snapshot compares with another configuration snapshot of the
**same server software**, or with `@live`. `--base`, `--scope`, `--filter` and
`--operational` do not apply, and a configuration is never compared with data
or with a schema.

- **The report is by setting:** counts, a table by section, then each
  difference with its section, key, resource and entry, the values as `-` and
  `+`, and one line saying whether Alder can change it.
- **Two providers are not compared.** Given an OpenLDAP configuration and a
  389 DS one, the command prints what each side holds and stops. Their settings
  are not the same settings, and every difference it could list would be an
  artefact.
- **A withheld value is never printed.** The line says `withheld`.
- **Differences are selected by the identifier `diff` prints**, or by the
  setting's key where that names only one difference.
- **Only a setting Alder already changes through a plan can be staged.**
  Anything else -- a secret, a setting the server maintains, a value nothing
  parses, a setting on one side only -- is refused by name rather than skipped
  silently.
- **`--stage-deletion` is refused:** adding or removing configuration entries is
  not something Alder does.

---

## `alder package`

```sh
alder package create --changes restore.json --title "team schema" --output package.json
alder package inspect package.json
alder package validate package.json
alder plan  --package package.json
alder apply --package package.json --yes
```

A change package is a portable description of intended changes: what to change,
not the operations one environment produced. The same file is carried to each
directory and validated there. See
[CHANGE-PACKAGES.md](CHANGE-PACKAGES.md).

- **`create`** turns a JSON array of change requests into a package. A schema
  change is recorded as the intent it expresses, so it can be carried to a
  server that keeps its schema somewhere else. A `baseline` or an `expect` on a
  change is dropped -- both describe the environment it was planned in -- and a
  password change is refused and recorded as an omission, because a package
  never carries a secret. Dependencies are derived from the changes and written
  into the file.
- **`inspect`** reads a package and checks everything that needs no directory:
  format, version, unknown fields, duplicate identifiers, missing dependencies,
  cycles, DNs, definitions, secrets and the checksum. It needs only `--api-url`,
  so it runs in a pipeline before there are credentials.
- **`validate`** asks a directory what the package means there: what is ready,
  what it already satisfies, what conflicts, what is missing, what this server
  cannot do.
- **There is no `package apply`.** `alder plan --package` and
  `alder apply --package` validate the package, plan the changes that are ready,
  show the plan, and -- for apply -- send back exactly that plan.
  `--schema-target` says which schema entry an added definition goes to on a
  server that keeps schema in several.

Exit status for `validate`: 0 applicable here or nothing left to do, 2 something
could not be decided, 3 some change cannot be applied here, 7 usage, 8 failure.
`plan --package` and `apply --package` keep their own codes.

Files follow the same rules as everywhere else: `-` is standard input, output is
written atomically, and an existing file is never replaced without `--force`.

---

## `alder key`, `alder sign`, `alder verify`

```sh
alder key generate --output ~/.alder/signing.pem
alder sign config.json --key ~/.alder/signing.pem --signer "alice" --output config.signed.json
alder verify config.signed.json --trusted-keys ~/keys/alice.pub.pem
```

Since 1.15. These three need no Alder server and no directory: they read files,
and the private key is read, used and forgotten. None of them takes the
connection flags.

- **`alder key generate`** writes an Ed25519 private key to `--output` and the
  public key beside it. `--public-output` says where instead. The private key is
  written readable by you only, and never to standard output.
- **`alder key show FILE`** prints a public key's key id. A private key is
  refused.
- **`alder sign DOCUMENT --key K --output F`** wraps a snapshot, change package
  or recovery bundle in a signed envelope. Signing an already-signed document
  adds a signature beside the first, so two people can sign one document; the
  same key twice is refused. `--signer` records a label.
- **`alder verify DOCUMENT --trusted-keys K`** exits 0 verified, 3 untrusted or
  unsigned, 8 invalid. `--trusted-keys` takes a file, a file of several keys, or
  a directory of `.pem` files, and repeats. `--json` prints the result and what
  the document claims to be, never its contents.

A server started with `alder serve --trusted-keys` reports what a document's
signature amounted to wherever it reads one, and `--require-signature` makes it
read signed documents only. See [SIGNING.md](SIGNING.md).

---

## `alder preflight`

Since 1.14 a configuration snapshot is read too. It is evaluated only against a
directory of the same server software; against another, the report has one
finding, `config_provider_mismatch`, and exits 2.

```sh
alder preflight package.json
alder preflight schema-snapshot.json --all
alder preflight data-snapshot.json --json > preflight.json
cat package.json | alder preflight -
```

Reads a change package, a schema snapshot or a data snapshot against the
directory and reports what would carry across: what is already there, what
could be added, what needs something done first, what contradicts the
directory, what it cannot represent, and what could not be seen. The server
recognises the artifact from its own `format` and `kind`; the client only
refuses what is not JSON before sending it. See [PREFLIGHT.md](PREFLIGHT.md).

A preflight writes nothing, changes nothing in the file, rewrites no DN and maps
no attribute or object class. The human report lists every finding that needs
attention (`--all` lists them all), the capabilities the artifact needs, and
what was not evaluated -- access control and server configuration, always.
`--json` writes the report exactly as Alder answered. `--schema-target` names
the schema entry a package's definitions would go to, where the server keeps
several.

Exit status: 0 compatible, 1 compatible once the listed prerequisites are done,
2 incomplete (something could not be decided), 3 incompatible, 7 usage,
8 failure -- including an artifact Alder refused as malformed or forged.

---

## `alder plan`

```sh
alder plan changes.ldif
alder plan --mode desired people.ldif
cat changes.ldif | alder plan -
alder plan --changes restore.json --json
alder plan --recovery recovery.json
```

`plan` shows what the input would do, without doing it. The plan is Alder's:

- what each change would do;
- what cannot be applied, and the stable problem code saying why;
- the target area (data, schema, configuration);
- membership and reference impact;
- the exact LDIF.

The input is one of:

- **An LDIF document, read according to `--mode`.**
  - `changes` (the default, as in the API): every record is the exact operation
    it states, and a record without a `changetype` is an add.
  - `desired`: the document is the state entries should be in. Entries it does
    not mention are never deleted.
- **`--changes`: a JSON array of change requests,** the objects
  `POST /api/v1/plan` takes, for example from `diff --changes-out`. Unknown
  fields are refused, not dropped.
- **`--recovery`: a recovery bundle** written by `alder apply --recovery-out`.
  The file is sent to Alder exactly as it was read, and Alder validates it and
  returns its compensating changes, which are planned like `--changes`. The plan
  is preceded by what the bundle says:
  - when it was made, and how recoverable it is;
  - the directory it was made against, with a warning if this one announces
    itself differently;
  - each applied change, with its limitations;
  - how many compensations no longer match the directory.

LDIF with Windows line endings is read as it is. **A plan never shows a
password or any other sensitive value**: the server withholds it, as
`withheld (26 bytes)` in the LDIF and as a `size` in the JSON.

Exit status is 0 when planned, and 3 when some change cannot be applied as
written.

---

## `alder apply`

```sh
alder apply changes.ldif                          # at a terminal: shows the plan, then asks
alder apply changes.ldif --yes                    # automation
alder apply --changes restore.json --yes --allow-deletes --json
```

`apply` is `plan` followed by applying **exactly the plan that was shown**. It
never sends a change that was not planned.

1. It plans the input, as `alder plan` does.
2. It shows the plan: on standard output, or on standard error with `--json`.
3. It refuses if any change cannot be applied as written (exit 3). If nothing
   would change, it says so and exits 0 without writing.
4. It asks for confirmation, or checks that `--yes` was given.
5. It sends each planned change with the token the plan issued. An exact change
   is sent from the client's own copy, because the plan withholds sensitive
   values. For LDIF, that copy is the document as the server itself parses it.
   A desired-state change is sent from the plan's record. This is the web
   interface's rule, applied the same way.

### Confirmation

- **At a terminal**, the plan is followed by `Apply 3 changes to host:636? [y/N]`
  on standard error. Only `y` or `yes` applies; anything else, including Enter
  and end of input, does not. If the plan deletes entries, the question says how
  many.
- **Anywhere else**, whether a pipe, a CI job or input on standard input,
  nothing is applied unless `--yes` is given. The command exits 5 before
  connecting.
- **`--yes` answers the question and nothing else.** The plan, its problems,
  the refusal of a stale plan and the server's checks all still apply.
- **A plan that deletes entries needs `--allow-deletes` as well**, when `--yes`
  is what answers. `--yes` alone never deletes.

### When the directory moves

The plan's tokens bind each operation to the directory state it was planned
against. If an entry changed between the plan and the apply, the server refuses
the whole set: `409 conflict`, `cause: plan_stale`. **Nothing is written, and
the command exits 4.** The client does not plan again and apply; run the
command again to review the new plan. A change that is not the operation that
was planned is refused the same way (`plan_mismatch`, also exit 4).

### Recovery bundles

```sh
alder apply changes.ldif --yes --recovery-out recovery.json
alder plan --recovery recovery.json
alder apply --recovery recovery.json --yes
```

A recovery bundle describes the compensating changes Alder can derive from each
entry as it was immediately before its change. It is not a rollback or a
backup, and it holds no password. See [RECOVERY.md](RECOVERY.md).

- **`--recovery-out FILE` asks Alder for one.**
  - The file is written only once it has been read back and verified as a
    bundle, and only when something was applied.
  - It is created with mode 0600 on Unix.
  - It never replaces an existing file without `--force`.
  - `-` is refused, because standard output carries the plan and the result.
- **After a run that stops partway** (exit 6), the bundle covers the changes
  before the failure, and standard error says where it went. When nothing was
  applied, no file is written.
- **If the bundle does not verify,** no file is written and the command exits 8
  with `recovery_not_written`. The changes it applied are in the directory.
- **`--recovery FILE` plans and applies a bundle's changes** like any other
  input: shown, confirmed, and applied with the plan's tokens.
  - A compensation whose entry has changed since the original apply is a
    conflict (exit 3), never an overwrite.
  - A bundle applied a second time finds conflicts.
  - A bundle made against a directory that announces a different vendor or
    naming contexts is refused (exit 5, `origin_mismatch`) unless
    `--allow-origin-mismatch` is given. What a directory announces is not proof
    of which one it is.
- **None of these flags reads the environment.**

### When an apply stops partway

Changes are applied in order, one at a time, because a directory has no
transaction across entries.

- **Exit 6:** a change was refused after earlier ones were written. The output
  says how many were applied.
- **Exit 8:** the first change was refused, so nothing was written.
- **Exit 8 with `outcome_unknown`:** the request was sent and no answer came
  back, for example because the connection dropped or the command was
  interrupted. Some changes may have been written, so plan again before
  retrying.

---

## Automation

A drift check in CI (Linux or macOS):

```sh
export ALDER_API_URL=https://alder.internal.example.com
export ALDER_HOST=ldap1.internal.example.com
export ALDER_BIND_DN=cn=drift-reader,ou=service,dc=example,dc=com
# ALDER_BIND_PASSWORD comes from the CI secret store

alder diff baseline.json @live --json > drift.json
case $? in
  0) echo "no drift" ;;
  1) echo "drift found"; exit 1 ;;
  2) echo "incomplete comparison: the reader cannot see everything"; exit 1 ;;
  *) echo "the check itself failed"; exit 1 ;;
esac
```

Applying a reviewed change, and treating a stale plan as its own outcome:

```sh
alder apply --mode changes change.ldif --yes --json > result.json
status=$?
if [ "$status" -eq 4 ]; then
  echo "the directory changed since the plan; review again" >&2
fi
exit "$status"
```

The same on Windows, in PowerShell:

```powershell
$env:ALDER_API_URL = "https://alder.internal.example.com"
$env:ALDER_HOST = "ldap1.internal.example.com"
$env:ALDER_BIND_DN = "cn=drift-reader,ou=service,dc=example,dc=com"
# $env:ALDER_BIND_PASSWORD is set from the secret store

alder.exe snapshot --base "ou=people,dc=example,dc=com" --output "C:\snapshots\people.json" --force
alder.exe diff "C:\snapshots\people.json" "@live" --json | Out-File -Encoding utf8 drift.json
if ($LASTEXITCODE -eq 1) { Write-Output "drift found" }
```

In PowerShell, quote `@live`, because `@` starts a splat. When piping a snapshot
into `alder diff -`, prefer `Get-Content -Raw` or a file argument: older
PowerShell versions re-encode text sent through a pipe.

---

## Interrupting and timeouts

**Ctrl+C cancels the command's request and closes its session.** A capture
interrupted partway leaves no file.

The server does not learn that a client disconnected. This is a property of the
HTTP server library, described in [SECURITY.md](../SECURITY.md). A read already
running there, such as a capture or a comparison, finishes within the server's
own per-operation limits and its result is discarded. An apply interrupted after
it was sent may have written some changes, and the command says so.

`--timeout` (default ten minutes) bounds the whole command, connection
included.

---

## Which servers the client works with

The client uses these endpoints:

| Commands | Endpoints | Needs a server of |
|---|---|---|
| all | `/session`, `/source` | any 1.x |
| `plan`, `apply` | `/plan` with LDIF and `mode`, `/import/ldif`, `/changeset/apply` with plan tokens | 1.6 or later, where a plan token binds the secrets in the operation it plans |
| `snapshot`, `diff` | `/snapshots/capture`, `/diff` | 1.7 or later |
| `diff` of two snapshot files, with no directory flags | `/diff` without a session | 1.8 or later |

This release adds no endpoint, so a 1.7 server provides everything the client
calls. A server without an endpoint the client needs is reported by name and
version (`unsupported_server`), not as a bare HTTP 404. `alder version --api-url
…` shows both versions. The snapshot format follows the snapshot compatibility
promise: version 1 is read by every 1.x release from 1.7 on.

---

## Limitations

- **No interactive password prompt.** Use a file, standard input or the
  environment.
- **A desired-state LDIF record that sets a sensitive attribute can be planned
  but not applied.** The plan's record withholds the value, and the server
  refuses a withheld value rather than writing an empty one. The web interface
  has the same limit. Use `--mode changes` for such records.
- **A subtree takes one apply per level to delete.** A plan is made against the
  directory as it is. Deleting an entry and its parent in one document therefore
  plans the parent as `conflict` (`has_children`), and `apply` refuses a plan with
  a conflict in it (exit 3) rather than applying only part of it. The changeset
  view in the web interface can apply the applicable part and plan again. From
  the command line, apply the deepest entries first, then their parents.
- **A recovery that removes an entry and one below it takes two applies.** Both
  compensations are planned against the directory as it is, so the parent is a
  `conflict` (`has_children`) until the child is gone, and `apply` refuses a
  plan with a conflict in it. The changeset view in the web interface applies
  the applicable part and plans again. The same holds for an entry deleted and
  added again within one original apply.
- **Every comparison must fit in one 16 MB request.**
- No colour, no shell completion, no configuration file, and no YAML or table
  output formats.
