# A Helm chart and a one-command demo

**Status: proposed (2026-10-31). No code until it is accepted.** This is
feature 8 of the 2026-10-21 batch. It touches charter section 4
("Packaging"), so it stops at this proposal and an amendment on the record.

---

## The problem

There are two ways to run Alder today. You can download the binary and
run `alder serve`, or you can run the distroless image
`ghcr.io/hazame-hub/alder`. Both work, but they leave two groups of
people to do work Alder could do for them:

1. **Someone trying Alder with no directory at hand.** GETTING-STARTED.md
   step 1 asks them to clone the repository, install Task, and run
   `task compose:up`. That builds the test harness images locally
   (OpenLDAP, 389 DS, replicas and seed data) and starts Alder from
   source. It is the right setup for working on Alder, but a long path
   for "show me what it looks like". The harness is not an interface
   (COMPATIBILITY.md), so pointing newcomers at it ties them to something
   that may change at any time.
2. **A platform team running Kubernetes.** Each team writes its own
   Deployment, and each one rediscovers the same constraints:
   - sessions live in one process's memory, so **one replica only**, or
     sticky sessions;
   - `--allow-http` is only safe when TLS is terminated in front of Alder;
   - `--allowed-targets` should be set, because otherwise a signed-in user
     can make Alder dial any host;
   - the image runs as non-root and needs no writable filesystem.

## What exists today

- `Dockerfile.release`: distroless `static-debian12:nonroot`, built by
  GoReleaser for amd64 and arm64. Each release's archives are checksummed
  and ship an SBOM.
- The flags a cluster deployment needs, all of which are covered by the
  1.x promise: `--allow-http`, `--tls-cert`/`--tls-key`,
  `--allowed-targets`, `--read-only`, `--max-sessions`,
  `--log-request-header`, `--log-format=json`, and `/healthz`.
- `test/compose/`: the harness, built locally, never published.
- Charter section 4: "Packaging: a single binary with the SPA embedded;
  `alder serve` starts the web UI. A distroless container image from a
  multi-stage Dockerfile."

## Part 1: the demo (`examples/demo/`)

One `compose.yaml`, so that someone can run
`curl -O …/compose.yaml && docker compose up` and get a working
Alder plus a small seeded OpenLDAP on `http://127.0.0.1:8080`.

| Option | What `up` does | Cost and risk |
|---|---|---|
| **A. Build a small OpenLDAP image from a Dockerfile in `examples/demo/`** (Debian slim, slapd, about 40 seed entries) | Builds the directory in about a minute the first time, and pulls the released Alder image | It is the demo's own small image, not the harness. It needs a reachable Debian mirror |
| B. Publish a demo directory image on ghcr with each release | Pulls only | It becomes a product: it would be scanned, signed and updated for slapd CVEs |
| C. Use a third-party OpenLDAP image | Pulls only | The well-known ones are unmaintained or have moved behind a licence. Their configuration is not ours to document |
| D. Point newcomers at the harness, as today | Clone and Task | No change |

**Recommended: A.** It is self-contained and has no image to maintain. It
also stays apart from the harness, so the harness can keep changing.

- **The Alder image is pinned to the release that ships the file**
  (release-please bumps it, as it already does for the README).
- **Loopback only:** `127.0.0.1:8080:8443` with `--allow-http`, and
  `--allowed-targets=openldap:389`. The README says why `--allow-http` is
  safe only there.
- **No TLS to the directory in the demo** (plain LDAP on the compose
  network). This is the one place Alder would be shown without it, and the
  page says so in its first paragraph.
- **The connect details are printed** by `docker compose up` (host, port,
  bind DN and the demo password), because the connect screen has no URL
  parameters to pre-fill. Adding them is not part of this proposal.
- **Tested in CI:** `docker compose up --wait`, a `GET /healthz`, and one
  CLI `alder search` against the demo, on every pull request that touches
  `examples/demo/`.
- GETTING-STARTED.md step 1 offers the demo first and the harness second.

## Part 2: the Helm chart (`deploy/helm/alder/`)

| Option | Where it lives | Cost |
|---|---|---|
| **A. In this repository, published as an OCI chart on ghcr with each release** (`oci://ghcr.io/hazame-hub/charts/alder`, chart version = Alder version) | `deploy/helm/alder/` | One more release step, plus `helm lint`, kubeconform and a kind install in CI |
| B. In this repository, not published (install from a checkout) | the same | No release step, but nobody installs a chart from a checkout |
| C. A separate repository | elsewhere | A second release cadence for one Deployment |
| D. No chart; a documented Deployment manifest in `docs/` | `docs/KUBERNETES.md` | Least work; every team copies and edits it |

**Recommended: A.** The chart runs the released image and nothing else.

What it contains:

- A **Deployment with `replicas: 1`**, not a value. The template comment
  and the README say why (in-memory sessions). Strategy `Recreate`, so two
  pods never share an upgrade window.
- A **Service**, and an optional **Ingress**. With the Ingress on,
  `--allow-http` is set, because TLS ends at the Ingress. With it off, a
  Secret holding the certificate is required, and the chart fails to
  render without one.
- **`allowedTargets` is required.** The chart fails to render without it,
  unless `allowAnyTarget: true` says so explicitly.
- Values for `readOnly`, `maxSessions`, `logRequestHeader`, `logFormat`
  (default `json`), resources, nodeSelector and tolerations.
- **Hardened by default:** `runAsNonRoot`, `readOnlyRootFilesystem`, all
  capabilities dropped, `seccompProfile: RuntimeDefault`, no service
  account token mounted.
- Probes on `/healthz`.
- An optional **NetworkPolicy** that limits egress to the `allowedTargets`
  (and DNS).
- No CRDs, no operator, no hooks.

**Compatibility:** the chart's values become part of the 1.x promise,
like the flags. A value is only ever added, never renamed or removed
within 1.x. COMPATIBILITY.md gets a "Helm chart" section.

## The charter amendment

Section 4, "Packaging", would read:

> a single binary with the SPA embedded; `alder serve` starts the web UI.
> A distroless container image from a multi-stage Dockerfile, **and a Helm
> chart that runs that image and nothing else (`deploy/helm/alder`). A
> demo compose file (`examples/demo/`) is an example, not packaging: it is
> not covered by the compatibility promise.**

## What it costs

- Demo: one Dockerfile, one compose file, seed LDIF, a CI job, and the
  GETTING-STARTED change. Small.
- Chart: the templates and values, the README, CI (lint, kubeconform, and
  a kind install with a `/healthz` check), and a release step (`helm
  package` and `helm push`, with a checksum like the other release
  assets). Medium.

## Open questions for review

1. **Demo: A (recommended), B, C or D?**
2. **Chart: A (recommended), B, C or D?**
3. **The amendment's wording**, and whether the demo should be named in
   the charter at all (recommended: named, as "not packaging").
4. **389 DS in the demo too?** Recommended: no. One directory keeps it
   under a minute, and the harness already covers both servers.
