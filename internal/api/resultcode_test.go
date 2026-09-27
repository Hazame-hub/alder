package api

import (
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/directory/ldapdriver"
)

// "(code 200)" on a failure, where 200 is not a result code at all.
//
// go-ldap numbers its own client-side conditions from 200 in the same uint16
// the protocol's result codes live in: a dead socket is 200 "Network Error",
// an unparseable reply is 205. The directory never sent them. A UI audit read
// the number as an HTTP 200 in a message about a failure, which is a fair
// reading -- 200 is not a number anybody has seen in a directory log either.

func TestAFailureThatNeverReachedTheDirectoryCarriesNoResultCode(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{
		readErr: &ldapdriver.Error{Code: 200, Message: "Network Error"},
	})

	res := rig.do(t, "GET", "/api/v1/entry?dn=uid%3Dalice%2Cou%3Dpeople%2Cdc%3Dalder%2Cdc%3Dtest", nil)

	if res.Status != 502 {
		t.Fatalf("status %d, want 502: %s", res.Status, res.Body)
	}
	if strings.Contains(res.Body, "ldapCode") {
		t.Errorf("the body reports a result code the directory never sent: %s", res.Body)
	}
	if strings.Contains(res.Body, "code 200") {
		t.Errorf("the detail still reads like an HTTP 200: %s", res.Body)
	}
	if !strings.Contains(res.Body, "Network Error") {
		t.Errorf("what actually went wrong is gone from the body: %s", res.Body)
	}
	// The one line the header badge shows. "Returned an error" is wrong when
	// nothing was returned, and the two cases send an operator to look in
	// different places.
	if !strings.Contains(res.Body, "The directory did not answer.") {
		t.Errorf("the message says the directory answered: %s", res.Body)
	}
}

func TestAFailureTheDirectoryAnsweredKeepsItsResultCode(t *testing.T) {
	// The other half, and the half that matters more: a real result code is
	// what the hint and the remedy are built from, and what an operator
	// searches the server's documentation for.
	rig := newRig(t, Config{}, &fakeSession{
		readErr: &ldapdriver.Error{Code: 50, Message: "Insufficient Access Rights"},
	})

	res := rig.do(t, "GET", "/api/v1/entry?dn=uid%3Dalice%2Cou%3Dpeople%2Cdc%3Dalder%2Cdc%3Dtest", nil)

	if res.Status != 403 {
		t.Fatalf("status %d, want 403: %s", res.Status, res.Body)
	}
	if !strings.Contains(res.Body, `"ldapCode":50`) {
		t.Errorf("the result code is missing: %s", res.Body)
	}
	if !strings.Contains(res.Body, "code 50") {
		t.Errorf("the detail no longer says which code: %s", res.Body)
	}
}

func TestWhichCodesAreTheLibrarysOwn(t *testing.T) {
	// The library's own numbers are 200 to 206 and nothing else. Everything
	// on either side is the protocol's, allocated or not: 123 is
	// authorizationDenied, 4096 is syncRefreshRequired, and 199 and 207 are
	// unallocated -- and an unallocated code the directory really sent is
	// still a code it sent, which the hint and the remedy are built from.
	for _, code := range []uint16{0, 32, 50, 53, 123, 199, 207, 4095, 4096, 4097} {
		if (&ldapdriver.Error{Code: code}).IsClientSide() {
			t.Errorf("result code %d is treated as the library's own", code)
		}
	}
	for code := uint16(200); code <= 206; code++ {
		if !(&ldapdriver.Error{Code: code}).IsClientSide() {
			t.Errorf("%d is the library's own number and is reported as a result code", code)
		}
	}
}

func TestAChangesetRunReportsCodesTheSameWay(t *testing.T) {
	// The same rule on the other error path. A changeset is where an
	// unexplained number costs most: the run has stopped, part of it has
	// applied, and the operator is deciding what to do next.
	caps := directory.Capabilities{}
	record := directory.ChangeRecord{Type: directory.ChangeModify}

	dead := errorBody(&ldapdriver.Error{Code: 200, Message: "Network Error"}, record, caps)
	if dead.LdapCode != nil {
		t.Errorf("a dead connection reports LDAP result code %d", *dead.LdapCode)
	}
	refused := errorBody(&ldapdriver.Error{Code: 50, Message: "Insufficient Access Rights"}, record, caps)
	if refused.LdapCode == nil || *refused.LdapCode != 50 {
		t.Errorf("a refusal lost its result code: %+v", refused.LdapCode)
	}
}
