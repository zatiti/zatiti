# E11 -- Hardening and distribution

Acceptance: docs/plans/browser-hardening.md exists with executable tasks and acc lines.
fidelity: outline

- [ ] T11.1 Plan browser hardening and distribution  Owner: TBD  Est: 1h  delivers: [UC-B14]  deps: [T10.3, T10.5]  blocked-by: [T10.3, T10.5]  kind: plan
  - Repo: zatiti. Paths: `docs/plans/browser-hardening.md`
  - What: Plan: - Chrome Web Store packaging and id pinning; - live reload of listener.json and the adapter profile without a serve restart (addressing the read-once problem); - automated revalidation (for example a controller-side job run as the owner); - credential rotation before 30 d; - an event.list resource filter (a contract change); - removing the tools anchor imports; - CDP edge cases (iframes, shadow DOM, debugger infobar reflow); - Windows and Linux support; - reconcile-loop backoff: prepare failures must call delayReconcile (today they retry every tick), and reconciliation after the 24 h review TTL parks the obligation as needs_human or exempts read-only reconciliation reads.
  - Acceptance: The plan file exists with tasks and acc lines.
  - Tests: none (planning)
  - acc: `test -s docs/plans/browser-hardening.md && grep -q 'acc:' docs/plans/browser-hardening.md`
