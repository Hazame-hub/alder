# Show HN

**Title** (75 characters):

> Show HN: Alder – Review every LDAP change as LDIF before it runs (AGPL, Go)

**URL:** https://github.com/Hazame-hub/alder

**Text:**

I look after OpenLDAP and 389 Directory Server, and most of that work has been
ldapsearch, hand-written LDIF and admin tools I did not quite trust to do what
they said. Alder is what I built instead: a self-hosted web UI and a command
line, in one Go binary, for OpenLDAP and 389 DS.

The idea it is built around: nothing is written to the directory until you have
seen the exact RFC 2849 change record that will be sent. Every change, from one
attribute to a staged set of forty, is planned against the directory first.
The plan says what the change will actually do and carries a fingerprint of the
entries it read. If someone changed one of those entries before you apply, the
apply is refused rather than overwriting their change. The same record can be
exported as an Ansible task or playbook.

Other things it does:

- An editor built from the schema, nested group membership with the chain that
  put each person there, and what refers to an entry before you delete it.
- A project mode: keep a subtree's intended state as LDIF in Git, and let
  `alder project plan --detailed-exitcode` tell CI whether production still
  matches. Entries the files do not mention are reported as unmanaged and never
  deleted.
- A second read-only server in the same session (staging beside production, a
  replica beside its primary) to compare data, schema, configuration and
  replication with.
- Plans that can be saved, signed with Ed25519 keys (one or several required
  signers), and applied exactly later after someone approves them, renames
  and moves included.
- Index advice from an access log you give it: which searches ran without an
  index, how often and how slowly.

It keeps no database and no state on disk, refuses to start without TLS unless
you tell it otherwise, and has a `--read-only` mode. Both servers are covered by
one conformance suite that runs every case against each of them in CI.

It is young (first release in September) and does not support Active Directory
or FreeIPA. I would like to hear where it gets something wrong about LDAP.

Release: https://github.com/Hazame-hub/alder/releases/tag/v1.42.0
What an edit looks like: https://github.com/Hazame-hub/alder/blob/main/docs/screenshots/edit-review-apply.gif
Ten-minute walkthrough with a seeded OpenLDAP and 389 DS in Docker:
https://github.com/Hazame-hub/alder/blob/main/docs/GETTING-STARTED.md
