# Launch drafts for 1.39.0

Drafts only. Nothing here has been posted anywhere. Each file is written to be
pasted by hand, after a read-through, by whoever posts it.

| File | Where it goes |
|---|---|
| [show-hn.md](show-hn.md) | Hacker News, "Show HN" |
| [reddit-selfhosted.md](reddit-selfhosted.md) | r/selfhosted |
| [reddit-sysadmin.md](reddit-sysadmin.md) | r/sysadmin |
| [linuxfr.md](linuxfr.md) | LinuxFr.org, as a dépêche (in French) |
| [mailing-lists.md](mailing-lists.md) | 389-users and openldap-technical |
| [awesome-selfhosted.md](awesome-selfhosted.md) | Notes on awesome-selfhosted: not ready to submit yet |

The three links every draft uses:

- Release: <https://github.com/Hazame-hub/alder/releases/tag/v1.39.0>
- The README GIF: <https://github.com/Hazame-hub/alder/blob/main/docs/screenshots/edit-review-apply.gif>
- Getting started: <https://github.com/Hazame-hub/alder/blob/main/docs/GETTING-STARTED.md>

## What every claim rests on

Each statement in the drafts is true of 1.39.0 and comes from one of these:

- One binary, web UI and CLI: README "Install", `docs/GETTING-STARTED.md`
  "What Alder is, in one minute".
- Every write planned, shown as the exact LDIF and as an Ansible task, and
  refused if the entry moved since the plan: `docs/FEATURES.md` "Change it
  safely", `docs/PLAN.md`.
- OpenLDAP and 389 DS tested by one conformance suite: README "Why Alder", CI
  job "conformance suite on both servers".
- Stateless, TLS unless `--allow-http`, `--read-only`: README "Why Alder" and
  "Quick start".
- Projects in Git, `project plan --detailed-exitcode`, unmanaged entries never
  deleted: `docs/PROJECT.md`.
- New in 1.39 (second read-only server, saved and signed plans, projects in the
  browser, access rules in recovery bundles, saved searches, bulk attribute
  edit, group membership tools): `CHANGELOG.md` 1.39.0.
- Not supported: Active Directory, FreeIPA, Entra ID (`CHARTER.md`).
- AGPL-3.0-only: `LICENSE`, image label in `.goreleaser.yaml`.
- First release 2026-09-03 (tag v0.1.2), so the project is about seven weeks
  old at 1.39.0. The drafts say it is young.

## Before posting

- Check the release page and the image tag exist (they are verified in the
  release thread, but check again on the day).
- Decide whether to say the code was written with an AI coding assistant.
  Commits carry a `Co-Authored-By: Claude` trailer, so readers who look will
  see it; Hacker News and r/selfhosted readers often ask. The drafts do not
  mention it either way.
- Post the drafts under your own account, and answer the comments yourself.
