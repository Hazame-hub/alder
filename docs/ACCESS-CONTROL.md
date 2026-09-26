# Access control, read

Alder reads the access control rules a server holds and shows you the ones that
bear on an entry. It does not write them, and it does not evaluate them.

Introduced in 1.19.

---

## What this is

*"Why can't I write this?"* is the question a directory answers worst, and until
1.19 Alder answered it with nothing at all: the preflight report listed access
control among the things it neither reads nor translates.

The **Access** button on an entry now answers the readable half of the question.
For that entry it lists:

- every rule the server holds that Alder could find, **in the order the server
  keeps them**;
- **where each one is written** — the database entry in a configuration tree, or
  the entry in the data tree it sits on;
- **whether it bears on this entry**: yes, no, or an honest *maybe*;
- who it names and what they get, parsed where Alder read the rule with
  confidence;
- **the rule exactly as the server holds it**, always, whatever was parsed.

## What this is not

> These are the rules the server holds, in its own order and its own words.
> Alder does not evaluate them: what a particular bind may do is the directory's
> answer, and the directory gives it when an operation is tried.

That sentence is in the API response, in the interface and in this document, and
it is the whole disclaimer. Access control evaluation depends on group
membership, on filters, on the connection's security strength, on the order of
rules across databases, and on rules Alder may not be able to read at all. A
screen that showed a verdict would be believed, and being believed while wrong
about access control is worse than saying nothing.

**Nothing here writes.** Editing access control stays out of scope: it is the
one thing in a directory that can lock every administrator out of it, including
the one making the change. See the decisions log.

---

## The two mechanisms

Both are looked in, on every server. Nothing branches on a vendor name: what is
reported is what was found.

### `aci`, on the entries themselves

389 Directory Server keeps access control in `aci` attributes on ordinary
entries. An aci on an ancestor is in force on everything beneath it, so Alder
reads the entry and each of its ancestors up to the naming context, and marks a
rule found above the entry as **inherited** — which is the fact that stops an
operator hunting for a rule on the wrong entry.

```
(targetattr!="userPassword")(version 3.0; acl "svc-alder administers people";
  allow (read,search,compare) userdn = "ldap:///cn=svc-alder,ou=services,dc=alder,dc=test";)
```

Read from that: the target attributes (`!userPassword` — the `!` is kept,
because a rule about every attribute *except* the password is not a rule about
the password), the rule's name, whether each clause allows or denies, the rights
and the subject.

### `olcAccess`, in the configuration tree

OpenLDAP keeps an ordered list on the database entry inside `cn=config`:

```
{0}to attrs=userPassword  by self write  by anonymous auth  by * none
{1}to dn.subtree="ou=services,dc=alder,dc=test"  by users read  by * read
{2}to *  by self write  by users read  by * read
```

The order *is* the mechanism — the server stops at the first match — so the
index is always reported and the rules are never sorted into something tidier.
What is shown is the rules of the database whose suffix holds the entry, then
the rules of anything beneath it that carries some -- an overlay's own -- then
the frontend's, which the server consults after the database's. Another
database's rules are not this entry's and are left out. Each rule says which
entry it is written on, so a rule an operator has to change can be found.

If there are more entries carrying rules than one search returns, the report
says so rather than presenting a short list as the whole one.

Reading them needs an identity that may read the configuration tree. A session
without one gets a report that **says the tree could not be read**, rather than
a report of no rules: on this feature, a confident empty answer is the most
dangerous thing Alder could say.

---

## Does it bear on this entry?

| Answer | Meaning |
|---|---|
| **yes** | The rule names this entry, the subtree it is in, or everything |
| **no** | Its target is somewhere else |
| **maybe** | Deciding would need evaluating something Alder does not evaluate: a regular expression, a filter, a group |

*maybe* is an answer, not a gap. Every one of them carries a sentence saying
what could not be decided and why.

## Parsed, or not

`parsed: false` means Alder did not take the rule apart, and the raw value is
all it is willing to say about it. A set specification, a regular expression
target, an SSF control, a syntax newer than this parser: reported whole rather
than half-read, because a half-read access rule invites a decision made on the
half that was understood.

A rule whose target Alder *can* read is still placed even when the rest of it
was not: the target decides whether it bears on the entry, and that much is
useful on its own.

---

## The API

`GET /access?dn=<dn>` returns an `AccessReport`: the rules, the styles they came
from, anywhere that could not be read, and the disclaimer. A DN this session
cannot read answers with the directory's own refusal — the rules of an entry
nobody can read would be a list with no subject.

## What is not here yet

- **Effective rights.** 389 Directory Server publishes the Get Effective Rights
  control (`1.3.6.1.4.1.42.2.27.9.5.2`), which answers "what may *this bind* do
  on this entry" authoritatively, from the server. OpenLDAP has no equivalent.
  Where a server can answer it, Alder should ask rather than leave the operator
  reading rules — that is the next piece of this feature, and it is deliberately
  separate from reading the rules themselves.
- **Evaluation by Alder.** Not planned, for the reason above.
- **Editing.** Out of scope for v1.
