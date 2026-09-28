package api

import (
	"net/http"
	"testing"
)

// Letting go of a directory must not queue behind the directory.
//
// Every /api/v1 route sat behind the in-flight gate, which exists to stop
// Alder asking a directory more at once than it can bear. `DELETE /session`
// asks it nothing -- it is a delete from a map in Alder's own memory -- and
// it was in the queue all the same. A UI audit with the gate saturated
// clicked Disconnect five times and watched nothing happen; the network log
// held three 503s. Exactly when an operator most wants to let go of a
// directory is when they could not.

func TestLettingGoOfASessionDoesNotQueueBehindTheDirectory(t *testing.T) {
	// A gate of one, held, so anything queued answers 503 rather than waiting
	// out the full timeout.
	rig := newRig(t, Config{MaxInFlight: 1}, &fakeSession{})
	if rig.server.gate == nil {
		t.Fatal("the rig built no gate, so this proves nothing")
	}
	rig.server.gate.slots <- struct{}{} // the only slot, taken

	res := rig.do(t, http.MethodDelete, "/api/v1/session", nil)
	if res.Status != http.StatusNoContent {
		t.Errorf("DELETE /session answered %d with the gate full; want 204\n%s", res.Status, res.Body)
	}

	// Reading the session is the same: the header polls it, and a header
	// that stops answering under load is the false-claim problem in reverse.
	if got := rig.anonymous(t, http.MethodGet, "/api/v1/source").Status; got != http.StatusOK {
		t.Errorf("GET /source answered %d with the gate full; want 200", got)
	}
}

func TestDirectoryWorkStillQueues(t *testing.T) {
	// The gate is not decoration. If everything bypassed it, the limit that
	// protects the directory would be gone.
	rig := newRig(t, Config{MaxInFlight: 1}, &fakeSession{})
	rig.server.gate.slots <- struct{}{}

	res := rig.do(t, http.MethodGet,
		"/api/v1/entry?dn=uid%3Dalice%2Cou%3Dpeople%2Cdc%3Dalder%2Cdc%3Dtest", nil)
	if res.Status != http.StatusServiceUnavailable {
		t.Errorf("a directory read answered %d with the gate full; want 503", res.Status)
	}
}
