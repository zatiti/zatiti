package connections

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// contract.LocalIO seam for connection.setup.begin/cancel/complete. The
// registry routes exactly these operations through Prepare/Perform/Finish;
// Handle refuses them. Prepare records the replayable challenge intent
// inside the admission transaction; Perform runs outside transactions and
// does the only permitted external work (consent URL assembly from trusted
// credential metadata, helper receipt verification — no network); Finish
// rechecks the pinned versions and commits the terminal transition.

// challengeExpiry bounds a setup challenge. Expired challenges cannot
// complete; a late cancel records the honest expired terminal state rather
// than a false cancellation.
const challengeExpiry = 30 * time.Minute

// helperReceiptPrefix prefixes every helper receipt. A complete call that
// carries anything else — a pasted OAuth code, a token, free text — refuses
// as verification_failed before any account check runs.
const helperReceiptPrefix = "zatiti-helper/v1."

// helperReceiptKeyRef is the secret-store reference holding the HMAC key
// shared with the trusted local helper.
const helperReceiptKeyRef = "connections/helper/receipt-key"

// ioPrivate is the namespaced owner-local metadata channel inside Prepared
// and IOResult.Data. No other package reads it.
type ioPrivate struct {
	CredentialRef   string         `json:"credential_ref,omitempty"`
	AccountIdentity string         `json:"account_identity,omitempty"`
	ConsentURL      string         `json:"consent_url,omitempty"`
	Receipt         *helperPayload `json:"receipt,omitempty"`
}

// helperPayload is the owner-decoded receipt body. It binds the receipt to
// one challenge, one credential reference and one account identity.
type helperPayload struct {
	ChallengeID     contract.ID `json:"challenge_id"`
	CredentialRef   string      `json:"credential_ref"`
	AccountIdentity string      `json:"account_identity"`
}

// credentialMeta is the owner-decoded subset of stored credential metadata
// used to assemble the browser consent URL. Metadata lives in the secret
// store under the connection's credential reference.
type credentialMeta struct {
	ClientID     string `json:"client_id"`
	AuthorizeURL string `json:"authorize_url"`
	RedirectURI  string `json:"redirect_uri"`
}

// ioEnvelope wraps a prepared or performed document: declared schema data
// plus the private x-connections channel.
type ioEnvelope struct {
	Resource     *wireChallenge `json:"resource,omitempty"`
	XConnections *ioPrivate     `json:"x-connections,omitempty"`
}

// decodePrivate extracts the private channel from a Prepared or Data
// document. A missing channel is an empty record, never an error.
func decodePrivate(raw json.RawMessage) ioPrivate {
	var env ioEnvelope
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &env); err != nil || env.XConnections == nil {
			return ioPrivate{}
		}
	}
	if env.XConnections == nil {
		return ioPrivate{}
	}
	return *env.XConnections
}

// challengeWire projects a stored challenge onto the wire shape.
func (r challengeRow) wire() wireChallenge {
	return wireChallenge{
		ID:           r.ID,
		Version:      r.Version,
		ConnectionID: r.ConnectionID,
		State:        r.State,
		ExpiresAt:    r.ExpiresAt,
		ConsentURL:   r.ConsentURL,
		HelperRef:    r.HelperRef,
		Requirements: r.Requirements,
	}
}

