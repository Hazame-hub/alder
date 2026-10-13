# Projects in the web interface

**Status: proposed, for review. Nothing here is implemented, and nothing here
is a decision yet.** `docs/PROJECT.md` ends its later slices with "Opening a
project in the browser is not in this specification"; this is that
specification. The recommendation adds one endpoint to the API, which the 1.x
compatibility promise then keeps for good, so it is recorded in
`docs/DECISIONS.md` when, and if, the review accepts it, and code waits until
then. It follows the way the project format and the second server were
proposed: a specification first.

---

## The problem

An Alder project (`docs/PROJECT.md`) is reachable only from a terminal:
`alder project validate`, `plan --env`, `apply --env` and `fmt`. The person
reviewing a directory change in a pull request often has the web interface
open against that directory already, and has no way to ask it *if this
repository is the truth, what would have to change here?* without installing
the client, exporting a password variable and writing an environment for a
directory they are already connected to.

The pieces the browser would need exist. Import LDIF already reads a file in
the browser and plans it as desired state (`POST /plan`, `mode: desired`); the
changeset already plans again, applies with the plan's baselines and offers a
recovery bundle; "Load recovery bundle" already turns a file into staged
changes for review. What is missing is the project itself: its `alder.yaml`,
its globs, its validation rules and its unmanaged-entries report, which today
live in the client.

## What exists today

- **`POST /plan` with `ldif` and `mode: desired`.** The engine of
  `project plan`: the client sends every project record as one document.
- **`POST /snapshots/capture`.** What `project plan` reads each managed
  subtree with to list the unmanaged entries, whole or not at all.
- **`internal/project`.** The format, the globs, every validation rule
  including "no secrets", and the desired-state document in `managed` order.
  It reads from the operating system's filesystem (`os.Stat`,
  `filepath.Glob`, `os.Open`).
- **`internal/cli/unmanaged.go`.** The unmanaged comparison, by `dn.Equal`.
  Client code, not reachable from the server.
- **The web interface.** Import LDIF (plan as desired state, stage), the
  changeset (plan, review as LDIF or Ansible, apply, recovery bundle), the
  plan summary component, the recovery loader, saved connections that hold no
  password.

## What any design has to keep

- **Stateless** (charter, section 2). The server keeps nothing: no project on
  disk, no project in memory between requests, nothing that survives a
  restart.
- **Credentials never persist** (section 7, rule 5), and an uploaded file
  never decides where credentials go or which secret the server reads.
- **One write path** (section 7, rule 1). A project's changes are applied as
  every change is: planned, shown, applied with baselines through
  `Session.Apply`.
- **One implementation of the format.** The review of the project format
  (decision 1) chose to reuse the server's packages rather than write a second
  LDIF parser; the same reasoning rules out a second project validator, glob
  matcher or DN comparison in TypeScript.
- **No secrets in project files.** Enforced by the same check as the CLI's,
  not a lookalike.
- **Which directory a plan is for is never in doubt.** The CLI prints the
  environment and host above every plan; the browser must say as much.
- **A project is not a deleter.** Unmanaged entries are reported, never
  acted on.

## How a project reaches the browser

### Option A: the person chooses a folder; the browser sends its files to one stateless endpoint (recommended)

A *Open project* button with an `<input type="file" webkitdirectory>` (and a
folder dropped on the same area). The browser enumerates the folder's names
and reads the contents of only two kinds of file: `alder.yaml` at the top of
the chosen folder, and every `*.ldif` below it, skipping dot-directories
(`.git`). It sends them, as relative paths and text, to
`POST /project/plan`. The server builds an in-memory filesystem from the
body, runs `internal/project` over it, and on a valid project plans the
document and reports unmanaged entries, in one response. When the response is
sent, the files are gone from the server.

- **For:** one implementation of everything -- validation, globs, the secrets
  rule, the document's order, DN comparison. The server reads only what the
  person chose to send; nothing on the server's filesystem is reachable by
  any path. No new state anywhere.
- **Against:** a new endpoint, and a refactor: `internal/project` reads
  through an `fs.FS` (`os.DirFS` for the CLI, the uploaded set for the
  server), and the unmanaged comparison moves out of `internal/cli` into
  `internal/project` so both callers share it.

### Option B: the browser does the project, and calls only existing endpoints

TypeScript reads `alder.yaml`, expands the globs, validates, concatenates the
document, calls `POST /plan`, captures each subtree and compares DNs.

- **For:** no new endpoint.
- **Against:** a second implementation of the format, the glob semantics
  (`**` refused, sorted matches), the secrets list and DN equality -- exactly
  what the format's review refused. The browser's `dnEquals` is an
  approximation used for display; it is not `dn.Equal`, and an unmanaged
  report built on it would be wrong on the DNs that need escaping. A project
  that validates in the browser and not in CI, or the reverse, is worse than
  no browser support.

### Option C: the server reads a project from its own filesystem

