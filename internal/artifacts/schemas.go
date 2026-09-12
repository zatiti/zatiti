package artifacts

// Operation input/output schema bodies, embedded verbatim from
// internal/artifacts/AGENTS.md. Each body resolves its #/$defs/...
// references against schemaDefs; withDefs (schema_defs.go) composes the
// complete document at registration time so contract.ValidateSchema sees
// document-local defs.
const (
	schemaMetadataIn  = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"artifacts":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096}},"required":["scope","artifacts"]}`
	schemaMetadataOut = `{"type":"object","additionalProperties":false,"properties":{"artifacts":{"type":"array","items":{"$ref":"#/$defs/Artifact"},"maxItems":4096}},"required":["artifacts"]}`

	schemaPublishIn  = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"size":{"type":"integer","minimum":0,"maximum":9223372036854775807},"media_type":{"type":"string","maxLength":8192},"classification":{"type":"string","enum":["internal","public","restricted"]},"encrypted":{"type":"boolean"}},"required":["scope","digest","size","media_type","classification","encrypted"]}`
	schemaPublishOut = `{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Artifact"}},"required":["resource"]}`

	schemaExportIn       = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}`
	schemaExportOut      = `{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Job"}},"required":["resource"]}`
	schemaExportComplete = `{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Artifact"}},"required":["resource"]}`

	schemaGetIn  = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}`
	schemaGetOut = `{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Artifact"}},"required":["resource"]}`

	schemaListIn  = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}`
	schemaListOut = `{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Artifact"},"maxItems":500}},"required":["items"]}`

	schemaReadIn  = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"offset":{"type":"integer","minimum":0,"maximum":9223372036854775807},"length":{"type":"integer","minimum":1,"maximum":1048576}},"required":["scope","id","offset","length"]}`
	schemaReadOut = `{"type":"object","additionalProperties":false,"properties":{"bytes_base64":{"type":"string","maxLength":1398104},"digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"offset":{"type":"integer","minimum":0,"maximum":9223372036854775807},"total_size":{"type":"integer","minimum":0,"maximum":9223372036854775807}},"required":["bytes_base64","digest","offset","total_size"]}`

	schemaUploadBeginIn  = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"size":{"type":"integer","minimum":0,"maximum":9223372036854775807},"digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"media_type":{"type":"string","maxLength":8192},"classification":{"type":"string","enum":["internal","public","restricted"]}},"required":["scope","size","digest","media_type","classification"]}`
	schemaUploadBeginOut = `{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Upload"}},"required":["resource"]}`

	schemaUploadRefIn = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"upload_id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","upload_id","expected_version"]}`

	schemaUploadCancelOut      = `{"oneOf":[{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Disposition"}},"required":["resource"]},{"type":"object","additionalProperties":false,"properties":{"job":{"$ref":"#/$defs/Job"}},"required":["job"]}]}`
	schemaUploadCancelComplete = `{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Disposition"}},"required":["resource"]}`

	schemaUploadChunkIn       = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"upload_id":{"type":"string","format":"uuid"},"offset":{"type":"integer","minimum":0,"maximum":9223372036854775807},"bytes_base64":{"type":"string","maxLength":1398104},"chunk_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"}},"required":["scope","upload_id","offset","bytes_base64","chunk_digest"]}`
	schemaUploadChunkOut      = `{"oneOf":[{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Upload"}},"required":["resource"]},{"type":"object","additionalProperties":false,"properties":{"job":{"$ref":"#/$defs/Job"}},"required":["job"]}]}`
	schemaUploadChunkComplete = `{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Upload"}},"required":["resource"]}`

	schemaUploadFinishOut      = `{"oneOf":[{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Artifact"}},"required":["resource"]},{"type":"object","additionalProperties":false,"properties":{"job":{"$ref":"#/$defs/Job"}},"required":["job"]}]}`
	schemaUploadFinishComplete = `{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Artifact"}},"required":["resource"]}`
)

// opSchemas is one operation's declared input/output bodies.
type opSchemas struct {
	input  string
	output string
}

// operationSchemaBodies holds every operation catalog entry exactly as the
// assignment embeds it. Internal operations precede public ones.
var operationSchemaBodies = map[string]opSchemas{
	opMetadata: {schemaMetadataIn, schemaMetadataOut},
	opPublish:  {schemaPublishIn, schemaPublishOut},

	opArtifactExport: {schemaExportIn, schemaExportOut},
	opArtifactGet:    {schemaGetIn, schemaGetOut},
	opArtifactList:   {schemaListIn, schemaListOut},
	opArtifactRead:   {schemaReadIn, schemaReadOut},
	opUploadBegin:    {schemaUploadBeginIn, schemaUploadBeginOut},
	opUploadCancel:   {schemaUploadRefIn, schemaUploadCancelOut},
	opUploadChunk:    {schemaUploadChunkIn, schemaUploadChunkOut},
	opUploadFinish:   {schemaUploadRefIn, schemaUploadFinishOut},
}

// completionBodies declares the eventual job result schema of every
// asynchronous operation, registered as the descriptor completion_schema.
var completionBodies = map[string]string{
	opArtifactExport: schemaExportComplete,
	opUploadCancel:   schemaUploadCancelComplete,
	opUploadChunk:    schemaUploadChunkComplete,
	opUploadFinish:   schemaUploadFinishComplete,
}
