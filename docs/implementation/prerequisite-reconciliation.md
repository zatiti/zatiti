# Prerequisite reconciliation

Revision 25 builds on main revision 24. The reply-delivery, worker-principal,
governed validation, persisted effect subject and voice sweep implementations
have landed. Their controlled tests do not qualify a release or live provider.

This revision adds execution-pinned operation mappings, bounded legacy inbox
classification, shared design tokens and optional browser dependency seams.
Legacy inbox classification does not acknowledge human mail. The browser
listener, pairing, hub, native adapter and full browser journeys remain planned.
The external Ferro bridge agreement and live qualification gates remain open.

Earlier recovery notes are historical snapshots. PRs 77, 78, 79 and 80 have
merged; they must not be re-landed from stale local copies. The iOS spike remains
a separate draft. Existing dirty worktrees are preserved for recovery; the
reconciliation changes are based on current main, not wholesale checkout diffs.

To validate tokens, run `go run ./tools/designtokens -check` and the desktop
`test/ui/tokens_parity_test.dart` test. Regenerate specification outputs with
`python3 tools/specgen/render.py`, then verify with its `--check` flag.
