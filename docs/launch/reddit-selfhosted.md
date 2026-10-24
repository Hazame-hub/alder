# r/selfhosted

**Flair:** Release (or the subreddit's equivalent for a project you made)

**Title:**

> Alder 1.39: a web UI and CLI for OpenLDAP and 389 DS that shows every change as LDIF before it runs

**Body:**

If you run your own LDAP directory for SSO, mail or a homelab, you probably
manage it with phpLDAPadmin, Apache Directory Studio, or ldapmodify and a text
editor. I wrote Alder because I wanted something in between: a browser
interface where every change is reviewable as code.

**What it is**

- One Go binary (`alder serve`) with the web UI embedded, plus a CLI in the
  same binary. Archives for Linux, macOS and Windows (amd64/arm64), and a
  container image `ghcr.io/hazame-hub/alder` for amd64 and arm64.
- Works with OpenLDAP and 389 Directory Server. One conformance suite runs every
  case against both in CI.
- AGPL-3.0. No database, no state on disk, no telemetry. Credentials stay in
  the server's memory for the session.

**What it does differently**

- Every write is planned against the directory and shown as the exact LDIF
  that will be sent, and as an Ansible task. If the entry changed since you
  planned, the apply is refused instead of overwriting.
- Stage several changes as a changeset, review them together, export them as
  one LDIF file or one playbook.
- Keep a subtree's intended state in Git as LDIF and check from CI whether the
  directory still matches. Entries the files don't mention are listed, never
  deleted.

**New in 1.39**

- Attach a second, read-only server to compare with (staging next to
  production, a replica next to its primary).
- Save a plan, sign it, and apply exactly that plan later, after approval.
- Open a project folder or .zip in the browser, saved searches, editing one
  attribute across selected entries, and adding or auditing many group
  members at once.

**Running it**

```sh
docker run --rm -p 8443:8443 -v "$PWD/tls:/certs:ro" \
  ghcr.io/hazame-hub/alder:1.39.0 serve --tls-cert /certs/tls.crt --tls-key /certs/tls.key
```

It refuses to start without TLS unless you pass `--allow-http` (fine on
127.0.0.1 or behind a reverse proxy that terminates TLS). `--read-only` turns
off every write.

Not supported: Active Directory, FreeIPA. It's a young project (first release
in September), so feedback and bug reports are welcome.

- Release: https://github.com/Hazame-hub/alder/releases/tag/v1.39.0
- GIF of an edit, from change to LDIF to apply: https://github.com/Hazame-hub/alder/blob/main/docs/screenshots/edit-review-apply.gif
- Getting started (seeded OpenLDAP and 389 DS in Docker): https://github.com/Hazame-hub/alder/blob/main/docs/GETTING-STARTED.md
