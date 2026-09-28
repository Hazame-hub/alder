# Claim sheet — "the replica's indexes do not match the primary"

**Date:** 2026-09-28 · **Build:** `e9a28a5` (v1.30.0, released) ·
**Target:** `http://127.0.0.1:8897`, single binary, embedded SPA ·
**Directory:** the `test/compose` harness, **OpenLDAP**, disposable data.

Task 3 from the [first sheet](../2026-09-27-since-the-first-audit/claims.md),
rewritten against real drift that the harness already contains, and run on
OpenLDAP — which no audit has touched. The 2026-09-27 run covered task 1 only.

## The task

> The replica is meant to match the primary. It does not: somebody added
> indexes to one and not the other. Bring the replica into line — and tell me
> whether Alder can do that, or whether I am back in `slapd.conf`.

The drift is real and was not planted for this run. Read from both servers
before the walkthrough began:

| | primary `10636` | replica `20636` |
|---|---|---|
| `member eq` | yes | **no** |
| `memberUid eq` | yes | **no** |
| `alderTeam eq,sub` | yes | **no** |
| `l,st eq` | no | **yes** (one value naming two attributes) |

`l,st eq` is the interesting one: OpenLDAP lets a single `olcDbIndex` value
name several attributes, so removing "the index on `l`" means editing a value
that also indexes `st`. Claim 8 below says Alder refuses that and says why.

## Ideal path

Connect to the replica → Snapshots & drift → capture the primary as the
standard → Compare → tick the three missing indexes → Review → Apply.
**Eight interactions**, one screen and one dialog, plus the connection.

## The claim sheet

Everything here is claimed by the CHANGELOG, `docs/DECISIONS.md` or the
release notes for 1.18 to 1.30. Anything that cannot be found in the browser
is a finding whether or not the code exists.

| # | Claimed capability | Where it should live | Source |
|---|---|---|---|
| 1 | Connect to a second server and be asked only for what it does not remember | Connection screen | 1.12, restyled #151 |
| 2 | Capture this server's configuration as a document, without a database | Snapshots & drift → capture | 1.13 |
| 3 | Compare a captured configuration against the server in front of you | Snapshots & drift → Compare | 1.13 |
| 4 | See which attributes each side indexes, on which backend, and what each index covers (eq, sub, pres) without a further click | Compare → "Configuration objects", rows named `index:<suffix>/<attribute>` | 1.23 |
| 5 | Create an index on either server, with Alder deriving that server's own write for it | Configuration objects → a row marked "Alder can create this" → tick → Review → apply | 1.23 |
| 6 | Have the created index appear as an ordinary reviewable change: LDIF first, confirm second | The review dialog, same as every other write | charter §7 rule 1 |
| 7 | Remove an index | Configuration objects → a row marked "Alder can remove this"; a removal is never ticked for you | 1.23 |
| 8 | Be told **in words** why a particular index cannot be removed | A refused row reading `index_value_shared` (the OpenLDAP value names other attributes too) or `system_index` | 1.23 |
| 9 | Read the intro to the configuration objects block and not be misled | The block's own text. **Known wrong since 1.23**: it says Alder creates "one kind: an OpenLDAP overlay whose module the server has already loaded". Indexes are the second kind. | noted in the 2026-09-27 sheet |
| 10 | See in the configuration entry editor that `olcDbIndex` is "not changed by Alder" rather than "not in Alder's model" | Directory → `olcDatabase={1}mdb,cn=config` → Edit | 1.18 + 1.23 |
| 11 | Have preflight call an index a change Alder can make, rather than manual work blocking portability | Preflight → run against this directory | 1.23 sweep |
| 12 | Never be shown or export a credential the configuration holds (`olcRootPW`, `olcSyncrepl`, `olcDbCryptKey`) | The same configuration entry, its LDIF panel, and the Export dialog | 1.26 security fix |
| 13 | See access rules the server holds on a configuration entry, read and never offered for editing | Entry viewer → `olcAccess` under "Shown here, edited elsewhere", with a sentence saying why | 1.30 |
| 14 | Not be offered a whole-schema replace: `olcAttributeTypes` on `cn={0}core,cn=schema,cn=config` is shown, not edited | Entry viewer → that entry | 1.30 |
| 15 | Be told a request is still running before it fails, with the seconds | Header badge, after four seconds | 1.30 |
| 16 | An eyebrow label above each screen title, a fade-in, larger headings | Every screen | #145–#153 |

## Notes for the auditor

- Two servers: primary `127.0.0.1:10636`, replica `127.0.0.1:20636`, both
  LDAPS with a self-signed certificate — tick "do not verify".
- Bind `cn=admin,dc=alder,dc=test` / `alder-admin`. The configuration
  identity, needed for anything under `cn=config`, is
  `cn=admin,cn=config` / `alder-config`.
- Disposable data. Writes are expected; this is the point of the task.
