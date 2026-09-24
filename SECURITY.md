# Security

Alder holds a directory administrator's bind credentials and can modify the
directory everything else in an organisation authenticates against. Treat a
vulnerability in it accordingly.

## Reporting

Report privately through GitHub's **Report a vulnerability** button on the
Security tab. Do not open a public issue.

Please include what you were connected to (OpenLDAP or 389 DS, and the version),
what you did, and what happened. A failing test against the harness in
`test/compose` is the most useful thing you can send.

Expect an acknowledgement within a few days. There is no bounty.

## What Alder promises

These are design commitments, not aspirations. A break in any of them is a
vulnerability, and each is enforced somewhere you can go and read.

**Credentials never persist.** A bind password lives in this process's memory
for the life of a session and nowhere else — not on disk, not in a token the
browser can read, not in `localStorage`. Restarting the server logs everyone
out. See `internal/session`.

**Credentials are never logged.** Not at any level, not in an error message, not
in a stack trace. The LDAP driver strips the server's diagnostic text from bind
failures for the same reason.

**Sensitive attributes never reach the browser.** `userPassword` and the rest of
the deny list in `internal/schema/syntax.go` are withheld from every API
response, with only a value count reported. They are omitted from LDIF exports
unless explicitly requested.

**The same holds for changes on their way in.** A plan record, a change preview
and an apply response show a sensitive value only as its length. Before 1.5 a
change that set `userPassword` directly had the value echoed back in its preview
and its apply response — to the browser that had just sent it, but a response is
not where a password belongs. A withheld value posted back is refused rather
than written as empty.

**A plan's token binds a secret without exposing it.** A baseline is an HMAC
under a per-process random key over the exact operation and the state it
depends on. Since 1.6 a secret in the operation — a new password, or a value of
a sensitive attribute being written — contributes an HMAC of the value under a
key derived from that process key and the session, so applying a different
password than the one planned is refused. It is never a plain digest: without
the process key, which never leaves memory, nothing can be tested against a
token, and with the server's help a guess only reproduces a token inside the
session that planned it. Tokens mean nothing to another process or after a
restart. Secrets already stored in the directory are still bound by value count
only.

**DNs and filters are never built by concatenation.** Both are parsed into typed
values and re-rendered with the correct escaping. `internal/dn` has no exported
way to build a DN from text, and a filter typed by a user is parsed by
`internal/filter` rather than passed through. There is a conformance test that
tries to inject through the filter builder.

**LDIF URL references are refused.** RFC 2849 allows `attr:< url`, and Alder
never fetches one. This process holds a privileged bind, so following a URL out
of a user-supplied document would be arbitrary file disclosure via `file://` and
server-side request forgery via `http://` in a single feature. LDAP controls in
LDIF are refused too: silently ignoring one would apply a different change than
the document describes.

**Snapshots hold no secrets Alder can recognise, and are read as untrusted.** A
snapshot records a deny-listed attribute as a count of values. It holds no value,
no hash and no other digest, so it is not an offline guessing oracle. It carries
no bind DN, credential, server address or local path. It does carry every other
value the bind DN could read, which may include secrets your directory stores
under attribute names Alder does not know are sensitive. Reading a snapshot, for
inspection or comparison, is read-only: it fetches nothing and writes nothing.
The JSON is decoded strictly: unknown fields, trailing content, invalid DNs,
entries outside the stated scope and sensitive attributes carrying values are
all refused. It is bounded at 50,000 entries within the server's 16 MB request
limit. A download's filename is built by the server from a sanitised base DN and
a timestamp, and nothing in a document becomes a path. The interface renders
every value as text. The checksum covers everything but `createdAt` and detects
corruption of that content; it is not authentication or a signature, since
anyone who edits a file can recompute it. A comparison proposes changes but never applies them: its candidates
are staged and go through the plan like any other write.

