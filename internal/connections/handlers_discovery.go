package connections

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// mcpDiscoveredToolID returns the deterministic UUID for one recorded MCP
// tool on a connection. Binding.target_id and _connections.resolve tool Refs
// use this identity so resolve can compose the Tool from the catalog without
// a separate contracts row.
func mcpDiscoveredToolID(connectionID contract.ID, name string) contract.ID {
	sum := sha1.Sum([]byte("zatiti.mcp.discovered-tool\x00" + string(connectionID) + "\x00" + name))
	sum[6] = (sum[6] & 0x0f) | 0x50 // UUID version 5
	sum[8] = (sum[8] & 0x3f) | 0x80 // RFC 4122 variant
	h := hex.EncodeToString(sum[:])
	return contract.ID(h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32])
}

// buildDiscoveryAction generates the exact list_tools adapter action
// connection.discover admits. The frozen discover input carries no
// session_handle (a contract defect: affected callers are connection.discover
// and the controller probe path); the handle is taken from the most recent
// succeeded validation observation for this connection. Absent handle =>
// prerequisite_missing, never an invented value.
func buildDiscoveryAction(sessionHandle string) (json.RawMessage, *contract.Fault) {
	if sessionHandle == "" {
		return nil, prerequisiteMissing(
			"connection.discover requires a prior succeeded open_session validation that recorded a session_handle")
	}
	raw, err := json.Marshal(map[string]any{
		"schema":         "zatiti.mcp.action/v1",
		"kind":           "list_tools",
		"session_handle": sessionHandle,
	})
	if err != nil {
		return nil, internalError("encoding the mcp list_tools discovery action failed")
	}
	return raw, nil
}

