package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/valyala/fasthttp"
)

// The bound this tests was declared in api/openapi.yaml and enforced by nothing.
//
// It held only because the sole route into the basket was one confirmation
// dialog at a time, so no one could reach five hundred by clicking. That is an
// accident of the UI, not a property of the API, and it stops being true the
// moment anything stages in bulk — which is why this is tested at the boundary
// rather than trusted to the caller.

// checkSize drives the check with a real request context and reports what the
// client would receive.
func checkSize(t *testing.T, n int) (status int, body map[string]any) {
	t.Helper()
	app := fiber.New()
	ctx := app.AcquireCtx(&fasthttp.RequestCtx{})
	defer app.ReleaseCtx(ctx)

	changes := make([]ChangeRequest, n)
	for i := range changes {
		changes[i] = ChangeRequest{Dn: "cn=x,dc=alder,dc=test", Type: "delete"}
	}

	if !changesetSizeOK(ctx, changes) {
		// A false return means the response is already written.
		out := map[string]any{}
		if len(ctx.Response().Body()) > 0 {
			if uerr := json.Unmarshal(ctx.Response().Body(), &out); uerr != nil {
				t.Fatalf("the refusal is not JSON: %v", uerr)
			}
		}
		return ctx.Response().StatusCode(), out
	}
	return 0, nil
}

func TestChangesetSizeAcceptsUpToTheDeclaredCap(t *testing.T) {
	for _, n := range []int{1, 2, MaxChangesetChanges - 1, MaxChangesetChanges} {
		if status, _ := checkSize(t, n); status != 0 {
			t.Errorf("%d changes were refused with %d; the cap is %d",
				n, status, MaxChangesetChanges)
		}
	}
}

func TestChangesetSizeRefusesPastTheCap(t *testing.T) {
	// One past the cap, and the shape a bulk stager would actually produce: an
	// imported document is bounded at eight megabytes, which is tens of
	// thousands of records.
	for _, n := range []int{MaxChangesetChanges + 1, 10_000} {
		status, body := checkSize(t, n)
		if status != fiber.StatusBadRequest {
			t.Fatalf("%d changes got status %d, want %d", n, status, fiber.StatusBadRequest)
		}
		message, _ := body["message"].(string)
		// The refusal has to name both numbers, or it cannot be acted on: the
		// operator needs to know the limit and how far over they are.
		if !strings.Contains(message, "500") {
			t.Errorf("the refusal does not name the limit: %q", message)
		}
		if !strings.Contains(message, itoa(n)) {
			t.Errorf("the refusal does not say how many were sent: %q", message)
		}
		if detail, _ := body["detail"].(string); detail == "" {
			t.Error("the refusal says what is wrong but not what to do about it")
		}
	}
}

// An empty changeset was already refused; it keeps being refused, and by the
// same function, so the two bounds cannot drift apart.
func TestChangesetSizeStillRefusesAnEmptySet(t *testing.T) {
	status, body := checkSize(t, 0)
	if status != fiber.StatusBadRequest {
		t.Fatalf("an empty changeset got status %d, want %d", status, fiber.StatusBadRequest)
	}
	if message, _ := body["message"].(string); !strings.Contains(message, "at least one") {
		t.Errorf("unhelpful refusal for an empty changeset: %q", message)
	}
}

// The constant and the spec have to agree. They are two files, and the spec's
// maxItems is a description that nothing validates against, so the only thing
// keeping them together is this.
func TestTheCapMatchesTheSpec(t *testing.T) {
	if MaxChangesetChanges != 500 {
		t.Errorf("MaxChangesetChanges is %d; api/openapi.yaml declares maxItems: 500 on "+
			"ChangesetRequest.changes. Change both or neither.", MaxChangesetChanges)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
