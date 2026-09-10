# Zatiti

Organize and run AI workers through a CLI or MCP.

Zatiti is an open-source project in Go for defining organizations, equipping workers with skills and connections, assigning durable tasks, and inspecting their results. It is designed for a single operator or team running its own installation.

A coding agent such as Claude Code, Codex, or Cursor should be able to operate Zatiti through MCP or its command line: create an organization, import a skill, configure a connection, assign a worker, start a task, and follow it to a verified result. CLI and MCP feature parity is a release requirement.

**Status: design stage.** The [RFC](docs/rfc.md) defines the proposed architecture and first release. There is no runnable implementation or published installation command yet. Examples below illustrate the intended interface; they are not commands you can run today. Compatibility with individual agent clients will be tested before it is advertised as supported.

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

The planned local MCP entry point is `zatiti mcp serve`. Client configuration will select a local credential profile. A configured MCP client needs no separate hosted Zatiti account. See the [transport contract](docs/rfc.md#8-cli-and-mcp-contract) for the proposed connection, Mint generation, and parity rules.

## Execution you can inspect

Zatiti distinguishes a worker's report from an independently verified result. It also distinguishes an accepted external request from a confirmed outcome. If a connection drops after a write might have happened, the operation stays unresolved until evidence establishes what happened; it is not blindly repeated.

Worker execution is separate from client access. A coding agent may manage Zatiti without being a runner that Zatiti can launch. The proposed first release supports a hosted worker loop and externally operated workers. External workers are advisory: Zatiti governs operations submitted to it, but cannot account for actions taken independently with other credentials.

## Scope

Zatiti is designed as a local Go controller with an embedded database, durable task state, and a local MCP server. Single tenant means one installation trust boundary, not one organization or one worker. There is no hosted multitenant service, required web dashboard, or mobile client in the first release.

The design favors one shared implementation over separate CLI and MCP behavior. Every released management capability must be available through both interfaces and pass the same behavioral tests.

## Contributing

Start with the [RFC](docs/rfc.md). Feedback on the operation model, CLI/MCP parity, worker lifecycle, and first-release boundaries is welcome through [GitHub issues](https://github.com/zatiti/zatiti/issues).

Keep examples synthetic and configuration exports free of secrets. Design claims and planned tests are not evidence of implemented behavior.

## License

Licensed under the [Apache License, Version 2.0](LICENSE).