**Schema snapshots are read as untrusted too.** A schema snapshot (1.10) holds
the definitions a server publishes, and no credential, server address or
configuration. Decoding is strict: unknown fields, trailing content, malformed
or duplicate OIDs, fields that disagree with their definition text, counts or
coverage that do not match the document, inheritance cycles, more than 50,000
definitions and a definition over 64 KiB are all refused. Every `NAME`, `DESC`,
extension value and definition is directory-controlled text: the interface and
the terminal escape control and bidirectional characters when showing it, and
never change it where it is kept or sent. A schema comparison proposes changes
and never applies them. A removal is never selected by default, a dependency is
never added on anyone's behalf, and a definition is never reported as unused.

**Configuration snapshots withhold secrets, and still describe a machine.** A
configuration snapshot (1.13) records the server's own configuration. A setting
whose value is a credential -- a root password, a replication credential, a
database encryption key -- is recorded as **present and counted**: no value, no
hash, no salt, no truncation, no digest of any kind, so it is not an offline
guessing oracle and a comparison of two captures can say only whether the number
of values changed. It carries no bind DN, no session credential and nothing
about the Alder host.

It does carry **operational infrastructure metadata**, marked `operational` and
counted: file and directory paths, ports, host names, socket names, module
paths, suffixes and backend names. *Config snapshots may contain operational
infrastructure metadata even when secrets are withheld.* Treat a configuration
document the way you would treat the server's configuration file: it tells a
reader how the machine is laid out. Runtime state -- monitoring entries,
counters, task entries and the attributes a server maintains for itself -- is
not configuration and is not captured at all.

Decoding is strict, as for the other kinds: an unknown field, another kind, a
version from the future, a provider with no model, a setting recorded twice, a
setting naming a resource the document does not hold, counts that disagree with
the document, a withheld setting carrying a value, a value over 64 KiB or one
that is not valid UTF-8 are all refused. Values are directory-controlled text
and are escaped where they are shown. A configuration comparison proposes
changes and never applies them, and it proposes them only for the settings
Alder already wrote through the ordinary plan: a document cannot make a setting
writable, because the live server's own model has to agree. Comparing two
different products' configuration compares nothing at all.

**A signature says who made a document; the key stays with the person.** Signing
(1.15) is done by `alder sign` on the operator's own machine with an Ed25519
private key Alder's server never receives: no flag supplies one, no request
carries one, and the server's configuration has nowhere to put one. The server
verifies only, against public keys named at startup with `--trusted-keys`.

A signature wraps a document rather than entering it, so nothing about a
snapshot, package or bundle changes and their checksums keep their meaning. What
is signed is the payload's compact form under a context line naming the format
and version, so a signature cannot be lifted onto another document. A document
whose signature does not match is refused before anything reads the payload; a
valid signature by a key the server was not told to trust is reported as
untrusted and the document is still read, because it is intact and only its
provenance is unestablished. `--require-signature` refuses both.

A signature authorises nothing: a verified document goes through the same plan,
review and apply as any other, and no directory permission follows from it.
There is no certificate chain, expiry or revocation list -- trust is the list of
keys an operator named, and removing one is how trust ends.

**Change packages hold no secret, and are never executed.** A change package
(1.11) describes intended changes so they can be carried to another directory.
It never contains a password, a hash, a reversible form of one, a placeholder
that could replay one, a bind credential, an API token, or a session MAC: a
password change is refused when a package is built and recorded as an omission
with its reason, and a change naming a sensitive attribute is refused outright,
including one written into a document by hand. It never contains a plan token
either -- a baseline is a MAC under a key that exists only in one server process,
and the strict reader refuses the field along with every other it does not know.
A package is read as untrusted input: unknown fields, trailing content,
duplicate identifiers, missing dependencies, self-dependencies, cycles,
malformed DNs, definitions that do not parse, oversized strings and graphs past
the bound are all refused. There is no templating, no interpolation and no
expression evaluation; a package is data. Its identity is provenance and
authorises nothing, every change still goes through the session's own access
rights and the plan, and validating one writes nothing.

