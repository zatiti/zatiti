package installation

import "fmt"

// FirstTaskSequence is the exact operator sequence from an initialized
// installation to its first task, as CLI commands with UPPER_CASE
// placeholders an operator fills from the previous command's output. It is
// printed by installation.doctor while no budget currency is configured
// (the state every fresh installation is in) and by `zatiti task create
// --help`, and the entrypoint's tests execute it verbatim against a real
// controller so it cannot drift from what the product accepts.
//
// The three facts a person cannot infer from the refusals alone are stated
// here rather than discovered by failing: the verifier a task names must be
// one this installation actually has (`zatiti installation verifier list`,
// never invented), its capability evidence is published as an artifact at
// INSTALLATION scope describing exactly that installed identity's real
// supported checks (not an arbitrary claim); the unconfigured budget
// currency is spelled XXX and admits only a zero-spend task until a budget
// is configured; and the task definition is complete only with every field
// below.
//
// The verifier identity below is not typed twice by hand: it is formatted
// from trustedVerifierProfiles, the same fixed catalog
// installation.verifier.list serves, so the documented sequence and the
// operation it tells the operator to run first can never name different
// verifiers.
var FirstTaskSequence = buildFirstTaskSequence()

func buildFirstTaskSequence() string {
	v := trustedVerifierProfiles[0]
	version := fmt.Sprintf("%d", v.Version)
	return fmt.Sprintf(`# Ids: INSTALLATION_ID is resource.installation_id from 'zatiti init'.
# OWNER_ID is the id of the principal of kind "human"; WORKER_ID is the chief worker's id.
zatiti principal list --json --input '{"scope":{"installation_id":"INSTALLATION_ID"}}'
zatiti worker list --json --input '{"scope":{"installation_id":"INSTALLATION_ID"}}'
# Confirm which trusted verifier this installation actually has before
# naming one: a task's acceptance must reference an installed identity, not
# an invented one. This installation currently reports exactly %[1]s v%[2]s.
zatiti installation verifier list --json --input '{"scope":{"installation_id":"INSTALLATION_ID"}}'
# Publish that installed verifier's capability evidence at INSTALLATION scope
# (a small JSON document describing the %[1]s build's real supported checks --
# never an arbitrary file: its id/version below must match verifier.list above).
# SIZE is its byte count, DIGEST its lowercase sha256 hex, BASE64 its standard base64.
# UPLOAD_ID is resource.id from begin; UPLOAD_VERSION is resource.version from chunk;
# ARTIFACT_ID is resource.id from finish.
zatiti artifact upload begin --json --submission-key evidence-1 --input '{"scope":{"installation_id":"INSTALLATION_ID"},"size":SIZE,"digest":"DIGEST","media_type":"application/json","classification":"internal"}'
zatiti artifact upload chunk --json --submission-key evidence-2 --input '{"scope":{"installation_id":"INSTALLATION_ID"},"upload_id":"UPLOAD_ID","offset":0,"bytes_base64":"BASE64","chunk_digest":"DIGEST"}'
zatiti artifact upload finish --json --submission-key evidence-3 --input '{"scope":{"installation_id":"INSTALLATION_ID"},"upload_id":"UPLOAD_ID","expected_version":UPLOAD_VERSION}'
# Create the first task: zero spend in the unconfigured currency XXX (a positive
# spend, or any other currency, is refused until a budget is configured).
zatiti task create --json --submission-key task-1 --input '{"scope":{"installation_id":"INSTALLATION_ID"},"definition":{"scope":{"installation_id":"INSTALLATION_ID"},"owner_id":"OWNER_ID","worker_id":"WORKER_ID","outcome":"Produce the first report","inputs":[],"required_outputs":["report.bin"],"acceptance":{"verifier_id":"%[1]s","verifier_version":"%[2]s","sealed_inputs":[],"expected_observations":[{"check_id":"report-present","kind":"artifact_presence","expected":"pass","artifact_name":"report.bin","expected_digest":"DIGEST"}],"mode":"independent","required_child_ids":[],"profile":{"schema":"zatiti.verifier-profile/v1","kind":"artifact_contract","id":"%[1]s","version":"%[2]s","code_digest":"DIGEST","supported_checks":["presence"],"max_bytes":1048576,"timeout_seconds":300,"capability_evidence":{"artifact":{"id":"ARTIFACT_ID","digest":"DIGEST"},"adapter_version":"%[2]s","source_revision":"unqualified","protocol_revision":"%[2]s","profile_digest":"DIGEST","qualified_at":"2026-01-01T00:00:00Z","capabilities":["presence"],"limitations":["generic presence/digest/json_schema checks only, matching what installation.verifier.list reports installed; no independent third-party qualification beyond this build"]}}},"limits":{"currency":"XXX","spend_micro_units":0,"concurrency":1,"model_steps":1,"child_count":0,"delegation_depth":0,"attempt_seconds":60,"root_deadline":"2027-01-01T00:00:00Z"},"dependencies":[]}}'
`, v.ID, version)
}

// firstTaskRequirementCode marks the doctor requirement that carries
// FirstTaskSequence.
const firstTaskRequirementCode = "first_task_sequence"
