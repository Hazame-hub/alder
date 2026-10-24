# 389-users and openldap-technical

Plain text, one message per list, sent from your own address. Both lists are
for users of those servers, so the message asks for feedback rather than
announcing. openldap-technical is for technical discussion of OpenLDAP; if in
doubt, ask the list owner first, or send only to 389-users.

- 389-users: 389-users@lists.fedoraproject.org
- openldap-technical: openldap-technical@openldap.org

---

**Subject:** Alder 1.39.0, a web UI and CLI for 389 DS and OpenLDAP: feedback wanted

Hello,

I have been writing a tool for administering OpenLDAP and 389 Directory
Server, and I would be grateful for feedback from people who run them.

Alder is a single Go binary that serves a web interface and also works as a
command-line client. It is AGPL-3.0 and self-hosted: no database, no state
on disk, credentials held in memory for the length of a session, TLS
required unless explicitly turned off, and a --read-only mode.

What it does differently from the admin tools I have used:

- Every change is previewed before it is sent, as the exact RFC 2849 change
  record and as an Ansible task. This holds for a single attribute and for
  a staged set of changes alike.
- Every change is planned against the directory first. The plan carries a
  fingerprint of the entries it read, and if one of them has changed when
  the change is applied, the apply is refused rather than overwriting it.
- 389 DS and OpenLDAP are both tested by one conformance suite, which runs
  every case against each server in CI.

Version 1.39.0 also adds a second, read-only server in the same session for
comparison, and saved plans that can be signed and applied later exactly as
reviewed. Active Directory and FreeIPA are not supported.

I would particularly like to hear:

- where it handles schema, the cn=config tree, ACIs or olcAccess
  in a way you consider wrong;
- what a change review would need to show before you trusted it on a
  production directory;
- anything that breaks on your server version.

Release: https://github.com/Hazame-hub/alder/releases/tag/v1.39.0
Getting started, with a seeded OpenLDAP and 389 DS in Docker:
https://github.com/Hazame-hub/alder/blob/main/docs/GETTING-STARTED.md
A short GIF of one edit:
https://github.com/Hazame-hub/alder/blob/main/docs/screenshots/edit-review-apply.gif

Issues and replies on the list are both welcome.

Thank you,
Hazame-hub