Either a path in the request, or `alder serve --project DIR`.

- **Against, path in the request:** any caller with a session names a path
  and the server process reads it -- a file read on the server's host for
  anyone who can bind to the directory, which is not the same population. The
  symlink rule would be the only thing between a request and `/etc`.
- **Against, a serve flag:** the project becomes configuration of the
  process -- state, pinned to one checkout that goes stale while the
  repository moves -- and the `bind-password-file` of an environment would
  name a file on the server's host. It also serves one project to every
  session, whichever directory each is connected to.

### Option D: the server fetches a Git URL

- **Against:** an outward connection to a host the request names (the
  `--allowed-targets` problem, for HTTP this time), credentials for private
  repositories, a Git dependency in the server, and a clone that is state
  however briefly it lives. Rejected.

### A zip file

A variant of A, not an alternative. Unpacked on the server it brings zip
bombs and path traversal in entry names; unpacked in the browser it needs a
library. Not in the first slice; if it is wanted, it is unpacked in the
browser and sent in the same shape as a folder.

## Recommendation: Option A

It is the only option that keeps one implementation of the format and keeps
the server from reading anything it was not sent. The endpoint it adds is
justified by what the browser lacks -- an LDIF parser, the project validator
and DN equality, all of which exist on the server and nowhere else in the
web interface.

### Which directory it plans against

**The session's, always.** The browser is already one directory, connected
with credentials held in the session. `alder.yaml`'s `environments` are
validated, so a project that fails `alder project validate` for a bad
environment fails here too, and are then **ignored entirely**:

- **`api-url`, `api-ca-file`:** the browser is already talking to an Alder.
  Following an `api-url` from an uploaded file would send the session to a
  server the file chose.
- **`host`, `port`, `tls`, `ca-file`, `server-name`, `bind-dn`,
  `config-bind-dn`:** the session is already connected. Re-dialling from a
  file is a second connection, which is the second-server proposal's question
  and not this one's; and a `ca-file` is a path the server would have to
  read.
- **`bind-password-env`, `bind-password-file`:** never read. On the server
  they would name the *server's* environment variables and files, which an
  uploaded file must never be able to choose. This is the reason "who may
  change `alder.yaml`" (PROJECT.md) needs no new answer: in the browser,
  `alder.yaml` decides *what* is planned and nothing about *where* or *as
  whom*.

There is no `--env`. The plan is for the session's directory, under the
session's bind, and says so.

### What is shown above the plan

As the CLI prints environment and host, the project view heads every plan
with the project and the directory, in that order:

> **Project `directory/`** -- 2 subtrees, 214 entries, from `alder.yaml`
> **Planned against `ldap.example.com:636` as `cn=admin,dc=example,dc=com`** --
> this session's directory. The environments in `alder.yaml` are not used
> here.

The host is the session's own (`SessionInfo`), never a value from the file.
The same line heads the changeset when the plan is staged, and the apply
confirmation names the host, as the second-server proposal asks every write
dialog to.

### How each command maps

| CLI | Web | Endpoint |
|---|---|---|
| `validate` | The first step of *Open project*; problems listed by file and line, in the CLI's words, and nothing planned. | `POST /project/plan` (refused with `project_invalid`) |
| `plan --env` | The plan, in the existing plan summary, under the heading above. | `POST /project/plan` (the same planner as `POST /plan`, `mode: desired`) |
| unmanaged report | Below the plan, per subtree: the DNs, each a link to the entry; *unreadable* with the reason when a capture is refused. No delete button. | the same response |
| `apply --env` | *Stage for review*: the plan's changes go to the changeset, which plans again and applies with baselines, recovery bundle and Ansible export as it does today. | none new (`/plan`, `/changeset/apply`) |
| `fmt` | Not offered. | none |

**Apply goes through the changeset**, as the recovery loader does, so there
is no project-specific write path and no new apply endpoint. Staging a project
requires an empty changeset: a changeset that is "the project plus three
other things" is no longer the project's plan, and the heading above would be
false. *Plan again* re-sends the files held in page memory; picking up edits
made on disk means choosing the folder again.

**`fmt` is out.** It rewrites files in a repository; a web page cannot write
to the person's folder without the File System Access API, which is
Chromium-only and is a write to someone's working tree from a browser tab.
Formatting belongs in a pre-commit hook or CI, where `fmt --check` already is.

### API sketch

```yaml
/project/plan:
  post:
    operationId: planProject
    summary: Validate a project sent as files, plan it against this session's directory, and report unmanaged entries
    description: |
      Stateless. The files exist for this request only. alder.yaml
      environments are validated and never used: the plan is the session's.
      Nothing is written.
    requestBody:
      ProjectPlanRequest:
        files:                        # 1..1000 items, alder.yaml required
          - path: people/engineering.ldif   # relative, slash-separated, clean,
            content: "dn: ..."              # no "..", no leading "/", unique
    responses:
      "200":
        ProjectPlan:
          project: { subtrees, entries, environments: [names only] }
          host:     # the session's, as SessionInfo reports it
          plan:     Plan              # exactly POST /plan's schema
          unmanaged: [{ base, entries, error? }]   # the CLI's --json shape
      "400": project_invalid          # problems: [{ file, line, message }]
             bad_request              # a path that is not clean, a duplicate
      "401": not connected
      "413": too large                # the server's body limit
      "502": the directory could not be read, so no plan could be made
```

