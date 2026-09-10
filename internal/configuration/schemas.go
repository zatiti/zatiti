package configuration

import (
	"encoding/json"
	"sync"

	"github.com/zatiti/zatiti/internal/contract"
)

// Operation input/output schema bodies, embedded verbatim from the
// implementation assignment. Each body resolves its #/$defs/... references
// against schemaDefs; withDefs composes the complete document at
// registration time so contract.ValidateSchema sees document-local defs.
//
// Shared reusable bodies.
const (
	schemaCandidateIn = `{"type":"object","additionalProperties":false,"properties":{"candidate":{"$ref":"#/$defs/Candidate"}},"required":["candidate"]}`
	schemaValidateOut = `{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Validation"}},"required":["resource"]}`
	schemaActivateOut = `{"type":"object","additionalProperties":false,"properties":{"versions":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096}},"required":["versions"]}`

	schemaGetIn = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}`

	schemaListIn = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}`

	schemaArchiveIn = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version"]}`

	schemaBindingDef   = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"kind":{"type":"string","enum":["tool","skill","connection","worker","repository","reporting","memory"]},"target_id":{"type":"string","format":"uuid"},"permissions":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"source_scope":{"$ref":"#/$defs/Scope"},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096}},"required":["scope","kind","target_id","permissions"]}`
	schemaProfileDef   = `{"type":"object","additionalProperties":false,"properties":{"executor":{"type":"string","enum":["hosted","cooperative"]},"model":{"type":"string","maxLength":8192},"connection_id":{"type":"string","format":"uuid"},"provider_destination":{"type":"string","maxLength":8192},"capabilities":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"cost_bound":{"$ref":"#/$defs/Money"},"classification":{"type":"string","enum":["internal","public","restricted"]},"context_capture":{"type":"string","enum":["complete","partial","advisory"]}},"required":["executor","model","connection_id","provider_destination","capabilities","cost_bound","classification","context_capture"]}`
	schemaOrgDefCreate = `{"type":"object","additionalProperties":false,"properties":{"key":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"limits":{"$ref":"#/$defs/Limits"},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["key","name"]}`
	schemaOrgDefUpdate = `{"type":"object","additionalProperties":false,"properties":{"key":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"chief_id":{"type":"string","format":"uuid"},"parent_id":{"type":"string","format":"uuid"},"limits":{"$ref":"#/$defs/Limits"},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["key","name","chief_id"]}`
	schemaChiefDef     = `{"type":"object","additionalProperties":false,"properties":{"key":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"purpose":{"type":"string","maxLength":8192},"instructions":{"type":"string","maxLength":8192},"skill_versions":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096},"bindings":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"profile":{"anyOf":[{"$ref":"#/$defs/ExecutionProfile"},{"type":"null"}]},"limits":{"anyOf":[{"$ref":"#/$defs/Limits"},{"type":"null"}]},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["key","name","purpose","instructions","skill_versions","bindings","profile","limits"]}`
	schemaWorkerDef    = `{"type":"object","additionalProperties":false,"properties":{"organization_id":{"type":"string","format":"uuid"},"key":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"purpose":{"type":"string","maxLength":8192},"instructions":{"type":"string","maxLength":8192},"skill_versions":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096},"bindings":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"profile":{"anyOf":[{"$ref":"#/$defs/ExecutionProfile"},{"type":"null"}]},"limits":{"anyOf":[{"$ref":"#/$defs/Limits"},{"type":"null"}]},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["organization_id","key","name","purpose","instructions","skill_versions","bindings","profile","limits"]}`
	schemaTeamDef      = `{"type":"object","additionalProperties":false,"properties":{"organization_id":{"type":"string","format":"uuid"},"key":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"worker_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["organization_id","key","name","worker_ids"]}`
	schemaProjectDef   = `{"type":"object","additionalProperties":false,"properties":{"organization_id":{"type":"string","format":"uuid"},"key":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"repositories":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"bindings":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"classification":{"type":"string","enum":["internal","public","restricted"]},"limits":{"$ref":"#/$defs/Limits"},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["organization_id","key","name","repositories","bindings","classification"]}`

	schemaJobOut = `{"type":"object","additionalProperties":false,"properties":{"job":{"$ref":"#/$defs/Job"}},"required":["job"]}`

	schemaImportIn  = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"artifact":{"$ref":"#/$defs/ArtifactRef"},"rebindings":{"type":"array","items":{"type":"object","additionalProperties":false,"properties":{"source_ref":{"type":"string","maxLength":8192},"destination_ref":{"type":"string","maxLength":8192}},"required":["source_ref","destination_ref"]},"maxItems":4096},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","artifact","rebindings"]}`
	schemaImportOut = `{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"diagnostics":{"type":"array","items":{"$ref":"#/$defs/Diagnostic"},"maxItems":4096}},"required":["draft","diagnostics"]}`

	schemaStageIn      = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"change":{"$ref":"#/$defs/Change"},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","change"]}`
	schemaSnapshotIn   = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"}},"required":["scope"]}`
	schemaSnapshotOut  = `{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/ScopeSnapshot"}},"required":["resource"]}`
	schemaBootstrapIn  = `{"type":"object","additionalProperties":false,"properties":{"installation_id":{"type":"string","format":"uuid"},"owner_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"chief_id":{"type":"string","format":"uuid"}},"required":["installation_id","owner_id","organization_id","chief_id"]}`
	schemaBootstrapOut = `{"type":"object","additionalProperties":false,"properties":{"organization":{"$ref":"#/$defs/Organization"},"chief":{"$ref":"#/$defs/Worker"}},"required":["organization","chief"]}`

	schemaApplyIn       = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"plan_id":{"type":"string","format":"uuid"},"base_revision":{"type":"integer","minimum":1,"maximum":9223372036854775807},"candidate_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"}},"required":["scope","plan_id","base_revision","candidate_digest"]}`
	schemaDraftCreateIn = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"base_revision":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","base_revision"]}`
	schemaDiscardIn     = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","id","expected_version"]}`
	schemaDraftUpdateIn = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"changes":{"type":"array","items":{"$ref":"#/$defs/Change"},"maxItems":4096}},"required":["scope","id","expected_version","changes"]}`
	schemaPlanIn        = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"draft_id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","draft_id","expected_version"]}`
	schemaRollbackIn    = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"revision_id":{"type":"string","format":"uuid"},"base_revision":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","revision_id","base_revision"]}`
)