**A migration preflight reads, and only reads.** A preflight (1.12) takes an
artifact from anywhere -- another environment, another team, another server --
and reads it against the session's directory. The artifact is decoded as
strictly as its own endpoints decode it, and is never changed. The session is
handed to the preflight as a type with no write method; the HTTP tests drive
every mode, including refused and failing ones, against a directory that
records writes and require none, and the conformance suite captures both
servers' data and schema before and after every mode and requires the same
documents. A report carries no bind credential, no password, no plan token, no
session identifier and no prepared change. What it reads is bounded: findings,
causes, prerequisites, explanation length, dependency depth, reference reads
and presence probes all have limits, and text from the artifact is shown with
control and bidirectional characters escaped. An entry the bind may not read is
reported as unknown where the server admits it exists; where a server answers
exactly as for a missing entry, the finding says it is what the server reported
to this bind. A presence probe is a Compare of a constant value, so it discloses
nothing about an attribute's content.

**Recovery bundles hold no secret, and are never executed.** A recovery bundle
(1.9) is derived from entries as they were read immediately before a change.
- **What it holds:** the earlier values of the ordinary attributes a change
  touched, and a deleted entry's ordinary attributes.
- **What it never holds:**
  - a password, or any value or hash of a deny-listed attribute;
  - a bind DN or credential;
  - a session or plan token;
  - a server address.

  A modified sensitive attribute is recorded by name only, a deleted entry's
  sensitive attributes are left out and the step says so, and a password change
  records only that it happened.
- **Reading a bundle** is read-only and strict, within the server's request
  limit. It refuses:
  - unknown fields and trailing content;
  - invalid DNs and attribute names;
  - a sensitive attribute carrying values;
  - a password change as a compensation;
  - recoverability its steps do not support;
  - more than 2,000 steps.

  The client sends the file exactly as it read it, so a field it does not know
  reaches the server and is refused there.
- **Using a bundle** turns it into ordinary change requests. Each carries the
  state its entry must still be in, and goes through a plan, a review and the
  plan's tokens. A compensation whose entry has changed since is a conflict.
  There is no endpoint that applies a bundle.
- **The checksum** detects corruption and is not a signature.
- **Origin** metadata is what the directory announced and proves nothing.
  Staging or applying a bundle whose origin differs needs explicit confirmation.
- **Files and display.** The client writes a bundle with mode 0600 on Unix.
  Every string from a bundle is displayed with control and bidirectional
  characters escaped. A bundle holds directory data, so keep it as you would
  that data.

**TLS is on by default in both directions.** The server refuses to start without
a certificate unless `--allow-http` says a reverse proxy terminates TLS.
Connecting to a directory over plaintext LDAP requires
`--i-know-this-is-insecure`. Certificate verification can be skipped only
per-connection, never as a default, and a session that skipped it is marked
unverified in the UI for as long as it lasts.

**The command line takes no secret as a flag.** The client commands read a
bind password from `ALDER_BIND_PASSWORD`, from a file, or from standard input --
never from an argument, where shell history, the process list and CI logs would
keep it. It is sent only to the Alder server named by `--api-url`, in the same
session request the connection screen makes, and it is never printed or logged:
the client has no mode that traces requests. It follows no redirect, so a
request body cannot be carried to another address, and it warns when
`--api-url` is plain HTTP to another machine. Text a directory controls is
printed with control characters and bidirectional overrides escaped, so a value
cannot rewrite the terminal showing it. No flag that confirms a write reads the
environment.

**Directory text cannot reorder the interface.** A DN, an attribute value, a
schema name or a server message can hold control characters and bidirectional
overrides, embeddings, isolates and marks, which would make
`uid=report<U+202E>txt.exe` display as `uid=reportexe.txt`. Since 1.9 every such
string is shown with those characters as escapes -- the tree, the entry
header, values, search results, members and references, plans, comparisons,
the monitor, the LDIF preview and recovery bundles -- by one helper,
`web/src/lib/display.ts`, with the same set the command line escapes. Only what
is displayed changes: navigation, copying and every request use the original
string.

