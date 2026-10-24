# awesome-selfhosted: not ready to submit yet

No entry is drafted here, for two reasons.

1. **The list requires a first release more than four months old.** Alder's
   first release, v0.1.2, was tagged on 2026-09-03, so it becomes eligible on
   **2027-01-03** at the earliest. A submission before then is closed with a
   request to resubmit.
2. **The list does not accept entries written by an AI.** Its contributing
   guide (awesome-selfhosted-data `CONTRIBUTING.md`) says agents must not
   write the text of a `software/*.yml` entry for a person to submit, and the
   submission template asks the submitter to confirm the entry was written by
   a human. So the entry has to be written by you. What follows is only the
   format and the checks, so you can do that quickly.

## The format

Entries are YAML files in
[awesome-selfhosted-data](https://github.com/awesome-selfhosted/awesome-selfhosted-data),
one per project, named in kebab-case (`software/alder.yml`), using the
fields of `.github/ISSUE_TEMPLATE/addition.md`: `name`, `website_url`,
`source_code_url`, `description` (sentence case, under 250 characters, no
"open-source", "free" or "self-hosted", short form without "Alder is"),
`licenses`, `platforms`, `tags`, and optional `demo_url`, `icon_url`,
`depends_3rdparty`. The README line is generated from that file, in the form
``- [Name](website) - Description. ([Source Code](url)) `Licence` `Platform` ``.

Values that are facts about Alder, to save you looking them up:

- Licence identifier: `AGPL-3.0` (the list's spelling for AGPL-3.0-only).
- Platforms: `Go` and `Docker` both exist in the list's `platforms/`.
- Tags: there is no LDAP or directory category. "Identity Management" sends
  readers to awesome-sysadmin instead, which may be the better home.
  "Database Management" is the closest existing tag in awesome-selfhosted.
  A tag needs three projects, so a new one is unlikely to be accepted.

## Checks the template asks for

- Not already listed in awesome-sysadmin: check before submitting.
- Actively maintained: yes (releases weekly in October 2026).
- Working installation instructions: README "Install" and
  docs/GETTING-STARTED.md.
- First release over four months old: not until 2027-01-03.
