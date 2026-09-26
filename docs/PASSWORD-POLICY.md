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
