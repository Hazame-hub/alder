# Screenshots and recordings

The images in the README and the docs. Every one is of a real Alder build
against the `test/compose` OpenLDAP directory, with its seeded data; nothing
is mocked up or edited beyond cropping and compression.

| File | Shows | Used in |
|---|---|---|
| `edit-review-apply.gif` | Editing an entry, the review dialog's LDIF and Ansible tabs, applying | README, Getting started |
| `cli-project.gif` | `alder project validate`, `apply`, a one-line change in Git, `plan --detailed-exitcode` | README, Getting started, Feature tour |
| `connect.png` | The connection screen | Getting started |
| `entry-viewer.png`, `-dark.png` | `cn=awkward-values`, whose values are all hard to display | README, Getting started, Feature tour |
| `search.png`, `-dark.png` | A filter search with its `ldapsearch` command open | README, Feature tour |
| `members.png` | `cn=everyone` expanded through its nested groups | README, Feature tour |
| `ldif-preview.png`, `-dark.png` | The review dialog on the LDIF tab | Feature tour |
| `ansible-preview.png` | The same dialog on the Ansible tab | README, Feature tour |
| `changeset-plan.png` | Three staged changes, checked against the directory | README, Getting started, Feature tour |
| `schema-browser.png` | `inetOrgPerson` in the schema browser | README, Feature tour |

## How they were made

Captured on 2026-10-13 from `main` (the connection screen after #222, which added saved connections), at a 1440×900 viewport (1280×800 for the
web GIF), in Chromium driven by Playwright from outside the repository.
Screenshots are PNG, reduced with `pngquant`. The web GIF was recorded through
the DevTools screencast and encoded with `ffmpeg` (a 128-colour palette, 12
frames a second, played 1.2× fast) and `gifsicle`. The terminal GIF is a real
`bash` session in a pseudo-terminal, every command typed into it and every
line of output exactly what `alder` printed, replayed in xterm.js.

Neither Playwright nor xterm.js is a dependency of Alder; the capture scripts
are kept out of the repository for that reason.

When the web interface changes enough that these mislead, retake them rather
than editing them: the point of every image here is that it is what Alder
actually shows. Keep each one small, and a GIF under about a megabyte.
