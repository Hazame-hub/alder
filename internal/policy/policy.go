// Package policy reads the password policy in force on an entry, and the
// state of that account. It writes nothing, and it does not decide whether a
// bind would succeed.
//
// "Why can't this user log in?" is the other question a directory answers
// badly, and the sibling of the one internal/access reads. The facts that
// answer it are all in the directory and all in places nobody thinks to look:
// an operational attribute on the entry says the account is locked, another
// says when the password was last changed, and the policy that turns those
// into a refusal is written somewhere else entirely -- in the server's own
// configuration, or in an entry the account happens to point at.
//
// The two servers keep all of it differently:
//
//   - OpenLDAP's ppolicy overlay reads a policy from an entry in the data
//     tree. An account may name its own with pwdPolicySubentry; otherwise the
//     overlay's configured default applies. The state lives on the account as
//     pwdAccountLockedTime, pwdChangedTime, pwdFailureTime, pwdReset.
//   - 389 Directory Server keeps the global policy as attributes on cn=config
//     and a per-entry one as a subentry beside the account. The state lives on
//     the account as nsAccountLock, passwordExpirationTime,
//     passwordRetryCount, accountUnlockTime.
//
// There is no common policy model here, for the same reason there is none for
// configuration or access control. What is shared is the shape of the answer:
// which policy is in force, where it is written, and what the server currently
// records about this account.
package policy

// Where a policy came from.
const (
	// SourceEntry: the account names this policy itself.
	SourceEntry = "entry"
	// SourceDefault: the server's own default, applying to everything that
	// does not name one.
	SourceDefault = "default"
	// SourceNone: no policy was found, which is not the same as no policy
	// being in force.
	SourceNone = "none"
)

// Setting is one policy attribute or one piece of account state, as the server
// holds it.
type Setting struct {
	// Key is the attribute, named as the server names it.
	Key    string   `json:"key"`
	Values []string `json:"values"`
	// Label is what it means, for the ones this package recognises. Empty for
	// the rest, which are still reported: a policy attribute Alder has never
	// heard of is still in force.
	Label string `json:"label,omitempty"`
	// Detail renders a value a person can read -- "90 days" for 7776000 --
	// without replacing the value itself.
	Detail string `json:"detail,omitempty"`
}

// Policy is the password policy in force on an entry.
type Policy struct {
	// Source is entry, default or none.
	Source string `json:"source"`
	// DN is where the policy is written, when it is an entry.
	DN string `json:"dn,omitempty"`
	// Why says how this policy came to apply, in a person's words.
	Why      string    `json:"why"`
	Settings []Setting `json:"settings"`
}

// State is what the server currently records about the account.
type State struct {
	// Locked, and what the server says about it. The fact is the server's:
	// an attribute it set, or one an administrator set on it.
	Locked       bool   `json:"locked"`
	LockedDetail string `json:"lockedDetail,omitempty"`
	// MustChange: the password must be changed at the next bind.
	MustChange bool `json:"mustChange"`
	// Expiry is what the server says about expiry, where it says anything.
	Expiry string `json:"expiry,omitempty"`
	// Failures is the failed-bind count the server is keeping, where it keeps
	// one.
	Failures string `json:"failures,omitempty"`
	// Changed is when the password was last changed, as the server recorded.
	Changed string `json:"changed,omitempty"`
	// Attributes is everything read, as the server holds it.
	Attributes []Setting `json:"attributes"`
}

// Unread is somewhere the answer might be that could not be read.
type Unread struct {
	Where  string `json:"where"`
	Reason string `json:"reason"`
}

// Report is the answer for one entry.
type Report struct {
	DN     string   `json:"dn"`
	Policy *Policy  `json:"policy,omitempty"`
	State  *State   `json:"state"`
	Unread []Unread `json:"unread,omitempty"`
	// Unlock is the change that would clear the lock, where the account is
	// locked and Alder knows how to clear that kind of lock. Nil otherwise,
	// which includes a lock it does not recognise -- a change invented for
	// one of those would be a change nobody asked for.
	Unlock *Unlock `json:"unlock,omitempty"`
}

// Disclaimer is what this report is and is not, in one sentence, so the API,
// the command line and the interface all say the same thing.
const Disclaimer = "This is what the server records about the account and the policy it names, " +
	"not a decision about whether a bind would succeed: the directory decides that when one is tried."

// policyAttributes are the settings each server's policy is written in. The
// order is the order they are reported in, which is roughly the order an
// operator asks about them.
var policyAttributes = []struct {
	key   string
	label string
	unit  string
}{
	// OpenLDAP, from the ppolicy overlay's schema.
	{"pwdminlength", "minimum length", "count"},
	{"pwdmaxage", "maximum age", "seconds"},
	{"pwdminage", "minimum age", "seconds"},
	{"pwdinhistory", "passwords remembered", "count"},
	{"pwdmaxfailure", "failures before lockout", "count"},
	{"pwdlockout", "locks after too many failures", "bool"},
	{"pwdlockoutduration", "lockout lasts", "seconds"},
	{"pwdfailurecountinterval", "failure count resets after", "seconds"},
	{"pwdmustchange", "must change after a reset", "bool"},
	{"pwdallowuserchange", "the user may change it", "bool"},
	{"pwdgraceauthnlimit", "logins allowed after expiry", "count"},
	{"pwdexpirewarning", "warning before expiry", "seconds"},
	{"pwdcheckquality", "quality checking", ""},
	{"pwdsafemodify", "the old password is required to change it", "bool"},
	{"pwdattribute", "the attribute it governs", ""},

	// 389 Directory Server.
	{"passwordminlength", "minimum length", "count"},
	{"passwordmaxage", "maximum age", "seconds"},
	{"passwordminage", "minimum age", "seconds"},
	{"passwordinhistory", "passwords remembered", "count"},
	{"passwordmaxfailure", "failures before lockout", "count"},
	{"passwordlockout", "locks after too many failures", "bool"},
	{"passwordlockoutduration", "lockout lasts", "seconds"},
	{"passwordresetfailurecount", "failure count resets after", "seconds"},
	{"passwordmustchange", "must change after a reset", "bool"},
	{"passwordchange", "the user may change it", "bool"},
	{"passwordexp", "passwords expire", "bool"},
	{"passwordwarning", "warning before expiry", "seconds"},
	{"passwordgracelimit", "logins allowed after expiry", "count"},
	{"passwordchecksyntax", "quality checking", "bool"},
	{"passwordstoragescheme", "how it is stored", ""},
	{"passwordmindigits", "minimum digits", "count"},
	{"passwordminalphas", "minimum letters", "count"},
	{"passwordminuppers", "minimum upper case", "count"},
	{"passwordminlowers", "minimum lower case", "count"},
	{"passwordminspecials", "minimum punctuation", "count"},
	{"passwordmintokenlength", "shortest token checked against the entry", "count"},
}

// PolicyAttributeNames is every policy attribute this package reads, for the
// search that fetches one.
func PolicyAttributeNames() []string {
	out := make([]string, 0, len(policyAttributes))
	for _, a := range policyAttributes {
		out = append(out, a.key)
	}
	return out
}
