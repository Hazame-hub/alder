package api

import (
	"errors"

	"github.com/hazame-hub/alder/internal/directory/ldapdriver"
)

// The reconcile logic itself lives in internal/plan.
//
// It moved there when the plan endpoint was written, because both need it and
// two implementations of "what would have to change to make this entry match
// this record" is exactly how a plan and an apply come to disagree. The import
// handler and the planner now call one Reconcile.
//
// What stays here is the one part that is about this driver rather than about
// records: recognising "there is no such entry" among the errors an LDAP server
// returns. internal/plan does not import the driver, so it takes this as a
// function.

// isNoSuchObject reports the one read failure that is not a failure: the entry
// the record describes does not exist yet, so the record stays an add.
func isNoSuchObject(err error) bool {
	var ldapErr *ldapdriver.Error
	return errors.As(err, &ldapErr) && ldapErr.IsNoSuchObject()
}
