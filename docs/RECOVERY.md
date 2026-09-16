# Recovery bundles

A recovery bundle answers:

> If this change turns out to be wrong, which changes would compensate for it,
> and how completely?

Before a change is applied, Alder can say how recoverable it is. If you ask, it
also produces a **recovery bundle** once the change has been applied. A bundle
is a versioned JSON document you keep, and Alder keeps no copy.

A bundle describes the compensating LDAP changes Alder can safely derive from
the state that existed immediately before the apply.

A bundle is **not**:
- a transaction, or a rollback, guaranteed or automatic;
- a backup;
- a restore of a snapshot;
- server-side history or an audit log;
- a reconstruction of the entries byte for byte.

LDAP has no transactions, and none of this pretends it does.

Recovering never has a path of its own. You load a bundle, and Alder turns it
into ordinary change requests. Those go through the plan like every other
write: they are read against the directory as it is now, you review them, and
they are applied with the plan's tokens. Nothing applies a bundle directly, and
there is no endpoint that would.

In the interface:
- A plan shows each change's recoverability.
- "Prepare a recovery bundle" in the change dialog and in the changeset offers
  the bundle for download after applying.
- "Load recovery bundle" in the Changeset view previews a bundle and stages its
  changes for review.

Over HTTP:
- `recovery: true` on `POST /changeset/apply`, or `?recovery=true` on
  `POST /changes/apply`, returns the bundle.
- `POST /recovery/inspect` validates a bundle and returns its changes to plan.

From a shell, `alder apply --recovery-out FILE` writes a bundle, and
`alder plan --recovery FILE` or `alder apply --recovery FILE` reads one; see
[CLI.md](CLI.md). `api/openapi.yaml` is the reference for the shapes.

---

## A bundle belongs to one directory, not to an intent

A recovery bundle is derived from the entries a change touched, in the directory
it was applied to, immediately before it ran. It is not a description of the
change; it is a description of what that directory held.

So a bundle made while applying a change package (1.11) in test must never be
used to recover production: the entries differed, and the compensations restore
what was there. Applying the same package to two directories produces two
different bundles, and the conformance suite proves it. A package never contains
a bundle, and a bundle never contains a package. See
[CHANGE-PACKAGES.md](CHANGE-PACKAGES.md).


## How recoverable a change is

Every step in a bundle has a recoverability, and every plan item that applies
something carries the same assessment:

| Recoverability | Meaning |
|---|---|
| `exact` | Alder can derive compensating changes that restore the ordinary directory state it captured before the change: every user attribute the change touched back to its earlier values, the entry the change created gone, or the entry back under its former name and parent. They are still planned, and refused if the directory has drifted. |
| `partial` | Some of the change's effect can be compensated and some cannot. `reasons` say which. |
| `unavailable` | Nothing about the change can be compensated. |

**What exact does not mean.** It is a statement about the ordinary directory
data Alder read and can write, not about the server's state byte for byte. A
compensation does not restore, and no recoverability claims to:

- `modifyTimestamp`, `createTimestamp`, `modifiersName` and the other
  operational attributes, which change again when the compensation is applied;
- server-generated identifiers such as `entryUUID` and `nsUniqueId`;
- replication metadata such as `entryCSN`;
- attributes the bind could not read, which were never captured.

This is why an add and a rename are exact -- the entry is removed, or is back
under its former name holding the same user data, still with its own identity
-- while a delete is partial: the entry that comes back is a new one.

### Per operation

