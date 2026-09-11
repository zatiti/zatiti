package execution

// Per-operation input and output schema bodies, embedded verbatim from
// internal/execution/AGENTS.md. Operation classes:
//
//	_execution.context               internal mutation
//	_execution.enqueue               internal mutation
//	_execution.fence                 internal mutation
//	_execution.job.claim             internal mutation
//	_execution.job.create            internal mutation
//	_execution.job.pending           internal query
//	_execution.job.record            internal mutation
//	_execution.observation           internal mutation
//	_execution.tick                  internal mutation
//	_execution.verification.record   internal mutation
//	attempt.cancel                   public   mutation
//	attempt.checkpoint               public   mutation
//	attempt.get                      public   query
//	attempt.heartbeat                public   mutation
//	attempt.list                     public   query
//	attempt.recovery                 public   query
//	attempt.report                   public   mutation
//	job.get                          public   query
//	run.cancel                       public   mutation
//	run.claim                        public   mutation
//	run.export                       public   mutation
//	run.get                          public   query
//	run.list                         public   query
//	run.recovery                     public   query
//	worker.pause                     public   mutation
//	worker.resume                    public   mutation
//
// The embedded $defs documents (schema_defs.go) resolve the document-local
// $refs; service.go merges each body onto the wire document at registration
// time so contract.ValidateSchema sees a complete schema.
const (
	schemaInExecutionContext            = `{"type":"object","additionalProperties":false,"properties":{"attempt_id":{"type":"string","format":"uuid"},"context":{"$ref":"#/$defs/Context"}},"required":["attempt_id","context"]}`
	schemaInExecutionEnqueue            = `{"type":"object","additionalProperties":false,"properties":{"task":{"$ref":"#/$defs/Task"}},"required":["task"]}`
	schemaInExecutionFence              = `{"type":"object","additionalProperties":false,"properties":{"generation":{"type":"integer","minimum":1,"maximum":9223372036854775807},"reason":{"type":"string","maxLength":8192}},"required":["generation","reason"]}`
	schemaInExecutionJobClaim           = `{"type":"object","additionalProperties":false,"properties":{"job_id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"generation":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["job_id","expected_version","generation"]}`
	schemaInExecutionJobCreate          = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"owner":{"type":"string","maxLength":8192},"operation":{"type":"string","maxLength":8192},"input":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"source_id":{"type":"string","format":"uuid"}},"required":["scope","owner","operation","input","source_id"]}`
	schemaInExecutionJobPending         = `{"type":"object","additionalProperties":false,"properties":{"limit":{"type":"integer","minimum":1,"maximum":100}},"required":["limit"]}`
	schemaInExecutionJobRecord          = `{"type":"object","additionalProperties":false,"properties":{"job_id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"generation":{"type":"integer","minimum":1,"maximum":9223372036854775807},"state":{"type":"string","enum":["pending","running","succeeded","failed","outcome_unknown","cancelled"]},"result":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"evidence_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096}},"required":["job_id","expected_version","generation","state","result","evidence_ids"]}`
	schemaInExecutionObservation        = `{"type":"object","additionalProperties":false,"properties":{"attempt_id":{"type":"string","format":"uuid"},"operation_id":{"type":"string","format":"uuid"},"observation":{"$ref":"#/$defs/Observation"}},"required":["attempt_id","operation_id","observation"]}`
	schemaInExecutionTick               = `{"type":"object","additionalProperties":false,"properties":{"now":{"type":"string","format":"date-time"},"limit":{"type":"integer","minimum":1,"maximum":100}},"required":["now","limit"]}`
	schemaInExecutionVerificationRecord = `{"type":"object","additionalProperties":false,"properties":{"attempt_id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"result":{"$ref":"#/$defs/Adapter_VerificationResult"}},"required":["attempt_id","expected_version","result"]}`
	schemaInAttemptCancel               = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"reason":{"type":"string","maxLength":8192}},"required":["scope","id","expected_version","reason"]}`
	schemaInAttemptCheckpoint           = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"attempt_id":{"type":"string","format":"uuid"},"lease_id":{"type":"string","format":"uuid"},"generation":{"type":"integer","minimum":1,"maximum":9223372036854775807},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"context":{"$ref":"#/$defs/ArtifactRef"},"outputs":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096}},"required":["scope","attempt_id","lease_id","generation","expected_version","context","outputs"]}`
	schemaInAttemptGet                  = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}`
	schemaInAttemptHeartbeat            = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"attempt_id":{"type":"string","format":"uuid"},"lease_id":{"type":"string","format":"uuid"},"generation":{"type":"integer","minimum":1,"maximum":9223372036854775807},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","attempt_id","lease_id","generation","expected_version"]}`
	schemaInAttemptList                 = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}`
	schemaInAttemptRecovery             = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}`
	schemaInAttemptReport               = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"attempt_id":{"type":"string","format":"uuid"},"lease_id":{"type":"string","format":"uuid"},"generation":{"type":"integer","minimum":1,"maximum":9223372036854775807},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"outputs":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096},"observations":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"usage":{"$ref":"#/$defs/Usage"}},"required":["scope","attempt_id","lease_id","generation","expected_version","outputs","observations","usage"]}`
	schemaInJobGet                      = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}`
	schemaInRunCancel                   = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"reason":{"type":"string","maxLength":8192}},"required":["scope","id","expected_version","reason"]}`
	schemaInRunClaim                    = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"run_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"capabilities":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096}},"required":["scope","run_id","worker_id","expected_version","capabilities"]}`
	schemaInRunExport                   = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}`
	schemaInRunGet                      = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}`
	schemaInRunList                     = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}`
	schemaInRunRecovery                 = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}`
	schemaInWorkerPause                 = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","id","expected_version"]}`
	schemaInWorkerResume                = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","id","expected_version"]}`
	schemaOutAttempt                    = `{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Attempt"}},"required":["resource"]}`
	schemaOutRun                        = `{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Run"}},"required":["resource"]}`
	schemaOutExecutionFence             = `{"type":"object","additionalProperties":false,"properties":{"attempt_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096}},"required":["attempt_ids"]}`
	schemaOutExecutionJobClaim          = `{"type":"object","additionalProperties":false,"properties":{"job":{"$ref":"#/$defs/Job"},"input":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["job","input"]}`
	schemaOutJob                        = `{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Job"}},"required":["resource"]}`
	schemaOutExecutionJobPending        = `{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Job"},"maxItems":500}},"required":["items"]}`
	schemaOutDisposition                = `{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Disposition"}},"required":["resource"]}`
	schemaOutAttemptList                = `{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Attempt"},"maxItems":500}},"required":["items"]}`
	schemaOutAttemptRecovery            = `{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Attempt"},"obligations":{"type":"array","items":{"$ref":"#/$defs/Requirement"},"maxItems":4096}},"required":["resource","obligations"]}`
	schemaOutRunClaim                   = `{"type":"object","additionalProperties":false,"properties":{"attempt":{"$ref":"#/$defs/Attempt"},"task":{"$ref":"#/$defs/Task"},"context":{"$ref":"#/$defs/ArtifactRef"}},"required":["attempt","task","context"]}`
	schemaOutRunList                    = `{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Run"},"maxItems":500}},"required":["items"]}`
	schemaOutRunRecovery                = `{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Run"},"obligations":{"type":"array","items":{"$ref":"#/$defs/Requirement"},"maxItems":4096}},"required":["resource","obligations"]}`
)

