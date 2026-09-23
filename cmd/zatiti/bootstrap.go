package main

import (
	"context"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/installation"
	"github.com/zatiti/zatiti/internal/platform"
)

// The controller runs every internal call under the service principal
// identity bootstrap creates in the same exclusive transaction as the owner.
// That principal has no wire credential: it is trusted by holding the
// installation lock and is handed to the in-process application.Internal
// seam as an explicit Actor. An operator may name a different, explicitly
// provisioned service principal with --controller-principal.

// resolveControllerIdentity picks the service principal the controller runs
// as: the explicitly configured one, else the one bootstrap created,
// resolved on one read snapshot through the identity module. Before
// bootstrap, identity reports prerequisite_missing; a revoked principal is
// permission_denied. Both fail serve closed.
func resolveControllerIdentity(ctx context.Context, h *installationHandle, installationID contract.ID) (contract.Actor, error) {
	if h.cfg.ControllerPrincipal != "" {
		return contract.Actor{PrincipalID: contract.ID(h.cfg.ControllerPrincipal), Kind: contract.KindService}, nil
	}
	var actor contract.Actor
	snapshot := contract.Actor{PrincipalID: contract.NewID(), Kind: contract.KindService}
	err := h.db.Read(ctx, snapshot, contract.Scope{InstallationID: installationID}, func(u contract.Unit) error {
		resolved, err := h.identity.ControllerPrincipal(ctx, u)
		if err != nil {
			return err
		}
		actor = resolved
		return nil
	})
	if err != nil {
		return contract.Actor{}, fmt.Errorf("resolving the controller principal: %w", err)
	}
	return actor, nil
}

// committedOwnerCredential reads the authoritative owner identity and opaque
// StoreRef from one database snapshot. It never relies on the process that
// handled installation.init still being alive.
func committedOwnerCredential(ctx context.Context, h *installationHandle, installationID contract.ID) (installation.OwnerCredential, error) {
	var owner installation.OwnerCredential
	actor := contract.Actor{PrincipalID: contract.NewID(), Kind: contract.KindService}
	err := h.db.Read(ctx, actor, contract.Scope{InstallationID: installationID}, func(u contract.Unit) error {
		var readErr error
		owner, readErr = h.installation.OwnerCredential(ctx, u)
		return readErr
	})
	if err != nil {
		return installation.OwnerCredential{}, fmt.Errorf("reading committed owner credential metadata: %w", err)
	}
	if owner.InstallationID != installationID || owner.OwnerID == "" || owner.CredentialID == "" || owner.StoreRef == "" {
		return installation.OwnerCredential{}, &contract.Fault{Code: contract.CodePrerequisiteMissing, Message: "committed owner credential metadata does not match this installation"}
	}
	return owner, nil
}

// recoverOwnerCredential maintains the CLI profile from the same persisted
// StoreRef used for desktop Keychain discovery. It works after a restart that
// occurred between the init commit and profile/discovery publication.
func recoverOwnerCredential(ctx context.Context, h *installationHandle, installationID contract.ID) (string, installation.OwnerCredential, error) {
	owner, err := committedOwnerCredential(ctx, h, installationID)
	if err != nil {
		return "", installation.OwnerCredential{}, err
	}
	credential, err := h.secrets.Get(ctx, owner.StoreRef)
	if err != nil {
		if code := platform.Code(err); code == contract.CodeNotFound || code == contract.CodeControllerUnavailable {
			return "", installation.OwnerCredential{}, &contract.Fault{Code: contract.CodePrerequisiteMissing, Message: "committed owner credential is missing or unreadable in secure custody"}
		}
		return "", installation.OwnerCredential{}, fmt.Errorf("resolving the committed owner credential from secure custody: %w", err)
	}
	defer zero(credential)
	if err := validateHeaderValue(credential); err != nil {
		return "", installation.OwnerCredential{}, &contract.Fault{Code: contract.CodePrerequisiteMissing, Message: "owner " + err.Error()}
	}
	var authenticated contract.Actor
	actor := contract.Actor{PrincipalID: contract.NewID(), Kind: contract.KindService}
	// Identity consumes and zeroes presented bytes; keep custody's original
	// slice alive only long enough to write the compatibility profile below.
	authCopy := append([]byte(nil), credential...)
	defer zero(authCopy)
	if err := h.db.Read(ctx, actor, contract.Scope{InstallationID: installationID}, func(u contract.Unit) error {
		var authErr error
		authenticated, authErr = h.identity.Authenticate(ctx, u, authCopy)
		return authErr
	}); err != nil || authenticated.PrincipalID != owner.OwnerID || authenticated.CredentialID != owner.CredentialID {
		return "", installation.OwnerCredential{}, &contract.Fault{Code: contract.CodePrerequisiteMissing, Message: "committed owner credential is no longer valid"}
	}
	store := profileStore{dir: h.cfg.profilesDir()}
	if err := store.write(defaultProfile, credential); err != nil {
		return "", installation.OwnerCredential{}, fmt.Errorf("handing the owner credential to the local operator: %w", err)
	}
	return defaultProfile, owner, nil
}

// handOverOwnerCredential is the original local handoff entrypoint retained
// for callers that need only the CLI profile name.
func handOverOwnerCredential(ctx context.Context, h *installationHandle) (string, error) {
	id, err := h.initialized(ctx)
	if err != nil {
		return "", err
	}
	profile, _, err := recoverOwnerCredential(ctx, h, id)
	return profile, err
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
