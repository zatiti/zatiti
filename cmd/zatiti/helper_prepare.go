package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// helperPrepareLocked is the common first half of terminal and AppKit setup.
// The caller holds the per-connection intent lock. A successful return has a
// durable challenge and can be resumed after the native UI collects a secret.
func helperPrepareLocked(ctx context.Context, store helperIntentStore, op contract.Operator, secrets contract.SecretStore, installationID, connectionID contract.ID) (*helperIntent, helperConnectionRecord, bool, error) {
	intent, err := store.read(string(connectionID))
	if err != nil {
		return nil, helperConnectionRecord{}, false, fmt.Errorf("reading helper recovery state: %w", err)
	}
	if intent != nil && intent.InstallationID != string(installationID) {
		return nil, helperConnectionRecord{}, false, errors.New("helper recovery state belongs to a different installation")
	}
	conn, err := helperConnection(ctx, op, installationID, connectionID)
	if err != nil {
		return nil, helperConnectionRecord{}, false, fmt.Errorf("connection.get: %w", err)
	}
	if intent == nil {
		intent = &helperIntent{Schema: helperIntentSchema, InstallationID: string(installationID), ConnectionID: string(connectionID), ConnectionVersion: conn.Version, AccountIdentity: conn.AccountIdentity,
			BeginKey: "helper-begin/" + string(contract.NewID()), CompleteKey: "helper-complete/" + string(contract.NewID()), CredentialName: "connections/credential/" + string(contract.NewID())}
		if err := store.write(*intent); err != nil {
			return nil, helperConnectionRecord{}, false, fmt.Errorf("saving helper begin intent: %w", err)
		}
	} else if intent.AccountIdentity != conn.AccountIdentity && conn.CredentialRef != intent.CredentialRef {
		return nil, helperConnectionRecord{}, false, errors.New("connection account changed while setup was pending; repair is required")
	}
	if intent.ChallengeID == "" {
		res, found, err := helperCommandLookup(ctx, op, installationID, "connection.setup.begin", intent.BeginKey)
		if err != nil {
			return nil, helperConnectionRecord{}, false, fmt.Errorf("reconciling setup.begin: %w", err)
		}
		if !found {
			if conn.Version != intent.ConnectionVersion {
				return nil, helperConnectionRecord{}, false, errors.New("connection changed before setup.begin could be confirmed; repair is required")
			}
			res, err = callOperation(ctx, op, "connection.setup.begin", map[string]any{"scope": map[string]any{"installation_id": installationID}, "connection_id": connectionID, "expected_version": intent.ConnectionVersion, "method": "store_reference"}, intent.BeginKey)
			if err != nil {
				return nil, helperConnectionRecord{}, false, fmt.Errorf("connection.setup.begin needs reconciliation with its original submission key: %w", err)
			}
		}
		var body struct {
			Resource struct {
				ID        contract.ID `json:"id"`
				Version   int64       `json:"version"`
				ExpiresAt time.Time   `json:"expires_at"`
			} `json:"resource"`
		}
		if res.Status != contract.StatusCompleted || json.Unmarshal(res.Data, &body) != nil || !helperIDPattern.MatchString(string(body.Resource.ID)) || body.Resource.Version < 1 || !body.Resource.ExpiresAt.After(time.Now()) {
			return nil, helperConnectionRecord{}, false, errors.New("setup.begin did not return a live challenge; repair is required")
		}
		intent.ChallengeID, intent.ChallengeVersion, intent.ExpiresAt = string(body.Resource.ID), body.Resource.Version, body.Resource.ExpiresAt
		if err := store.write(*intent); err != nil {
			return nil, helperConnectionRecord{}, false, fmt.Errorf("saving helper challenge: %w", err)
		}
	}
	challenge, err := helperChallenge(ctx, op, installationID, contract.ID(intent.ChallengeID))
	if err != nil {
		return nil, helperConnectionRecord{}, false, fmt.Errorf("connection.setup.status: %w", err)
	}
	if challenge.ConnectionID != string(connectionID) {
		return nil, helperConnectionRecord{}, false, errors.New("challenge belongs to a different connection")
	}
	if challenge.State == "completed" {
		if intent.CredentialRef != "" && conn.CredentialRef == intent.CredentialRef {
			if err := store.remove(string(connectionID)); err != nil {
				return nil, helperConnectionRecord{}, false, err
			}
			return nil, conn, true, nil
		}
		return nil, helperConnectionRecord{}, false, errors.New("completed challenge does not match the recorded credential; repair is required")
	}
	if challenge.State == "cancelled" || challenge.State == "expired" || challenge.State == "failed" {
		return nil, helperConnectionRecord{}, false, helperDiscardTerminal(ctx, store, secrets, *intent, conn.CredentialRef)
	}
	if challenge.State != "external_action_required" || challenge.Version != intent.ChallengeVersion || !challenge.ExpiresAt.Equal(intent.ExpiresAt) || !challenge.ExpiresAt.After(time.Now()) {
		return nil, helperConnectionRecord{}, false, errors.New("setup challenge is pending or changed; inspect authoritative status before retrying")
	}
	return intent, conn, false, nil
}