// schemaExportResult is the run.export eventual job result schema: the
// registered completion schema for the catalog's run.export job operation
// (job.get resource.result).
const schemaExportResult = `{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Artifact"}},"required":["resource"]}`

// opSchemas holds the input and output schema bodies for every owned
// operation, keyed by operation ID.
var opSchemas = map[string][2]string{
	"_execution.context":             {schemaInExecutionContext, schemaOutAttempt},
	"_execution.enqueue":             {schemaInExecutionEnqueue, schemaOutRun},
	"_execution.fence":               {schemaInExecutionFence, schemaOutExecutionFence},
	"_execution.job.claim":           {schemaInExecutionJobClaim, schemaOutExecutionJobClaim},
	"_execution.job.create":          {schemaInExecutionJobCreate, schemaOutJob},
	"_execution.job.pending":         {schemaInExecutionJobPending, schemaOutExecutionJobPending},
	"_execution.job.record":          {schemaInExecutionJobRecord, schemaOutJob},
	"_execution.observation":         {schemaInExecutionObservation, schemaOutAttempt},
	"_execution.tick":                {schemaInExecutionTick, schemaOutExecutionFence},
	"_execution.verification.record": {schemaInExecutionVerificationRecord, schemaOutAttempt},
	"attempt.cancel":                 {schemaInAttemptCancel, schemaOutDisposition},
	"attempt.checkpoint":             {schemaInAttemptCheckpoint, schemaOutAttempt},
	"attempt.get":                    {schemaInAttemptGet, schemaOutAttempt},
	"attempt.heartbeat":              {schemaInAttemptHeartbeat, schemaOutAttempt},
	"attempt.list":                   {schemaInAttemptList, schemaOutAttemptList},
	"attempt.recovery":               {schemaInAttemptRecovery, schemaOutAttemptRecovery},
	"attempt.report":                 {schemaInAttemptReport, schemaOutAttempt},
	"job.get":                        {schemaInJobGet, schemaOutJob},
	"run.cancel":                     {schemaInRunCancel, schemaOutDisposition},
	"run.claim":                      {schemaInRunClaim, schemaOutRunClaim},
	"run.export":                     {schemaInRunExport, schemaOutJob},
	"run.get":                        {schemaInRunGet, schemaOutRun},
	"run.list":                       {schemaInRunList, schemaOutRunList},
	"run.recovery":                   {schemaInRunRecovery, schemaOutRunRecovery},
	"worker.pause":                   {schemaInWorkerPause, schemaOutDisposition},
	"worker.resume":                  {schemaInWorkerResume, schemaOutDisposition},
}