// schema builders compose parameterized bodies over the shared defs. They are
// package-level vars (not consts) because composition needs function calls;
// the results feed operationSchemaBodies at init time.
var (
	schemaGetOut = func(resource string) string {
		return `{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/` + resource + `"}},"required":["resource"]}`
	}
	schemaListOut = func(resource string) string {
		return `{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/` + resource + `"},"maxItems":500}},"required":["items"]}`
	}
	schemaCreateIn = func(def string) string {
		return `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"definition":` + def + `,"draft_id":{"type":"string","format":"uuid"}},"required":["scope","definition"]}`
	}
	schemaCreateOut = func(resource string) string {
		return `{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/` + resource + `"}},"required":["draft","resource"]}`
	}
	schemaMoveIn = func(field string) string {
		return `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"` + field + `":{"type":"string","format":"uuid"},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version","` + field + `"]}`
	}
	schemaDraftOut = func(field string) string {
		return `{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"}},"required":["draft"]}`
	}
)

// opSchema is one operation's declared input/output bodies.
type opSchemas struct {
	input  string
	output string
}

// operationSchemaBodies holds every operation catalog entry exactly as the
// assignment embeds it. Internal operations precede public ones.
var operationSchemaBodies = map[string]opSchemas{
	// Internal operations.
	"_accounting.validate":     {schemaCandidateIn, schemaValidateOut},
	"_configuration.activate":  {schemaCandidateIn, schemaActivateOut},
	"_configuration.bootstrap": {schemaBootstrapIn, schemaBootstrapOut},
	"_configuration.snapshot":  {schemaSnapshotIn, schemaSnapshotOut},
	"_configuration.stage":     {schemaStageIn, schemaGetOut("Draft")},
	"_configuration.validate":  {schemaCandidateIn, schemaValidateOut},

	"binding.archive": {schemaArchiveIn, schemaCreateOut("Binding")},
	"binding.create":  {schemaCreateIn(schemaBindingDef), schemaCreateOut("Binding")},
	"binding.get":     {schemaGetIn, schemaGetOut("Binding")},
	"binding.list":    {schemaListIn, schemaListOut("Binding")},
	"binding.update":  {`{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"definition":` + schemaBindingDef + `,"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version","definition"]}`, schemaCreateOut("Binding")},

	"configuration.apply":         {schemaApplyIn, schemaGetOut("Revision")},
	"configuration.draft.create":  {schemaDraftCreateIn, schemaGetOut("Draft")},
	"configuration.draft.discard": {schemaDiscardIn, schemaGetOut("Disposition")},
	"configuration.draft.get":     {schemaGetIn, schemaGetOut("Draft")},
	"configuration.draft.list":    {schemaListIn, schemaListOut("Draft")},
	"configuration.draft.update":  {schemaDraftUpdateIn, schemaGetOut("Draft")},
	"configuration.plan":          {schemaPlanIn, schemaGetOut("Plan")},
	"configuration.plan.get":      {schemaGetIn, schemaGetOut("Plan")},
	"configuration.plan.list":     {schemaListIn, schemaListOut("Plan")},
	"configuration.revision.get":  {schemaGetIn, schemaGetOut("Revision")},
	"configuration.revision.list": {schemaListIn, schemaListOut("Revision")},
	"configuration.rollback.plan": {`{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"revision_id":{"type":"string","format":"uuid"},"base_revision":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","revision_id","base_revision"]}`, schemaGetOut("Plan")},

	"execution_profile.archive": {schemaArchiveIn, schemaCreateOut("ExecutionProfile")},
	"execution_profile.create":  {schemaCreateIn(schemaProfileDef), schemaCreateOut("ExecutionProfile")},
	"execution_profile.get":     {schemaGetIn, schemaGetOut("ExecutionProfile")},
	"execution_profile.list":    {schemaListIn, schemaListOut("ExecutionProfile")},
	"execution_profile.update":  {`{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"definition":` + schemaProfileDef + `,"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version","definition"]}`, schemaCreateOut("ExecutionProfile")},

	"organization.archive":       {schemaArchiveIn, schemaCreateOut("Organization")},
	"organization.chief.replace": {schemaMoveIn("chief_id"), schemaDraftOut("")},
	"organization.create":        {`{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"definition":` + schemaOrgDefCreate + `,"draft_id":{"type":"string","format":"uuid"},"chief":` + schemaChiefDef + `},"required":["scope","definition","chief"]}`, schemaCreateOut("Organization")},
	"organization.export":        {schemaGetIn, schemaJobOut},
	"organization.get":           {schemaGetIn, schemaGetOut("Organization")},
	"organization.import":        {schemaImportIn, schemaImportOut},
	"organization.list":          {schemaListIn, schemaListOut("Organization")},
	"organization.move":          {schemaMoveIn("parent_id"), schemaDraftOut("")},
	"organization.update":        {`{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"definition":` + schemaOrgDefUpdate + `,"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version","definition"]}`, schemaCreateOut("Organization")},

	"project.archive": {schemaArchiveIn, schemaCreateOut("Project")},
	"project.create":  {schemaCreateIn(schemaProjectDef), schemaCreateOut("Project")},
	"project.export":  {schemaGetIn, schemaJobOut},
	"project.get":     {schemaGetIn, schemaGetOut("Project")},
	"project.import":  {schemaImportIn, schemaImportOut},
	"project.list":    {schemaListIn, schemaListOut("Project")},
	"project.update":  {`{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"definition":` + schemaProjectDef + `,"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version","definition"]}`, schemaCreateOut("Project")},

	"team.archive": {schemaArchiveIn, schemaCreateOut("Team")},
	"team.create":  {schemaCreateIn(schemaTeamDef), schemaCreateOut("Team")},
	"team.export":  {schemaGetIn, schemaJobOut},
	"team.get":     {schemaGetIn, schemaGetOut("Team")},
	"team.import":  {schemaImportIn, schemaImportOut},
	"team.list":    {schemaListIn, schemaListOut("Team")},
	"team.update":  {`{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"definition":` + schemaTeamDef + `,"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version","definition"]}`, schemaCreateOut("Team")},

	"worker.archive": {schemaArchiveIn, schemaCreateOut("Worker")},
	"worker.create":  {schemaCreateIn(schemaWorkerDef), schemaCreateOut("Worker")},
	"worker.get":     {schemaGetIn, schemaGetOut("Worker")},
	"worker.list":    {schemaListIn, schemaListOut("Worker")},
	"worker.move":    {schemaMoveIn("organization_id"), schemaDraftOut("")},
	"worker.update":  {`{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"definition":` + schemaWorkerDef + `,"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version","definition"]}`, schemaCreateOut("Worker")},
}

