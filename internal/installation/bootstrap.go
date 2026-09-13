package installation

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// installation.init is the one-time local bootstrap, served through the
// LocalIO seam so the trusted helper that custodies the owner secret runs
// outside any transaction (Perform), while the destination check, the
// opaque pending-intent bookkeeping and the final identity/configuration/
// memory/messaging composition run inside transactions (Prepare, Finish).
//
// Prepare never emits an event: application.installationID() resolves the
// durable installation identity from the first ever persisted event, and
// this operation's own scope carries a freshly minted, otherwise
// meaningless installation_id on every single call (application always
// routes a submission-key-less mutation through its bootstrap path,
// regardless of whether some earlier attempt already completed). Staying
// event-silent until Finish's peer calls emit their own domain events keeps
// a doomed or crashed attempt from being mistaken for the real installation
// identity on a later restart.

// bootstrapPlan is Prepare's sealed decision, carried to Perform and Finish.
type bootstrapPlan struct {
	IntentID        contract.ID `json:"intent_id"`
	InstallationID  contract.ID `json:"installation_id"`
	OwnerID         contract.ID `json:"owner_id"`
	CredentialID    contract.ID `json:"credential_id"`
	OwnerName       string      `json:"owner_name"`
	CredentialStore string      `json:"credential_store"`
	HeadlessKeyRef  string      `json:"headless_key_ref"`
}

// bootstrapResult is Perform's outcome: the opaque store reference the
// trusted helper custodied the owner secret under. It never carries the
// secret bytes themselves.
type bootstrapResult struct {
	StoreRef string `json:"store_ref"`
}

