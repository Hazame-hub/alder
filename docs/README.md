# Alder documentation

## Start here

| | |
|---|---|
| [Getting started](GETTING-STARTED.md) | Run Alder against a practice directory, make a first reviewed change, then do the same from a terminal. About ten minutes. |
| [Feature tour](FEATURES.md) | Everything Alder does, grouped by the job you are doing, with screenshots. |
| [The README](../README.md) | Install, the security posture, and the licence. |

## Words Alder uses

These come up on every page. Each links to its reference.

| | |
|---|---|
| **Change record** | One LDIF operation (add, modify, rename, delete) on one entry. Every write Alder makes is one, shown to you before it is sent. |
| **Plan** | What a set of change records would actually do to the directory as it is now, worked out without writing anything. Applying sends exactly the plan you saw, and is refused if the directory has moved since. [PLAN.md](PLAN.md) |
| **Changeset** | Several changes staged in the browser, reviewed as one LDIF document or Ansible playbook, and applied in order. [Feature tour](FEATURES.md#change-it-safely) |
| **Saved plan** | A plan written to a file with `alder plan --out` or `alder project plan --out`, applied later with `--plan`: those changes and no others, refused whole if the directory has moved. [CLI.md](CLI.md#a-saved-plan) |
| **Recovery bundle** | A file of compensating changes for what a change applied, derived from each entry as it was just before. Not a backup and not a rollback: loading it is a new change, planned and reviewed. [RECOVERY.md](RECOVERY.md) |
| **Snapshot** | A checksummed JSON capture of a subtree, the schema or the server's configuration, kept by you and compared later. [SNAPSHOTS.md](SNAPSHOTS.md) |
| **Change package** | A portable description of what you intend to change, carried to another directory and checked against it there. [CHANGE-PACKAGES.md](CHANGE-PACKAGES.md) |
| **Preflight** | A read-only report of what a package or snapshot would carry across to the directory you are connected to. [PREFLIGHT.md](PREFLIGHT.md) |
| **Reference server** | A second directory a session holds read-only beside the one it is connected to, so data, schema, configuration and single entries can be compared without reconnecting. [Feature tour](FEATURES.md#drift-snapshots-and-migrations) |
| **Project** | An `alder.yaml` beside LDIF files in a repository: a directory's intended state, planned against named environments. [PROJECT.md](PROJECT.md) |

## Using Alder

| | |
|---|---|
| [CLI.md](CLI.md) | The command line: `snapshot`, `diff`, `plan`, `apply`, `project`, `package`, `preflight`, `sign`, `verify`. |
| [PROJECT.md](PROJECT.md) | The Alder project format, and `alder project validate`, `plan`, `apply` and `fmt`. See also [`examples/project/`](../examples/project/). |
| [PLAN.md](PLAN.md) | Plans: what each classification means, impact, and the tokens that make an apply refuse a stale plan. |
| [RECOVERY.md](RECOVERY.md) | Recovery bundles, and how recoverable a change is. |
| [SNAPSHOTS.md](SNAPSHOTS.md) | Data snapshots and comparisons, and staging the differences back. |
| [SCHEMA-SNAPSHOTS.md](SCHEMA-SNAPSHOTS.md) | Schema snapshots, and comparing schemas by meaning rather than text. |
| [CONFIG-SNAPSHOTS.md](CONFIG-SNAPSHOTS.md) | The server's own configuration: capture, compare, and the settings Alder can change. |
| [CHANGE-PACKAGES.md](CHANGE-PACKAGES.md) | Carrying an intended change from one directory to another. |
| [PREFLIGHT.md](PREFLIGHT.md) | Checking what would carry across before a migration. |
| [SIGNING.md](SIGNING.md) | Signing snapshots, packages and bundles, and requiring signatures. |
| [ACCESS-CONTROL.md](ACCESS-CONTROL.md) | Reading the access rules that bear on an entry. Alder never writes them. |
| [PASSWORD-POLICY.md](PASSWORD-POLICY.md) | Reading the password policy and account state of an entry. |
| [REPLICATION.md](REPLICATION.md) | Reading what a server records about its own replication. |

## The project

| | |
|---|---|
| [COMPATIBILITY.md](COMPATIBILITY.md) | What 1.x promises: byte-stable LDIF and Ansible, an API that only grows. |
| [DECISIONS.md](DECISIONS.md) | Decisions settled once, and why the alternatives were rejected. |
| [CHARTER.md](../CHARTER.md) | What Alder is, what it will and will not do. |
| [CONTRIBUTING.md](../CONTRIBUTING.md) | Setting up, the rules that are not style preferences, and a map of the code. |
| [`ui-audits/`](ui-audits/) | Black-box audits of the web interface, each against a real task. |
| [`screenshots/`](screenshots/) | The images on these pages, and how they were made. |
