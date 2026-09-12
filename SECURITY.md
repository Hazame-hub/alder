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

**TLS is on by default in both directions.** The server refuses to start without
a certificate unless `--allow-http` says a reverse proxy terminates TLS.
Connecting to a directory over plaintext LDAP requires
`--i-know-this-is-insecure`. Certificate verification can be skipped only
per-connection, never as a default, and a session that skipped it is marked
unverified in the UI for as long as it lasts.

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

- **A disconnected client does not stop the work it asked for.** fasthttp does
  not cancel a request's context when the peer goes away, so a bounded handler —
  a tally, a comparison — runs to its limit for a caller who has gone. A
  streamed one stops sooner, because the write fails. Measured and pinned in
  `internal/api/disconnect_test.go`. What contains it is the per-operation
  timeout and the concurrency cap above, not cancellation.

## Supported versions

The current 1.x series. Fixes land on `main` and reach a release from there;
there are no backports to earlier minors, and no separate maintenance branch.

`docs/COMPATIBILITY.md` says what 1.x promises not to break. A security fix that
has to break one of those promises is still a security fix, and the changelog
will say so plainly rather than the promise quietly bending.
