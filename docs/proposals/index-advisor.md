# Index advisor

**Status: accepted (2026-10-30), every open question as recommended, and
built in 1.41.** What the build settled is in `docs/DECISIONS.md` under the
same date. Feature 6
of the 2026-10-21 batch. The 2026-10-22 review settled its input
(`docs/DECISIONS.md`, "the later features of the same batch"): **the advisor
reads a log the operator uploads or pipes in, and nothing else.** No
accesslog read over LDAP, because that would be Alder fetching a log. The
same review corrected the charter so the index path it stages through is
stated there ("the charter names the index path 1.32 built").

---

## The problem

1.32 gave indexes a door: `GET /config/indexes` lists what each backend
indexes, and `POST /config/indexes/candidate` derives the change that adds
one, which is planned, previewed and applied like any change. What it
cannot tell the operator is **which** index to add. That answer is in the
server's log: the searches it ran without an index, how often, and how long
they took. Reading it today means grepping an access log, extracting
filters by hand, and working out which attribute and which match type each
one needed.

## What exists today

- **The index list and the candidate** (`internal/config/indexes.go`): both
  servers' indexes reported as the same kind of thing, an attribute with the
  types it covers (`eq`, `sub`, `pres`, `approx`), per backend that serves a
  naming context. "Already indexed" is an answer (`exists`), and the refusals
  are sentences with a code.
- **A strict RFC 4515 filter parser** (`internal/filter`), fuzzed.
- **Config snapshots** (`alder snapshot --kind config`) carry the index list,
  so the advisor can run with no connection at all.
- **No log parser anywhere.** The server's own body limit is 16 MiB.

## The proposal

### 1. What it reads

Two log formats, recognised by their lines, never by a flag that names a
vendor:

| Server | The lines used | What they give |
|---|---|---|
| 389 DS access log | `conn=N op=M SRCH base=… scope=… filter="…"` and its `RESULT … etime=… notes=U` / `notes=A` | Each search's filter, base, duration, and the server's own word that it was partly (`U`) or fully (`A`) unindexed |
| OpenLDAP with `loglevel stats` | `conn=N op=M SRCH base=… filter="…"` and `SEARCH RESULT … etime=…` (2.5+), plus `<= mdb_equality_candidates: (attr) not indexed` | Filter, base, duration; the "not indexed" lines name the attribute but no operation, so they corroborate rather than decide |

Lines are paired by `conn` and `op`. A line Alder does not recognise is
counted and skipped, never an error: real logs interleave everything. A
search whose `RESULT` is missing (the log was cut) still counts for its
filter, without a duration. Which exact lines each server version writes is
to be confirmed against the harness's four servers before any parser is
written; the table is what the documentation of 389 DS 2.x and OpenLDAP
2.6 says.

### 2. What it decides

For each search, the filter is parsed and reduced to the
`(attribute, match type)` pairs it tests: `(mail=x)` is `mail eq`,
`(cn=*smith*)` is `cn sub`, `(manager=*)` is `manager pres`, `(cn~=x)` is
`cn approx`. Then, per backend (the one whose suffix holds the search base):

- **A candidate** is a pair the backend does not index, from a search the
  server marked unindexed or that took longer than a threshold
  (`--slower-than`, default 100ms).
- **It is ranked** by the total time those searches took, then by count.
- **It carries its evidence:** how many searches, total and worst duration,
  the first and last time seen, and up to three example filters **with
  their values replaced by `…`** (point 4).
- **It is not a candidate** when the attribute already has that index type,
  or when the attribute is not in the server's schema.
- **It is shown but cannot be staged** when the attribute is indexed for
  other types only (`mail eq` exists, the searches need `mail sub`). 1.32
  answers `exists` for any index on the attribute, and CONFIG-SNAPSHOTS.md
  says Alder does not change the types of an index that already exists, so
  the report says which type is missing and that adding it is the
  operator's own change (open question 5). The same holds for a candidate
  the candidate endpoint would refuse: the reason is shown, so the operator
  knows why the slow search stays slow.
- **What it never suggests:** an index for `objectClass` presence (every
  entry has one, and both servers already treat it specially), or removing
  an index. The log says nothing about an index nobody used *in that
  window*, and removing one is the change most likely to make the next
  morning slow.

### 3. Where it runs