**Nothing writes without a confirmed ChangeRecord.** There is one code path that
modifies a directory, and the LDIF the user confirmed is rendered from the same
record that path receives.

## What Alder does not defend against

Being explicit about the boundary is more useful than implying a wider one.

- **Alder has no users of its own.** It authenticates to a directory on your
  behalf; it does not authenticate you. Anyone who can reach the HTTP endpoint
  can attempt a bind. Put it behind something that controls access, and do not
  expose it to the internet.

  *Which* directory they can attempt it against is now restrictable.
  `--allowed-targets` (or `ALDER_ALLOWED_TARGETS`) takes a comma-separated list
  of hosts, `host:port` pairs, or `ldap://` and `ldaps://` URLs, and the server
  refuses anything else with `403` and `target_not_allowed` before it opens a
  connection. Unset, any target is permitted, which is the right default for the
  laptop-beside-the-directory case and the wrong one for anything reachable by
  other people. Matching is on the normalised host and port — case folded,
  trailing dot removed, IPv4-mapped addresses unmapped — and never on a
  substring, so an entry for `example.com` does not admit `notexample.com`.

  What it bounds is where a caller can send Alder, not who the caller is. Alder
  does not resolve names before matching, so an entry naming a host permits
  whatever that name resolves to at connection time; a deployment that needs
  more than that should reach the directory through a network path that enforces
  it.
- **It is single-tenant by design.** There is no RBAC, no delegation, no
  approval workflow, and no audit log. Authorisation is whatever your directory
  grants the DN you bind as, which is the right place for it but means the bind
  DN is the whole security boundary. Bind as an account with the rights the task
  needs, not as `cn=Directory Manager`.
- **There is no CSRF token.** The session cookie is `SameSite=Strict`, which is
  the entire defence. It is sufficient for the browsers Alder supports, and it
  is the reason the cookie is Strict rather than Lax.
- **A malicious directory is partly trusted.** Alder parses schema and entries
  from whatever server you point it at. The parsers are fuzzed and bounded
  against hangs and unbounded allocation, but a server you do not control is a
  server whose data you are rendering.
- **Nothing is rate-limited.** There is no per-caller quota and no throttle;
  Alder has no users of its own to account to, so a quota would be keyed on
  nothing. What bounds it is size and concurrency: page sizes, result counts,
  export and import sizes, changeset length, a thirty-second timeout on each
  directory operation, and `--max-in-flight`, which caps how many API requests
  are answered at once and refuses the rest with `503`. A streamed response
  releases its slot when the handler returns rather than when the last byte is
  written, so the cap bounds directory work rather than transfer.

- **A disconnected client does not stop the work it asked for.** A bounded
  handler — a tally, a comparison — runs to its limit for a caller who has gone;
  a streamed one stops sooner, because the write fails.

  This is a property of fasthttp rather than an oversight, and both ways out
  were tried and measured. Its `RequestCtx` implements `context.Context` but
  does not close `Done` on disconnect. Its `ConnState` hook does fire on close,
  but a connection is served from one goroutine, so nothing reads the socket
  while a handler runs and the notice arrives after the handler has already
  finished — 537 ms late in the measurement, by which time the abandoned work
  had served ninety-seven more pages and stopped on its own. The remaining
  option is a watchdog reading the socket underneath the server, which would
  steal the bytes of a pipelined request. Alder does not do that.

  What contains it is the per-operation timeout and the concurrency cap above.
  All of this is pinned in `internal/api/disconnect_test.go`, including a test
  that fails if fasthttp ever starts reporting the disconnect in time to act
  on.

## Supported versions

The current 1.x series. Fixes land on `main` and reach a release from there;
there are no backports to earlier minors, and no separate maintenance branch.

`docs/COMPATIBILITY.md` says what 1.x promises not to break. A security fix that
has to break one of those promises is still a security fix, and the changelog
will say so plainly rather than the promise quietly bending.