// completionExportResult is the eventual job result schema declared by every
// export operation (asynchronous job result artifact).
const completionExportResult = `{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Artifact"}},"required":["resource"]}`

// composedSchemas lazily composes every operation schema document with the
// shared $defs catalog. Composition happens once; documents are immutable.
var (
	composeOnce        sync.Once
	composedIn         map[string]json.RawMessage
	composedOut        map[string]json.RawMessage
	composedCompletion map[string]json.RawMessage
)

func composeSchemas() {
	composeOnce.Do(func() {
		// The catalog is spliced verbatim: decoding it into any would
		// round-trip every number through float64 and corrupt integer
		// boundaries such as the int64 maximum on configuration_revision.
		var doc map[string]json.RawMessage
		if err := contract.DecodeStrict([]byte(schemaDefs), &doc); err != nil {
			panic("configuration: embedded $defs catalog is not valid JSON: " + err.Error())
		}
		defs, ok := doc["$defs"]
		if !ok {
			panic("configuration: embedded $defs catalog has no $defs object")
		}
		composedIn = make(map[string]json.RawMessage, len(operationSchemaBodies))
		composedOut = make(map[string]json.RawMessage, len(operationSchemaBodies))
		for id, bodies := range operationSchemaBodies {
			composedIn[id] = withDefs(bodies.input, defs)
			composedOut[id] = withDefs(bodies.output, defs)
		}
		composedCompletion = make(map[string]json.RawMessage, len(completionOps))
		for _, id := range completionOps {
			composedCompletion[id] = withDefs(completionExportResult, defs)
		}
	})
}

