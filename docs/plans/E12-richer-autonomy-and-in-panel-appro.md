# E12 -- Richer autonomy and in-panel approval

Acceptance: docs/plans/browser-autonomy.md exists and covers the in-panel approval revision.
fidelity: outline

- [ ] T12.1 Plan richer autonomy and the in-panel approval contract revision 23 (N+1, after the browser revision 22)  Owner: TBD  Est: 1h  delivers: [UC-B12, UC-B13]  deps: [T10.3, T10.5]  blocked-by: [T10.3, T10.5]  kind: plan
  - Repo: both. Paths: `docs/plans/browser-autonomy.md`
  - What: Plan the in-panel approval contract revision 23 (N+1, after the browser revision 22), which changes decide.go's non-human denial to allow a bound, attested owner-presence path. Also plan standing per-origin allows, multi-step plans, a ferro RunDriver integration, and a fill-value preview policy.
  - Acceptance: The plan file exists and addresses in-panel approval and decide.go.
  - Tests: none (planning)
  - acc: `test -s docs/plans/browser-autonomy.md && grep -qi 'in-panel approval' docs/plans/browser-autonomy.md && grep -q 'decide.go' docs/plans/browser-autonomy.md`
