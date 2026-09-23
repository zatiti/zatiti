# Zatiti

Organize and run AI workers through a CLI or MCP.

Zatiti is an open-source project in Go for defining organizations, equipping workers with skills and connections, assigning durable tasks, and inspecting their results. It is designed for a single operator or team running its own installation.

A coding agent such as Claude Code, Codex, or Cursor should be able to operate Zatiti through MCP or its command line: create an organization, import a skill, configure a connection, assign a worker, start a task, and follow it to a verified result. CLI and MCP feature parity is a release requirement.

**Status: implemented, pre-release (2026-09-23).** The [RFC](docs/rfc.md) defines the architecture, and every card of the [implementation completion plan](docs/implementation-remediation/README.md) has landed on `main`. What works today, through real production code:

- `cmd/zatiti` builds, and the CLI/MCP command tree below is real: both are generated from the same catalog of 201 public operations.
- First run works: `zatiti serve` on an empty state directory, then `zatiti init`, creates the installation and writes the owner credential profile.
- The core worker loop runs end to end: create a task, start it, have a worker claim and check in on a run, report a result, verify that result independently, and reach a terminal state.
- Backup and restore complete end to end.

What is not done yet:

- There is no tagged release, installer, or signed artifact. Run it from source (see [Run from source](#run-from-source)).
- Hosted workers need a model adapter profile that you write by hand; no hosted model step can start without one. Until you configure it, the controller runs in storage-only readiness, and the practical mode is a coding agent acting as an external worker over MCP.
- The tool adapters (GitHub, HTTP read, Serenity memory) are not registered by default.
- Release qualification on real hosts and agent clients has not been run. Compatibility with individual agent clients will be tested before it is advertised as supported.
- Known open items, none blocking: two intermittent CI tests, and three restore follow-ups recorded in the [roadmap](docs/roadmap.md).

The [package implementation scaffold](docs/implementation/README.md) freezes ownership and shared contracts for parallel implementation. Each package directory contains a committed, self-contained `AGENTS.md` with its requirements, interfaces, schemas, and acceptance cases.

## What Zatiti is for

Give a coding agent persistent, inspectable infrastructure for ongoing work:

- **Organizations and projects** define responsibilities and scope. One installation can contain several organizations within the same trust boundary.
- **Workers** have persistent identities, instructions, skills, tool bindings, and resource limits. Their processes can come and go without losing task history.
- **Skills** are versioned procedures with declared dependencies. Installing instructions does not silently grant permissions.
- **Connections** bind tools to external services while keeping credentials out of manifests, model context, and ordinary tool responses.
- **Tasks and schedules** record outcomes, dependencies, attempts, artifacts, and recovery state.
- **Policies and reviews** determine which actions can run under standing authorization and which need an explicit decision.

Use it for workflows such as maintaining a repository, producing a research brief, reviewing a patch, or preparing content for publication. The same task and execution contracts apply across domains.

## Your agent is the interface

The first release focuses on CLI and MCP. The local API contract can be fed to [Mint](https://github.com/sirerun/mint), an OpenAPI-to-MCP generator, to produce the MCP transport. Generated code remains a reviewed build artifact: it calls the authenticated Zatiti API and cannot bypass application policy.

You can ask your coding agent:

> Create an organization for this repository. Add an implementation worker and a review worker. Give them access to this repository only. Have the implementation worker prepare a patch and run the declared checks. Require review before publishing changes.

The agent discovers Zatiti's capabilities, submits a configuration plan, resolves missing prerequisites, applies changes within its authority, and creates the task. You can inspect or continue the same work from the CLI. Starting through MCP does not create a separate configuration store or task engine.

The agent can administer everything its principal is authorized to manage. It cannot turn its own text into a human approval or grant itself more authority. External account consent and credential provisioning may still require the account owner.

## Intended interface

The CLI offers readable output and a stable JSON mode. MCP exposes typed tools backed by the same operations and validation.

| Action | CLI | MCP tool |
|---|---|---|
| Inspect available capabilities | `zatiti capabilities --json` | `zatiti_capabilities` |
| Create an organization draft | `zatiti organization create --input @organization.json --json` | `zatiti_organization_create` |
| Import a skill | `zatiti skill import --input @skill.json --json` | `zatiti_skill_import` |
| Prepare a configuration plan | `zatiti configuration plan --input @changes.json --json` | `zatiti_configuration_plan` |
| Apply an authorized plan | `zatiti configuration apply --input @apply.json --json` | `zatiti_configuration_apply` |
| Create a task | `zatiti task create --input @task.json --json` | `zatiti_task_create` |
| Inspect a task | `zatiti task get --input @task-query.json --json` | `zatiti_task_get` |

Resource creation produces drafts where activation changes live configuration. Applying a plan checks its exact contents, current revision, permissions, and required prerequisites. Neither interface provides a shortcut around those checks.

The local MCP entry point is `zatiti mcp serve`. Client configuration selects a local credential profile. A configured MCP client needs no separate hosted Zatiti account. See the [transport contract](docs/rfc.md#8-cli-and-mcp-contract) for the connection, Mint generation, and parity rules.

## Run from source

These steps build the controller and bootstrap a local installation. They require Go 1.26.

1. Build the binary:

   ```sh
   go build -o zatiti ./cmd/zatiti
   ```

1. Create a 32-byte master key outside the state directory. Artifacts and stored secrets are always encrypted at rest, so `serve` refuses to start without one:

   ```sh
   mkdir -p ~/.zatiti-keys
   head -c 32 /dev/urandom > ~/.zatiti-keys/master.key
   chmod 600 ~/.zatiti-keys/master.key
   ```

1. Start the controller. It serves bootstrap only until the installation is initialized:

   ```sh
   ./zatiti serve --credential-backend headless --master-key file:$HOME/.zatiti-keys/master.key
   ```

1. In another terminal, initialize the installation. This writes the `owner` credential profile:

   ```sh
   ./zatiti init --json --input '{"credential_store":"headless","owner_name":"Owner","headless_key_ref":"installation/owner"}'
   ```

1. Confirm that the owner profile authenticates:

   ```sh
   ./zatiti capabilities --json
   ```

1. Point your MCP client at `zatiti mcp serve`. It uses the `owner` profile by default.

Use `--state-dir` or `ZATITI_STATE_DIR` to keep an installation somewhere other than the default per-user state directory.

## Execution you can inspect

Zatiti distinguishes a worker's report from an independently verified result. It also distinguishes an accepted external request from a confirmed outcome. If a connection drops after a write might have happened, the operation stays unresolved until evidence establishes what happened; it is not blindly repeated.

Worker execution is separate from client access. A coding agent may manage Zatiti without being a runner that Zatiti can launch. The first release supports a hosted worker loop and externally operated workers. External workers are advisory: Zatiti governs operations submitted to it, but cannot account for actions taken independently with other credentials.

## Scope

Zatiti is designed as a local Go controller with an embedded database, durable task state, and a local MCP server. Single tenant means one installation trust boundary, not one organization or one worker. There is no hosted multitenant service, required web dashboard, or mobile client in the first release.

The design favors one shared implementation over separate CLI and MCP behavior. Every released management capability must be available through both interfaces and pass the same behavioral tests.

## Contributing

Start with the [RFC](docs/rfc.md). Feedback on the operation model, CLI/MCP parity, worker lifecycle, and first-release boundaries is welcome through [GitHub issues](https://github.com/zatiti/zatiti/issues).

Keep examples synthetic and configuration exports free of secrets. Design claims and planned tests are not evidence of implemented behavior.

## License

Licensed under the [Apache License, Version 2.0](LICENSE).
