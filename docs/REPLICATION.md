# Replication, read

Alder reports what a directory server records about its own replication: what
it is, who it is linked to, and how far along it is. It configures nothing,
and it never contacts the other servers.

Introduced in 1.25.

---

## The question

*"Is replication working?"* Nobody can answer that from one server, and the
places a server writes down what it knows are all different:

- whether it supplies, consumes or both is a fact about its configuration,
  kept in one place on OpenLDAP and another on 389 DS;
- who it is linked to is in a configuration value on one and an entry on the
  other;
- how far along it is is an operational attribute on the suffix on one, and a
  vector on a replica entry on the other;
- and the outcome of the last exchange exists on 389 DS and does not exist at
  all on OpenLDAP.

The **Replication** card on the overview puts them together, per suffix, in
that order.

## What it is not

> This is what this server records about its own replication. Alder does not
> contact the other servers, so a peer's own view may differ, and nothing here
> is a promise that a change has arrived everywhere.

The same rule as [access control](ACCESS-CONTROL.md) and [password
policy](PASSWORD-POLICY.md). Alder reports; the servers decide. A report that
dialled out to whatever host a configuration value happened to name would be
a report that can be pointed at anything, so it does not.

It is read on request rather than when the overview opens: it is a search of
the configuration tree, and the overview is the page that opens instantly.

---

## How far along a server is

This is the part both servers can be compared on, and it is the answer to "is
it working".

Each server stamps every change with a sequence number carrying **when** and
**which server made it**, and records, per origin, the most recent one it
holds. Alder reads both forms into the same pair:

| | Where it is written | The form |
|---|---|---|
| OpenLDAP | `contextCSN` on the suffix | `20260927082421.145666Z#000000#001#000000` |
| 389 DS | the replica update vector on the replica entry | `{replica 1 ldap://ds389:3389} <first> <last>` |

Both mean *"I have everything from server N up to this moment"*. Open the card
against two servers and compare: the same numbers mean the two are in step,
and a number that is not moving on one of them is the thing to chase. The raw
value is shown beside the time, because a change sequence is what operators
paste to each other.

A peer whose clock runs ahead produces a change stamped in the reader's
future. Alder says so rather than counting backwards: "-40s ago" reads as a
bug in Alder, and the disagreement between the clocks is the actual fact.

---

## The two mechanisms

### OpenLDAP

An **incoming** link is an `olcSyncrepl` value on the database entry:

```ldif
dn: olcDatabase={1}mdb,cn=config
olcSyncrepl: {0}rid=001 provider=ldap://openldap:389 type=refreshAndPersist
  binddn="cn=admin,dc=alder,dc=test" credentials="…" searchbase="dc=alder,dc=test"
olcUpdateRef: ldap://openldap:389/
```

It is reported by its `rid`, never by the `{0}` in front of it: that is a
position, and it moves when a different link is removed.

The ability to serve **outgoing** ones is the `syncprov` overlay. OpenLDAP
does not record who has asked, so the consumers are not listed — each one
knows its own provider, and Alder says so rather than leaving a blank that
reads as "nobody". For the same reason a syncrepl link's status is
`no status recorded`: OpenLDAP keeps none anywhere a client can read, and how
far along the suffix is, above, is the measure it does keep.

`olcMirrorMode` and `olcMultiProvider` are reported too: a server that accepts
writes of its own as well as receiving them is one where two servers may
change the same entry, which is worth knowing before you start.

### 389 Directory Server

A server's role for a suffix is an `nsds5Replica` entry under the mapping
tree, and each **outgoing** link is an agreement beneath it:

```ldif
dn: cn=to-ds389-replica,cn=replica,cn=dc\3Dalder\2Cdc\3Dtest,cn=mapping tree,cn=config
nsDS5ReplicaHost: ds389-replica
nsDS5ReplicaPort: 3389
nsds5replicaLastUpdateStatus: Error (0) Replica acquired successfully: Incremental update succeeded
nsds5replicaLastUpdateStatusJSON: {"state": "green", …}
```

The agreement carries the outcome of the last exchange in the server's own
words, and since 1.4 a colour alongside it. Alder shows both: the colour
decides the badge because it is the server's own judgement rather than ours,
and the sentence is always printed under it — including the `Error (0)` that
389 DS uses to mean success.

A **consumer** has no agreement of its own; what it has is the replica entry
saying it receives only. Alder reports that as a note rather than as an empty
list. Its replica id is `65535`, which is not an identity anybody chose but
the one 389 DS gives a server that originates no changes, and the report says
so.

---

## Credentials

An `olcSyncrepl` value carries, in the clear, the password the consumer binds
to its provider with. It is dropped as the value is parsed, before it reaches
anything that could render or log it — nothing downstream has to remember that
this particular configuration value is different from every other one. A 389
DS agreement's `nsDS5ReplicaCredentials` is never asked for.

The conformance suite asserts this against all four harness servers: no
harness password, and no attribute that carries one, appears anywhere in the
response.

---

## Proved against four servers

The harness runs a replica of each server for this feature; see
[test/compose/README.md](../../test/compose/README.md). Two suppliers and two
consumers, with nothing in common at the wire level, and one set of
assertions that never learns which server it is talking to.

---

## One entry, and conflicts (1.26)

The **Replication** button on an entry answers the question the suffix view
cannot: *has this change arrived there yet?* It shows the entry's change
sequence, the identity the server keeps for it across a rename, and whether
the server has marked it as the losing side of a collision. Open the same
entry on the other server and compare the number.

OpenLDAP stamps every entry with `entryCSN`, carrying both the moment and the
server that made the change. 389 DS stamps none a client can read, so the
answer there is the modification time — which the report says is weaker
evidence rather than presenting it as the same thing: two changes in the same
second are indistinguishable.

**Conflicts** are looked for below a suffix, on request, and only one of the
two servers marks anything:

- **389 DS** keeps both sides of a collision and marks the loser with
  `nsds5ReplConflict`, so a conflict is an entry you can read, fix and remove.
  It also invents a `glue` entry where a parent is missing.
- **OpenLDAP** resolves a collision by change sequence and discards the loser.
  Nothing is left behind, so an empty list from OpenLDAP is *not* evidence
  that nothing collided — and the report says that in as many words, because
  an empty list otherwise reads as good news.

The search is sent to both regardless. Deciding not to ask would make the
answer depend on Alder's model of the server being right, rather than on the
directory's own answer.

---

## Not in this

- **Configuring replication.** Alder reports it. Setting up an agreement is a
  write to `cn=config` beyond the schema subtree, which v1 does not do.
- **Contacting a peer.** See above.
- **Reindexing, reinitialising, or forcing an update.** Those are operations
  on a server rather than changes to a directory.
