# Saved searches, kept in a project

**Status: accepted (2026-10-22), Option B, with every open question
answered as recommended and one change: there is no `POST /search/file`.**
*Save as file* renders the YAML in the browser from the URL parameters, and
`alder project validate` is the check. The decision is in
`docs/DECISIONS.md` ("2026-10-22 — saved searches: accepted"). The first of
the next open-source features. Nothing here changes the
charter; one decision of the project format's review is extended, and the
client's dependency rule gains one package (see "Does it need a charter
change?").

---

## The problem

A team that owns a directory asks the same handful of questions every week:
*which accounts are locked*, *who has no manager*, *which groups have an
owner outside `ou=people`*, *every service account under `ou=apps`*. Each is a
base, a scope, a filter and the attributes worth looking at. Today each lives
in somebody's shell history as an `ldapsearch`, in a wiki page that drifted
from the directory a year ago, or in a browser bookmark of Alder's search
page.

Counted from the code, not from an audit: re-running a search somebody else
wrote takes finding the link or the filter, opening the search page, pasting
the base and the filter, choosing the scope, and running it -- and then the
columns are wrong, because the search page always asks for the same six
attributes (`web/src/features/search.tsx`, `cn objectClass uid mail ou
description`) and shows whichever of five of them came back. There is no way
to see `pwdAccountLockedTime` or `manager` in the results table at all; the
row has to be opened.

What a team wants is the search **written down once, reviewed like code, and
opened by name** -- from the browser and from a shell.

## What is true today, read from the code

