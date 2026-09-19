package main

import (
	"context"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
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

// handOverOwnerCredential runs once, in the process that served
// installation.init, right after the bootstrap transaction committed: it
// hands the owner credential installation custodied to the local operator
// through the owner profile (a 0600 file under the state directory). The
// credential bytes exist in this function's frame and nowhere else; they
// are zeroed before return and never logged. It returns the profile name.
func handOverOwnerCredential(ctx context.Context, h *installationHandle) (string, error) {
	put, ok := h.secrets.latest()
	if !ok {
		return "", &contract.Fault{
			Code:    contract.CodePrerequisiteMissing,
			Message: "installation is initialized but this process custodied no owner credential; the bootstrap ran elsewhere",
		}
	}
	credential, err := h.secrets.Get(ctx, put.ref)
	if err != nil {
		return "", fmt.Errorf("resolving the custodied owner credential: %w", err)
	}
	defer zero(credential)
	if err := validateHeaderValue(credential); err != nil {
		return "", &contract.Fault{Code: contract.CodePrerequisiteMissing, Message: "owner " + err.Error()}
	}
	store := profileStore{dir: h.cfg.profilesDir()}
	if err := store.write(defaultProfile, credential); err != nil {
		return "", fmt.Errorf("handing the owner credential to the local operator: %w", err)
	}
	return defaultProfile, nil
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
