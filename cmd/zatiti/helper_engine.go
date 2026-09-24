package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/platform"
)

// runHelperEngine is shared by the terminal command and the future in-process
// AppKit/c-archive entrypoint. Only the reader supplies credential bytes;
// public operations carry IDs, an opaque receipt and stable submission keys.
func runHelperEngine(ctx context.Context, stateDir string, op contract.Operator, secrets contract.SecretStore, input io.Reader, diagnostics io.Writer, installationID, connectionID contract.ID) error {
	store, err := newHelperIntentStore(stateDir)
	if err != nil {
		return fmt.Errorf("opening helper recovery state: %w", err)
	}
	unlock, err := store.lock(string(connectionID))
	if err != nil {
		return fmt.Errorf("locking helper recovery state: %w", err)
	}
	defer unlock()
	intent, conn, done, err := helperPrepareLocked(ctx, store, op, secrets, installationID, connectionID)
	if err != nil {
		return err
	}
	if done {
		return nil
	}
	scope := map[string]any{"installation_id": installationID}
	if intent.CredentialRef == "" {
		// A crash can occur after Put but before intent publication. The name
		// was durable before Put, so an existing item is reused, never overwritten.
		ref, lookupErr := secrets.Lookup(ctx, intent.CredentialName)
		switch {
		case lookupErr == nil:
			intent.CredentialRef = ref
		case helperNotFound(lookupErr):
			if conn.Version != intent.ConnectionVersion {
				return errors.New("connection changed before credential capture; repair is required")
			}
			_, _ = fmt.Fprintf(diagnostics, "zatiti: enter the credential for connection %s (account %s); input is not echoed and never leaves this terminal as an operation argument\n", connectionID, conn.AccountIdentity)
			secret, readErr := readSecretLine(input)
			if readErr != nil {
				return fmt.Errorf("reading the credential: %w", readErr)
			}
			ref, err = secrets.Put(ctx, intent.CredentialName, secret)
			zero(secret)
			if err != nil {
				return fmt.Errorf("writing the credential to the local secret store: %w", err)
			}
			intent.CredentialRef = ref
		default:
			return fmt.Errorf("checking a pending credential write: %w", lookupErr)
		}
		if err := store.write(*intent); err != nil {
			return fmt.Errorf("saving helper credential reference: %w", err)
		}
	}
	if intent.CredentialRef == "" {
		return errors.New("stored credential has no opaque reference")
	}
	res, found, err := helperCommandLookup(ctx, op, installationID, "connection.setup.complete", intent.CompleteKey)
	if err != nil {
		return fmt.Errorf("reconciling setup.complete: %w", err)
	}
	if found {
		if res.Status != contract.StatusCompleted {
			return errors.New("retained setup.complete is not completed; repair is required")
		}
	} else {
		key, err := ensureHelperReceiptKey(ctx, secrets)
		if err != nil {
			return err
		}
		receipt, receiptErr := mintHelperReceipt(key, helperPayload{
			ChallengeID: contract.ID(intent.ChallengeID), CredentialRef: intent.CredentialRef,
			AccountIdentity: intent.AccountIdentity, ExpiresAt: intent.ExpiresAt,
		})
		zero(key)
		if receiptErr != nil {
			return receiptErr
		}
		if _, err := callOperation(ctx, op, "connection.setup.complete", map[string]any{
			"scope": scope, "challenge_id": intent.ChallengeID, "expected_version": intent.ChallengeVersion, "helper_ref": receipt,
		}, intent.CompleteKey); err != nil {
			return fmt.Errorf("connection.setup.complete needs reconciliation with its original submission key: %w", err)
		}
	}
	challenge, err := helperChallenge(ctx, op, installationID, contract.ID(intent.ChallengeID))
	if err != nil || challenge.State != "completed" {
		return errors.New("setup completion is awaiting authoritative challenge status")
	}
	conn, err = helperConnection(ctx, op, installationID, connectionID)
	if err != nil || conn.CredentialRef != intent.CredentialRef {
		return errors.New("setup completion is awaiting authoritative connection status")
	}
	if err := store.remove(string(connectionID)); err != nil {
		return fmt.Errorf("clearing completed helper intent: %w", err)
	}
	_, _ = fmt.Fprintf(diagnostics, "zatiti: connection %s setup complete\n", connectionID)
	return nil
}