// handleDiscoverPublic admits a bounded, separately authorized list_tools
// read for provider mcp only. Other providers refuse capability_unsupported.
// The observed catalog lands through _connections.discovery.record.
func handleDiscoverPublic(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[connValidateIn](s, "connection.discover", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if f := checkInstallation(unit, in.Scope); f != nil {
		return contract.Payload{}, f
	}
	row, err := s.loadConnectionChecked(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !scopeCovers(in.Scope.toContract(), row.Scope) {
		return contract.Payload{}, notFound("connection %s is unknown in this scope", in.ID)
	}
	if f := refuseInactive(row); f != nil {
		return contract.Payload{}, f
	}
	if row.Version != in.ExpectedVersion {
		return contract.Payload{}, staleVersion("connection %s version %d does not match expected version %d",
			in.ID, row.Version, in.ExpectedVersion)
	}
	if row.Provider != "mcp" {
		return contract.Payload{}, capabilityUnsupportedFault(
			"connection.discover is supported only for provider mcp; connection %s has provider %q",
			row.ID, row.Provider)
	}
	handle, ok, herr := s.loadLatestSucceededSessionHandle(ctx, unit, row.ID)
	if herr != nil {
		return contract.Payload{}, herr
	}
	if !ok {
		return contract.Payload{}, prerequisiteMissing(
			"connection %s has no succeeded open_session validation with a session_handle; run connection.validate first",
			row.ID)
	}
	action, faultErr := buildDiscoveryAction(handle)
	if faultErr != nil {
		return contract.Payload{}, faultErr
	}
	tool, found, terr := s.loadContractByName(ctx, unit, toolNameMCPProbe)
	if terr != nil {
		return contract.Payload{}, terr
	}
	if !found {
		return contract.Payload{}, internalError("built-in tool %q is not seeded", toolNameMCPProbe)
	}
	if verr := contract.ValidateSchema(tool.InputSchema, action); verr != nil {
		return contract.Payload{}, internalError(
			"generated connection.discover action does not match the %s adapter schema: %v", toolNameMCPProbe, verr)
	}
	probeInput, err := marshalData(struct {
		Scope           wireScope       `json:"scope"`
		ConnectionID    contract.ID     `json:"connection_id"`
		ExpectedVersion int64           `json:"expected_version"`
		Tool            wireRef         `json:"tool"`
		Action          json.RawMessage `json:"action"`
	}{Scope: in.Scope, ConnectionID: in.ID, ExpectedVersion: in.ExpectedVersion,
		Tool: wireRef{ID: tool.ID, Version: tool.Version}, Action: action})
	if err != nil {
		return contract.Payload{}, err
	}
	job, err := s.createJob(ctx, unit, "connection.discover", probeInput)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := s.recordPendingProbe(ctx, unit, pendingProbeRow{
		ConnectionID: row.ID, JobID: job.ID, JobVersion: job.Version, Kind: "discover",
	}, s.clock.Now()); err != nil {
		return contract.Payload{}, err
	}
	return s.completed(jobOut{Resource: job})
}

// handleToolsList pages the connection's recorded MCP discovery catalog.
// It never dials the server. Filters are unsupported for this resource.
func handleToolsList(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[struct {
		Scope        wireScope   `json:"scope"`
		ConnectionID contract.ID `json:"connection_id"`
		Cursor       string      `json:"cursor"`
		Limit        int         `json:"limit"`
		Filter       *struct{}   `json:"filter"`
	}](s, "connection.tools", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if f := checkInstallation(unit, in.Scope); f != nil {
		return contract.Payload{}, f
	}
	if in.Filter != nil {
		return contract.Payload{}, invalidInput(
			"the connection.tools resource supports no structured filters; omit the filter field")
	}
	row, err := s.loadConnectionChecked(ctx, unit, in.ConnectionID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !scopeCovers(in.Scope.toContract(), row.Scope) {
		return contract.Payload{}, notFound("connection %s is unknown in this scope", in.ConnectionID)
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 100
	}
	// Bind the cursor to connection_id + limit via a dedicated digest so a
	// page from another connection or limit cannot be replayed here.
	qid := hashHex(fmt.Sprintf("mcp-tools:%s:%d", in.ConnectionID, limit))
	offset := 0
	if in.Cursor != "" {
		cur, cerr := decodeCursor(in.Cursor)
		if cerr != nil {
			return contract.Payload{}, cerr
		}
		if cur.Actor != unit.Actor().PrincipalID {
			return contract.Payload{}, cursorExpired("cursor is bound to the requesting principal")
		}
		if cur.QueryID != qid {
			return contract.Payload{}, cursorExpired("cursor is bound to a different query")
		}
		offset = cur.Offset
	}
	rows, err := s.listMCPToolsPage(ctx, unit, in.ConnectionID, offset, limit+1)
	if err != nil {
		return contract.Payload{}, err
	}
	items := make([]wireMCPDiscoveredTool, 0, len(rows))
	for _, r := range rows {
		items = append(items, r.wire())
	}
	var next *string
	if len(items) > limit {
		items = items[:limit]
		c, cerr := encodeCursor(cursorPayload{Offset: offset + limit, QueryID: qid, Actor: unit.Actor().PrincipalID})
		if cerr != nil {
			return contract.Payload{}, cerr
		}
		next = &c
	}
	return s.completedWithCursor(struct {
		Items []wireMCPDiscoveredTool `json:"items"`
	}{Items: items}, next)
}

// discoveryEvidence is the owner-decoded subset of list_tools observation
// evidence. Unknown keys ride along into storage inert.
type discoveryEvidence struct {
	Kind          string `json:"kind"`
	SessionHandle string `json:"session_handle"`
	NextCursor    string `json:"next_cursor"`
	PhysicalCall  struct {
		OperationID contract.ID `json:"operation_id"`
	} `json:"physical_call"`
	Tools []struct {
		Name              string          `json:"name"`
		Title             string          `json:"title"`
		Description       string          `json:"description"`
		InputSchema       json.RawMessage `json:"input_schema"`
		InputSchemaDigest string          `json:"input_schema_digest"`
		OutputSchema      json.RawMessage `json:"output_schema"`
		Annotations       json.RawMessage `json:"annotations"`
	} `json:"tools"`
}

// handleDiscoveryRecord records an authorized list_tools observation into
// the connections-private catalog. Succeeded complete pages mark absent
// names stale. Discovery installs no authority and never moves validation
// state. Completes the pending discover job when disposition is terminal.
func handleDiscoveryRecord(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[struct {
		ConnectionID    contract.ID     `json:"connection_id"`
		ExpectedVersion int64           `json:"expected_version"`
		Observation     wireObservation `json:"observation"`
	}](s, "_connections.discovery.record", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	row, found, lerr := s.loadConnection(ctx, unit, in.ConnectionID)
	if lerr != nil {
		return contract.Payload{}, lerr
	}
	if !found || row.Scope.InstallationID != unit.Scope().InstallationID {
		return contract.Payload{}, notFound("connection %s is unknown in this installation", in.ConnectionID)
	}
	if row.Version != in.ExpectedVersion {
		return contract.Payload{}, staleVersion("connection %s version %d does not match expected version %d",
			row.ID, row.Version, in.ExpectedVersion)
	}
	var ev discoveryEvidence
	if len(in.Observation.Evidence) > 0 {
		if uerr := json.Unmarshal(in.Observation.Evidence, &ev); uerr != nil {
			return contract.Payload{}, invalidInput("observation evidence is not a JSON object: %v", uerr)
		}
	}
	confirmed := s.clock.Now()
	if in.Observation.ConfirmedAt != nil {
		confirmed = *in.Observation.ConfirmedAt
	}
	opID := ev.PhysicalCall.OperationID
	if opID == "" {
		opID = s.ids.New()
	}
	switch in.Observation.Disposition {
	case obsSucceeded:
		if ev.Kind != "" && ev.Kind != "list_tools" {
			return contract.Payload{}, invalidInput(
				"discovery.record expects list_tools evidence; got kind %q", ev.Kind)
		}
		names := make([]string, 0, len(ev.Tools))
		for _, t := range ev.Tools {
			if t.Name == "" || len(t.InputSchema) == 0 || t.InputSchemaDigest == "" {
				return contract.Payload{}, invalidInput(
					"discovered tool is missing required name, input_schema or input_schema_digest")
			}
			r := mcpToolRow{
				ConnectionID:         row.ID,
				Name:                 t.Name,
				Title:                t.Title,
				Description:          t.Description,
				InputSchema:          t.InputSchema,
				InputSchemaDigest:    t.InputSchemaDigest,
				OutputSchema:         t.OutputSchema,
				Annotations:          t.Annotations,
				DiscoveredAt:         confirmed,
				DiscoveryOperationID: opID,
			}
			if err := s.upsertMCPTool(ctx, unit, r); err != nil {
				return contract.Payload{}, err
			}
			names = append(names, t.Name)
		}
		// A complete catalog page (no next_cursor) marks tools the server
		// no longer advertises as stale so resolve stops composing them.
		if ev.NextCursor == "" {
			if err := s.markMCPToolsStaleExcept(ctx, unit, row.ID, names); err != nil {
				return contract.Payload{}, err
			}
		}
	case obsFailed, obsAccepted, obsUnknown, obsNotSent:
		// Record-only for non-success: no catalog mutation. Terminal job
		// completion follows below for failed/unknown.
	default:
		return contract.Payload{}, invalidInput("unknown observation disposition %q", in.Observation.Disposition)
	}
	if err := s.emit(ctx, unit, "connections.discovery.recorded", row.ID, row.Version, map[string]any{
		"id": row.ID, "version": row.Version, "disposition": in.Observation.Disposition,
	}); err != nil {
		return contract.Payload{}, err
	}
	if pending, pfound, perr := s.loadPendingProbe(ctx, unit, row.ID); perr != nil {
		return contract.Payload{}, perr
	} else if pfound && pending.Kind == "discover" {
		var jobState string
		switch in.Observation.Disposition {
		case obsSucceeded:
			jobState = "succeeded"
		case obsFailed:
			jobState = "failed"
		case obsUnknown:
			jobState = "outcome_unknown"
		}
		if jobState != "" {
			if err := s.completeJob(ctx, unit, pending.JobID, pending.JobVersion, jobState,
				resourceOut{Resource: row.wire()}); err != nil {
				return contract.Payload{}, err
			}
			if err := s.deletePendingProbe(ctx, unit, row.ID); err != nil {
				return contract.Payload{}, err
			}
		}
	}
	updated, found, uerr := s.loadConnection(ctx, unit, row.ID)
	if uerr != nil {
		return contract.Payload{}, uerr
	}
	if !found {
		return contract.Payload{}, internalError("connection %s disappeared during discovery recording", row.ID)
	}
	return s.completed(resourceOut{Resource: updated.wire()})
}

// composeMCPTool builds the dynamic Tool resolve returns for one recorded
// MCP binding: pinned discovered schema/digest under the mcp adapter, with
// effect defaulting to external_mutation. Destinations come from the
// connection (mcp-probe's seeded destinations are empty).
func composeMCPTool(probe contractRow, discovered mcpToolRow, destinations []string) wireTool {
	outSchema := json.RawMessage(`{}`)
	if len(discovered.OutputSchema) > 0 && string(discovered.OutputSchema) != "null" {
		outSchema = discovered.OutputSchema
	}
	return wireTool{
		ID:                  mcpDiscoveredToolID(discovered.ConnectionID, discovered.Name),
		Version:             1,
		Name:                discovered.Name,
		InputSchema:         discovered.InputSchema,
		OutputSchema:        outSchema,
		Effect:              "external_mutation",
		Destinations:        append([]string(nil), destinations...),
		CredentialKind:      probe.CredentialKind,
		CostBound:           probe.CostBound,
		TimeoutSeconds:      probe.TimeoutSeconds,
		Idempotency:         "none",
		KeyRetentionSeconds: 0,
		Confirmation:        "synchronous",
		Reconciliation:      "none",
		Adapter:             "mcp",
	}
}

// findMCPToolByID scans non-stale catalog rows for connectionID whose
// deterministic identity equals toolID.
func (s *Service) findMCPToolByID(ctx context.Context, unit contract.Unit, connectionID, toolID contract.ID) (mcpToolRow, bool, error) {
	rows, err := s.listMCPToolsPage(ctx, unit, connectionID, 0, 256)
	if err != nil {
		return mcpToolRow{}, false, err
	}
	for _, r := range rows {
		if r.Stale {
			continue
		}
		if mcpDiscoveredToolID(r.ConnectionID, r.Name) == toolID {
			return r, true, nil
		}
	}
	return mcpToolRow{}, false, nil
}

// findAnyMCPToolByID scans every non-stale MCP catalog row in the
// installation for a deterministic identity match. Used by checkBinding,
// which has no connection pin.
func (s *Service) findAnyMCPToolByID(ctx context.Context, unit contract.Unit, toolID contract.ID) (result mcpToolRow, found bool, retErr error) {
	rows, err := unit.QueryContext(ctx, `
		SELECT connection_id, name, title, description, input_schema, input_schema_digest,
			output_schema, annotations_json, discovered_at, discovery_operation_id, stale
		FROM connections_mcp_tools
		WHERE stale = 0`)
	if err != nil {
		return mcpToolRow{}, false, fmt.Errorf("connections: scan mcp tools: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil && retErr == nil {
			retErr = fmt.Errorf("connections: close mcp tool rows: %w", err)
		}
	}()
	for rows.Next() {
		r, serr := scanMCPTool(rows.Scan)
		if serr != nil {
			return mcpToolRow{}, false, serr
		}
		if mcpDiscoveredToolID(r.ConnectionID, r.Name) == toolID {
			return r, true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return mcpToolRow{}, false, fmt.Errorf("connections: iterate mcp tools: %w", err)
	}
	return mcpToolRow{}, false, nil
}
