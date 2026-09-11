package connections

import (
	"context"
	"encoding/json"

	"github.com/zatiti/zatiti/internal/contract"
)

// tool.* handlers: trusted adapter contract discovery and explicit binding
// changes staged through the compiler. Contracts ship with the binary;
// discovery installs no authority and no runtime registration exists.

// wireBinding mirrors the Binding half of the $defs/Change oneOf (binding
// definitions carry the Binding shape directly).
type wireBinding struct {
	ID           contract.ID `json:"id"`
	Version      int64       `json:"version"`
	Scope        wireScope   `json:"scope"`
	Kind         string      `json:"kind"`
	TargetID     contract.ID `json:"target_id"`
	Permissions  []string    `json:"permissions"`
	SourceScope  *wireScope  `json:"source_scope,omitempty"`
	Destinations []string    `json:"destinations,omitempty"`
}

// Binding kinds the connections owner stages.
const bindingKindTool = "tool"

type bindIn struct {
	Scope   wireScope    `json:"scope"`
	Binding wireBinding  `json:"binding"`
	DraftID *contract.ID `json:"draft_id,omitempty"`
}

type draftOut struct {
	Resource wireDraft `json:"resource"`
}

// executableKinds are binding kinds whose targets execute; they refuse
// unqualified bindings outright.
var executableKinds = map[string]bool{
	"tool": true, "skill": true, "worker": true,
}

// handleToolBind stages an explicit binding create. The target adapter must
// exist, binding destinations must stay inside the contract's declared
// destinations, and executable bindings demand at least one permission.
func handleToolBind(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[bindIn](s, "tool.bind", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if f := checkInstallation(unit, in.Scope); f != nil {
		return contract.Payload{}, f
	}
	if f := checkBinding(ctx, s, unit, in.Binding); f != nil {
		return contract.Payload{}, f
	}
	def, err := marshalData(in.Binding)
	if err != nil {
		return contract.Payload{}, err
	}
	draft, err := s.stageChange(ctx, unit, in.Scope, wireChange{
		Kind:            kindBinding,
		Action:          actionCreate,
		ID:              in.Binding.ID,
		ExpectedVersion: 0,
		Definition:      def,
	}, in.DraftID)
	if err != nil {
		return contract.Payload{}, err
	}
	return s.completed(draftOut{Resource: draft})
}

// handleToolUnbind stages an explicit binding archive. The input carries the
// binding's current version; activation archives it under the compiler's
// authority.
func handleToolUnbind(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[bindIn](s, "tool.unbind", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if f := checkInstallation(unit, in.Scope); f != nil {
		return contract.Payload{}, f
	}
	if in.Binding.Kind != bindingKindTool {
		return contract.Payload{}, invalidInput("unbind expects a binding of kind %q", bindingKindTool)
	}
	def, err := marshalData(in.Binding)
	if err != nil {
		return contract.Payload{}, err
	}
	draft, err := s.stageChange(ctx, unit, in.Scope, wireChange{
		Kind:            kindBinding,
		Action:          actionArchive,
		ID:              in.Binding.ID,
		ExpectedVersion: in.Binding.Version,
		Definition:      def,
	}, in.DraftID)
	if err != nil {
		return contract.Payload{}, err
	}
	return s.completed(draftOut{Resource: draft})
}

// checkBinding enforces the binding rules the connections owner owns: scope
// containment, known adapter target for tool bindings, qualified permissions
// and destination containment inside the contract.
func checkBinding(ctx context.Context, s *Service, unit contract.Unit, b wireBinding) *contract.Fault {
	if b.Scope.InstallationID != unit.Scope().InstallationID {
		return invalidInput("binding scope installation %s does not match the transaction scope", b.Scope.InstallationID)
	}
	if b.Kind == bindingKindTool {
		tool, found, err := s.loadContract(ctx, unit, b.TargetID)
		if err != nil {
			return internalError("tool contract lookup failed: %v", err)
		}
		if !found {
			return notFound("unknown adapter %s", b.TargetID)
		}
		for _, d := range b.Destinations {
			if !contains(tool.Destinations, d) {
				return invalidInput("destination %s is outside tool %s contract destinations", d, tool.ID)
			}
		}
	}
	if executableKinds[b.Kind] && len(b.Permissions) == 0 {
		return invalidInput("binding %s targets an executable kind and demands at least one permission", b.ID)
	}
	return nil
}

// handleToolGet resolves one trusted adapter contract exactly.
func handleToolGet(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[connGetIn](s, "tool.get", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if f := checkInstallation(unit, in.Scope); f != nil {
		return contract.Payload{}, f
	}
	tool, found, err := s.loadContract(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("tool %s is unknown", in.ID)
	}
	return s.completed(resourceToolOut{Resource: tool.wire()})
}

// resourceToolOut is the {resource: Tool} output body.
type resourceToolOut struct {
	Resource wireTool `json:"resource"`
}

// handleToolList reads the trusted adapter catalog. Contracts are
// installation-independent; structured filters refuse as unsupported for
// this resource.
func handleToolList(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[connListIn](s, "tool.list", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if f := checkInstallation(unit, in.Scope); f != nil {
		return contract.Payload{}, f
	}
	if in.Filter != nil {
		return contract.Payload{}, invalidInput(
			"the tool resource supports no structured filters; omit the filter field")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 100
	}
	if in.Cursor != "" {
		return contract.Payload{}, cursorExpired("the tool catalog is a fixed snapshot; restart the listing without a cursor")
	}
	rows, err := s.listContracts(ctx, unit, limit)
	if err != nil {
		return contract.Payload{}, err
	}
	items := make([]wireTool, 0, len(rows))
	for _, r := range rows {
		items = append(items, r.wire())
	}
	return s.completedWithCursor(toolListOut{Items: items}, nil)
}

// toolListOut is the {items: [Tool]} output body.
type toolListOut struct {
	Items []wireTool `json:"items"`
}

// handleToolSchema inspects one pinned contract's inert schemas. Discovery
// installs no authority: the response carries JSON data only.
func handleToolSchema(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[connGetIn](s, "tool.schema", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if f := checkInstallation(unit, in.Scope); f != nil {
		return contract.Payload{}, f
	}
	tool, found, err := s.loadContract(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("tool %s is unknown", in.ID)
	}
	return s.completed(schemaOut{InputSchema: tool.InputSchema, OutputSchema: tool.OutputSchema})
}

// schemaOut is the tool.schema output body.
type schemaOut struct {
	InputSchema  json.RawMessage `json:"input_schema"`
	OutputSchema json.RawMessage `json:"output_schema"`
}
