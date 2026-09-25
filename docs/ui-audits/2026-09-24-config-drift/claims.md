# Claim sheet — what the interface is supposed to let an operator do

Taken from the router, the API surface and the release notes, not from the UI. Anything
here that cannot be found by clicking is a finding in itself.

| # | Claimed capability | Where it should live | Source |
|---|---|---|---|
| 1 | Connect to a directory, with a second identity for the configuration tree | connection screen | `web/src/features/connect.tsx`, 1.0 |
| 2 | See what the session can do and where the schema and configuration live | Overview | `/session`, 1.2 |
| 3 | Browse the DIT and read an entry | Directory | 1.0 |
| 4 | Search with a filter builder or a raw filter | Search | 1.0 |
| 5 | Browse the schema and follow cross-links | Schema | 1.0 |
| 6 | Capture a snapshot of directory data, the schema, or the configuration | Snapshots | 1.7, 1.10, 1.13 |
| 7 | Compare two snapshots, or one and the directory now | Snapshots → Compare | 1.7 |
| 8 | See which configuration settings differ and which Alder can change | Snapshots → configuration comparison | 1.13 |
| 9 | See which configuration objects differ, and create or remove an overlay | Snapshots → configuration comparison | 1.16 |
| 10 | Stage differences into the changeset and review them as LDIF | Changeset | 1.0, 1.7 |
| 11 | Plan a change against the directory before applying it | Changeset → check | 1.3 |
| 12 | Apply the plan, and see what a configuration change does not recover | Changeset → apply | 1.9 |
| 13 | Preflight a package or snapshot against this directory | Preflight | 1.12, 1.14 |
| 14 | See whether a loaded document was signed, and by whom | Snapshots, Preflight | 1.15 |
| 15 | Build and validate a change package | Packages | 1.11 |
| 16 | Import LDIF | Import | 1.4 |

## The task for this audit

**A configuration setting drifted on the OpenLDAP server. Find it and put it back.**

This is the task the last four releases were built for, it crosses four screens
(Overview → Snapshots → Changeset → back), and it is the one an operator would do at
2am. The drift is created outside Alder before the run, as a real drift would be.

Ideal path, as a well-built tool would offer it: capture the configuration → compare
with the directory → tick the setting → review → apply. **Five interactions.**