A session is required even though validation reads no directory: the
endpoint parses up to the body limit of untrusted text, and like
`/import/ldif` it is not offered to unauthenticated callers.

### Security

- **The server reads only the request.** The in-memory filesystem holds the
  sent files and nothing else; a glob cannot reach past it, and paths are
  checked to be clean relative names before anything is parsed. The symlink
  rule has nothing left to guard on the server: there are no links in a
  request body.
- **The browser reads only what it sends.** Choosing a folder lets the page
  enumerate names; it reads the contents of `alder.yaml` and `*.ldif` only,
  never `.git`, and holds them in page memory, never in browser storage.
- **Secrets.** The CLI's rule, by the same code: any matched file carrying an
  attribute `schema.IsSensitive` names is refused, with file, line and
  attribute. A `.ldif` that no glob matches is sent but never parsed, and is
  not part of the project. Nothing from the files is logged.
- **No outward reach from a file.** No `api-url` followed, no host dialled,
  no environment variable or file on the server read.
- **`--read-only`.** Opening and planning work; staging works; applying is
  refused, as every apply is.
- **Untrusted text.** File names and problem messages quoting the files are
  shown with control and bidirectional characters made visible, as the
  recovery loader does.

### Limits

- **Body:** the server's existing 16 MiB `BodyLimit`, unchanged. The CLI
  allows 64 MiB per file; a project bigger than 16 MiB stays a CLI project,
  and the view says so rather than truncating.
- **Files:** at most 1,000 in a request; `alder.yaml` at most 1 MiB, as now.
- **Plan:** bounded like an import, as `POST /plan` is.
- **Unmanaged:** each subtree captured whole or reported unreadable, at the
  snapshot's 50,000-entry ceiling, as in the CLI.
- **Apply:** the changeset's 2,000 changes. A plan with more can be read and
  not staged, and the view says to use `alder project apply`.

### Does it need a charter change?

**Section 2: no.** The project format is in scope (2026-10-10); this is a
way to reach it from the interface the charter is about. The decision that
accepts this records the new endpoint and the rule that environments are
ignored in the browser. **Section 7: no.** Rule 1 holds through the changeset,
rule 5 because no credential moves, rule 6 by the existing secrets check.

## Scope boundary, and the smallest first slice

**First slice:** *Open project* (folder picker) beside Import LDIF;
`POST /project/plan`; problems, or the heading, the plan summary and the
unmanaged list; *Stage for review* into an empty changeset. The
`internal/project` `fs.FS` refactor and the move of the unmanaged comparison
come first and change nothing the CLI does; the conformance suite runs the
endpoint against both servers with `examples/project/`, and asserts its plan
and unmanaged report equal `alder project plan --json`'s.

**Later, if asked for:** dropping a folder; a zip, unpacked in the browser;
the environment hint in open question 2.

**Out, and not scaffolded:** `fmt`; choosing an environment or dialling one;
writing back to the folder; remembering a project across reloads; deleting
unmanaged entries from the report; saved plans.

## Open questions for review

1. **Which files the browser sends.** `alder.yaml` and every `*.ldif` outside
   dot-directories, or the names of every file plus the contents of only
   those? Recommendation: the first. A `files` pattern that matches a file
   not ending in `.ldif` then matches nothing here and is reported as such,
   which a CLI project would not see; sending names as well would let the
   server say "matches `people/a.txt`, which was not sent" at the cost of
   disclosing every file name in the folder.
2. **Naming a matching environment.** Show "matches environment *prod* in
   `alder.yaml`" when exactly one environment's `host`, `port` and `tls` equal
   the session's? Recommendation: not in the first slice. It is a label, not
   a choice, but the CLI's guarantee is that the environment named is the one
   used, and here it would only be a guess that happens to agree.
3. **`alder.yaml` below the chosen folder.** Accept a folder whose
   `alder.yaml` is one or more levels down (a repository root chosen instead
   of `directory/`)? Recommendation: only at the top in the first slice, and
   say so when it is not found; picking among several is a later step.
4. **One request or two.** Plan and unmanaged report in one response, or the
   report as a second call so a slow capture does not hold the plan back?
   Recommendation: one, as the CLI makes both on every run; split only if
   the request timeout is hit in practice.
5. **Staging into a non-empty changeset.** Refuse, or offer to replace what is
   staged? Recommendation: refuse with the count of what is staged and a
   *Clear changeset* action beside it; replacing silently loses work.