func (s *Service) prepareInit(ctx context.Context, unit contract.Unit, inv contract.Invocation) (contract.IOPlan, error) {
	in, err := decodeInto[initInput](s, opInit, inv.Input)
	if err != nil {
		return contract.IOPlan{}, err
	}
	if in.CredentialStore != "os" && in.CredentialStore != "headless" {
		return contract.IOPlan{}, invalidInput("credential_store must be \"os\" or \"headless\"")
	}
	if in.OwnerName == "" {
		return contract.IOPlan{}, invalidInput("owner_name is required")
	}
	headlessKeyRef := ""
	if in.HeadlessKeyRef != nil {
		headlessKeyRef = *in.HeadlessKeyRef
	}
	if in.CredentialStore == "headless" && headlessKeyRef == "" {
		return contract.IOPlan{}, prerequisiteMissing("headless_key_ref is required when credential_store is \"headless\"")
	}

	existing, err := loadState(ctx, unit)
	if err != nil {
		return contract.IOPlan{}, err
	}
	if existing != nil {
		return contract.IOPlan{}, conflictFault("this installation is already initialized; bootstrap runs exactly once")
	}

	now := s.deps.Clock.Now()
	if _, err := abandonPendingBootstrapIntents(ctx, unit, now); err != nil {
		return contract.IOPlan{}, err
	}

	intent := bootstrapIntentRow{
		ID:              s.deps.IDs.New(),
		InstallationID:  unit.Scope().InstallationID,
		OwnerID:         s.deps.IDs.New(),
		CredentialID:    s.deps.IDs.New(),
		OwnerName:       in.OwnerName,
		CredentialStore: in.CredentialStore,
		HeadlessKeyRef:  headlessKeyRef,
		State:           "pending",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := insertBootstrapIntent(ctx, unit, intent); err != nil {
		return contract.IOPlan{}, err
	}

	prepared, err := json.Marshal(bootstrapPlan{
		IntentID:        intent.ID,
		InstallationID:  intent.InstallationID,
		OwnerID:         intent.OwnerID,
		CredentialID:    intent.CredentialID,
		OwnerName:       intent.OwnerName,
		CredentialStore: intent.CredentialStore,
		HeadlessKeyRef:  intent.HeadlessKeyRef,
	})
	if err != nil {
		return contract.IOPlan{}, fmt.Errorf("installation: encode bootstrap plan: %w", err)
	}
	return contract.IOPlan{
		ID:         intent.ID,
		Owner:      owner,
		Invocation: inv,
		Actor:      unit.Actor(),
		Scope:      unit.Scope(),
		Generation: unit.Generation(),
		Prepared:   prepared,
	}, nil
}

// performInit is the trusted helper: it mints a fresh high-entropy owner
// secret and custodies it in the secret store outside any transaction. It
// never returns the secret bytes; only the opaque store reference crosses
// back into Finish.
func (s *Service) performInit(ctx context.Context, plan contract.IOPlan) (contract.IOResult, error) {
	var p bootstrapPlan
	if err := json.Unmarshal(plan.Prepared, &p); err != nil {
		return contract.IOResult{}, fmt.Errorf("installation: decode bootstrap plan: %w", err)
	}
	if s.deps.Secrets == nil {
		return contract.IOResult{Fault: faultOf(prerequisiteMissing(
			"no secret store is configured; the owner credential cannot be custodied"))}, nil
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return contract.IOResult{Fault: faultOf(internalError("owner credential entropy source unavailable"))}, nil
	}
	key := p.HeadlessKeyRef
	if key == "" {
		key = fmt.Sprintf("installation/%s/owner", p.InstallationID)
	}
	storeRef, err := s.deps.Secrets.Put(ctx, key, secret)
	for i := range secret {
		secret[i] = 0
	}
	if err != nil {
		return contract.IOResult{Fault: faultOf(prerequisiteMissing("owner credential could not be custodied: %v", err))}, nil
	}
	raw, err := json.Marshal(bootstrapResult{StoreRef: storeRef})
	if err != nil {
		return contract.IOResult{}, fmt.Errorf("installation: encode bootstrap result: %w", err)
	}
	return contract.IOResult{Data: raw}, nil
}

// finishInit commits identity, root organization/chief, distinct memory
// brains and the pinned personal-chief conversation in one transaction. A
// failure here (including a peer refusal) rolls the whole transaction back;
// the intent row stays "pending" so the next init attempt's Prepare retires
// it before starting its own, which is this package's crash-recovery path
// for both a genuine process crash and an ordinary peer failure.
func (s *Service) finishInit(ctx context.Context, unit contract.Unit, plan contract.IOPlan, result contract.IOResult) (contract.Payload, error) {
	var p bootstrapPlan
	if err := json.Unmarshal(plan.Prepared, &p); err != nil {
		return contract.Payload{}, fmt.Errorf("installation: decode bootstrap plan: %w", err)
	}
	now := s.deps.Clock.Now()
	if result.Fault != nil {
		if err := markBootstrapIntent(ctx, unit, p.IntentID, "failed", now); err != nil {
			return contract.Payload{}, err
		}
		return contract.Payload{Status: contract.StatusFailed, Error: result.Fault}, nil
	}
	var perf bootstrapResult
	if err := json.Unmarshal(result.Data, &perf); err != nil {
		return contract.Payload{}, fmt.Errorf("installation: decode bootstrap perform result: %w", err)
	}

	if _, err := s.identityBootstrap(ctx, unit, identityBootstrapInput{
		OwnerID:        p.OwnerID,
		CredentialID:   p.CredentialID,
		StoreRef:       perf.StoreRef,
		Name:           p.OwnerName,
		InstallationID: p.InstallationID,
	}); err != nil {
		return contract.Payload{}, err
	}

	organizationID := s.deps.IDs.New()
	chiefID := s.deps.IDs.New()
	if _, err := s.configurationBootstrap(ctx, unit, configurationBootstrapInput{
		InstallationID: p.InstallationID,
		OwnerID:        p.OwnerID,
		OrganizationID: organizationID,
		ChiefID:        chiefID,
	}); err != nil {
		return contract.Payload{}, err
	}

	if _, err := s.memoryBootstrap(ctx, unit, memoryBootstrapInput{
		InstallationID: p.InstallationID,
		OrganizationID: organizationID,
		ChiefID:        chiefID,
	}); err != nil {
		return contract.Payload{}, err
	}

	if _, err := s.messagingBootstrap(ctx, unit, messagingBootstrapInput{
		Scope:   wireScope{InstallationID: p.InstallationID},
		OwnerID: p.OwnerID,
		ChiefID: chiefID,
	}); err != nil {
		return contract.Payload{}, err
	}

	state := stateRow{
		ID:        p.InstallationID,
		Version:   1,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := insertState(ctx, unit, state); err != nil {
		return contract.Payload{}, err
	}
	if err := markBootstrapIntent(ctx, unit, p.IntentID, "completed", now); err != nil {
		return contract.Payload{}, err
	}
	if err := emitTransition(ctx, unit, eventInitialized, state.ID, contract.Version(state.Version)); err != nil {
		return contract.Payload{}, err
	}

	return completed(resourceOut[wireStatus]{Resource: wireStatus{
		InstallationID: state.ID,
		Generation:     unit.Generation(),
		Paused:         false,
		Maintenance:    false,
		Initialized:    true,
		Requirements:   []wireRequirement{},
		Version:        contract.Version(state.Version),
	}})
}

// faultOf normalizes any error into a *contract.Fault for an IOResult, so a
// non-fault error (e.g. a wrapped standard error) still surfaces as a named,
// inspectable code instead of an opaque internal failure.
func faultOf(err error) *contract.Fault {
	if err == nil {
		return nil
	}
	if f, ok := err.(*contract.Fault); ok {
		return f
	}
	return &contract.Fault{Code: contract.CodeInternalError, Message: err.Error()}
}
