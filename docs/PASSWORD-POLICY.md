# Password policy and account state, read

Alder reads the password policy in force on an entry and what the server
records about that account. It writes neither, and it does not decide whether
a bind would succeed.

Introduced in 1.21.

---

## The question

*"Why can't this user log in?"* The facts that answer it are all in the
directory and all in places nobody looks by accident:

- an operational attribute on the account says it is locked, or that the
  password must change, or when it last changed;
- the policy that turns those into a refusal is written **somewhere else** —
  in the server's own configuration, or in an entry the account points at.

The **Policy** button on an entry puts the three together. An account the
server currently holds locked is also marked in the entry header, before
anything is opened, because that is the fact worth not having to look for.

## Clearing a lock (1.29)

Where the account is locked and Alder recognises the kind of lock, the dialog
that found it offers **Unlock this account**, beside the line that names it.
It opens the ordinary review dialog — the same LDIF, the same plan, the same
apply as every other write — and the change is derived by the server, because
there is one code path that builds what gets sent.

Until 1.29 there was no way to do this in the product at all. The entry viewer
filed the lock attribute under *"operational, kept by the directory, yours to
set"*, no editor offered it, the password dialog did not mention it, and the
only way to clear it was to hand-write an LDIF `changetype: modify` into the
import screen. A black-box audit walked *"this account cannot log in, why"*
and spent **seventeen interactions** between knowing the answer and applying
it; the fix is three.

What gets removed depends on what the entry actually carries:

| Lock | Also cleared | Why |
|---|---|---|
| `pwdAccountLockedTime` (OpenLDAP ppolicy) | `pwdFailureTime` | the overlay re-locks at `pwdMaxFailure`, so leaving the counted failures behind means the next single failure locks it again |
| `nsAccountLock` (389 DS) | — | deleting it is the unlock; setting it to `false` leaves a value that reads as though somebody meant something by it |
| `accountUnlockTime` (389 DS lockout) | `passwordRetryCount` | clears the automatic lockout and the failure count behind it |

An attribute the entry does not hold is never named: a delete of an absent
attribute is an error on both servers, so naming it would turn a working
unlock into a refusal. And **a lock Alder does not recognise gets no change** —
the report says so and stops. A change invented for an attribute Alder has
never seen would be a write nobody asked for, on the screen whose whole point
is that it reports rather than decides.

## What it is not

> This is what the server records about the account and the policy it names,
> not a decision about whether a bind would succeed: the directory decides that
> when one is tried.

The same rule as [access control](ACCESS-CONTROL.md). Alder reports; the
directory decides.

---

## The two mechanisms

### OpenLDAP: the ppolicy overlay

A policy is an ordinary entry in the data tree. An account may name its own
with `pwdPolicySubentry`; otherwise the overlay's configured default applies,
which Alder finds by reading `olcPPolicyDefault` from the configuration tree
and then reading the entry it names.

The state lives on the account: `pwdAccountLockedTime`, `pwdChangedTime`,
`pwdFailureTime`, `pwdReset`, `pwdGraceUseTime`.

### 389 Directory Server: cn=config, and subentries

The global policy is a set of attributes on `cn=config` itself. A per-entry
policy is a subentry beside the account, pointed at by `pwdpolicysubentry`.

The state lives on the account: `nsAccountLock`, `passwordExpirationTime`,
`passwordRetryCount`, `accountUnlockTime`, `passwordGraceUserTime`.

Neither is translated into the other. What is shared is the shape of the
answer: which policy is in force, where it is written, and what the server
records.

---

## What a report says

| Field | Meaning |
|---|---|
| `policy.source` | `entry` when the account names its own, `default` when it is the server's, `none` when Alder could find none it could read |
| `policy.dn` | Where the policy is written, when it is an entry — so a policy that needs changing can be found |
| `policy.why` | How this policy came to apply, in a sentence |
| `policy.settings` | Each setting as the server holds it, with a label for the ones Alder recognises and a readable form beside the value: `7776000` *(90 days)* |
| `state.locked` | The server holds an attribute that locks the account, with what it says about it |
| `state.mustChange`, `state.expiry`, `state.failures`, `state.changed` | The account's own record, where the server keeps one |
| `state.attributes` | Everything read, as the server holds it |
| `unread` | Where the policy might be and could not be read — a session with no configuration identity cannot reach either server's default |

A policy attribute Alder does not recognise is **still reported**, with its
value: a setting nobody here has heard of is still in force. A value it can
read for a person is glossed beside the value, never instead of it.

## Reached from a refusal

A password the server refuses on policy grounds comes back as
`constraintViolation`, which says nothing about which rule. Since 1.22 that
refusal carries `remedy` pointing here, on that entry, so the policy actually
in force is one click away rather than three screens away.

## The API

`GET /policy?dn=<dn>` returns a `PolicyReport`. Read-only; there is no
endpoint that writes a policy or unlocks an account, and unlocking one is an
ordinary modification through the editor, with the LDIF preview and the plan
like any other change.

## What is not here

- **Evaluation.** Alder does not work out whether this account can bind. Where
  a server answers that sort of question itself, Alder asks it — see effective
  rights in [access control](ACCESS-CONTROL.md) — and no server answers this
  one.
- **Writing policies.** Out of scope, with the rest of `cn=config` beyond what
  a configuration comparison has proved.
