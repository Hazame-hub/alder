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
  can attempt a bind against whatever directory they name. Put it behind
  something that controls access, and do not expose it to the internet.
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
- **Nothing is rate-limited.** A caller with a valid session can issue searches
  as fast as the directory will answer them.

## Supported versions

Pre-1.0. Fixes land on `main`; there are no backports.
