package serenity

import (
	"encoding/json"

	"github.com/zatiti/zatiti/internal/contract"
)

// action is the decoded, kind-discriminated Dispatch.Action, reduced to the
// fields the adapter's local checks read. WriterOwner is empty for read
// kinds; Bounds is nil for kinds whose frozen schema carries no spend or
// disclosure bound.
type action struct {
	Kind             string
	BrainID          contract.ID
	AdapterCommandID contract.ID
	WriterOwner      string
	Classification   string
	Bounds           *actionBounds
}

// actionBounds is the spend and disclosure envelope an action requests for
// nested upstream provider calls.
type actionBounds struct {
	MaximumCost                 wireMoney
	AllowedProviderDestinations []string
}

// decodeAction validates raw against the composed zatiti.serenity.action/v1
// oneOf schema and strict-decodes it into the variant named by its "kind"
// discriminator.
func decodeAction(raw json.RawMessage) (*action, error) {
	schema, err := parametersSchema()
	if err != nil {
		return nil, internalError("serenity parameters schema composition failed: %v", err)
	}
	if err := contract.ValidateSchema(schema, raw); err != nil {
		return nil, invalidInput("serenity action does not match the zatiti.serenity.action/v1 schema: %v", err)
	}

	var peek struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &peek); err != nil {
		return nil, invalidInput("serenity action kind could not be read: %v", err)
	}

	switch peek.Kind {
	case kindRecall:
		var w wireRecall
		if err := contract.DecodeStrict(raw, &w); err != nil {
			return nil, invalidInput("serenity recall action decode failed: %v", err)
		}
		return &action{Kind: w.Kind, BrainID: w.BrainID, AdapterCommandID: w.AdapterCommandID,
			Classification: w.Classification,
			Bounds:         &actionBounds{w.MaximumCost, w.AllowedProviderDestinations}}, nil
	case kindRemember:
		var w wireRemember
		if err := contract.DecodeStrict(raw, &w); err != nil {
			return nil, invalidInput("serenity remember action decode failed: %v", err)
		}
		return &action{Kind: w.Kind, BrainID: w.BrainID, AdapterCommandID: w.AdapterCommandID,
			WriterOwner: w.WriterOwner,
			Bounds:      &actionBounds{w.MaximumCost, w.AllowedProviderDestinations}}, nil
	case kindInspect:
		var w wireInspect
		if err := contract.DecodeStrict(raw, &w); err != nil {
			return nil, invalidInput("serenity inspect action decode failed: %v", err)
		}
		return &action{Kind: w.Kind, BrainID: w.BrainID, AdapterCommandID: w.AdapterCommandID}, nil
	case kindPromote:
		var w wirePromote
		if err := contract.DecodeStrict(raw, &w); err != nil {
			return nil, invalidInput("serenity promote action decode failed: %v", err)
		}
		return &action{Kind: w.Kind, BrainID: w.BrainID, AdapterCommandID: w.AdapterCommandID,
			WriterOwner: w.WriterOwner,
			Bounds:      &actionBounds{w.MaximumCost, w.AllowedProviderDestinations}}, nil
	case kindRetract:
		var w wireRetract
		if err := contract.DecodeStrict(raw, &w); err != nil {
			return nil, invalidInput("serenity retract action decode failed: %v", err)
		}
		return &action{Kind: w.Kind, BrainID: w.BrainID, AdapterCommandID: w.AdapterCommandID,
			WriterOwner: w.WriterOwner}, nil
	case kindExportRevision:
		var w wireExportRevision
		if err := contract.DecodeStrict(raw, &w); err != nil {
			return nil, invalidInput("serenity export_revision action decode failed: %v", err)
		}
		if w.Revision.BrainID != w.BrainID {
			return nil, invalidInput("serenity export_revision names revision brain %s but targets brain %s", w.Revision.BrainID, w.BrainID)
		}
		return &action{Kind: w.Kind, BrainID: w.BrainID, AdapterCommandID: w.AdapterCommandID}, nil
	default:
		return nil, invalidInput("serenity action kind %q is not one of the six qualified kinds", peek.Kind)
	}
}