- **A search is already a link.** Base, scope, filter and limit are in the
  URL (`web/src/lib/route.ts`, decision 2026-09-05 "the URL, and a landing
  page"). The attributes and columns are not: they are fixed in
  `search.tsx`.
- **`GET /views` already calls itself "saved searches".** Users, Groups and
  Organizational units are filters and columns *derived from the schema*
  (`internal/api/objectviews.go`, `ObjectView` in `api/openapi.yaml`). They
  are computed per connection and never stored (decision 2026-09-05 "object
  views and the table": "the moment one of these becomes a list Alder keeps,
  v1 stops being stateless"). The object views also have a column picker
  (`components/column-picker.tsx`), held per tab and deliberately not
  stored (`features/objects.tsx`: "a stored preference would be state on a
  server that has none").
- **The project format** (`docs/PROJECT.md`, `internal/project`) is a
  directory with `alder.yaml` and LDIF files: managed subtrees and
  environments. Version 1 refuses unknown keys in `alder.yaml`, and
  `managed` must name at least one subtree. Nothing in it describes a
  search.
- **Opening a project in the browser** sends only `alder.yaml` and `*.ldif`,
  outside dot-directories, to `POST /project/plan` (decision 2026-10-13,
  "projects in the web interface: the specification is accepted"). Nothing
  of the project is kept after the response.
- **The client has no search command.** `alder snapshot --filter` captures a
  subtree as a snapshot file; nothing prints a search's entries.
  `GET /export/ldif` takes `dn`, `scope`, `filter` and `limit`, and no
  attribute list.
- **localStorage holds two things**, both connection settings and never a
  password: the last connection and up to 25 saved ones
  (`lib/recent-connection.ts`, decision 2026-10-13 "saved connections").
  The changeset is held in memory, not even in `sessionStorage` (decision
  2026-09-04).
- **The client's dependency rule** allows LDIF, DNs and the
  sensitive-attribute list "only through the server's own packages"
  (PROJECT.md, review decision 1). `internal/filter` depends on `internal/dn`
  and the standard library only; it links no LDAP code.

## What any design has to keep

- **Stateless** (charter, section 2): no list of searches on the server,
  nothing that survives a restart.
- **Every search is paged and bounded** (section 7, rule 4): a saved search
  runs through `POST /search` with its limit, and nothing else.
- **Filters are never interpolated** (rule 3): a saved filter is parsed by
  `internal/filter` when the file is read and again when it runs.
- **Sensitive values are never shown** (rule 6): a saved search goes through
  the same handler, so withheld values stay withheld.
- **The project format stays reviewable**: what a person reads in the file
  is what runs. PROJECT.md says it is "not a templating language".

## Options

### Option A: a `searches` key in `alder.yaml`

```yaml
searches:
  locked-accounts:
    base: ou=people,dc=example,dc=com
    filter: (|(pwdAccountLockedTime=*)(nsAccountLock=true))
    attributes: [uid, cn, pwdAccountLockedTime, nsAccountLock]
```

- **For:** one file, one reader, no new file kind; the browser already sends
  `alder.yaml`.
- **Against:** `alder.yaml` is the file that decides where credentials go,
  and PROJECT.md tells teams to protect it ("What `alder.yaml` decides, and
  who may change it"). Putting every search in it means every new search
  needs the review an environment change needs, and the file a team most
  wants to keep still becomes the one that churns. It is also a new key in
  version 1, so an older Alder refuses a project that uses it outright --
  including for `plan` and `apply`, which have nothing to do with searches.

### Option B: one search per file, `*.search.yaml`, beside the LDIF

```
directory/
├── alder.yaml
├── people/
│   ├── engineering.ldif
│   └── locked-accounts.search.yaml
└── groups/
    ├── groups.ldif
    └── without-owner.search.yaml
```

A search is a small YAML document of its own, found by its name ending
`.search.yaml` anywhere below `alder.yaml` outside dot-directories. Its name
is its path without the suffix (`people/locked-accounts`).

- **For:** `alder.yaml` does not change, so an older Alder plans and applies
  a project with search files exactly as before: it never reads them. A
  search is reviewed on its own, and a CODEOWNERS rule can treat
  `*.search.yaml` differently from `alder.yaml`. One file per search makes a
  Git diff of a changed search one hunk.
- **Against:** a second file kind, so the browser's rule "only `alder.yaml`
  and `*.ldif` are sent" grows by one suffix, and the project is no longer
  entirely described by `alder.yaml` -- `validate` has to say which search
  files it found.

### Option C: a file format of its own, outside projects

An `alder-searches` document anyone can load, as a recovery bundle or a
saved plan is loaded.

- **For:** no project needed.
- **Against:** it is a second place to keep searches and a second loader,
  and the brief is "stored as files in an Alder project". A search that
  belongs to no project has no suffix to move with an environment and no
  repository to be reviewed in. Without a project, the URL already is the
  portable form.

### Option D: searches in localStorage

- **Against, and out:** per browser, unreviewed, invisible to the command
  line, and a list a script on the origin can rewrite. It is the opposite of
  "reviewed like code", and the brief excludes it.

## Recommendation: Option B

It keeps `alder.yaml` as it is, which keeps every existing project and every
older Alder working, and it keeps the file that names credentials separate
from the files anyone on the team should be able to add.

### The file

```yaml
# people/locked-accounts.search.yaml
version: 1
title: Locked accounts
description: Accounts either server currently holds locked.
base: ou=people,dc=example,dc=com
scope: sub
filter: (|(pwdAccountLockedTime=*)(nsAccountLock=true))
attributes: [uid, cn, mail, pwdAccountLockedTime, nsAccountLock]
columns: [uid, mail, pwdAccountLockedTime, nsAccountLock]
limit: 500
```

| Key | | Meaning |
|---|---|---|
| `version` | required | `1`. Any other is refused, not guessed at. |
| `base` | required | A DN, parsed with `internal/dn`. At or below the project's `suffix` when it has one, so an environment's suffix can move it. |
| `filter` | required | RFC 4515, parsed with `internal/filter`. Refused if it does not parse. |
| `scope` | optional | `base`, `one` or `sub` (default `sub`), as `POST /search`. |
| `attributes` | optional | Attribute names to ask for. Default: the columns, plus `objectClass`. |
| `columns` | optional | Which of the attributes the table shows, in order. Default: the attributes. Every column must be in `attributes`. |
| `limit` | optional | 1 to 10,000, as `POST /search`; default 100. |
| `title`, `description` | optional | Text for a person. Shown through `safeText` and the client's `safe`, as every string Alder did not write is. |

Rules, all checked by `alder project validate`:

- **Strict**, as `alder.yaml` is: an unknown key is refused, a key given
  twice is refused, keys are read exactly as written.
- **No sensitive attribute** in `attributes` or `columns` (`schema.IsSensitive`,
  the table and its subtypes once a schema is known). The server would withhold
  the values anyway; refusing the name keeps the file from implying otherwise,
  the same way `POST /inventory` refuses one before searching.
- **Attribute names are names**: a descriptor or a numeric OID, with
  options. Whether the directory defines them is not checked offline; the
  search answers that, as it does today.
- **No variables.** A filter is literal. `(uid={user})` is an ordinary
  filter that matches a `uid` of `{user}`, not a placeholder. A
  parameterised search is out (see "What it will not do").
- **Bounded**: a file of at most 64 KiB, at most 200 search files in a
  project, a filter of at most 8 KiB, at most 100 attributes. Read with the
  bounds of 2026-10-16 ("reading a project is bounded by its size").
- **Names are unique without regard to case**, since `People/Locked` beside
  `people/locked` is a trap on a case-insensitive filesystem.
- A search file **does not change the project's digest**. The saved-plan
  digest covers the project's entries (decision 2026-10-15), so adding a
  search to a project never invalidates a reviewed plan.

### In an environment with its own suffix

The base is rebased with `dn.DN.Rebase`, as every managed base is. **The
filter is not rewritten**: a filter value that names a DN below the suffix
(`(manager=uid=ana,ou=people,dc=example,dc=com)`) is reported with the
existing `contains_dn` reason (decision 2026-10-16 "a rebase says the DNs it
leaves inside values") and sent as written, so the search the reviewer read
is the search that runs.

### The command line

```sh
alder project search                         # list the project's searches, offline
alder project search --env dev locked-accounts            # run one: LDIF on stdout
alder project search --env dev locked-accounts --json     # POST /search's response
```

- **Without a name** it lists each search's name, title, base and filter,
  and connects to nothing, like `validate`.
- **With a name** it opens a session for the environment exactly as
  `project plan` does, and writes the entries as LDIF through
  `GET /export/ldif` with the search's base, scope, filter, limit and
  attributes -- the same byte-stable LDIF an export is (COMPATIBILITY.md
  section 1), so a search's result can be committed and diffed. `--json`
  writes the server's `POST /search` response, every page, as one array.
- **Exit codes** from the existing table: 0 when the search ran, whether or
  not it matched anything; 3 when the project, a search file included, does
  not validate; 7 for a name the project does not have, as for any other
  wrong command line; 8 when the directory refused the search. A search that stopped at its limit says so on standard
  error, and `--detailed-exitcode` is not offered: a search is not a plan.
- The client links `internal/filter` to validate offline. The rule "no LDAP
  code" holds -- `internal/filter` has none -- and its list of allowed server
  packages grows by one, held by the existing `go list -deps` test.

### The web interface

- **Opening a project lists its searches.** *Open project* sends, besides
  `alder.yaml` and `*.ldif`, the files named `*.search.yaml` under the same
  rules (outside dot-directories, within the request limits). The server
  validates them with `internal/project` and returns them in the
  `POST /project/plan` response, beside the plan and the unmanaged report.
  A search file that does not validate makes the project invalid, with its
  file and line, as an LDIF file does.
- **Opening a search is navigating to a link.** Each listed search is a link
  to the search page with its base, scope, filter and limit, and two new URL
  parameters, `attrs` and `cols`. The search page asks for `attrs` (or its
  six defaults without them) and shows `cols`. A saved search opened in the
  browser is therefore exactly a search somebody could have typed, and it
  can be sent on as a link.
- **The heading says where it came from**: *people/locked-accounts, from
  directory/*, above the search boxes, until the person edits a box. Edited,
  it is an ordinary search, as an edited staged project plan is an ordinary
  changeset (decision 2026-10-13).
- **Saving goes the other way as a file.** The search page gains *Save as
  file*, which downloads the current search as a `.search.yaml`, for the
  person to commit to the project. Alder never writes into the person's
  folder. *Accepted with a change:* the file is rendered in the browser from
  the search's URL parameters, as typed, and `alder project validate` is the
  check of what was written -- the one reader, rather than a second endpoint
  that would render what the server parsed. No endpoint is added for it.
- **The project, and its searches, are not kept.** A refresh forgets them,
  as it forgets an opened project today.

### localStorage: what it holds, and what it does not

**Saved searches are never written to browser storage.** The file in the
repository is the source of truth; a copy in `localStorage` would go stale
the first time somebody edited the file, would be shown as if it were the
reviewed search, and is writable by any script on the origin. The browser's
own conveniences stay what they are: the URL is the per-browser form (a
bookmark of a search link is a personal saved search, and needs nothing
from Alder), and the saved connections stay connections. The column picker
on the object views stays per tab.

### API delta (1.x: only additions)

```yaml
/search:            # unchanged
/project/plan:
  # request: ProjectFile entries may be *.search.yaml; unchanged schema
  # response: ProjectPlan gains an optional
  searches:
    type: array
    items: { $ref: SavedSearch }

/export/ldif:
  # query gains an optional `attributes` (comma-separated). Omitted, the
  # export is every user attribute, as before.

SavedSearch:
  required: [name, file, base, scope, filter, attributes, columns, limit]
  properties: name, file, title, description, base, scope, filter,
              attributes, columns, limit
```

`Error.problems` already carries a file and line, so a bad
search file needs no new error shape. The `.search.yaml` format, version 1,
is covered from the release that ships it, as `alder-plan` version 1 is.

### Tests

- **Conformance**, one case on both servers: the example project gains two
  search files; `alder project search --env` and the search the web's link
  posts return the same entries, and an environment with its own suffix runs
  the rebased base. The LDIF of `--env` equals `GET /export/ldif` with the
  same parameters.
- **Unit**: the strict reader (unknown key, duplicate key, case-twin names,
  bounds, a filter that does not parse, a sensitive attribute, a column not
  in `attributes`), the same problems from `Load` and `LoadFS` word for word,
  and a fuzz target on the file reader beside `FuzzUnmarshal`.
- **Playwright journey**: open the example project, open *Locked accounts*,
  and check the rows on screen against `ldapsearch` run in each server's
  container with the same base and filter.

## What it will not do

- **No parameters, variables or prompts in a saved search.** A filter with a
  hole in it is a filter built from text at run time, which is the thing
  rule 3 exists to prevent; filling it safely would need a second filter
  builder. A team that needs "the groups of user X" has the entry's own
  *Which groups is this in* link.
- **No searches stored by Alder**, on the server or in the browser.
- **No writing to the project from the browser.** *Save as file* downloads;
  the person commits.
- **No `alder search` outside a project.** `ldapsearch` already exists, and
  Alder's search page already shows the command it would run.
- **No sorting, grouping or computed columns** in the file. The table sorts
  what arrived, as today.
- **No change to `/views`.** The object views stay derived from the schema;
  a saved search is a search somebody wrote.

## Does it need a charter change?

**No.** Search is section 2, item 8, and the project format is on the record
(section 2, "Added since"). A saved search writes nothing and is read like
any search. Three things need a `docs/DECISIONS.md` entry when accepted:

1. The project format gains a second file kind, outside `alder.yaml`.
   PROJECT.md describes a project as intended *state*; a saved search is
   not state, and the entry should say so rather than leave it implied.
2. The web-projects rule "only `alder.yaml` and `*.ldif` are sent" grows to
   `*.search.yaml`.
3. The client's allowed server packages grow by `internal/filter`.

## Open questions for review

1. **Option B's discovery by suffix, or a `searches:` glob list in
   `alder.yaml`?** Recommendation: by suffix, so `alder.yaml` and older
   releases are untouched. A glob list would make `alder.yaml` the complete
   description again, at the cost of a new key an older release refuses.
2. **Should a project be allowed to hold searches and no managed subtree?**
   Today `managed` must name one. Recommendation: no, not in this slice; a
   searches-only project is easy to allow later and hard to take back.
3. **Does the search page take `attrs` from a link for every search, not
   only saved ones?** Recommendation: yes. It is the same page, and a link
   that shows the attributes it asked for is the point.
4. **Is `--json` one array or one document per page?** Recommendation: one
   array, bounded by the search's limit, which is at most 10,000 entries.
5. **Should `/export/ldif`'s new `attributes` also reach the Ansible
   export?** Recommendation: not in this slice; the playbook enforces whole
   entries, and a partial one is a different promise.
