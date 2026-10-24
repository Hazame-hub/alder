# r/sysadmin

Check the subreddit's self-promotion rules on the day; if a weekly thread is
required, post it there.

**Title:**

> I built an LDAP tool that refuses to write anything you haven't reviewed as LDIF (OpenLDAP / 389 DS)

**Body:**

Alder is a web UI and CLI for OpenLDAP and 389 Directory Server, in one
self-hosted binary, AGPL-3.0.

Every change is planned against the directory and shown as the exact LDIF it
will send, and as an Ansible task. If the entry moved since the plan, the apply
is refused. You can keep a subtree in Git as LDIF and have CI tell you when the
directory drifts from it, and 1.39 adds saved, signed plans for a
plan-then-approve-then-apply flow, plus a second read-only server to compare
against.

No database, TLS by default, `--read-only` for looking at production. Both
servers are tested by the same conformance suite. No AD or FreeIPA.

Release: https://github.com/Hazame-hub/alder/releases/tag/v1.39.0
GIF: https://github.com/Hazame-hub/alder/blob/main/docs/screenshots/edit-review-apply.gif
Getting started: https://github.com/Hazame-hub/alder/blob/main/docs/GETTING-STARTED.md

Feedback welcome, especially from people who run 389 DS or OpenLDAP at scale.