- **On the command line, offline:**
  `alder indexes advise --log access.log --config config.json`, or the log
  on standard input (`-`). The index list comes from a config snapshot, so
  the log never leaves the machine it is on, it can be any size (it is
  read as a stream), and nothing connects anywhere. `--json` prints the
  report. Exit 0 with no candidate, 1 with at least one, 2 when part of the
  log could not be attributed to a backend in the snapshot.
- **In the browser:** a **Find missing indexes** action on the indexes
  panel takes a log file (at most 16 MiB, the server's existing limit, which
  is an excerpt of a busy server's log, and the panel says so and names the
  CLI for more). `POST /config/indexes/advice` parses it against the
  session's live index list and returns the same report. The upload is read
  in memory and dropped when the response is written.
- **Staging a candidate** goes through `POST /config/indexes/candidate`,
  unchanged: the advisor writes nothing, and its "Stage" button opens the
  ordinary change dialog with the LDIF preview. From the CLI, the report
  names the attribute and type, and the operator runs the usual plan and
  apply.

### 4. A log is sensitive

Access logs hold filter values (a user's name, an email address), bind DNs
and client addresses.

- **No value leaves the parser.** Example filters are re-serialised from
  the parsed tree with every assertion value replaced by `…`; bind DNs and
  addresses are not read at all.
- **Nothing is stored or logged.** Alder's own log gets one line per advice
  request: the byte and line counts and the number of candidates.
- **The web upload uses the session's existing same-site checks**
  (`samesite.go`), and the endpoint needs a configuration identity like the
  rest of `/config/indexes`.

### 5. What it says about an index it suggests

Every candidate carries the two facts 1.32 already documents and an
operator tends to forget:

> Adding an index is not building it. Neither server indexes existing
> entries on its own: run a `reindex` task on 389 DS, or `slapindex` on
> OpenLDAP. Until then, the index can make searches miss entries.
> Every index also makes every write to that attribute slower.

Reindexing stays outside Alder, as REPLICATION.md and CONFIG-SNAPSHOTS.md
already say.

## Why not the alternatives

- **Read the accesslog overlay, or `cn=monitor`, over LDAP.** Ruled out on
  2026-10-22: Alder would be fetching a log.
- **Parse in the browser.** It would remove the 16 MiB limit, but it means
  a second filter parser in TypeScript that must agree with the Go one, and
  the CLI could not share it.
- **Suggest from the schema** (index everything that is `EQUALITY`-capable
  and often searched). Without the log there is no "often"; that is a list
  of guesses.
- **Raise the body limit for this route.** Possible later; a log of a busy
  server is gigabytes, so no web limit fits, and the CLI is the answer for
  those.

## What it changes, when built

A new `internal/indexadvice` (log readers, filter reduction, ranking,
redaction), with fuzz targets for both log formats beside the existing
ones; `internal/cli` (`indexes advise`); `api/openapi.yaml`
(`/config/indexes/advice`, `IndexAdvice`) and the regenerated server and
client; the indexes panel; `docs/CONFIG-SNAPSHOTS.md` (indexes section),
`docs/CLI.md`, `docs/COMPATIBILITY.md` and a DECISIONS entry. Conformance:
on each harness server, run searches on an unindexed attribute, capture
the server's own log from the container, feed it to the advisor, expect
that attribute as the top candidate, stage it, and expect `exists` on a
second run.

## Open questions for review

1. **Both entry points, or the CLI only.** Recommended: both, as above.
   The CLI handles real log sizes; the web action is the path the 1.32
   audit asked for, starting from the panel where indexes are added.
2. **The default threshold.** Recommended: 100ms, plus anything the server
   itself marked unindexed whatever its duration. `--slower-than` and a
   field in the web action change it.
3. **Example filters with values masked.** Recommended: yes, three per
   candidate. Without examples a candidate is hard to trust; with values
   the report becomes as sensitive as the log.
4. **OpenLDAP's "not indexed" lines** as corroboration only, since they
   carry no operation. Recommended: yes. If the harness shows a version
   where they can be paired, revisit then.
5. **A missing type on an existing index.** Recommended: report it and
   stage nothing, as above. Letting Alder add a type would change the 1.32
   rule: easy on 389 DS (one more `nsIndexType` value), a rewrite of an
   `olcDbIndex` value on OpenLDAP, which is what `index_value_shared`
   refuses today. If it is wanted, it is its own decision after this ships.