| Change | Compensation | Recoverability |
|---|---|---|
| Modify an attribute (add, delete or replace values, or remove the attribute) | Removes the values the change added and adds back the values it removed, value by value. The values come from the entry as it was read before the change, compared by the attribute's equality rule. A value stored in a different but equal form (`Alice` replaced by `alice`) is written back as it was. | `exact` |
| Modify a sensitive attribute (`userPassword` and the configured deny list) | None for that attribute: its earlier value is never captured. | `partial`, or `unavailable` if nothing else was touched. Reason `sensitive_value_not_captured`. |
| Modify an operational or NO-USER-MODIFICATION attribute | None for that attribute. | `partial`. Reason `server_owned_attribute`. |
| Add an entry | Deletes the entry, and only while it holds exactly what the add created and nothing else. A plan never deletes its children: an entry that has gained children is a `has_children` conflict. | `exact` |
| Delete an entry | Adds it back with the user attributes that were read before the delete. | `partial`, always. Reasons `identity_regenerated` (a new `entryUUID` or `nsUniqueId`, new timestamps) and `hidden_attributes_unknown` (anything the bind could not read). Also `sensitive_values_not_restored` when it held a password or other sensitive value, which is not put back. |
| Rename or move an entry | Renames or moves it back. The new naming value is removed on the way back only if the entry did not already hold it. | `exact` |
| Set a password | None. The previous password is never captured. | `unavailable`. Reason `password_not_captured`. |
| Any change to the schema or the server's configuration | None, in this version. | `unavailable`. Reason `schema_or_config_not_supported`. |

A change whose entry could not be read before it ran is `unavailable`, with the
reason `pre_state_unavailable`. Alder never guesses the missing state.

A bundle's overall recoverability follows from its steps:
- `exact` when every step is exact;
- `unavailable` when every step is unavailable;
- `partial` otherwise.

## Derived before, emitted after

- **Derived from the state before the change.** The entry is read before each
  change runs. When the plan check has just read it and nothing has run since,
  that read is reused and widened to cover what recovery needs. Otherwise it is
  read again. Nothing is derived by reading the entry after the change and
  calling that the state before.
- **Only what was applied.** A step is recorded only once its change has
  succeeded. A run that stops at a failure covers the changes before the
  failure. The failed change and the changes after it are not in the bundle,
  because none of them changed anything. When nothing was applied, there is no
  bundle.
- **Compensated in reverse.** Steps are kept in the order the changes ran.
  Their compensations run in the reverse order, so a child added after its
  parent is removed before it. Nothing is sorted by DN.

LDAP has no way to read and write atomically. An entry changed by somebody else
between Alder's read and its write can make the recorded state wrong for that
entry. That window is the same one a plan's baseline narrows. A compensation
derived from a wrong state fails safe: its expectation does not hold, and its
plan reports drift.

### One entry changed more than once

A plan reads every change against the directory as it is now. It does not read
them as the changes before would leave it. When a bundle is turned into
changes, compensations of the same entry are therefore merged wherever that is
safe:

- **Several modifications of one entry.** Within a run of modifications they
  become one modification, with the mods in execution order. LDAP applies a
  modification's mods in order, and modifications of different entries do not
  depend on each other.
- **An add followed by an edit of the same entry.** This compensates as the
  delete, with an expectation of the entry as the edit left it.

Some dependencies are not merged, and those recover in rounds:
- Removing a parent and its child, both added by the change. The child's
  removal applies first, and the parent shows `has_children` until then.
- An entry deleted and then added again.

In the changeset view, "Apply" applies what the plan found applicable. The rest
stays staged, and checking again shows the next round.

## Drift

Every compensating change carries an **expectation**: the state the original
change left the entry in. For a modification, that is the attributes it
touched, with the values they held afterwards. For the delete that compensates
an add, it is every user attribute the add created, and nothing else.

A plan checks the expectation against the directory as it is now:

- If the entry no longer holds what the compensation expects, the change is a
  `conflict` with `expected_state_differs`. Nothing is decided for it and
  nothing is applied. The preview reports it as "the directory has drifted since
  the original apply".
- The attributes an expectation names are bound into the plan's baseline. The
  expectation is also checked again at apply, so an attribute added between
  planning and applying is refused as `plan_stale`.
- A change that carries an expectation but no baseline is refused at apply.
  Only a plan evaluates an expectation.

Recovery never overwrites a later change and never resolves a conflict for
you. If the directory has moved on, deciding what the entry should be is yours.

## Security

- **Never captured.** No password, bind credential, session token, plan token,
  baseline or server secret is written into a bundle.
  - A modification of a sensitive attribute records the attribute's name only.
  - A deleted entry's sensitive attributes are left out of its compensation,
    and the step says so.
  - A password change records that it happened.

  No hash is kept either: a reusable hash of a secret would be an offline
  guessing oracle.
