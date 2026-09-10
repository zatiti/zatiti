package registry

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// descriptorDTO is the public capabilities view of one descriptor,
// matching the frozen $defs/Descriptor schema exactly: id, version, owner,
// input/output schemas, effect, scope requirements, CLI tokens, MCP name,
// submission-key and expected-version semantics. Visibility and mode are
// deliberately absent — the public catalog describes effect, schemas and
// mappings, not routing internals.
type descriptorDTO struct {
	ID               string          `json:"id"`
	Version          int64           `json:"version"`
	Owner            string          `json:"owner"`
	InputSchema      json.RawMessage `json:"input_schema"`
	OutputSchema     json.RawMessage `json:"output_schema"`
	Effect           string          `json:"effect"`
	ScopeRequired    []string        `json:"scope_requirements"`
	CLI              []string        `json:"cli"`
	MCP              string          `json:"mcp"`
	SubmissionKey    bool            `json:"submission_key"`
	ExpectedVersion  bool            `json:"expected_version"`
	CompletionSchema json.RawMessage `json:"completion_schema,omitempty"`
}

// descriptorFromContract maps a registry descriptor to the public DTO.
// Schema and slice fields are copied; empty slices marshal as [] (never
// null) because the frozen Descriptor schema requires arrays.
func descriptorFromContract(d contract.Descriptor) descriptorDTO {
	return descriptorDTO{
		ID:               d.ID,
		Version:          d.Version,
		Owner:            d.Owner,
		InputSchema:      append(json.RawMessage(nil), d.InputSchema...),
		OutputSchema:     append(json.RawMessage(nil), d.OutputSchema...),
		Effect:           d.Effect,
		ScopeRequired:    stringsOrEmpty(d.ScopeRequired),
		CLI:              stringsOrEmpty(d.CLI),
		MCP:              d.MCP,
		SubmissionKey:    d.SubmissionKey,
		ExpectedVersion:  d.ExpectedVersion,
		CompletionSchema: append(json.RawMessage(nil), d.CompletionSchema...),
	}
}

// stringsOrEmpty copies s, preserving an empty (non-nil) result so the
// JSON wire form is [] rather than null.
func stringsOrEmpty(s []string) []string {
	if len(s) == 0 {
		return []string{}
	}
	return append([]string(nil), s...)
}

// registerCapabilities installs the registry-owned capabilities operations
// from the embedded catalog. Dependency-free discovery: the descriptors are
// materialized from the frozen catalog the registry already embeds, so the
// registry never registers itself as a module and never recurses.
func (r *Registry) registerCapabilities() error {
	name := r.Name()
	for i := range r.catalog.document.Operations {
		op := &r.catalog.document.Operations[i]
		if op.Owner != name {
			continue
		}
		d := op.descriptor()
		if localIOOperations[d.ID] {
			return fmt.Errorf("registry: capabilities operation %s cannot be a local IO operation", d.ID)
		}
		mergedInput, err := mergedSchema(r.catalog.defsJSON, d.InputSchema)
		if err != nil {
			return fmt.Errorf("registry: capabilities operation %s: input schema: %w", d.ID, err)
		}
		if err := checkSchemaDocument(mergedInput); err != nil {
			return fmt.Errorf("registry: capabilities operation %s: input schema: %w", d.ID, err)
		}
		mergedOutput, err := mergedSchema(r.catalog.defsJSON, d.OutputSchema)
		if err != nil {
			return fmt.Errorf("registry: capabilities operation %s: output schema: %w", d.ID, err)
		}
		if err := checkSchemaDocument(mergedOutput); err != nil {
			return fmt.Errorf("registry: capabilities operation %s: output schema: %w", d.ID, err)
		}

		var handler contract.Handler
		switch d.ID {
		case "capabilities.list":
			handler = r.capabilitiesList
		case "capabilities.schema":
			handler = r.capabilitiesSchema
		default:
			return fmt.Errorf("registry: catalog operation %s is owned by the registry without a handler", d.ID)
		}
		key := versionKey{d.ID, d.Version}
		if _, dup := r.entries[key]; dup {
			return fmt.Errorf("registry: capabilities operation %s registered more than once", d.ID)
		}
		r.entries[key] = &entry{
			descriptor:   cloneDescriptor(&d),
			mergedInput:  mergedInput,
			mergedOutput: mergedOutput,
			handler:      wrapHandler(d, mergedInput, handler),
			owner:        name,
			self:         true,
		}
		r.registered[d.ID] = true
		r.capabilityDescriptors = append(r.capabilityDescriptors, cloneDescriptor(&d))
	}
	if len(r.capabilityDescriptors) == 0 {
		return fmt.Errorf("registry: embedded catalog holds no registry-owned capabilities operations")
	}
	return nil
}