// insertChallenge stores the challenge intent created by prepareBegin.
func (s *Service) insertChallenge(ctx context.Context, unit contract.Unit, c challengeRow, now time.Time) error {
	stamp := formatStamp(now)
	raw, err := json.Marshal(c.Requirements)
	if err != nil {
		return internalError("challenge requirements encoding failed")
	}
	_, err = unit.ExecContext(ctx, `
		INSERT INTO connections_challenges
			(id, version, installation_id, connection_id, connection_version, method,
			 principal_id, account_identity, state, expires_at, consent_url, helper_ref,
			 requirements_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(c.ID), c.Version, string(c.InstallationID), string(c.ConnectionID),
		c.ConnectionVersion, c.Method, string(c.PrincipalID), c.AccountIdentity,
		c.State, formatStamp(c.ExpiresAt), nilIfEmpty(c.ConsentURL), nilIfEmpty(c.HelperRef),
		string(raw), stamp, stamp)
	if err != nil {
		return fmt.Errorf("connections: insert challenge: %w", err)
	}
	return nil
}

// nilIfEmpty maps empty strings to SQL NULL.
func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// updateChallenge transitions one challenge state with an optimistic version
// check and the supplied consent/helper annotations.
func (s *Service) updateChallenge(ctx context.Context, unit contract.Unit, row challengeRow, state, consentURL, helperRef string) error {
	res, err := unit.ExecContext(ctx, `
		UPDATE connections_challenges
		SET version = ?, state = ?, consent_url = ?, helper_ref = ?, updated_at = ?
		WHERE id = ? AND version = ?`,
		row.Version+1, state, nilIfEmpty(consentURL), nilIfEmpty(helperRef),
		formatStamp(s.clock.Now()), string(row.ID), row.Version)
	if err != nil {
		return fmt.Errorf("connections: update challenge: %w", err)
	}
	return expectOneRow(res, "challenge", row.ID)
}

// beginInput is the setup.begin request body, shared by Prepare and Perform.
type beginInput struct {
	Scope           wireScope   `json:"scope"`
	ConnectionID    contract.ID `json:"connection_id"`
	ExpectedVersion int64       `json:"expected_version"`
	Method          string      `json:"method"`
}

// prepareBegin records the challenge intent for setup.begin.
func (s *Service) prepareBegin(ctx context.Context, unit contract.Unit, inv contract.Invocation) (contract.IOPlan, error) {
	in, err := decodeInto[beginInput](s, "connection.setup.begin", inv.Input)
	if err != nil {
		return contract.IOPlan{}, err
	}
	if f := checkInstallation(unit, in.Scope); f != nil {
		return contract.IOPlan{}, f
	}
	row, err := s.loadConnectionChecked(ctx, unit, in.ConnectionID)
	if err != nil {
		return contract.IOPlan{}, err
	}
	if !scopeCovers(in.Scope.toContract(), row.Scope) {
		return contract.IOPlan{}, notFound("connection %s is unknown in this scope", in.ConnectionID)
	}
	if f := refuseInactive(row); f != nil {
		return contract.IOPlan{}, f
	}
	if row.Version != in.ExpectedVersion {
		return contract.IOPlan{}, staleVersion("connection %s version %d does not match expected version %d",
			in.ConnectionID, row.Version, in.ExpectedVersion)
	}
	if live, lerr := s.liveChallengeExists(ctx, unit, row.ID); lerr != nil {
		return contract.IOPlan{}, lerr
	} else if live {
		return contract.IOPlan{}, conflictFault(
			"connection %s already carries a live setup challenge; cancel it before beginning another", row.ID)
	}
	now := s.clock.Now()
	challenge := challengeRow{
		ID:                s.ids.New(),
		Version:           1,
		InstallationID:    row.Scope.InstallationID,
		ConnectionID:      row.ID,
		ConnectionVersion: row.Version,
		Method:            in.Method,
		PrincipalID:       unit.Actor().PrincipalID,
		AccountIdentity:   row.AccountIdentity,
		State:             challengePending,
		ExpiresAt:         now.Add(challengeExpiry),
	}
	if err := s.insertChallenge(ctx, unit, challenge, now); err != nil {
		return contract.IOPlan{}, err
	}
	if err := s.emit(ctx, unit, "connections.challenge.begun", challenge.ID, 1, map[string]any{
		"id": challenge.ID, "connection_id": row.ID, "method": in.Method,
	}); err != nil {
		return contract.IOPlan{}, err
	}
	prepared, err := marshalData(ioEnvelope{
		Resource: challenge.wireAddr(),
		XConnections: &ioPrivate{
			CredentialRef: row.CredentialRef,
		},
	})
	if err != nil {
		return contract.IOPlan{}, err
	}
	return contract.IOPlan{
		ID:         challenge.ID,
		Owner:      ownerName,
		Invocation: inv,
		Actor:      unit.Actor(),
		Scope:      unit.Scope(),
		ExpectedVersions: map[contract.ID]contract.Version{
			row.ID:       contract.Version(row.Version),
			challenge.ID: 1,
		},
		Prepared: prepared,
	}, nil
}

// prepareCancel records the cancel intent for setup.cancel. Terminal
// challenges replay unchanged; the state transition itself commits in Finish,
// which records expired — never a false cancelled — when the challenge's
// expiry passed first.
func (s *Service) prepareCancel(ctx context.Context, unit contract.Unit, inv contract.Invocation) (contract.IOPlan, error) {
	in, err := decodeInto[struct {
		Scope           wireScope   `json:"scope"`
		ChallengeID     contract.ID `json:"challenge_id"`
		ExpectedVersion int64       `json:"expected_version"`
	}](s, "connection.setup.cancel", inv.Input)
	if err != nil {
		return contract.IOPlan{}, err
	}
	if f := checkInstallation(unit, in.Scope); f != nil {
		return contract.IOPlan{}, f
	}
	row, found, lerr := s.loadChallenge(ctx, unit, in.ChallengeID)
	if lerr != nil {
		return contract.IOPlan{}, lerr
	}
	if !found || row.InstallationID != unit.Scope().InstallationID {
		return contract.IOPlan{}, notFound("challenge %s is unknown in this installation", in.ChallengeID)
	}
	if row.PrincipalID != unit.Actor().PrincipalID {
		return contract.IOPlan{}, permissionDenied(
			"challenge %s is bound to the initiating principal; only that principal may cancel it", row.ID)
	}
	if row.State == challengeCancelled || row.State == challengeExpired {
		// Idempotent terminal replay: cancelled stands; expired is the
		// honest outcome a late cancel records.
		return s.terminalPlan(unit, inv, row)
	}
	if row.State == challengeCompleted || row.State == challengeFailed {
		return contract.IOPlan{}, conflictFault("challenge %s is already %s", row.ID, row.State)
	}
	if row.Version != in.ExpectedVersion {
		return contract.IOPlan{}, staleVersion("challenge %s version %d does not match expected version %d",
			row.ID, row.Version, in.ExpectedVersion)
	}
	return s.transitionPlan(ctx, unit, inv, row)
}

// prepareComplete validates the completion request for setup.complete. The
// opaque helper receipt verification happens in Perform outside the
// transaction; Finish commits the transition after revalidating pins.
func (s *Service) prepareComplete(ctx context.Context, unit contract.Unit, inv contract.Invocation) (contract.IOPlan, error) {
	in, err := decodeInto[struct {
		Scope           wireScope   `json:"scope"`
		ChallengeID     contract.ID `json:"challenge_id"`
		ExpectedVersion int64       `json:"expected_version"`
		HelperRef       string      `json:"helper_ref"`
	}](s, "connection.setup.complete", inv.Input)
	if err != nil {
		return contract.IOPlan{}, err
	}
	if f := checkInstallation(unit, in.Scope); f != nil {
		return contract.IOPlan{}, f
	}
	row, found, lerr := s.loadChallenge(ctx, unit, in.ChallengeID)
	if lerr != nil {
		return contract.IOPlan{}, lerr
	}
	if !found || row.InstallationID != unit.Scope().InstallationID {
		return contract.IOPlan{}, notFound("challenge %s is unknown in this installation", in.ChallengeID)
	}
	if row.PrincipalID != unit.Actor().PrincipalID {
		return contract.IOPlan{}, permissionDenied(
			"challenge %s is bound to the initiating principal; only that principal may complete it", row.ID)
	}
	if row.State == challengeCompleted {
		return contract.IOPlan{}, conflictFault("challenge %s is already completed", row.ID)
	}
	if row.State == challengeCancelled || row.State == challengeExpired || row.State == challengeFailed {
		return contract.IOPlan{}, prerequisiteMissing("challenge %s is %s and cannot complete", row.ID, row.State)
	}
	if !row.ExpiresAt.After(s.clock.Now()) {
		return contract.IOPlan{}, prerequisiteMissing("challenge %s expired at %s and cannot complete",
			row.ID, formatStamp(row.ExpiresAt))
	}
	if row.State != challengeExternalActionRequired {
		return contract.IOPlan{}, invalidInput(
			"challenge %s has no external action in progress; wait for begin to finish", row.ID)
	}
	if row.Version != in.ExpectedVersion {
		return contract.IOPlan{}, staleVersion("challenge %s version %d does not match expected version %d",
			row.ID, row.Version, in.ExpectedVersion)
	}
	if !strings.HasPrefix(in.HelperRef, helperReceiptPrefix) {
		return contract.IOPlan{}, verificationFailed(
			"complete accepts an opaque helper receipt only; pasted codes and tokens are refused")
	}
	return s.transitionPlan(ctx, unit, inv, row)
}

// terminalPlan builds the accepted plan for an idempotent terminal replay.
func (s *Service) terminalPlan(unit contract.Unit, inv contract.Invocation, row challengeRow) (contract.IOPlan, error) {
	prepared, err := marshalData(ioEnvelope{Resource: row.wireAddr()})
	if err != nil {
		return contract.IOPlan{}, err
	}
	return contract.IOPlan{
		ID:         row.ID,
		Owner:      ownerName,
		Invocation: inv,
		Actor:      unit.Actor(),
		Scope:      unit.Scope(),
		ExpectedVersions: map[contract.ID]contract.Version{
			row.ConnectionID: contract.Version(row.ConnectionVersion),
			row.ID:           contract.Version(row.Version),
		},
		Prepared: prepared,
	}, nil
}

// transitionPlan builds the accepted plan for a challenge that Perform or
// Finish will transition, carrying the private channel the later phases read.
func (s *Service) transitionPlan(ctx context.Context, unit contract.Unit, inv contract.Invocation, row challengeRow) (contract.IOPlan, error) {
	conn, found, err := s.loadConnection(ctx, unit, row.ConnectionID)
	if err != nil {
		return contract.IOPlan{}, err
	}
	private := &ioPrivate{}
	if found {
		private.CredentialRef = conn.CredentialRef
		private.AccountIdentity = row.AccountIdentity
	}
	prepared, err := marshalData(ioEnvelope{Resource: row.wireAddr(), XConnections: private})
	if err != nil {
		return contract.IOPlan{}, err
	}
	return contract.IOPlan{
		ID:         row.ID,
		Owner:      ownerName,
		Invocation: inv,
		Actor:      unit.Actor(),
		Scope:      unit.Scope(),
		ExpectedVersions: map[contract.ID]contract.Version{
			row.ConnectionID: contract.Version(row.ConnectionVersion),
			row.ID:           contract.Version(row.Version),
		},
		Prepared: prepared,
	}, nil
}

// wireAddr returns a pointer to the row's wire projection.
func (r challengeRow) wireAddr() *wireChallenge {
	w := r.wire()
	return &w
}

// Prepare implements contract.LocalIO.
func (s *Service) Prepare(ctx context.Context, unit contract.Unit, invocation contract.Invocation) (contract.IOPlan, error) {
	switch invocation.Operation {
	case "connection.setup.begin":
		return s.prepareBegin(ctx, unit, invocation)
	case "connection.setup.cancel":
		return s.prepareCancel(ctx, unit, invocation)
	case "connection.setup.complete":
		return s.prepareComplete(ctx, unit, invocation)
	default:
		return contract.IOPlan{}, internalError(
			"operation %s does not route through the connections local IO seam", invocation.Operation)
	}
}

// Perform implements contract.LocalIO. It runs outside transactions with no
// Unit: the only permitted work is consent URL assembly from trusted
// credential metadata and helper receipt verification. Nothing here performs
// network access; provider adapters are qualified separately and unsupported
// semantics refuse capability_unsupported rather than faking success.
func (s *Service) Perform(ctx context.Context, plan contract.IOPlan) (contract.IOResult, error) {
	switch plan.Invocation.Operation {
	case "connection.setup.begin":
		return s.performBegin(ctx, plan)
	case "connection.setup.cancel":
		// Cancellation is local: no external work exists.
		return contract.IOResult{Data: nil}, nil
	case "connection.setup.complete":
		return s.performComplete(ctx, plan)
	default:
		return contract.IOResult{}, internalError(
			"operation %s does not route through the connections local IO seam", plan.Invocation.Operation)
	}
}

// performBegin assembles the browser consent URL for method browser from the
// credential metadata held in the secret store. Missing secret plumbing or
// unreadable metadata refuses prerequisite_missing: the consent link is an
// actionable prerequisite, never invented. store_reference needs no external
// work at this phase.
func (s *Service) performBegin(ctx context.Context, plan contract.IOPlan) (contract.IOResult, error) {
	in, err := decodeInto[beginInput](s, "connection.setup.begin", plan.Invocation.Input)
	if err != nil {
		return contract.IOResult{}, err
	}
	if in.Method != methodBrowser {
		// store_reference: the user stores the credential with the trusted
		// helper next; begin itself has nothing external to do.
		return contract.IOResult{Data: nil}, nil
	}
	private := decodePrivate(plan.Prepared)
	if private.CredentialRef == "" {
		return contract.IOResult{Fault: internalError("begin plan carries no credential reference")}, nil
	}
	if s.secrets == nil {
		return contract.IOResult{Fault: prerequisiteMissing(
			"no secret store is configured; the browser consent link cannot be assembled")}, nil
	}
	material, err := s.secrets.Get(ctx, private.CredentialRef)
	if err != nil {
		return contract.IOResult{Fault: prerequisiteMissing(
			"credential reference %s is unreadable; browser setup cannot begin", private.CredentialRef)}, nil
	}
	var meta credentialMeta
	if uerr := json.Unmarshal(material, &meta); uerr != nil {
		return contract.IOResult{Fault: prerequisiteMissing(
			"credential reference %s does not carry usable authorization metadata", private.CredentialRef)}, nil
	}
	if meta.AuthorizeURL == "" || meta.ClientID == "" || meta.RedirectURI == "" {
		return contract.IOResult{Fault: prerequisiteMissing(
			"credential reference %s is missing authorize_url, client_id or redirect_uri metadata",
			private.CredentialRef)}, nil
	}
	parsed, perr := url.Parse(meta.AuthorizeURL)
	if perr != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return contract.IOResult{Fault: prerequisiteMissing(
			"credential reference %s carries an unusable authorize_url", private.CredentialRef)}, nil
	}
	q := parsed.Query()
	q.Set("response_type", "code")
	q.Set("client_id", meta.ClientID)
	q.Set("redirect_uri", meta.RedirectURI)
	q.Set("state", string(plan.ID))
	parsed.RawQuery = q.Encode()
	data, err := marshalData(ioEnvelope{XConnections: &ioPrivate{ConsentURL: parsed.String()}})
	if err != nil {
		return contract.IOResult{}, err
	}
	return contract.IOResult{Data: data}, nil
}

// performComplete verifies the opaque helper receipt: exact receipt format,
// HMAC over the payload, challenge binding, account identity and credential
// existence. Every refusal is verification_failed so a forged or misdirected
// receipt can never pass for consent.
func (s *Service) performComplete(ctx context.Context, plan contract.IOPlan) (contract.IOResult, error) {
	in, err := decodeInto[struct {
		Scope           wireScope   `json:"scope"`
		ChallengeID     contract.ID `json:"challenge_id"`
		ExpectedVersion int64       `json:"expected_version"`
		HelperRef       string      `json:"helper_ref"`
	}](s, "connection.setup.complete", plan.Invocation.Input)
	if err != nil {
		return contract.IOResult{}, err
	}
	private := decodePrivate(plan.Prepared)
	if s.secrets == nil {
		return contract.IOResult{Fault: prerequisiteMissing(
			"no secret store is configured; helper receipts cannot be verified")}, nil
	}
	key, err := s.secrets.Get(ctx, helperReceiptKeyRef)
	if err != nil {
		return contract.IOResult{Fault: prerequisiteMissing(
			"the helper receipt key is not provisioned; helper receipts cannot be verified")}, nil
	}
	payload, verr := verifyReceipt(in.HelperRef, key)
	if verr != nil {
		return contract.IOResult{Fault: verr}, nil
	}
	if payload.ChallengeID != plan.ID {
		return contract.IOResult{Fault: verificationFailed(
			"helper receipt is bound to challenge %s, not %s", payload.ChallengeID, plan.ID)}, nil
	}
	if payload.AccountIdentity != private.AccountIdentity {
		return contract.IOResult{Fault: verificationFailed(
			"helper receipt reports account %q but the challenge is bound to %q; substitution is refused",
			payload.AccountIdentity, private.AccountIdentity)}, nil
	}
	if _, gerr := s.secrets.Get(ctx, payload.CredentialRef); gerr != nil {
		return contract.IOResult{Fault: verificationFailed(
			"helper receipt names credential reference %s which the store does not hold", payload.CredentialRef)}, nil
	}
	data, err := marshalData(ioEnvelope{XConnections: &ioPrivate{Receipt: payload}})
	if err != nil {
		return contract.IOResult{}, err
	}
	return contract.IOResult{Data: data}, nil
}

// verifyReceipt checks the receipt envelope and its HMAC. The decoded
// payload is returned only when the signature matches exactly.
func verifyReceipt(receipt string, key []byte) (*helperPayload, *contract.Fault) {
	rest, ok := strings.CutPrefix(receipt, helperReceiptPrefix)
	if !ok {
		return nil, verificationFailed("complete accepts an opaque helper receipt only; pasted codes and tokens are refused")
	}
	body, sig, ok := strings.Cut(rest, ".")
	if !ok || body == "" || sig == "" || strings.Contains(sig, ".") {
		return nil, verificationFailed("helper receipt is malformed")
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return nil, verificationFailed("helper receipt is malformed")
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(payloadBytes)
	want, err := hex.DecodeString(sig)
	if err != nil || !hmac.Equal(mac.Sum(nil), want) {
		return nil, verificationFailed("helper receipt signature does not verify")
	}
	var payload helperPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return nil, verificationFailed("helper receipt is malformed")
	}
	if payload.ChallengeID == "" || payload.CredentialRef == "" || payload.AccountIdentity == "" {
		return nil, verificationFailed("helper receipt is missing its binding fields")
	}
	return &payload, nil
}

// Finish implements contract.LocalIO. It revalidates the pinned connection
// and challenge versions inside the completion transaction, commits the
// terminal transition and returns the completed payload. Perform faults
// record the challenge as failed: never an invented success.
func (s *Service) Finish(ctx context.Context, unit contract.Unit, plan contract.IOPlan, result contract.IOResult) (contract.Payload, error) {
	op := plan.Invocation.Operation
	row, found, lerr := s.loadChallenge(ctx, unit, plan.ID)
	if lerr != nil {
		return contract.Payload{}, lerr
	}
	if !found {
		return contract.Payload{}, notFound("challenge %s is unknown in this installation", plan.ID)
	}
	if f := revalidatePins(plan, row); f != nil {
		return contract.Payload{}, f
	}
	if result.Fault != nil {
		if row.State == challengePending || row.State == challengeExternalActionRequired {
			if err := s.updateChallenge(ctx, unit, row, challengeFailed, "", ""); err != nil {
				return contract.Payload{}, err
			}
			if err := s.emit(ctx, unit, "connections.challenge.failed", row.ID, row.Version+1, map[string]any{
				"id": row.ID, "state": challengeFailed, "operation": op,
			}); err != nil {
				return contract.Payload{}, err
			}
		}
		return contract.Payload{Status: contract.StatusFailed, Error: result.Fault}, nil
	}
	switch op {
	case "connection.setup.begin":
		return s.finishBegin(ctx, unit, row, result.Data)
	case "connection.setup.cancel":
		if row.State == challengeCancelled || row.State == challengeExpired {
			// Idempotent replay: the terminal row already stands.
			return s.completed(resourceChallengeOut{Resource: row.wire()})
		}
		// A challenge whose expiry passed before the cancel commits records
		// expired — the honest observation — rather than a false cancelled.
		state := challengeCancelled
		kind := "connections.challenge.cancelled"
		if !row.ExpiresAt.After(s.clock.Now()) {
			state = challengeExpired
			kind = "connections.challenge.expired"
		}
		if err := s.updateChallenge(ctx, unit, row, state, "", ""); err != nil {
			return contract.Payload{}, err
		}
		if err := s.emit(ctx, unit, kind, row.ID, row.Version+1, map[string]any{
			"id": row.ID, "state": state,
		}); err != nil {
			return contract.Payload{}, err
		}
		updated, _, uerr := s.loadChallenge(ctx, unit, row.ID)
		if uerr != nil {
			return contract.Payload{}, uerr
		}
		return s.completed(resourceChallengeOut{Resource: updated.wire()})
	case "connection.setup.complete":
		return s.finishComplete(ctx, unit, row, plan, result.Data)
	default:
		return contract.Payload{}, internalError(
			"operation %s does not route through the connections local IO seam", op)
	}
}

// revalidatePins refuses completion when the challenge or its connection
// moved between Prepare and Finish.
func revalidatePins(plan contract.IOPlan, row challengeRow) *contract.Fault {
	wantChallenge, ok := plan.ExpectedVersions[row.ID]
	if !ok || wantChallenge != contract.Version(row.Version) {
		return staleVersion("challenge %s version %d does not match the prepared plan version %d",
			row.ID, row.Version, wantChallenge)
	}
	wantConnection, ok := plan.ExpectedVersions[row.ConnectionID]
	if ok && wantConnection != contract.Version(row.ConnectionVersion) {
		return staleVersion("connection %s version %d does not match the prepared plan version %d",
			row.ConnectionID, row.ConnectionVersion, wantConnection)
	}
	return nil
}

// finishBegin commits the external-action state: browser challenges carry
// the assembled consent URL; store_reference challenges wait on the helper.
func (s *Service) finishBegin(ctx context.Context, unit contract.Unit, row challengeRow, data json.RawMessage) (contract.Payload, error) {
	private := decodePrivate(data)
	consent := private.ConsentURL
	if row.Method == methodBrowser && consent == "" {
		return contract.Payload{}, internalError("browser challenge completion carries no consent URL")
	}
	if err := s.updateChallenge(ctx, unit, row, challengeExternalActionRequired, consent, ""); err != nil {
		return contract.Payload{}, err
	}
	if err := s.emit(ctx, unit, "connections.challenge.awaiting_external_action", row.ID, row.Version+1, map[string]any{
		"id": row.ID, "state": challengeExternalActionRequired, "method": row.Method,
	}); err != nil {
		return contract.Payload{}, err
	}
	updated, _, uerr := s.loadChallenge(ctx, unit, row.ID)
	if uerr != nil {
		return contract.Payload{}, uerr
	}
	return s.completed(resourceChallengeOut{Resource: updated.wire()})
}

// finishComplete commits the verified completion. Every binding check ran in
// Perform; Finish revalidates versions and records the terminal state with
// the opaque receipt reference.
func (s *Service) finishComplete(ctx context.Context, unit contract.Unit, row challengeRow, plan contract.IOPlan, data json.RawMessage) (contract.Payload, error) {
	in, err := decodeInto[struct {
		Scope           wireScope   `json:"scope"`
		ChallengeID     contract.ID `json:"challenge_id"`
		ExpectedVersion int64       `json:"expected_version"`
		HelperRef       string      `json:"helper_ref"`
	}](s, "connection.setup.complete", plan.Invocation.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	private := decodePrivate(data)
	if private.Receipt == nil {
		return contract.Payload{}, internalError("completion result carries no verified helper receipt")
	}
	if err := s.updateChallenge(ctx, unit, row, challengeCompleted, row.ConsentURL, in.HelperRef); err != nil {
		return contract.Payload{}, err
	}
	if err := s.emit(ctx, unit, "connections.challenge.completed", row.ID, row.Version+1, map[string]any{
		"id": row.ID, "state": challengeCompleted, "connection_id": row.ConnectionID,
	}); err != nil {
		return contract.Payload{}, err
	}
	updated, _, uerr := s.loadChallenge(ctx, unit, row.ID)
	if uerr != nil {
		return contract.Payload{}, uerr
	}
	return s.completed(resourceChallengeOut{Resource: updated.wire()})
}