- **Untrusted input.** A bundle is read as if anyone could have edited the file.
  - `POST /recovery/inspect` refuses:
    - an unknown field;
    - another format or version;
    - a malformed DN, attribute name or value;
    - a sensitive attribute carrying values;
    - a password change as a compensation;
    - a recoverability its steps do not support;
    - a partial or unavailable step with no reason;
    - more than 2,000 steps;
    - a checksum that does not match.
  - The request is bounded by the server's body limit.
  - The client sends the file as it was read, so a field the client does not
    know reaches the server to be refused.
  - Loading a bundle writes nothing.
- **Replay.** A bundle applied twice does nothing the second time. The
  expectations no longer hold, and the plan shows conflicts.
- **The wrong directory.** A bundle records what the directory announced about
  itself: vendor, vendor version and naming contexts. It records no host, port
  or bind DN, and none of these prove which directory it came from. A plan
  against another directory mostly fails on expectations anyway. When the
  announcement differs, the interface and `alder apply --recovery` ask for
  explicit confirmation (`--allow-origin-mismatch`) before staging or applying.
- **What you see.** Every DN, attribute name and reason from a loaded bundle is
  displayed with control and bidirectional formatting characters made visible,
  in the browser and in the terminal.
- **What you keep.** A bundle holds the directory data it compensates: the
  earlier values of the attributes a change touched, and a deleted entry's
  ordinary attributes. Store it as you would that data. `alder apply
  --recovery-out` creates the file with mode 0600 on Unix. The server logs a
  bundle's step count and recoverability, never its values.

## The format

Recovery format version 1 was introduced in Alder 1.9. Later Alder 1.x releases
will continue to read it.

```json
{
  "format": "alder-recovery",
  "version": 1,
  "createdAt": "2026-09-14T12:00:00Z",
  "origin": {
    "vendor": "OpenLDAP",
    "namingContexts": ["dc=alder,dc=test"]
  },
  "recoverability": "partial",
  "steps": [
    {
      "index": 0,
      "original": { "type": "modify", "dn": "uid=alice,ou=people,dc=alder,dc=test", "attributes": ["description", "userPassword"] },
      "kind": "data",
      "recoverability": "partial",
      "reasons": [{ "code": "sensitive_value_not_captured", "attribute": "userPassword" }],
      "compensation": [
        {
          "dn": "uid=alice,ou=people,dc=alder,dc=test",
          "type": "modify",
          "mods": [
            { "op": "delete", "name": "description", "values": [{ "text": "after" }] },
            { "op": "add", "name": "description", "values": [{ "text": "before" }] }
          ],
          "expect": {
            "attributes": [{ "name": "description", "values": [{ "text": "after" }] }]
          }
        }
      ]
    }
  ],
  "checksum": "sha256:…"
}
```

- `steps` are in the order the changes were applied. `index` is each change's
  position in the set that was sent. `original` names the change by type, DN,
  new DN for a rename, and attribute names, never values.
- `compensation` is one or more change requests. Their types are `add`,
  `modify`, `delete` and `modrdn`, never `setpassword`. Values are `{text}` or
  `{base64}`, as in a snapshot.
- `expect.attributes` lists attributes that must hold exactly the given values.
  No values means the attribute must be absent. With `exhaustive: true`, the
  entry must hold no other user attribute. `objectClass`, operational, identity
  and sensitive attributes are not counted.
- `checksum` is `sha256:<hex>` over every field except `createdAt` and
  `checksum`. It is optional, and a bundle without one is described as
  `integrity: unverified`. It detects corruption. It is not a signature, and it
  proves nothing about who wrote the file.

## Using a bundle

1. Load it: "Load recovery bundle" in the Changeset view,
   `alder plan --recovery FILE`, or `POST /recovery/inspect`. The preview shows:
   - the changes that were applied, and how recoverable each one is;
   - its limitations;
   - whether the directory announces itself the same way;
   - the proposed compensating changes, in order, with each one's drift
     against the directory now.
2. Review the plan. "Review plan" stages the changes and checks them against
   the directory. `alder apply --recovery FILE` shows the plan and asks.
3. Apply. The changes are applied like any other: with the plan's baselines,
   refused if the directory has moved, and stopped at the first failure. That
   apply can itself produce a bundle.