// capabilitiesList enumerates the complete public operation catalog with
// support versions. Discovery is not execution authorization: the response
// describes the surface, it grants nothing.
func (r *Registry) capabilitiesList(ctx context.Context, unit contract.Unit, invocation contract.Invocation) (contract.Payload, error) {
	var in struct {
		Scope *contract.Scope `json:"scope"`
	}
	if err := contract.DecodeStrict(invocation.Input, &in); err != nil {
		return contract.Payload{}, fault(contract.CodeInvalidInput,
			"operation capabilities.list input rejected: %v", err)
	}
	items := make([]descriptorDTO, 0, len(r.publicList))
	for _, e := range r.publicList {
		items = append(items, descriptorFromContract(e.descriptor))
	}
	return r.capabilityResult("capabilities.list", map[string]any{"items": items})
}

// capabilitiesSchema returns the exact schemas, names, semantics and
// availability prerequisites of one public operation at one exact version.
// Internal operations are never discoverable here.
func (r *Registry) capabilitiesSchema(ctx context.Context, unit contract.Unit, invocation contract.Invocation) (contract.Payload, error) {
	var in struct {
		Operation string `json:"operation"`
		Version   int64  `json:"version"`
	}
	if err := contract.DecodeStrict(invocation.Input, &in); err != nil {
		return contract.Payload{}, fault(contract.CodeInvalidInput,
			"operation capabilities.schema input rejected: %v", err)
	}
	e, ok := r.entries[versionKey{in.Operation, in.Version}]
	if !ok || e.descriptor.Visibility != contract.VisibilityPublic {
		return contract.Payload{}, fault(contract.CodeNotFound,
			"operation %s version %d is not part of the public catalog", in.Operation, in.Version)
	}
	return r.capabilityResult("capabilities.schema", map[string]any{"resource": descriptorFromContract(e.descriptor)})
}

// capabilityResult marshals one capabilities output, validates it against
// the operation's merged output schema (DTO/schema consistency is proven on
// every response, not assumed) and returns the completed payload.
func (r *Registry) capabilityResult(op string, out any) (contract.Payload, error) {
	version := int64(1)
	if frozen, ok := r.catalog.byID[op]; ok {
		version = frozen.Version
	}
	e, ok := r.entries[versionKey{op, version}]
	if !ok {
		return contract.Payload{}, fault(contract.CodeInternalError, "operation %s is not registered", op)
	}
	data, err := json.Marshal(out)
	if err != nil {
		return contract.Payload{}, fault(contract.CodeInternalError,
			"operation %s output could not be marshaled: %v", op, err)
	}
	if data, err = contract.Canonicalize(data); err != nil {
		return contract.Payload{}, fault(contract.CodeInternalError,
			"operation %s output is not canonical JSON: %v", op, err)
	}
	if err := contract.ValidateSchema(e.mergedOutput, data); err != nil {
		return contract.Payload{}, fault(contract.CodeInternalError,
			"operation %s output does not match its declared schema: %v", op, err)
	}
	return contract.Payload{Status: contract.StatusCompleted, Data: data}, nil
}
