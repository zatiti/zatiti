# Zatiti

**An AI worker platform prototype built around durable work, scoped permissions, and inspectable outcomes.**

Zatiti is an open-source project building a local controller, Flutter desktop client, CLI, and MCP interface. Code for these components is present, but the repository does not yet deliver a supported, end-to-end product. The desktop preview uses fictional data; hosted Serenity sign-in and memory, real model-backed chat, and a qualified installer remain unfinished.

If you have explored GrokBot, Rakazo, OpenClaw, or Hermes, Zatiti is aimed at the same broad space: AI workers that can do more than answer one prompt. Its design emphasizes durable work, scoped permissions, and evidence that can be checked independently of a worker's report. The desktop, CLI, and MCP are being built around one operation model so policy can be enforced consistently across interfaces.

**Why Zatiti:**

- **Durable work:** the design gives workers persistent roles, skills, task history, and recovery state beyond a single chat transcript.
- **Permission by design:** the operation model scopes connections and actions, with human review for sensitive changes.
- **Evidence over narration:** the system is designed to distinguish a worker's report from an independently checked result, and to preserve uncertainty when an external action may have happened.
- **Shared memory, by design:** the planned Mac setup connects a personal chief to hosted Serenity. That OAuth and governed-memory flow is not implemented yet.
- **Several interfaces:** the project includes desktop, CLI, and MCP code intended to use the same controller and policy model; end-to-end parity is not yet qualified.

## Try Zatiti

The first planned release targets Mac on Intel and Apple Silicon. **There is no supported install-to-first-chat flow or copy-paste installer yet.** Hosted Serenity sign-in, provider setup, first-chat behavior, signed artifacts, and clean-Mac qualification remain release gates. Follow [GitHub Releases](https://github.com/zatiti/zatiti/releases) for a supported install command after those gates pass.

**Want to see the desktop now?** If Flutter and macOS desktop support are installed, paste this to launch the synthetic UI demo:

```sh
git clone https://github.com/zatiti/zatiti.git zatiti-demo && cd zatiti-demo/apps/desktop && flutter pub get && flutter run -d macos --dart-define=ZATITI_DEMO=true
```

This is a visual preview with fictional data, not a live AI chat. The [desktop guide](apps/desktop/README.md) has details.

Zatiti is pre-release software. This preview demonstrates only the desktop's synthetic interface; it is not connected to a controller, Serenity, or a model. The repository also contains controller, CLI/MCP, and packaging implementation work, but the specification itself explicitly says it is not evidence of a working product or completed qualification. Hosted OAuth and memory guarantees, signed Mac installation, backup restore, and first-chat qualification remain incomplete.

Flutter provides a basis for iOS, Android, and web clients after Mac, but those targets still need implementation and platform qualification. Linux and Windows desktop are also planned; none are supported releases today.

## Intended workflows

The product is being designed to give a coding agent persistent, inspectable infrastructure for ongoing work:

- **Organizations and projects** define responsibilities and scope. One installation can contain several organizations within the same trust boundary.
- **Workers** have persistent identities, instructions, skills, tool bindings, and resource limits. Their processes can come and go without losing task history.
- **Skills** are versioned procedures with declared dependencies. Installing instructions does not silently grant permissions.
- **Connections** bind tools to external services while keeping credentials out of manifests, model context, and ordinary tool responses.
- **Tasks and schedules** record outcomes, dependencies, attempts, artifacts, and recovery state.
- **Policies and reviews** determine which actions can run under standing authorization and which need an explicit decision.

The planned workflows include maintaining a repository, producing a research brief, reviewing a patch, or preparing content for publication. These examples describe the product direction, not a claim that the current build supports them end to end.

## Planned desktop workspace, with agents alongside you

The desktop client is intended to let a person talk with a personal chief, follow work, review decisions, and inspect results. CLI and MCP are intended to let coding agents such as Claude Code, Codex, or Cursor operate the same installation within their own permissions. The current desktop demo is synthetic, and end-to-end behavior and client compatibility have not been qualified.

You can ask your coding agent:

> Create an organization for this repository. Add an implementation worker and a review worker. Give them access to this repository only. Have the implementation worker prepare a patch and run the declared checks. Require review before publishing changes.

The intended flow lets an agent discover capabilities, submit a configuration plan, resolve missing prerequisites, and create work within its authority. The operation catalog and CLI/MCP code are present, but this complete journey has not been release-qualified.

The authorization model is designed to prevent an agent from turning its own text into human approval or granting itself more authority. External account consent and credential provisioning are expected to require the account owner.

## CLI and MCP

The CLI and MCP implementations are intended to offer readable and structured access to the controller's operation catalog. They are pre-release code and are not yet qualified for general use.

| Action | CLI | MCP tool |
|---|---|---|
| Inspect available capabilities | `zatiti capabilities --json` | `zatiti_capabilities` |
| Create an organization draft | `zatiti organization create --input @organization.json --json` | `zatiti_organization_create` |
| Import a skill | `zatiti skill import --input @skill.json --json` | `zatiti_skill_import` |
| Prepare a configuration plan | `zatiti configuration plan --input @changes.json --json` | `zatiti_configuration_plan` |
| Apply an authorized plan | `zatiti configuration apply --input @apply.json --json` | `zatiti_configuration_apply` |
| Create a task | `zatiti task create --input @task.json --json` | `zatiti_task_create` |
| Inspect a task | `zatiti task get --input @task-query.json --json` | `zatiti_task_get` |

The designed workflow uses drafts for resource creation that changes live configuration. Applying a plan is intended to check its exact contents, current revision, permissions, and prerequisites.

The intended local MCP entry point is `zatiti mcp serve`. See the [transport contract](docs/rfc.md#8-cli-and-mcp-contract) for the connection and parity design.

## Execution you can inspect

The design distinguishes a worker's report from an independently verified result, and an accepted external request from a confirmed outcome. Recovery and provider behavior still require full integration and qualification.

Worker execution is separate from client access in the design. A coding agent may manage Zatiti without being a runner that Zatiti can launch. The specification describes a hosted worker loop and externally operated workers; those modes should not be considered supported until qualified.

## Planned architecture and roadmap

The planned controller is local and single-tenant: one installation owns its task state, access rules, and recovery. Hosted Serenity is the intended canonical memory service. The Mac target is planned to use OAuth to connect to the user's selected Serenity brain; this flow and the governed-memory integration are not implemented yet. A local Serenity process is not part of the intended hosted Mac mode.

Mac is the first planned release target. iOS, Android, and web may follow because the client is built with Flutter; Linux and Windows desktop are also planned. Each target needs implementation and platform qualification before it can be advertised as supported.

## Project status

The [implementation specification](docs/implementation/README.md) records ownership, shared contracts, and acceptance criteria for code that now exists across the repository. It remains a contributor reference, not evidence that hosted services or release builds have passed qualification.

Every released management capability is intended to use the same operation model through the desktop, CLI, and MCP.

## Contributing

Start with the [RFC](docs/rfc.md). Feedback on the operation model, CLI/MCP parity, worker lifecycle, and first-release boundaries is welcome through [GitHub issues](https://github.com/zatiti/zatiti/issues).

Keep examples synthetic and configuration exports free of secrets. Design claims and planned tests are not evidence of implemented behavior.

## License

Licensed under the [Apache License, Version 2.0](LICENSE).
