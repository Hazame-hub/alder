# Configuration snapshots and configuration comparison

A configuration snapshot answers:

> What was this server's own configuration, at a moment I can name?

A configuration comparison answers:

> What changed in it since, or how does it differ from that other server of the
> same software — and which of those settings can Alder actually change?

Alder 1.13 captures a directory server's persistent configuration as a
deterministic document, compares two of them — or one and the live server —
setting by setting, and turns the few differences it already knows how to write
into ordinary change requests that go through the same plan and apply as every
other write.

It is the same three endpoints with `kind: config`, the **Configuration**
option in the Snapshots tab, and `alder snapshot --kind config` and `alder diff`
from a shell. Nothing is stored on the server.

**The rule this whole feature is built on:**

> Configuration is provider-specific unless equivalence is explicitly proven.

OpenLDAP's `olcDbMaxSize` is not 389 Directory Server's `nsslapd-cachememsize`.
An overlay is not a plugin. Alder therefore keeps **one model per provider**,
compares a configuration only with another capture of the same provider, and,
asked to compare two different ones, says so and stops rather than producing a
list of differences that would all be artefacts of the comparison. There is no
portable configuration model here, and adding one is not deferred work — it is
work that would have to prove each equivalence first, one setting at a time.

---

## What is captured

| | OpenLDAP | 389 Directory Server |
|---|---|---|
| Provider identifier | `openldap` | `389ds` |
| Configuration tree | `cn=config` (the server's `configContext`, or where it answered) | `cn=config` |
| Resources | the global entry, module lists, databases, overlays | the global entry, encryption, backends under `cn=ldbm database`, the mapping tree, plugins, replication |
| A database or backend is named by | its `olcSuffix` | its `nsslapd-suffix`, or its path of names |
| An overlay or plugin is named by | its name, under its database | its name |
| Not captured | everything under `cn=schema,cn=config` | `cn=tasks` and every `cn=monitor` entry |
| Harness numbers | 6 resources, 88 settings, 2 withheld | 182 resources, 1431 settings, 6 withheld |

Every other entry in the tree is still captured, as an **area** in the `other`
section: a model that does not recognise something must not make it disappear.

### What a setting is

One attribute of one configuration entry, normalised by that provider's model:

```json
{
  "section": "limits",
  "resource": "database:dc=alder,dc=test",
  "key": "olcIdleTimeout",
  "values": ["1800"],
  "type": "int",
  "mutability": "writable",
  "comparison": "normalised",
  "dn": "olcDatabase={1}mdb,cn=config"
}
```

Its **identity** is `section / resource / key`, with the key lower-cased, inside
a document that names its provider. It is never a position. A database
renumbered from `{1}mdb` to `{3}mdb` is the same database, because it serves the
same suffix; two overlays both called `memberof`, on two databases, are two
different settings. The number in a DN is where something sat, and it changes
when something unrelated is removed.

### Sections

`server`, `network`, `backend`, `limits`, `logging`, `tls`, `plugins`,
`replication`, `performance`, `password_policy`, `access_control`, `schema`,
`other`. A section says where to look. **Two providers having a section with the
same name is not a claim that anything in them corresponds.**

### Values

- A value is compared as the model normalised it: booleans lower-cased, integers
  canonicalised, and the `{n}` ordering prefix stripped from values that carry
  one — a configuration that differs only in renumbering is the same
  configuration.
- `ordered: true` marks a setting whose order is the configuration (`olcAccess`,
  `olcSyncrepl`, `olcLimits`, the module load list). Those keep the server's
  order and are compared in it; everything else is compared as a set.
- `comparison: raw` marks a value with a syntax of its own that nothing in Alder
  parses — an access rule, a `syncrepl` statement, an index definition. It is
  compared as text, the difference says so, and it is never offered as something
  to change.
- A value that is not text (389 DS keeps replication state as bytes) is recorded
  as base64 with `type: binary`, rather than dropped, so the document is not
  quietly smaller than the configuration.

### Secrets

A setting whose value is a credential is recorded as **present and counted**,
never as a value:

```json
{ "section": "backend", "key": "olcRootPW", "sensitive": true, "withheld": 1, "mutability": "unknown" }
```

There is **no digest, no hash, no fingerprint** — nothing a guess could be
checked against. Both harness servers keep a root password in their
configuration (OpenLDAP's in the clear, 389 DS's as PBKDF2), and neither
appears in a capture. What is withheld is a value that could be used: a
credential, a secret, a key, or an attribute whose name ends in the word
password. What is *not* withheld is the large family of settings that describe
passwords without holding one — `passwordMinLength`, `passwordStorageScheme`,
`nsslapd-pwpolicy-local`, a key *file*'s path. Withholding those would hide
ordinary configuration and protect nothing.

A withheld setting still compares: if the number of values changes, the
comparison reports it as modified. It is never actionable, because the value was
never read.

### The runtime boundary

Configuration is what persists. These are not configuration and are not
captured:

- monitoring entries and counters (`cn=monitor` at any depth, `cn=tasks`);
- the attributes a server maintains for itself — `entryUUID`, `entryCSN`,
  `modifyTimestamp`, `creatorsName`, `nsUniqueId`, `numSubordinates`,
  `structuralObjectClass`, `objectClass` and the rest;
- schema definitions, which are a schema snapshot's subject. Which schema
  entries are *loaded* is configuration, so OpenLDAP captures their names and
  nothing else.

A document lists what it deliberately left out in `coverage.excluded`:
`runtime-state`, `schema-definitions`, `sensitive-values`,
`server-maintained-attributes`.

**Operational metadata is not a secret and is still a fact about the machine.**
Paths, ports, host names, socket and file names stay in the document, marked
`operational: true` and counted. *Config snapshots may contain operational
infrastructure metadata even when secrets are withheld.* Treat a configuration
document as you would treat the server's configuration file.

---

## The format

Same document family as the other two — `alder-snapshot`, version 1 — with kind
`config` and a field set of its own:

```json
{
  "format": "alder-snapshot",
  "version": 1,
  "kind": "config",
  "createdAt": "2026-09-17T10:00:00Z",
  "source": { "provider": "openldap", "vendor": "OpenLDAP", "vendorVersion": "2.6.7", "root": "cn=config" },
  "completeness": "complete",
  "incomplete": [],
  "coverage": { "sections": ["server", "..."], "excluded": ["runtime-state", "..."] },
  "counts": { "resources": 6, "settings": 88, "withheld": 2, "operational": 9, "readOnly": 13 },
  "resources": [
    { "section": "backend", "kind": "database", "name": "dc=alder,dc=test",
      "dn": "olcDatabase={1}mdb,cn=config", "label": "{1}mdb" }
  ],
  "settings": [ "..." ],
  "checksum": "sha256:..."
}
```

- **Canonical.** Resources and settings sorted by identity, values sorted except
  where order means something, counts derived. Two captures of an unchanged
  configuration are byte-identical but for `createdAt`.
- **Checksummed** over everything but `createdAt` and `checksum` itself. A
  withheld value contributes its count, never a digest of itself. The checksum
  is integrity, not authenticity.
- **Read strictly.** An unknown field, another kind, a version from the future,
  a second document in the file, a provider with no model, a setting recorded
  twice, a setting naming a resource the document does not hold, counts that do
  not match, a withheld setting carrying a value, a value that is not UTF-8 or
  is longer than 64 KiB — each is refused rather than partly read.
- **Never partial by accident.** Anything a capture could not read is in
  `incomplete`, with a reason: `insufficient_access`, `read_failed`,
  `unsupported_section`, `read_limit_reached`. A document with reasons is
  `completeness: partial`, and a comparison involving one is never complete.

A release before 1.13 refuses `kind: config` as a kind it does not know. Every
later 1.x release will keep reading version 1 of it.

---

## Comparing

`POST /diff` with two configuration sides, or `alder diff a.json b.json`, or the
Snapshots tab. Two documents need no directory and no LDAP credentials; a live
side needs the session.

**Source is where you are, target is what you are comparing with**, as
everywhere else in Alder: `added` means the target holds a setting the source
does not.

| Answer | Meaning |
|---|---|
| `added` / `removed` | The setting is on one side only, and both sides were read in full. |
| `modified` | Both hold it, with different values — or a withheld setting whose number of values changed. |
| `unchanged` | Both hold it, equal under this setting's comparison. |
| `unknown` | One side did not read it. **Absence in a partial capture is never a removal.** |

and, separately from what the difference *is*, what Alder could *do*:

| `actionable` | Meaning |
|---|---|
| `writable` | The provider's model states this is a setting an administrator sets, both sides agree, it is not a secret, and Alder already changes it through the ordinary plan. |
| `read_only` | The server maintains it, it is fixed at start, it is a secret, or it appears on one side only. Reported, never staged. |
| `unknown` | The model does not know. Reported, never staged. |

### Two providers are not compared

If the two sides are different providers, the answer is a **provider mismatch**:
no items, no counts, `complete: false`, and a summary of what each side holds —
provider, vendor, completeness, how many settings and resources, and the size of
each section. The UI shows the two summaries side by side; the CLI prints them
and stops.

```
$ alder diff openldap-config.json ds389-config.json
These configurations belong to different servers' software, and their settings
are not the same settings. Alder does not translate configuration between
providers, and does not compare them setting by setting.

openldap-config.json: openldap, 88 settings in 6 resources (complete)
ds389-config.json: 389ds, 1431 settings in 182 resources (complete)
```

Comparing the harness servers this way produces zero items where a naive
comparison would have produced roughly 1,500 spurious additions and removals.

---

## From differences to changes

Only where a proven write path already exists:

- The **source must be the live directory** — the change is made there.
- The setting must exist on **both** sides, so this is a modification of an
  attribute on an entry that is already there. A setting that appears or
  disappears would mean adding or removing a configuration entry, which Alder
  does not do.
- Both sides' models must call it `writable`.
- It must not be a secret, and it must not be compared as text: a value nothing
  in Alder parses is never written back.

The result is one ordinary `ChangeRecord`: a `modify` with a `replace` on the
live side's own entry — never the other side's DN, because two servers may keep
the same setting in differently named entries. From there it is the ordinary
pipeline: changeset, plan, review, apply. The plan reports it as a
`config` target, and **recovery is unavailable for configuration changes**, as
it is for schema.

Everything else is reported and left alone, with a reason on the item:
`sensitive_withheld`, `not_comparable`, `no_write_path`, `not_read`.

> **Diff does not create a new config write capability.** The comparison only
> points at settings Alder already changes through the ordinary plan. Each
> provider's list is explicit in `internal/config/openldap.go` and
> `internal/config/ds389.go`.

### Which settings Alder changes (1.14)

A setting is on a provider's list only if Alder has **written it, read it back
and restored it, through a comparison, a plan and an apply, on the harness**.
The conformance suite does that for every setting a fresh capture calls
writable -- not for a list kept in the test -- so a setting added to a model
without a proof fails the suite. In 1.14 that is 28 settings on OpenLDAP and 36
on 389 Directory Server, counting each database or backend that holds one.

| OpenLDAP | 389 Directory Server |
|---|---|
| `olcIdleTimeout`, `olcWriteTimeout`, `olcSizeLimit`, `olcTimeLimit` | `nsslapd-idletimeout`, `nsslapd-ioblocktimeout`, `nsslapd-sizelimit`, `nsslapd-timelimit` |
| `olcMaxDerefDepth`, `olcMaxFilterDepth` | `nsslapd-max-filter-nest-level`, `nsslapd-groupevalnestlevel` |
| `olcThreads`, `olcConnMaxPending`, `olcConnMaxPendingAuth` | `nsslapd-threadnumber`, `nsslapd-maxthreadsperconn` |
| `olcLogLevel`, `olcGentleHUP` | the access, error, audit, security and statistics log levels, whether each log is enabled, and each log's rotation, retention, disk-space and file-count limits |
| `olcDbMaxSize`, `olcDbMaxEntrySize`, `olcDbSearchStack`, `olcDbRtxnSize`, `olcDbNoSync` | `nsslapd-lookthroughlimit`, `nsslapd-pagedlookthroughlimit`, `nsslapd-rangelookthroughlimit`, `nsslapd-pagedsizelimit`, `nsslapd-dncachememsize` |
| `olcReadOnly` (on a database), `olcLastMod`, `olcLastBind`, `olcMonitoring` | `nsslapd-readonly` (on a backend), `nsslapd-require-index` |

Three rules narrow the lists further, and each is a fact rather than a
preference:

- **Read-only mode is offered only on an ordinary database or backend.** On
  OpenLDAP's global entry, the frontend or the configuration database, and on
  389 DS's global entry, it would also stop the write that switches it back.
- **A setting the server says needs a restart is never offered.** 389 DS
  publishes that list itself, as `nsslapd-requiresrestart` on `cn=config`, and
  Alder reads it at capture time. `nsslapd-cachesize` and
  `nsslapd-maxdescriptors`, which 1.13 listed, came off this way.
- **A value compared as text is never offered**, even on a writable setting.
  `olcDbIndex`, which 1.13 listed, came off this way: writing another server's
  index list back would reindex from a structure Alder cannot read.

Left out on purpose: anything that can lock a person out (TLS, SASL, the root
DN, access rules, `olcLocalSSF`, the socket buffer limits); anything read only
at start (`olcDbMaxReaders`, `olcListenerThreads`); anything that silently
invalidates an index (the `olcIndex*` settings); the password policy on both
servers; `nsslapd-security`, schema and syntax checking; and
`nsslapd-cachememsize`, which 389 DS refuses to set while cache autosizing is
on, its default.

A boolean keeps the server's spelling in a snapshot -- `TRUE`, `off` -- because
that spelling is what a change writes back, and OpenLDAP refuses a lower-case
`true`. Two spellings of one boolean compare equal.

---

## On the command line

```console
$ alder snapshot --kind config --output config.json
Captured the openldap configuration at cn=config (88 settings in 6 resources, 2 withheld; complete) to config.json, sha256:...

$ alder diff @live config.json
Configuration of openldap. Added means set on the target and not on the source; removed, the other way round.

88 settings compared
      0 added
      1 modified
      0 removed
     87 unchanged
      0 unknown
      1 that Alder can change through a plan

modified   limits             olcIdleTimeout
    entry    cn=config
    -        0
    +        1800
    Alder     can change this setting through a plan (--stage limits//olcidletimeout)

$ alder diff @live config.json --stage olcIdleTimeout --changes-out changes.json
$ alder plan --changes changes.json
```

`--kind config` takes no `--base`, `--scope`, `--filter` or `--operational`: a
configuration is one tree, the server's own, and there is nothing to narrow.
`--stage-deletion` is refused for the same reason additions are not derived.

See [CLI.md](CLI.md).

---

## In the browser

Snapshots tab → **Configuration** → Capture. A captured or loaded document is
summarised from the document itself; comparing is where it is decoded strictly
and its checksum checked.

The comparison view filters by text, section and kind of difference, and has
"Only what Alder can change" and "Hide withheld". Only a `writable` row can be
selected; selected rows stage into the changeset and go to the review screen
like any other change. A provider mismatch replaces the whole list with the two
summaries and an explanation.

---

## Preflight (1.14)

A configuration snapshot can be preflighted against the directory you are
connected to: `alder preflight config.json`, or the Preflight tab. Against the
**same server software** each setting is reported as already present, a
difference Alder changes through a plan, a difference an operator has to make, a
setting or a whole database, overlay or plugin the target lacks, a secret
(excluded), or a path, host or port (excluded: it names the machine). Against
**other software** nothing is judged, and the report says configuration is not
evaluated, exactly as every other report does. See
[PREFLIGHT.md](PREFLIGHT.md#configuration).

## Not in 1.14

- **Configuration conversion.** No OpenLDAP → 389 DS translation, in either
  direction, at any level.
- **A portable desired-state language.** A configuration document is a state of
  one server's software, not a specification.
- **ACL analysis.** `olcAccess` and `aci` are captured, marked `raw`, and never
  interpreted. Alder still does not edit access control.
- **Creating or removing configuration entries.** Adding a database, loading a
  module, enabling a plugin: reported, never derived. A narrow list -- OpenLDAP
  overlays whose module is already loaded, and switching 389 DS plugins on or
  off -- is in scope for a later release; see the decisions log.
- **Applying a configuration wholesale.** There is no "restore this
  configuration" button, as there is no "restore this subtree" one.
- **Recovery for configuration changes**, which stays unavailable.
- **Preflight across providers.** Configuration compatibility between two
  products is not evaluated, and the report says so. See
  [PREFLIGHT.md](PREFLIGHT.md).
- **Storage.** Alder keeps no snapshots, no history and no schedule.

---

## See also

- [SNAPSHOTS.md](SNAPSHOTS.md) — data snapshots, the format, and the comparison model
- [SCHEMA-SNAPSHOTS.md](SCHEMA-SNAPSHOTS.md) — the other non-data kind
- [PLAN.md](PLAN.md) — what a plan does with a configuration change
- [SECURITY.md](../SECURITY.md) — what a document may and may not carry
- [COMPATIBILITY.md](COMPATIBILITY.md) — which releases read which documents
