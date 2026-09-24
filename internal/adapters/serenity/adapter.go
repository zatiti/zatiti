package serenity

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/zatiti/zatiti/internal/contract"
)

const adapterName = "serenity"

// Adapter is the Serenity memory adapter pinned to the upstream source
// PROTOCOL.md records. It deliberately holds no HTTP client, secret store,
// blob store or clock: at this pin no operation can be dispatched, so "zero
// physical calls" is a property of the type, not of control flow.
type Adapter struct {
	profile *serenityProfile
	schema  json.RawMessage
}

// New constructs the Serenity adapter from a zatiti.serenity/v1 profile. The
// profile must bind its own capability evidence, name the pinned upstream,
// and claim nothing that upstream does not provide (see loadProfile). No
// dependency is required: the adapter reaches nothing outside itself.
func New(_ contract.AdapterDependencies, raw json.RawMessage) (contract.Adapter, error) {
	profile, err := loadProfile(raw)
	if err != nil {
		return nil, err
	}
	contractDoc, err := buildContractDocument()
	if err != nil {
		return nil, err
	}
	return &Adapter{profile: profile, schema: contractDoc}, nil
}

// Name implements contract.Adapter.
func (a *Adapter) Name() string { return adapterName }

// Contract implements contract.Adapter: the local frozen seam (profile,
// parameters and evidence schemas) plus the writer capability report for the
// pinned upstream.
func (a *Adapter) Contract() json.RawMessage { return a.schema }

type contractDocument struct {
	Schema           string           `json:"schema"`
	ProfileSchema    json.RawMessage  `json:"profile_schema"`
	ParametersSchema json.RawMessage  `json:"parameters_schema"`
	EvidenceSchema   json.RawMessage  `json:"evidence_schema"`
	CapabilityReport capabilityReport `json:"capability_report"`
}

func buildContractDocument() (json.RawMessage, error) {
	ps, err := profileSchema()
	if err != nil {
		return nil, internalError("serenity contract profile schema composition failed: %v", err)
	}
	as, err := parametersSchema()
	if err != nil {
		return nil, internalError("serenity contract parameters schema composition failed: %v", err)
	}
	es, err := evidenceSchema()
	if err != nil {
		return nil, internalError("serenity contract evidence schema composition failed: %v", err)
	}
	doc := contractDocument{
		Schema:        "zatiti.serenity.contract/v1",
		ProfileSchema: ps, ParametersSchema: as, EvidenceSchema: es,
		CapabilityReport: buildCapabilityReport(),
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, internalError("serenity contract document encoding failed: %v", err)
	}
	return out, nil
}

// Invoke implements contract.Adapter. It runs every local check the frozen
// seam allows and then refuses, because no action kind can be dispatched to
// the pinned upstream. A non-nil error means no physical call was attempted,
// which here is always.
//
// The checks run before the refusal so that a caller asking for something it
// may not have is told that, rather than being told only that the adapter is
// unavailable: an unmapped brain, a foreign writer owner and an over-wide
// spend or disclosure bound are permission faults at any pin.
func (a *Adapter) Invoke(_ context.Context, dispatch contract.Dispatch) (contract.Observation, error) {
	act, err := a.admit(dispatch)
	if err != nil {
		return contract.Observation{}, err
	}
	// pinnedOperations supports no kind, and this build has no dispatch path
	// for one. A later pin that supports a kind adds its path here.
	op, _ := capabilityFor(act.Kind)
	return contract.Observation{}, operationUnsupported(op)
}

// Reconcile implements contract.Adapter. Keyed remember now stores a caller
// identity, but the upstream serves no read-only status lookup. An accepted
// upstream call can outlive a dropped connection, so nothing this adapter
// could read establishes whether the original command committed. Reconcile
// therefore builds no request and refuses with capability_unsupported naming
// the lookup gap. It never repeats the original call: replay would be another
// physical mutation attempt and other action kinds lack keyed replay.
//
// The refusal carries no Observation. PhysicalCallEvidence records one
// physical request and its staged request record; this attempt builds
// neither, and staging a record of a request that was never built would be a
// fabricated request context. A reconcile attempt that was never sent cannot
// resolve the original effect, which therefore stays outcome_unknown.
func (a *Adapter) Reconcile(_ context.Context, dispatch contract.Dispatch) (contract.Observation, error) {
	act, err := a.admit(dispatch)
	if err != nil {
		return contract.Observation{}, err
	}
	return contract.Observation{}, lookupUnsupported(act.Kind)
}

// admit is the local admission shared by Invoke and Reconcile: the dispatch
// addresses this adapter, its action matches the frozen schema, its brain is
// one the profile maps, a write names that brain's single writer owner, and
// its requested bounds stay inside the profile's.
func (a *Adapter) admit(dispatch contract.Dispatch) (*action, error) {
	if dispatch.Adapter != adapterName {
		return nil, invalidInput("dispatch adapter %q does not match %q", dispatch.Adapter, adapterName)
	}
	if dispatch.CredentialRef == "" {
		return nil, invalidInput("dispatch credential_ref is empty")
	}
	act, err := decodeAction(dispatch.Action)
	if err != nil {
		return nil, err
	}
	brain, ok := a.profile.Brains[act.BrainID]
	if !ok {
		return nil, permissionDenied("brain %s is not in the profile's brain_mappings; the adapter cannot widen a query", act.BrainID)
	}
	if act.WriterOwner != "" && act.WriterOwner != brain.WriterOwner {
		return nil, permissionDenied("writer_owner does not own brain %s; each brain has exactly one canonical writer", act.BrainID)
	}
	if err := a.withinBounds(act); err != nil {
		return nil, err
	}
	return act, nil
}

// withinBounds refuses an action whose spend or disclosure envelope is wider
// than the profile's. The profile is the ceiling; an action may only narrow.
func (a *Adapter) withinBounds(act *action) error {
	enforcement := a.profile.Enforcement
	if act.Classification != "" && !slices.Contains(enforcement.Classifications, act.Classification) {
		return permissionDenied("classification %q is not in the profile's enforcement.classifications", act.Classification)
	}
	if act.Bounds == nil {
		return nil
	}
	if act.Bounds.MaximumCost.Currency != enforcement.MaximumCost.Currency {
		return permissionDenied("maximum_cost currency %q does not match the profile currency %q",
			act.Bounds.MaximumCost.Currency, enforcement.MaximumCost.Currency)
	}
	if act.Bounds.MaximumCost.MicroUnits > enforcement.MaximumCost.MicroUnits {
		return permissionDenied("maximum_cost %d exceeds the profile's enforcement.maximum_cost %d micro-units",
			act.Bounds.MaximumCost.MicroUnits, enforcement.MaximumCost.MicroUnits)
	}
	for _, dest := range act.Bounds.AllowedProviderDestinations {
		if !slices.Contains(enforcement.ProviderDestinations, dest) {
			return permissionDenied("allowed_provider_destinations includes a destination the profile's enforcement.provider_destinations does not permit")
		}
	}
	return nil
}
