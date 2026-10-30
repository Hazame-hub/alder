# Replication health across two servers

**Status: accepted (2026-10-30), every open question as recommended, and
built in 1.41.** What the build settled is in `docs/DECISIONS.md` under the
same date. Feature 5
of the 2026-10-21 batch. The 2026-10-22 review already settled its one
hard limit (`docs/DECISIONS.md`, "the later features of the same batch"):
**the verdict is in time only.** A change sequence number says *when*, per
origin, never *how many*; "behind by N changes" would need a changelog
Alder does not read.

---

## The problem

*"Is the replica keeping up?"* Since 1.39 Alder can show two servers'
replication records side by side: the session's own, and the reference's
(`GET /replication?on=reference`, the B6 second connection). What it shows
is two lists of raw change sequences, one per origin, in two columns. The
operator then does the comparison by eye: find the same origin in both
columns, read two 20-digit numbers, work out which is later and by how
much.

That is the part a tool should do, and it is the part an operator gets wrong
at 3am. It is also the part a monitoring job cannot do at all today: the
command line has no replication command, so there is nothing a cron job or
a CI step can branch on.

## What exists today

- **`internal/replication`** reads, for each replicated suffix, a list of
  `Cursor{Origin, At, Raw}`: OpenLDAP's `contextCSN` values on the suffix,
  389 DS's replica update vector (`nsds50ruv`) on the replica entry. Both
  mean "I hold everything from server N up to this moment". The origin is
  normalised to a decimal server id on both servers.
- **`Suffix.Behind(now)`** answers the one-server question: how long ago the
  newest change this server holds was made. It compares a peer's clock with
  this server's, which is why the UI already has to say "the other server's
  clock is ahead".
- **The Replication card** (`web/src/features/replication.tsx`) shows the
  reference's report beside the session's when a reference is attached, and
  tells the operator to "compare them".
- **What is not read:** 389 DS's `{replicageneration}` line is skipped by
  `parseRUV`, because one server alone had no use for it.
- **The rule** (REPLICATION.md, and the package comment): Alder never dials a
  host named in an agreement. Both servers here are ones the operator
  connected to.

## The proposal

### 1. A comparison, per suffix and per origin

A new pure function, `replication.Compare(a, b Report) Comparison`, with no
directory access. For each suffix DN present in both reports, and for each
origin present in either:

| What the two cursors say | Verdict for that origin |
|---|---|
| Same raw change sequence | **in step** |
| Both present, A's is later | **B is behind** by `A.At - B.At` |
| Present on A only | **B holds nothing from origin N** |

The span is computed between two stamps **made by the same origin's
clock**, so it is immune to clock skew between A and B, which is the problem
`Behind` has today. It reads as: *B has not yet received origin N's changes
made in the 40 seconds before 14:02:11.* It is never phrased as a count.

The suffix then gets one verdict from its origins: **in step** (all in
step), **behind** (one side is behind on at least one origin and ahead on
none), **both hold changes the other lacks** (each is ahead on a different
origin: normal in mid-flight on a multi-supplier pair, and worth saying in
those words rather than as two contradictory "behinds"), or **not
comparable** (below).

### 2. When two reports cannot be compared

The comparison refuses to give a verdict, and says why, when:

- **The two servers follow different replication models** (one OpenLDAP,
  one 389 DS). They cannot replicate to each other, so origin 1 on one and
  origin 1 on the other are not the same server. This uses the report's
  `Provider`, which `providerOf` already decides by the rules in charter
  section 3.
- **389 DS: the replica generations differ.** A generation changes when a
  replica is reinitialised from a different supplier lineage; vectors from
  two generations are not positions on the same history. `parseRUV` starts
  reading the `{replicageneration}` value into a new `Suffix.Generation`
  field (empty on OpenLDAP, which has no equivalent).
- **A suffix is replicated on one side only.** Reported as such, not as
  "behind".
- **Either side's cursors could not be read** (`Unread` names the suffix).
  "Incomplete", never "in step".

### 3. What "in step" does not mean

The verdict is said with its limit, in the same place as the existing
disclaimer:

> In step means both servers hold the same latest change from each origin.
> If an origin has made no change since, a link that stopped working looks
> the same. The age of the latest change is shown so you can tell.

The age of the newest change per origin (by the reader's clock, as today) is
shown beside each in-step line for exactly that reason. Alder does not write
a probe entry to prove the link moves: that would be Alder writing to a
directory nobody asked it to change.

### 4. In the web interface

The Replication card, when a reference is attached and both reports are
read, gets one row per suffix above the two columns: the suffix verdict,
then one line per origin (`server 1: in step`, `server 2: reference is
behind by 40s`). The two columns stay as they are, with the raw values,
because a change sequence is what operators paste to each other. Without a
reference nothing changes. The card asks the API for the comparison
(point 5) rather than computing it in the browser, so the command line and
the interface cannot disagree.

### 5. On the API and the command line

- **`GET /replication/compare`**, which reads the session's report and the
  reference's and returns the `Comparison`. 409 with the existing
  "no reference attached" error otherwise. Read-only, like `/replication`.
- **`alder replication --output FILE`** writes one server's report as a
  document, `format: alder-replication`, version 1, read as strictly as
  every other Alder document. A replication report is not configuration
  (2026-09-27, "replication's running state is not configuration"), so it
  is its own document kind rather than part of a config snapshot.
- **`alder replication compare A.json B.json`** compares two such files
  offline, the way `alder diff` compares two snapshots. This keeps the CLI's
  rule that one command is one connection, and lets a job read two servers
  from two places, at the same moment, with two different credentials.
- **Exit codes follow `alder diff`**: 0 in step, 1 behind or both holding
  changes the other lacks, 2 incomplete or not comparable (wins over 1).
  `--tolerate DURATION` (default 0) counts an origin behind by no more than
  that as in step, for a monitoring job that should not page on a change in
  flight. `--json` prints the `Comparison`.

## Why not the alternatives

- **Contact the peers named in the agreements.** It would make the verdict
  one click, and it is exactly the report "that can be pointed at anything"
  the 1.25 decision rules out.
- **Count missing changes.** 389 DS's changelog could answer it on a
  supplier; OpenLDAP's accesslog only where the overlay is configured. Ruled
  out by the 2026-10-22 decision, and the time span answers the operational
  question ("how stale is the replica?") better than a count.
- **Compare with the reader's clock** (`Behind` on each side, then
  subtract). Two skewed clocks make that number wrong in a way the operator
  cannot see. The same-origin comparison needs no clock but the origin's.
- **A threshold in the web interface** that turns a row red. Any default is
  wrong for someone; the span is shown and the operator judges. The CLI's
  `--tolerate` is the one place a number is needed, and the caller sets it.

## What it changes, when built

`internal/replication` (`Compare`, `Generation`, tests for every row of the
tables above with both CSN forms); `api/openapi.yaml` (`/replication/compare`,
`ReplicationComparison`) and the regenerated server and client;
`internal/cli` (`replication`, `replication compare`, the document reader);
the Replication card; `docs/REPLICATION.md`, `docs/CLI.md`,
`docs/COMPATIBILITY.md` (new document kind, new exit-code use) and a
DECISIONS entry. Conformance: the harness already runs a supplier and a
consumer of each server, so one case per server reads both, compares them,
and expects "in step" after a write has settled, and "behind" when the
comparison is computed from a report read before the write.

## Open questions for review

1. **Offline CLI comparison, or two connections in one command.**
   Recommended: offline, `alder replication` + `alder replication compare`,
   as above. The alternative is `--reference-host` and a second copy of
   every connection flag, which doubles the CLI's surface for one command.
2. **`--tolerate` default.** Recommended: 0, so the verdict is exact unless
   the caller asks otherwise. A non-zero default would hide a stopped link
   for that long on every install.
3. **"Both hold changes the other lacks" as its own verdict** rather than
   "behind" twice. Recommended: yes, and exit 1 like behind.
4. **Read the 389 DS generation** and refuse to compare across generations.
   Recommended: yes. Without it, a reinitialised replica can be reported in
   step or behind against a history it is no longer part of.
