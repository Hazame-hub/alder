package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/session"
)

// A page on another origin of the same site -- a sibling subdomain, or another
// port on localhost -- gets the SameSite=Strict cookie. These are the requests
// it can make without a CORS preflight, and none of them may write.
func TestAWriteFromAnotherOriginIsRefused(t *testing.T) {
	form := url.Values{"dn": {planAlice}, "type": {"delete"}}.Encode()
	deleteAlice := `{"dn":"` + planAlice + `","type":"delete"}`

	cases := []struct {
		name        string
		contentType string
		fetchSite   string
		body        string
		want        int
	}{
		{"an HTML form", "application/x-www-form-urlencoded", "", form, http.StatusUnsupportedMediaType},
		{"a multipart form", "multipart/form-data; boundary=x", "", "--x\r\nContent-Disposition: form-data; name=\"dn\"\r\n\r\n" + planAlice + "\r\n--x--\r\n", http.StatusUnsupportedMediaType},
		{"text/plain holding JSON", "text/plain", "", deleteAlice, http.StatusUnsupportedMediaType},
		{"no content type at all", "", "", deleteAlice, http.StatusUnsupportedMediaType},
		{"JSON a browser calls same-site", "application/json", "same-site", deleteAlice, http.StatusForbidden},
		{"JSON a browser calls cross-site", "application/json", "cross-site", deleteAlice, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := planRig(t, personAt(t, planAlice, []string{"cn", "Alice"}, []string{"sn", "L"}))
			res := rig.raw(t, http.MethodPost, "/api/v1/changes/apply", tc.contentType, tc.fetchSite, tc.body)
			if res.Status != tc.want {
				t.Errorf("status = %d, want %d\nbody: %s", res.Status, tc.want, res.Body)
			}
			if len(rig.fake.applied) != 0 {
				t.Fatalf("the change was applied: %+v", rig.fake.applied)
			}
		})
	}
}

// What Alder's own clients send still works: the SPA's same-origin JSON, a
// request a browser calls "none", and the command line, which sends no
// Sec-Fetch-Site at all. A charset parameter is still JSON.
func TestAlderOwnWritesStillGoThrough(t *testing.T) {
	deleteAlice := `{"dn":"` + planAlice + `","type":"delete"}`
	for _, tc := range []struct{ name, contentType, fetchSite string }{
		{"the web interface", "application/json", "same-origin"},
		{"typed into the browser", "application/json", "none"},
		{"the command line", "application/json", ""},
		{"with a charset", "application/json; charset=utf-8", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rig := planRig(t, personAt(t, planAlice, []string{"cn", "Alice"}, []string{"sn", "L"}))
			res := rig.raw(t, http.MethodPost, "/api/v1/changes/apply", tc.contentType, tc.fetchSite, deleteAlice)
			if res.Status != http.StatusOK {
				t.Fatalf("status = %d, want 200\nbody: %s", res.Status, res.Body)
			}
			if len(rig.fake.applied) != 1 {
				t.Errorf("applied %d changes, want 1", len(rig.fake.applied))
			}
		})
	}
}

// Reading is not affected: a cross-site GET cannot read the answer anyway, and
// a link to Alder from elsewhere must still open it.
func TestAReadFromAnotherOriginIsNotRefused(t *testing.T) {
	rig := planRig(t, personAt(t, planAlice, []string{"cn", "Alice"}, []string{"sn", "L"}))
	res := rig.raw(t, http.MethodGet, "/api/v1/source", "", "cross-site", "")
	if res.Status != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody: %s", res.Status, res.Body)
	}
}

// raw sends a request with the session cookie and exactly the headers given.
func (r *testRig) raw(t testing.TB, method, target, contentType, fetchSite, body string) response {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if fetchSite != "" {
		req.Header.Set("Sec-Fetch-Site", fetchSite)
	}
	req.AddCookie(&http.Cookie{Name: session.CookieNameInsecure, Value: r.cookie})
	return r.send(t, req)
}
