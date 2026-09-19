package installation

// FirstTaskSequence is the exact operator sequence from an initialized
// installation to its first task, as CLI commands with UPPER_CASE
// placeholders an operator fills from the previous command's output. It is
// printed by installation.doctor while no budget currency is configured
// (the state every fresh installation is in) and by `zatiti task create
// --help`, and the entrypoint's tests execute it verbatim against a real
// controller so it cannot drift from what the product accepts.
//
// The three facts a person cannot infer from the refusals alone are stated
// here rather than discovered by failing: the verifier's capability
// evidence must be published as an artifact at INSTALLATION scope before a
// task can name it; the unconfigured budget currency is spelled XXX and
// admits only a zero-spend task until a budget is configured; and the
// task definition is complete only with every field below.
const FirstTaskSequence = `# Ids: INSTALLATION_ID is resource.installation_id from 'zatiti init'.
# OWNER_ID is the id of the principal of kind "human"; WORKER_ID is the chief worker's id.
zatiti principal list --json --input '{"scope":{"installation_id":"INSTALLATION_ID"}}'
zatiti worker list --json --input '{"scope":{"installation_id":"INSTALLATION_ID"}}'
# Publish the verifier's capability evidence at INSTALLATION scope (any small file):
# SIZE is its byte count, DIGEST its lowercase sha256 hex, BASE64 its standard base64.
# UPLOAD_ID is resource.id from begin; UPLOAD_VERSION is resource.version from chunk;
# ARTIFACT_ID is resource.id from finish.
zatiti artifact upload begin --json --submission-key evidence-1 --input '{"scope":{"installation_id":"INSTALLATION_ID"},"size":SIZE,"digest":"DIGEST","media_type":"application/json","classification":"internal"}'
zatiti artifact upload chunk --json --submission-key evidence-2 --input '{"scope":{"installation_id":"INSTALLATION_ID"},"upload_id":"UPLOAD_ID","offset":0,"bytes_base64":"BASE64","chunk_digest":"DIGEST"}'
zatiti artifact upload finish --json --submission-key evidence-3 --input '{"scope":{"installation_id":"INSTALLATION_ID"},"upload_id":"UPLOAD_ID","expected_version":UPLOAD_VERSION}'
# Create the first task: zero spend in the unconfigured currency XXX (a positive
# spend, or any other currency, is refused until a budget is configured).
zatiti task create --json --submission-key task-1 --input '{"scope":{"installation_id":"INSTALLATION_ID"},"definition":{"scope":{"installation_id":"INSTALLATION_ID"},"owner_id":"OWNER_ID","worker_id":"WORKER_ID","outcome":"Produce the first report","inputs":[],"required_outputs":["report.bin"],"acceptance":{"verifier_id":"zatiti-verifier","verifier_version":"v1","sealed_inputs":[],"expected_observations":[{"check_id":"report-present","kind":"artifact_presence","expected":"pass","artifact_name":"report.bin","expected_digest":"DIGEST"}],"mode":"independent","required_child_ids":[],"profile":{"schema":"zatiti.verifier-profile/v1","kind":"artifact_contract","id":"zatiti-verifier","version":"v1","code_digest":"DIGEST","supported_checks":["presence"],"max_bytes":1048576,"timeout_seconds":300,"capability_evidence":{"artifact":{"id":"ARTIFACT_ID","digest":"DIGEST"},"adapter_version":"v1","source_revision":"unqualified","protocol_revision":"v1","profile_digest":"DIGEST","qualified_at":"2026-01-01T00:00:00Z","capabilities":["presence"],"limitations":["self-published evidence; no independent qualification"]}}},"limits":{"currency":"XXX","spend_micro_units":0,"concurrency":1,"model_steps":1,"child_count":0,"delegation_depth":0,"attempt_seconds":60,"root_deadline":"2027-01-01T00:00:00Z"},"dependencies":[]}}'
`

// firstTaskRequirementCode marks the doctor requirement that carries
// FirstTaskSequence.
const firstTaskRequirementCode = "first_task_sequence"