// completionOps lists the asynchronous operations whose declared
// completion_schema is the eventual export artifact result.
var completionOps = []string{
	"organization.export",
	"project.export",
	"team.export",
}

// withDefs returns body with the $defs catalog injected at the document
// root, so every #/$defs/... reference resolves document-locally. The
// catalog rides as raw JSON: encoding/json splices json.RawMessage values
// byte-for-byte, so schema integers survive composition exactly.
func withDefs(body string, defs json.RawMessage) json.RawMessage {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		panic("configuration: operation schema is not valid JSON: " + err.Error())
	}
	if _, ok := doc["$defs"]; ok {
		panic("configuration: operation schema already declares $defs: " + body)
	}
	doc["$defs"] = defs
	out, err := json.Marshal(doc)
	if err != nil {
		panic("configuration: schema composition failed: " + err.Error())
	}
	return json.RawMessage(out)
}

// inputSchema returns the composed input schema document for op.
func inputSchema(op string) json.RawMessage {
	composeSchemas()
	return composedIn[op]
}

// outputSchema returns the composed output schema document for op.
func outputSchema(op string) json.RawMessage {
	composeSchemas()
	return composedOut[op]
}

// completionSchema returns the composed eventual result schema for an
// asynchronous operation, or nil.
func completionSchema(op string) json.RawMessage {
	composeSchemas()
	return composedCompletion[op]
}
