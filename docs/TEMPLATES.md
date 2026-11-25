# Entry templates

"New user" and "New group" without knowing which object classes and
attributes a person needs. A template is the shape of a new entry, written down
once: its classes, the few things someone types, and the values derived from
them. It can also take a number from the directory: the next free `uidNumber`
or `gidNumber`.

A template never writes. "Review" turns it into an ordinary add change, which
you see as exact LDIF and apply (or stage) through the same dialog every other
change goes through.

## Using one

On **Directory → Users** or **Groups**, **New user** / **New group** opens a
short form: where to create the entry, then the template's fields. **Review**
asks the server to fill the template in. That is when the next free number is
found, and the review's title says which number it is and why ("uidNumber
10301, the next free after 10300").

The button appears only when a template for that view fits the connected
server's schema, and never on a `--read-only` Alder.

## The built-in templates

| Name | View | Classes | Number |
|---|---|---|---|
| `posix-user` | Users | `inetOrgPerson`, `posixAccount` | `uidNumber`, 10000-60000 |
| `posix-group` | Groups | `posixGroup` (where it is structural: OpenLDAP) | `gidNumber`, 10000-60000 |
| `posix-group-of-names` | Groups | `groupOfNames` + `posixGroup` (where posixGroup is auxiliary: 389 DS) | `gidNumber`, 10000-60000 |
| `group-of-names` | Groups | `groupOfNames`, with a first member | none |

A built-in is listed only on a server whose schema accepts it, so each server
sees one POSIX group variant. `posix-user` derives `cn` from first and last
name, `homeDirectory` as `/home/<uid>` and sets `loginShell` to `/bin/bash`; it
asks for the primary group's `gidNumber`.

## Your own templates

`alder serve --templates DIR` reads every `*.template.yaml` file directly in
`DIR` at start. A file with the same `name` as a built-in replaces it. A file
that is not a valid template stops the server with the file named, rather than
starting with a "New user" that is not the one you wrote.

```yaml
# staff.template.yaml
name: staff                      # lower-case letters, digits, dashes
title: Staff member              # shown as "New staff member"
description: An employee with a login and a home on the file server.
view: users                      # users, groups or organizationalUnits
parent: ou=people,dc=example,dc=org   # optional default for "Create under"
rdn: uid                         # the new entry is uid=<value>,<parent>
objectClasses: [inetOrgPerson, posixAccount, shadowAccount]

fields:                          # what the person types; variables, not attributes
  - name: uid
    label: User name
    required: true
  - name: givenName
    label: First name
  - name: sn
    label: Last name
    required: true
  - name: gidNumber
    label: Primary group ID
    required: true
    kind: number                 # text (default), number or dn
    default: "5000"

attributes:                      # what the entry gets; {name} is a field or an allocated number
  uid: ["{uid}"]
  givenName: ["{givenName}"]
  sn: ["{sn}"]
  cn: ["{givenName} {sn}"]
  gidNumber: ["{gidNumber}"]
  homeDirectory: ["/srv/home/{uid}"]
  loginShell: ["/bin/bash"]

allocate:                        # the next free number, found when rendered
  uidNumber: {from: 50000, to: 59999}
  # base: ou=people,dc=example,dc=org   # where to look; default every naming context
```

- **A field is a variable.** It reaches the entry only through an attribute that
  names it, so a reviewer of the file sees exactly what each typed value
  becomes.
- **A blank optional field drops the value**, and an attribute left with no
  values is left out. Spaces are collapsed, so `{givenName} {sn}` with no first
  name is `Doe`.
- **The schema is checked before the template is offered.** Every class must be
  defined, there must be exactly one structural class, every attribute the
  classes require must always be set (by a constant, a required field or an
  allocated number), and nothing may be set that the classes do not permit. A
  file template that fails is listed with the reasons and cannot be chosen.

## How the next free number is found

When a template is rendered, Alder searches each naming context (or the
template's `base`) for entries holding the attribute, reads every value, and
takes **one more than the highest within the range**, or the start of the range
when none is in use. Not the first gap: a gap is often the number of someone who
left, and giving it to someone new gives them the old account's files.

It is refused, with `409`, rather than guessed when:

- every number in the range is taken;
- the server stopped the search before every entry was read (a size limit on
  the bind) -- bind as an account with no limit, or set a narrower `base`;
- part of the tree is a referral to another server;
- more than 500,000 entries hold the attribute under one base.

**The number is a suggestion, not a reservation.** Nothing is stored between
rendering and applying. Two people who render at the same moment are given the
same number, and the second add plans and applies like any other. If your
directory must refuse a duplicate, turn on its own check: OpenLDAP's `unique`
overlay, or 389 Directory Server's attribute uniqueness plugin. Alder does not
keep a counter in the directory, because that would be a write nobody reviewed.

## API

`GET /api/v1/templates` lists the templates with `usable` and `problems` for
this server. `POST /api/v1/templates/render` with `{template, parent, values}`
returns `{change, allocated}`; the change goes to `POST /plan` like any other.
See `api/openapi.yaml`.
