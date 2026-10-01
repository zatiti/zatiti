# E13 -- Zatiti Cloud streaming deployment

Acceptance: docs/plans/zatiti-cloud-streaming.md exists with executable tasks and acc lines.
fidelity: outline

- [ ] T13.1 Plan Zatiti Cloud streaming deployment and client adoption  Owner: TBD  Est: 1h  delivers: [UC-B17, UC-B18]  deps: [T10.1, T10.5]  blocked-by: [T10.1, T10.5]  kind: plan
  - Repo: zatiti. Paths: `docs/plans/zatiti-cloud-streaming.md`
  - What: Plan: - the cloud deployment of the remote mTLS listener (certificate issuance and rotation, load balancer or TLS passthrough, WebSocket idle limits, per-tenant caps); - a Go stream client in internal/client (a brief amendment and library-family widening); - optional desktop stream adoption; - a storage commit-notification seam to replace 750 ms event.list polling; - horizontal-scale questions (sticky sessions, fan-out); - qualification cases; - zatiti.ws.v1 replies subscription for human principals on Unix and remote, reusing an extracted SSE authorize-and-snapshot helper (internal/server/replyfeed.go) shared with POST /v1/replies/stream; - desktop migration from SSE; - the SSE deprecation decision.
  - Acceptance: The plan file exists with tasks and acc lines and names internal/client.
  - Tests: none (planning)
  - acc: `test -s docs/plans/zatiti-cloud-streaming.md && grep -q 'acc:' docs/plans/zatiti-cloud-streaming.md && grep -q 'internal/client' docs/plans/zatiti-cloud-streaming.md && grep -q 'replies/stream' docs/plans/zatiti-cloud-streaming.md`
