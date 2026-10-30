# Logging a request header the operator names

**Status: accepted (2026-10-30), every open question as recommended, and
built.** What the build settled is in `docs/DECISIONS.md` under the same
date.

`alder serve --log-request-header NAME`: Alder reads the value of one
request header the operator names, `X-Request-Id` for example, and puts it on
every log line written while it answers that request. It is a correlation
key, nothing more. Alder does not trust it, does not authorise on it, and
does not send it anywhere.

---

## The problem

Alder is often run behind something that terminates TLS and signs people in:
oauth2-proxy, Traefik with forward auth, nginx with `auth_request`, an
ingress controller. That proxy writes an access log with the user, the time
and a request ID. Alder writes its own log: `session opened` with the bind
DN, `change applied` with the change's summary, `change refused before it
ran` with the reason.

Nothing joins the two. To find who applied a change, an operator matches
Alder's `change applied` line to the proxy's access log by timestamp and
path. Two people applying within the same second, or a proxy and Alder
whose clocks drift, make that guesswork. Every one of those proxies can add
a request ID header (most do by default), so the key already arrives at
Alder; Alder throws it away.

## What exists today

- `cmd/alder/serve.go` builds one `slog.Logger` from `--log-level` and
  `--log-format` and hands it to the API server, the session store and each
  directory session.
- `requestLogger` writes a `request` line at debug level, with method, path,
  status and duration. No header.
- The lines that matter for an audit trail (`session opened`,
  `change applied`, `change rejected`, `changeset applied`,
  `change refused before it ran`, `recovery bundle prepared`, the allowlist
  refusals) are written with `logger.Info` and no context. The ones in the
  directory driver are written by a session's own logger, not the request's.
- `Session.Apply` and the other driver calls already take the request's
  `context.Context` (`c.UserContext()` with a timeout), so the request is
  reachable from every one of those lines.

## The proposal

1. **A flag.** `--log-request-header NAME`, read from
   `ALDER_LOG_REQUEST_HEADER` like every other flag. Empty (the default)
   turns it off and nothing changes. One header, not a list: a proxy that
   sends several IDs can be told which one to send.
2. **A middleware** after `recover`, before everything else: it reads the
   header, cleans the value (point 5), and stores it in the request's
   context. Without the header, or with an empty value after cleaning,
   nothing is stored and the lines are as they are today.
3. **A log handler** wrapping the text or JSON handler: when the record's
   context carries a value, it adds one attribute, `request_header`, with
   it. The attribute's name is fixed rather than taken from the header, so
   a log query does not depend on how each deployment spells its header.
4. **The call sites** listed above move from `Info(...)` to
   `InfoContext(ctx, ...)` (and the same for `Warn` and `Error`), with the
   request's context. The debug `request` line gets the attribute too. A
   line written outside a request (startup, the idle reaper's
   `session closed`) has no context and carries nothing, which is correct.
5. **The value is untrusted input written to a log.** It is cut at 128
   bytes, and anything outside printable ASCII is dropped before it is
   stored. slog already quotes and escapes values in both formats, so this
   is about keeping one line one line, and a hostile client from making the
   log large, not about injection.
6. **Names Alder refuses.** At start, `serve` refuses a header that carries
   credentials or sessions: `Authorization`, `Proxy-Authorization`,
   `Cookie`, `Set-Cookie`, and any name containing `token`, `secret`,
   `password` or `key`, in any case. Logging one of those would break hard
   rule 6 by configuration. The list is not configurable, like rule 6's.

### What it does not do

- **It is not an identity.** Alder does not read `X-Forwarded-User`,
  `X-Auth-Request-Email` or similar as a user, and an operator who names
  one gets a correlation key that happens to be a name, written to a log and
  used for nothing else. Charter section 2 (no multi-user concept) is
  untouched: no decision in Alder depends on the value.
- **It is not echoed.** Alder does not copy the header to its response. A
  proxy that wants that already has the value.
- **It does not touch the CLI.** `alder apply`, `alder project apply` and
  the other commands log to the terminal of whoever ran them, and a CI job
  already ties that output to its own run.

## Why not the alternatives

- **Generate a request ID in Alder and return it.** It would join Alder's
  lines to each other, not to the proxy's log, which is the problem. A
  proxy can be told to log a response header, but that is a second change
  in every deployment for a worse result.
- **Log every request header at debug.** Debug is not left on in
  production, and a header dump is where a cookie or a bearer token ends up
  in a log.
- **OpenTelemetry trace context.** A real tracing integration is a larger
  dependency and an export path, and "telemetry plumbing" is out of scope.
  An operator who sends `traceparent` can name it here and get the trace ID
  in Alder's lines, which is most of the value.

## What it changes, when built

`cmd/alder/serve.go` (flag, refusal list, middleware, handler wrapper);
the call sites in `internal/api`, `internal/session` and
`internal/directory/ldapdriver` (context variants, no new fields); tests for
the cleaning, the refusals, and one line from each layer carrying the
attribute; `docs/CLI.md` for the flag; and a DECISIONS entry. No API
shape changes, so no OpenAPI or web change, and nothing for the harness.

## Open questions for review

1. **One header or a list.** Recommended: one, as above. A list is easy to
   add later and hard to take back.
2. **The attribute's name.** Recommended: `request_header`, fixed. The
   alternative is the header's own name, lower-cased (`x_request_id`),
   which reads better but differs per deployment.
3. **The refusal list.** Recommended as in point 6, matched in any case.
   Anything an operator wants and the list blocks can be renamed at the
   proxy.
4. **Which lines.** Recommended: every line written while answering a
   request, by moving the call sites to the context variants. The smaller
   option is only the apply and refusal lines; it is less churn, but the
   next audit question will be about a line that was left out.
