package connections

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// MCPProfile is a secret-free admission snapshot of the exact local adapter
// profile. Only startup assembly supplies it; operation input cannot install it.
type MCPProfile struct {
	Digest         contract.Digest
	Endpoint       string
	CredentialKind string
	Cost           wireMoney
	ControlCost    wireMoney
	ControlLimit   int64
	TimeoutSeconds int64
	AllowedTools   []string
}

// NewWithMCPProfile binds the same immutable startup bytes used to construct
// the adapter. An absent profile leaves MCP admission unavailable.
func NewWithMCPProfile(deps contract.Dependencies, raw json.RawMessage) (*Service, error) {
	s, err := New(deps)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return s, nil
	}
	if err := contract.ValidateSchema(withDefsMCPProfile(), raw); err != nil {
		return nil, fmt.Errorf("connections: invalid MCP profile: %w", err)
	}
	var p struct {
		Transport struct {
			Kind     string `json:"kind"`
			Endpoint string `json:"endpoint"`
		} `json:"transport"`
		CredentialKind     string    `json:"credential_kind"`
		ToolCallCost       wireMoney `json:"tool_call_cost"`
		ControlReplyCost   wireMoney `json:"control_reply_cost"`
		MaxControlReplies  int64     `json:"max_control_replies"`
		TimeoutSeconds     int64     `json:"timeout_seconds"`
		AllowedTools       []string  `json:"allowed_tools"`
		CapabilityEvidence struct {
			ProfileDigest contract.Digest `json:"profile_digest"`
		} `json:"capability_evidence"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	if p.Transport.Kind != "streamable_http" {
		return nil, capabilityUnsupportedFault("MCP admission requires streamable HTTPS")
	}
	if p.MaxControlReplies > 0 && (p.ControlReplyCost.Currency != p.ToolCallCost.Currency || p.ControlReplyCost.MicroUnits > (math.MaxInt64-p.ToolCallCost.MicroUnits)/p.MaxControlReplies) {
		return nil, invalidInput("MCP control cost is invalid or overflows")
	}
	var unsigned map[string]json.RawMessage
	if err := json.Unmarshal(raw, &unsigned); err != nil {
		return nil, err
	}
	delete(unsigned, "capability_evidence")
	stripped, err := json.Marshal(unsigned)
	if err != nil {
		return nil, err
	}
	canonical, err := contract.Canonicalize(stripped)
	if err != nil {
		return nil, err
	}
	if p.CapabilityEvidence.ProfileDigest != contract.Hash(canonical) {
		return nil, invalidInput("MCP capability evidence does not bind the installed profile")
	}
	s.mcpProfile = &MCPProfile{Digest: contract.Hash(canonical), Endpoint: p.Transport.Endpoint, CredentialKind: p.CredentialKind, Cost: p.ToolCallCost, ControlCost: p.ControlReplyCost, ControlLimit: p.MaxControlReplies, TimeoutSeconds: p.TimeoutSeconds, AllowedTools: p.AllowedTools}
	return s, nil
}

func (p *MCPProfile) bound() wireMoney {
	return wireMoney{Currency: p.Cost.Currency, MicroUnits: p.Cost.MicroUnits + p.ControlLimit*p.ControlCost.MicroUnits}
}

// governedMCPAction is the exact immutable Action passed to effects.
type governedMCPAction struct {
	Scope                 wireScope         `json:"scope"`
	Tool                  wireRef           `json:"tool"`
	Connection            wireRef           `json:"connection"`
	AccountIdentity       string            `json:"account_identity"`
	Destination           string            `json:"destination"`
	Content               []wireArtifactRef `json:"content"`
	NotBefore             time.Time         `json:"not_before"`
	ExpiresAt             time.Time         `json:"expires_at"`
	Preconditions         json.RawMessage   `json:"preconditions"`
	ConfigurationRevision int64             `json:"configuration_revision"`
	Parameters            json.RawMessage   `json:"parameters"`
	CostBound             wireMoney         `json:"cost_bound"`
}

func (s *Service) mcpConnection(row connectionRow) error {
	if row.Provider != "mcp" {
		return capabilityUnsupportedFault("connection is not an MCP provider")
	}
	p := s.mcpProfile
	if p == nil {
		return prerequisiteMissing("MCP admission profile is not installed")
	}
	if !contains(row.Destinations, p.Endpoint) {
		return permissionDenied("connection does not bind the installed MCP endpoint")
	}
	account := "none"
	if p.CredentialKind == "bearer" {
		if row.CredentialRef == "" {
			return prerequisiteMissing("MCP bearer connection has no credential reference")
		}
		account = "credential:" + row.CredentialRef
	} else if row.CredentialRef != "" {
		return permissionDenied("credential-free MCP profile cannot use a credential")
	}
	if row.AccountIdentity != account {
		return permissionDenied("generic MCP validates only its exact credential binding, not an upstream account identity")
	}
	return nil
}

// createMCPProbe prepares the action, linked durable job and owner intent in
// one transaction. Effects admission happens only after this transaction.
func (s *Service) createMCPProbe(ctx context.Context, u contract.Unit, row connectionRow, tool contractRow, kind string, params json.RawMessage) (wireJob, error) {
	if err := s.mcpConnection(row); err != nil {
		return wireJob{}, err
	}
	if _, found, err := s.loadPendingProbe(ctx, u, row.ID); err != nil {
		return wireJob{}, err
	} else if found {
		return wireJob{}, conflictFault("connection already has an outstanding probe")
	}
	var snapshot struct {
		Resource struct {
			Revision int64 `json:"revision"`
		} `json:"resource"`
	}
	input, _ := json.Marshal(map[string]any{"scope": row.Scope})
	payload, err := s.ports.Call(ctx, u, contract.Invocation{Operation: "_configuration.snapshot", Version: 1, Input: input})
	if err != nil {
		return wireJob{}, err
	}
	if err = json.Unmarshal(payload.Data, &snapshot); err != nil {
		return wireJob{}, err
	}
	if snapshot.Resource.Revision < 1 {
		return wireJob{}, prerequisiteMissing("configuration revision unavailable")
	}
	var par map[string]any
	if err = json.Unmarshal(params, &par); err != nil {
		return wireJob{}, err
	}
	par["control_reply_limit"] = s.mcpProfile.ControlLimit
	par["profile_digest"] = s.mcpProfile.Digest
	params, err = json.Marshal(par)
	if err != nil {
		return wireJob{}, err
	}
	now := s.clock.Now()
	action := governedMCPAction{Scope: scopeFromContract(row.Scope), Tool: wireRef{ID: tool.ID, Version: tool.Version}, Connection: wireRef{ID: row.ID, Version: row.Version}, AccountIdentity: row.AccountIdentity, Destination: s.mcpProfile.Endpoint, Content: []wireArtifactRef{}, NotBefore: now, ExpiresAt: now.Add(time.Duration(s.mcpProfile.TimeoutSeconds) * time.Second), Preconditions: json.RawMessage(`{}`), ConfigurationRevision: snapshot.Resource.Revision, Parameters: params, CostBound: s.mcpProfile.bound()}
	actionRaw, err := json.Marshal(action)
	if err != nil {
		return wireJob{}, err
	}
	source := s.ids.New()
	input, err = json.Marshal(map[string]any{"scope": action.Scope, "action": action, "source_id": source, "callback_route": map[string]any{"kind": "connection"}})
	if err != nil {
		return wireJob{}, err
	}
	payload, err = s.ports.Call(ctx, u, contract.Invocation{Operation: "_effects.prepare", Version: 1, Input: input})
	if err != nil {
		return wireJob{}, err
	}
	var prepared struct {
		Resource struct {
			ID contract.ID `json:"id"`
		} `json:"resource"`
	}
	if err = json.Unmarshal(payload.Data, &prepared); err != nil {
		return wireJob{}, err
	}
	if prepared.Resource.ID == "" {
		return wireJob{}, internalError("effects returned no operation identity")
	}
	input, err = json.Marshal(map[string]any{"scope": action.Scope, "connection_id": row.ID, "expected_version": row.Version, "tool": action.Tool, "action": params, "operation_id": prepared.Resource.ID})
	if err != nil {
		return wireJob{}, err
	}
	job, err := s.createLinkedJob(ctx, u, "connection."+kind, input, prepared.Resource.ID)
	if err != nil {
		return wireJob{}, err
	}
	canonical, err := contract.Canonicalize(actionRaw)
	if err != nil {
		return wireJob{}, err
	}
	_, err = u.ExecContext(ctx, `INSERT INTO connections_mcp_intents(operation_id,connection_id,connection_version,action_digest,kind,job_id) VALUES(?,?,?,?,?,?)`, prepared.Resource.ID, row.ID, row.Version, contract.Hash(canonical), kind, job.ID)
	if err != nil {
		return wireJob{}, fmt.Errorf("record MCP intent: %w", err)
	}
	if err = s.recordPendingProbe(ctx, u, pendingProbeRow{ConnectionID: row.ID, JobID: job.ID, JobVersion: job.Version, Kind: kind}, now); err != nil {
		return wireJob{}, err
	}
	return job, nil
}

// matchingMCPIntent verifies authority from owner-created durable state, never
// from a caller's purpose or a built-in tool identity alone.
func (s *Service) matchingMCPIntent(ctx context.Context, u contract.Unit, row connectionRow, operation contract.ID, action json.RawMessage, kind string) (bool, error) {
	if operation == "" || len(action) == 0 {
		return false, nil
	}
	var version int64
	var digest, storedKind string
	var job contract.ID
	err := u.QueryRowContext(ctx, `SELECT connection_version,action_digest,kind,job_id FROM connections_mcp_intents WHERE operation_id=? AND connection_id=?`, operation, row.ID).Scan(&version, &digest, &storedKind, &job)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	pending, found, err := s.loadPendingProbe(ctx, u, row.ID)
	if err != nil {
		return false, err
	}
	canonical, err := contract.Canonicalize(action)
	if err != nil {
		return false, err
	}
	return found && pending.JobID == job && pending.Kind == kind && storedKind == kind && version == row.Version && digest == string(contract.Hash(canonical)), nil
}

func (s *Service) activeMCPSession(ctx context.Context, u contract.Unit, row connectionRow) (string, error) {
	if err := s.mcpConnection(row); err != nil {
		return "", err
	}
	var handle string
	err := u.QueryRowContext(ctx, `SELECT session_handle FROM connections_mcp_sessions WHERE connection_id=? AND connection_version=? AND profile_digest=? AND generation=?`, row.ID, row.Version, s.mcpProfile.Digest, u.Generation()).Scan(&handle)
	if errors.Is(err, sql.ErrNoRows) {
		return "", prerequisiteMissing("MCP session requires fresh validation in this controller generation")
	}
	if err != nil {
		return "", err
	}
	return handle, nil
}

func (s *Service) composeMCPTool(ctx context.Context, u contract.Unit, probe contractRow, d mcpToolRow, row connectionRow) (wireTool, error) {
	handle, err := s.activeMCPSession(ctx, u, row)
	if err != nil {
		return wireTool{}, err
	}
	if !contains(s.mcpProfile.AllowedTools, d.Name) {
		return wireTool{}, permissionDenied("MCP tool is absent from installed profile allowlist")
	}
	var arguments any
	if err = json.Unmarshal(d.InputSchema, &arguments); err != nil {
		return wireTool{}, err
	}
	if err = relocateMCPSchema(arguments); err != nil {
		return wireTool{}, err
	}
	var original any
	if err = json.Unmarshal(d.InputSchema, &original); err != nil {
		return wireTool{}, err
	}
	constants := map[string]any{"schema": "zatiti.mcp.action/v1", "kind": "call_tool", "session_handle": handle, "tool": d.Name, "input_schema": original, "input_schema_digest": d.InputSchemaDigest, "classification": "restricted", "profile_digest": s.mcpProfile.Digest, "control_reply_limit": s.mcpProfile.ControlLimit}
	props := map[string]any{"arguments": arguments}
	required := []string{"arguments"}
	for _, k := range []string{"schema", "kind", "session_handle", "tool", "input_schema", "input_schema_digest", "classification", "profile_digest", "control_reply_limit"} {
		props[k] = map[string]any{"const": constants[k]}
		required = append(required, k)
	}
	schema, err := json.Marshal(map[string]any{"type": "object", "additionalProperties": false, "properties": props, "required": required})
	if err != nil {
		return wireTool{}, err
	}
	return wireTool{ID: mcpDiscoveredToolID(d.ConnectionID, d.Name), Version: d.Version, Name: d.Name, InputSchema: schema, OutputSchema: json.RawMessage(`{}`), Effect: "external_mutation", Destinations: []string{s.mcpProfile.Endpoint}, CredentialKind: probe.CredentialKind, CostBound: s.mcpProfile.bound(), TimeoutSeconds: s.mcpProfile.TimeoutSeconds, Idempotency: "none", Confirmation: "synchronous", Reconciliation: "none", Adapter: "mcp"}, nil
}

// Relocate local JSON pointers into the composed arguments subschema. Named
// anchors/resource IDs are refused instead of acquiring a new resolution base.
func relocateMCPSchema(v any) error {
	switch x := v.(type) {
	case map[string]any:
		for k, value := range x {
			if k == "$id" || k == "$anchor" || k == "$dynamicRef" || k == "$dynamicAnchor" {
				return capabilityUnsupportedFault("MCP schemas with resource IDs or anchors cannot be composed")
			}
			if k == "$ref" {
				r, ok := value.(string)
				if !ok || (r != "#" && !strings.HasPrefix(r, "#/")) {
					return capabilityUnsupportedFault("MCP schema references must be local JSON pointers")
				}
				x[k] = "#/properties/arguments" + strings.TrimPrefix(r, "#")
				continue
			}
			if err := relocateMCPSchema(value); err != nil {
				return err
			}
		}
	case []any:
		for _, value := range x {
			if err := relocateMCPSchema(value); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) checkMCPAction(ctx context.Context, u contract.Unit, row connectionRow, raw json.RawMessage, tool wireTool, operation contract.ID, validation bool) error {
	var a governedMCPAction
	if err := contract.DecodeStrict(raw, &a); err != nil {
		return invalidInput("invalid governed MCP action: %v", err)
	}
	if a.Connection.ID != row.ID || a.Connection.Version != row.Version || a.Tool.ID != tool.ID || a.Tool.Version != tool.Version || a.AccountIdentity != row.AccountIdentity || a.Destination != s.mcpProfile.Endpoint {
		return permissionDenied("MCP action does not match resolved account, endpoint or versions")
	}
	if a.CostBound != s.mcpProfile.bound() {
		return permissionDenied("MCP action must reserve the complete profile and control-reply bound")
	}
	var p struct {
		Kind          string          `json:"kind"`
		ProfileDigest contract.Digest `json:"profile_digest"`
		ControlLimit  int64           `json:"control_reply_limit"`
		SessionHandle string          `json:"session_handle"`
	}
	if err := json.Unmarshal(a.Parameters, &p); err != nil {
		return err
	}
	if p.ProfileDigest != s.mcpProfile.Digest || p.ControlLimit != s.mcpProfile.ControlLimit {
		return staleVersion("MCP action profile pin changed")
	}
	if tool.Name == toolNameMCPProbe {
		kind := "discover"
		if validation {
			kind = "validate"
		}
		ok, err := s.matchingMCPIntent(ctx, u, row, operation, raw, kind)
		if err != nil {
			return err
		}
		if !ok {
			return permissionDenied("MCP probes require their exact owner-created intent")
		}
		expected := "list_tools"
		if validation {
			expected = "open_session"
		}
		if p.Kind != expected {
			return permissionDenied("MCP probe action kind differs from intent")
		}
		// Profile pins are validated above; the built-in action schema handles
		// remaining protocol fields, excluding the admission-only digest.
		var params map[string]json.RawMessage
		if err = json.Unmarshal(a.Parameters, &params); err != nil {
			return err
		}
		delete(params, "profile_digest")
		delete(params, "control_reply_limit")
		stripped, err := json.Marshal(params)
		if err != nil {
			return err
		}
		if err = contract.ValidateSchema(tool.InputSchema, stripped); err != nil {
			return invalidInput("MCP probe parameters rejected: %v", err)
		}
		if !validation {
			handle, err := s.activeMCPSession(ctx, u, row)
			if err != nil {
				return err
			}
			if handle != p.SessionHandle {
				return staleVersion("MCP discovery session changed")
			}
		}
	} else {
		if err := s.requireMCPBindings(ctx, u, a); err != nil {
			return err
		}
		if err := contract.ValidateSchema(tool.InputSchema, a.Parameters); err != nil {
			return invalidInput("MCP pinned tool parameters rejected: %v", err)
		}
	}
	return nil
}

func handleMCPToolResolve(ctx context.Context, s *Service, u contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	var in struct {
		Scope  wireScope   `json:"scope"`
		ToolID contract.ID `json:"tool_id"`
	}
	if err := contract.DecodeStrict(inv.Input, &in); err != nil {
		return contract.Payload{}, invalidInput("invalid tool lookup")
	}
	d, found, err := s.findAnyMCPToolByID(ctx, u, in.ToolID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("not a discovered MCP tool")
	}
	row, err := s.loadConnectionChecked(ctx, u, d.ConnectionID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !scopeCovers(in.Scope.toContract(), row.Scope) {
		return contract.Payload{}, notFound("MCP tool is outside scope")
	}
	if fault := refuseInactive(row); fault != nil {
		return contract.Payload{}, fault
	}
	if row.ValidationState != connStateValid || row.ValidUntil == nil || !row.ValidUntil.After(s.clock.Now()) {
		return contract.Payload{}, prerequisiteMissing("MCP tool connection validation is stale")
	}
	probe, found, err := s.loadContractByName(ctx, u, toolNameMCPProbe)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("MCP probe missing")
	}
	tool, err := s.composeMCPTool(ctx, u, probe, d, row)
	if err != nil {
		return contract.Payload{}, err
	}
	return s.completed(struct {
		Connection wireConnection `json:"connection"`
		Tool       wireTool       `json:"tool"`
	}{row.wire(), tool})
}

// verifiedMCPCallback reads the actual recorded effects observation. The
// delivery input supplies routing IDs, never the truth used for validation.
func (s *Service) verifiedMCPCallback(ctx context.Context, u contract.Unit, row connectionRow, operation, attempt contract.ID, kind string, delivered wireObservation) (wireObservation, bool, error) {
	var connection contract.ID
	var version int64
	var storedKind, digest string
	var completed string
	err := u.QueryRowContext(ctx, `SELECT connection_id,connection_version,kind,action_digest,completed_attempt FROM connections_mcp_intents WHERE operation_id=?`, operation).Scan(&connection, &version, &storedKind, &digest, &completed)
	if errors.Is(err, sql.ErrNoRows) {
		return wireObservation{}, false, permissionDenied("callback has no owner-created MCP intent")
	}
	if err != nil {
		return wireObservation{}, false, err
	}
	if connection != row.ID || storedKind != kind || attempt == "" {
		return wireObservation{}, false, permissionDenied("callback differs from MCP intent")
	}
	if completed != "" {
		if completed == string(attempt) {
			return wireObservation{}, true, nil
		}
		return wireObservation{}, false, conflictFault("MCP intent completed by another attempt")
	}
	if version != row.Version {
		return wireObservation{}, false, staleVersion("MCP callback connection version changed")
	}
	input, err := json.Marshal(map[string]any{"operation_id": operation, "attempt_id": attempt})
	if err != nil {
		return wireObservation{}, false, err
	}
	payload, err := s.ports.Call(ctx, u, contract.Invocation{Operation: "_effects.callback.evidence", Version: 1, Input: input})
	if err != nil {
		return wireObservation{}, false, err
	}
	var out struct {
		Action      json.RawMessage `json:"action"`
		Observation wireObservation `json:"observation"`
		Generation  int64           `json:"generation"`
	}
	if err = json.Unmarshal(payload.Data, &out); err != nil {
		return wireObservation{}, false, err
	}
	canon, err := contract.Canonicalize(out.Action)
	if err != nil {
		return wireObservation{}, false, err
	}
	if string(contract.Hash(canon)) != digest {
		return wireObservation{}, false, permissionDenied("recorded action differs from MCP intent")
	}
	var recordedAction governedMCPAction
	if err = json.Unmarshal(out.Action, &recordedAction); err != nil {
		return wireObservation{}, false, err
	}
	out.Observation.Evidence, err = s.normalizeMCPPublication(ctx, u, recordedAction.Scope, out.Observation.Evidence, delivered.Evidence)
	if err != nil {
		return wireObservation{}, false, err
	}
	var ev struct {
		Kind          string `json:"kind"`
		SessionHandle string `json:"session_handle"`
		Physical      struct {
			OperationID contract.ID     `json:"operation_id"`
			AttemptID   contract.ID     `json:"attempt_id"`
			Account     string          `json:"account_identity"`
			Profile     contract.Digest `json:"profile_digest"`
			Destination string          `json:"requested_destination"`
		} `json:"physical_call"`
	}
	if err = json.Unmarshal(out.Observation.Evidence, &ev); err != nil {
		return wireObservation{}, false, err
	}
	expected := "list_tools"
	if kind == "validate" {
		expected = "open_session"
	}
	if ev.Kind != expected || ev.Physical.OperationID != operation || ev.Physical.AttemptID != attempt {
		return wireObservation{}, false, permissionDenied("recorded callback provenance mismatch")
	}
	if err = s.mcpConnection(row); err != nil {
		return wireObservation{}, false, err
	}
	if ev.Physical.Account != row.AccountIdentity || ev.Physical.Profile != s.mcpProfile.Digest || ev.Physical.Destination != s.mcpProfile.Endpoint {
		return wireObservation{}, false, permissionDenied("recorded MCP account/profile/endpoint mismatch")
	}
	if out.Observation.Disposition == obsSucceeded && kind == "validate" {
		if ev.SessionHandle == "" || out.Generation != int64(u.Generation()) {
			return wireObservation{}, false, prerequisiteMissing("validated MCP session is unavailable in current generation")
		}
		_, err = u.ExecContext(ctx, `INSERT INTO connections_mcp_sessions(connection_id,connection_version,profile_digest,generation,session_handle) VALUES(?,?,?,?,?) ON CONFLICT(connection_id) DO UPDATE SET connection_version=excluded.connection_version,profile_digest=excluded.profile_digest,generation=excluded.generation,session_handle=excluded.session_handle`, row.ID, row.Version+1, s.mcpProfile.Digest, u.Generation(), ev.SessionHandle)
		if err != nil {
			return wireObservation{}, false, err
		}
		_, err = u.ExecContext(ctx, `UPDATE connections_mcp_tools SET stale=1 WHERE connection_id=?`, row.ID)
		if err != nil {
			return wireObservation{}, false, err
		}
	}
	if out.Observation.Disposition == obsSucceeded || out.Observation.Disposition == obsFailed || out.Observation.Disposition == obsUnknown || out.Observation.Disposition == obsNotSent {
		_, err = u.ExecContext(ctx, `UPDATE connections_mcp_intents SET completed_attempt=? WHERE operation_id=? AND completed_attempt=''`, attempt, operation)
		if err != nil {
			return wireObservation{}, false, err
		}
	}
	return out.Observation, false, nil
}

func (s *Service) requireMCPBindings(ctx context.Context, u contract.Unit, a governedMCPAction) error {
	input, err := json.Marshal(map[string]any{"scope": a.Scope})
	if err != nil {
		return err
	}
	payload, err := s.ports.Call(ctx, u, contract.Invocation{Operation: "_configuration.snapshot", Version: 1, Input: input})
	if err != nil {
		return err
	}
	var out struct {
		Resource struct {
			Bindings []wireBinding `json:"bindings"`
		} `json:"resource"`
	}
	if err = json.Unmarshal(payload.Data, &out); err != nil {
		return err
	}
	tool, connection := false, false
	for _, b := range out.Resource.Bindings {
		if len(b.Permissions) == 0 || !contains(b.Destinations, a.Destination) {
			continue
		}
		if b.Kind == "tool" && b.TargetID == a.Tool.ID {
			tool = true
		}
		if b.Kind == "connection" && b.TargetID == a.Connection.ID {
			connection = true
		}
	}
	if !tool || !connection {
		return permissionDenied("MCP dispatch requires explicit tool and connection bindings at this endpoint")
	}
	return nil
}