type helperConnectionRecord struct {
	Version         int64  `json:"version"`
	Provider        string `json:"provider"`
	AccountIdentity string `json:"account_identity"`
	CredentialRef   string `json:"credential_ref"`
}

func helperConnection(ctx context.Context, op contract.Operator, installationID, connectionID contract.ID) (helperConnectionRecord, error) {
	res, err := callOperation(ctx, op, "connection.get", map[string]any{"scope": map[string]any{"installation_id": installationID}, "id": connectionID}, "")
	if err != nil {
		return helperConnectionRecord{}, err
	}
	var body struct {
		Resource helperConnectionRecord `json:"resource"`
	}
	if err := json.Unmarshal(res.Data, &body); err != nil || body.Resource.Version < 1 {
		return helperConnectionRecord{}, errors.New("connection.get returned malformed metadata")
	}
	return body.Resource, nil
}

type helperChallengeRecord struct {
	ID           string    `json:"id"`
	Version      int64     `json:"version"`
	ConnectionID string    `json:"connection_id"`
	State        string    `json:"state"`
	ExpiresAt    time.Time `json:"expires_at"`
}

func helperChallenge(ctx context.Context, op contract.Operator, installationID, challengeID contract.ID) (helperChallengeRecord, error) {
	res, err := callOperation(ctx, op, "connection.setup.status", map[string]any{"scope": map[string]any{"installation_id": installationID}, "challenge_id": challengeID}, "")
	if err != nil {
		return helperChallengeRecord{}, err
	}
	var body struct {
		Resource helperChallengeRecord `json:"resource"`
	}
	if err := json.Unmarshal(res.Data, &body); err != nil || body.Resource.ID != string(challengeID) || body.Resource.Version < 1 {
		return helperChallengeRecord{}, errors.New("setup.status returned malformed metadata")
	}
	return body.Resource, nil
}

func helperCommandLookup(ctx context.Context, op contract.Operator, installationID contract.ID, operation, key string) (contract.Result, bool, error) {
	res, err := callOperation(ctx, op, "command.get", map[string]any{
		"scope": map[string]any{"installation_id": installationID}, "submission_key": key, "operation": operation, "operation_version": 1,
	}, "")
	if helperNotFound(err) {
		return contract.Result{}, false, nil
	}
	if err != nil {
		return contract.Result{}, false, err
	}
	var body struct {
		Resource struct {
			Operation        string          `json:"operation"`
			OperationVersion int64           `json:"operation_version"`
			SubmissionKey    string          `json:"submission_key"`
			Result           contract.Result `json:"result"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(res.Data, &body); err != nil || body.Resource.Operation != operation || body.Resource.OperationVersion != 1 || body.Resource.SubmissionKey != key {
		return contract.Result{}, false, errors.New("command.get returned an unrelated command")
	}
	return body.Resource.Result, true, nil
}

func helperNotFound(err error) bool {
	if err == nil {
		return false
	}
	if platform.Code(err) == contract.CodeNotFound {
		return true
	}
	var fault *contract.Fault
	return errors.As(err, &fault) && fault.Code == contract.CodeNotFound
}

func helperDiscardTerminal(ctx context.Context, store helperIntentStore, secrets contract.SecretStore, intent helperIntent, effectiveRef string) error {
	ref := intent.CredentialRef
	if ref == "" {
		var err error
		ref, err = secrets.Lookup(ctx, intent.CredentialName)
		if err != nil && !helperNotFound(err) {
			return err
		}
	}
	if ref != "" && ref != effectiveRef {
		if err := secrets.Delete(ctx, ref); err != nil && !helperNotFound(err) {
			return err
		}
	}
	if err := store.remove(intent.ConnectionID); err != nil {
		return err
	}
	return errors.New("setup challenge is no longer live; begin a new explicit setup")
}
