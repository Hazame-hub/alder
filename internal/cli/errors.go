package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/hazame-hub/alder/internal/api"
)

// refusal turns a response Alder did not answer with success into an ExitError.
//
// Alder's typed error is kept whole: its code is what a script switches on, and
// flattening it into "HTTP 409" would throw away the difference between a stale
// plan and a directory refusing a write. The two cases where the reviewed plan
// no longer holds -- the directory moved, or the operation sent is not the one
// planned -- get their own exit code, because the right response to both is to
// plan again and review, never to retry.
func (r *remote) refusal(ctx context.Context, what string, res *http.Response, body []byte) *ExitError {
	var apiErr api.Error
	if isJSON(res.Header) && json.Unmarshal(body, &apiErr) == nil && apiErr.Error != "" {
		e := &ExitError{Code: ExitFailed, Server: compactJSON(body), Message: describeAPIError(what, apiErr)}
		if planNoLongerHolds(apiErr) {
			e.Code = ExitStale
		}
		return e
	}
	switch res.StatusCode {
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return r.unsupported(ctx, what)
	case http.StatusRequestEntityTooLarge:
		return failf("request_too_large",
			"Alder refused %s as too large: it reads at most %d MB in one request", what, maxRequestBytes>>20)
	case http.StatusServiceUnavailable:
		return failf("busy", "Alder is answering as many requests as it allows at once; %s was not attempted. Try again", what)
	}
	return failf("unexpected_response", "Alder answered %s with HTTP %d and no error description", what, res.StatusCode)
}

func planNoLongerHolds(e api.Error) bool {
	if e.Error == api.ErrorErrorPlanMismatch {
		return true
	}
	return e.Error == api.ErrorErrorConflict && e.Cause != nil && *e.Cause == api.ErrorCausePlanStale
}

func describeAPIError(what string, e api.Error) string {
	var b strings.Builder
	switch {
	case e.Error == api.ErrorErrorConflict && e.Cause != nil && *e.Cause == api.ErrorCausePlanStale:
		b.WriteString("the plan is stale: the directory changed after it was made, so nothing was written.\n" +
			"Run the command again to review a new plan.")
	case e.Error == api.ErrorErrorPlanMismatch:
		b.WriteString("the changes sent are not the operations that were planned, so nothing was written.\n" +
			"Run the command again to review a new plan.")
	default:
		writef(&b, "%s was refused [%s]: %s", what, e.Error, safe(e.Message))
	}
	if e.Detail != nil && *e.Detail != "" {
		writef(&b, "\n  %s", safe(*e.Detail))
	}
	if e.Hint != nil && *e.Hint != "" {
		writef(&b, "\n  %s", safe(*e.Hint))
	}
	if e.Affected != nil {
		for i, a := range *e.Affected {
			if i == 10 {
				writef(&b, "\n  ... and %d more", len(*e.Affected)-i)
				break
			}
			writef(&b, "\n  change %d: %s", a.Index+1, safe(a.Dn))
		}
	}
	return b.String()
}

// transportFailure is a request that got no answer at all.
func transportFailure(ctx context.Context, what string, err error) *ExitError {
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		return failf("interrupted", "interrupted while %s", what)
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return failf("timeout", "gave up while %s: --timeout elapsed", what)
	}
	return failf("unreachable", "could not reach Alder while %s: %v", what, err)
}

// unsupported explains a server that does not have an endpoint this client
// needs, which in practice is an older Alder.
func (r *remote) unsupported(ctx context.Context, what string) *ExitError {
	version := "a version that did not say"
	probe, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if res, err := r.api.GetSourceOfferWithResponse(probe); err == nil && res.JSON200 != nil {
		version = "version " + safe(res.JSON200.Version)
	}
	return failf("unsupported_server",
		"the Alder server at %s (%s) has no endpoint for %s. This client is alder %s; "+
			"snapshots and diff need an Alder server of 1.7 or later, and plan and apply need 1.6 or later",
		r.base, version, what, r.env.Version)
}

func isJSON(h http.Header) bool {
	mt, _, err := mime.ParseMediaType(h.Get("Content-Type"))
	return err == nil && (mt == "application/json" || strings.HasSuffix(mt, "+json"))
}

func compactJSON(doc []byte) json.RawMessage {
	var buf bytes.Buffer
	if err := json.Compact(&buf, doc); err != nil {
		return json.RawMessage(bytes.Clone(doc))
	}
	return json.RawMessage(buf.Bytes())
}
