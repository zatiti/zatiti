# Zatiti repository guidance

This repository contains a product specification and a frozen package implementation scaffold. It does not yet contain a working product. Keep implementation and qualification claims honest.

For an assigned implementation package, use the AGENTS.md in that directory. It embeds the necessary product requirements and shared interfaces; no RFC copy is required. Write only in that assignment's root. Do not independently edit generated prompts, shared contracts, sibling packages, root dependency files or acceptance criteria. Report required contract changes to the integration owner with the affected callers and proposed revision.

The integration owner has a separate serialized foundation assignment for root go.mod/go.sum and docs/implementation/dependencies.lock.json. Do not run a root write assignment concurrently with child-package assignments. Use isolated worktrees for parallel implementation and one integration owner for landing. These are coordination rules, not a security sandbox or permission to publish.

Specification maintenance is an explicitly separate assignment: edit tools/specgen/model.py, tools/specgen/packages.py and the authored inputs in docs/implementation, then run `python3 tools/specgen/render.py` and `python3 tools/specgen/render.py --check`. Generated prompts are committed. They are harness-neutral and do not require Kazi, apply, a model or a rendering service.

Use synthetic fixtures and public dependency references. Never commit credentials, private hostnames/accounts, personal filesystem paths or confidential records. Preserve Apache-2.0 and dependency notices. Planned tests, skipped suites and compilation are not release evidence.

The implementation ownership map and dispatch sequence are in docs/implementation/README.md. ADR 001 records why directory prompts are committed and self-contained.
