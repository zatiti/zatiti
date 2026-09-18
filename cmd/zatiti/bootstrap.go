package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/zatiti/zatiti/internal/contract"
)

// The controller runs every internal call under an explicitly provisioned
// service principal. The identity brief assigns creating "scoped service
// identities" to the exclusive bootstrap transaction, but the landed
// bootstrap (internal/identity/internal_ops.go bootstrap) creates only the
// human owner. Until that lands, the entrypoint completes bootstrap in the
// same process, under the same held installation lock, before the owner
// credential is handed to anyone: it authenticates the freshly custodied
// owner credential through the application and provisions the controller
// principal with the landed public operations principal.create and
// grant.create. Nothing is inserted behind the application's back. The
// resulting principal is recorded in a protected file so later starts attach
// it without touching the owner credential again; an operator may also name
// an explicitly provisioned service principal with --controller-principal.

const (
	controllerPrincipalName  = "controller"
	controllerIdentitySchema = "zatiti.controller-identity/v1"
)

// controllerIdentity is the recorded service principal of this state
// directory. It carries no secret: the controller never authenticates over
// the wire, it is trusted by holding the installation lock.
type controllerIdentity struct {
	Schema         string      `json:"schema"`
	InstallationID contract.ID `json:"installation_id"`
	PrincipalID    contract.ID `json:"principal_id"`
	GrantID        contract.ID `json:"grant_id,omitempty"`
}

// readControllerIdentity loads the recorded identity, reporting absence
// distinctly from corruption.
func readControllerIdentity(path string) (controllerIdentity, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return controllerIdentity{}, false, nil
	}
	if err != nil {
		return controllerIdentity{}, false, fmt.Errorf("reading the controller identity record: %w", err)
	}
	var rec controllerIdentity
	if err := json.Unmarshal(data, &rec); err != nil {
		return controllerIdentity{}, false, fmt.Errorf("decoding the controller identity record: %w", err)
	}
	if rec.Schema != controllerIdentitySchema || rec.PrincipalID == "" || rec.InstallationID == "" {
		return controllerIdentity{}, false, errors.New("controller identity record is incomplete")
	}
	return rec, true, nil
}

func writeControllerIdentity(path string, rec controllerIdentity) error {
	rec.Schema = controllerIdentitySchema
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the controller identity record: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), filePrivate); err != nil {
		return fmt.Errorf("writing the controller identity record: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("publishing the controller identity record: %w", err)
	}
	return nil
}

// resolveControllerIdentity picks the service principal the controller runs
// as: the explicitly configured one, else the recorded one, else nothing.
// The caller decides whether "nothing" is a bootstrap in progress or a fatal
// prerequisite.
func resolveControllerIdentity(cfg config, installationID contract.ID) (contract.Actor, bool, error) {
	if cfg.ControllerPrincipal != "" {
		return contract.Actor{PrincipalID: contract.ID(cfg.ControllerPrincipal), Kind: contract.KindService}, true, nil
	}
	rec, found, err := readControllerIdentity(cfg.identityPath())
	if err != nil {
		return contract.Actor{}, false, err
	}
	if !found {
		return contract.Actor{}, false, nil
	}
	if rec.InstallationID != installationID {
		return contract.Actor{}, false, fmt.Errorf("controller identity record belongs to installation %s, not %s", rec.InstallationID, installationID)
	}
	return contract.Actor{PrincipalID: rec.PrincipalID, Kind: contract.KindService}, true, nil
}

// missingIdentityFault names the exact gap when an initialized installation
// has no controller identity this process can attach.
func missingIdentityFault() *contract.Fault {
	return &contract.Fault{
		Code: contract.CodePrerequisiteMissing,
		Message: "no controller service identity: identity bootstrap creates only the human owner and no " +
			"controller identity record exists in the state directory; provision a service principal " +
			"(principal create, grant create) as the owner and start serve with --controller-principal",
	}
}

// bootstrapOutcome is what completeBootstrap established.
type bootstrapOutcome struct {
	Identity contract.Actor
	Profile  string
}

// completeBootstrap runs once, in the process that served installation.init,
// right after the bootstrap transaction committed: it hands the owner
// credential to the local operator through the owner profile, provisions
// the controller principal with the owner's bootstrap authority, and
// records it. The owner credential bytes exist in this function's frame and
// nowhere else; they are zeroed before return and never logged.
func completeBootstrap(ctx context.Context, h *installationHandle, installationID contract.ID) (bootstrapOutcome, error) {
	put, ok := h.secrets.latest()
	if !ok {
		return bootstrapOutcome{}, &contract.Fault{
			Code:    contract.CodePrerequisiteMissing,
			Message: "installation is initialized but this process custodied no owner credential; the bootstrap ran elsewhere",
		}
	}
	credential, err := h.secrets.Get(ctx, put.ref)
	if err != nil {
		return bootstrapOutcome{}, fmt.Errorf("resolving the custodied owner credential: %w", err)
	}
	defer zero(credential)
	if err := validateHeaderValue(credential); err != nil {
		return bootstrapOutcome{}, &contract.Fault{Code: contract.CodePrerequisiteMissing, Message: "owner " + err.Error()}
	}

	// The owner credential is handed over first: it is the owner's, and a
	// failure to provision the controller must never leave an initialized
	// installation whose owner cannot authenticate.
	store := profileStore{dir: h.cfg.profilesDir()}
	if err := store.write(defaultProfile, credential); err != nil {
		return bootstrapOutcome{}, fmt.Errorf("handing the owner credential to the local operator: %w", err)
	}
	owner, err := h.app.Authenticate(ctx, append([]byte(nil), credential...))
	if err != nil {
		return bootstrapOutcome{}, fmt.Errorf("authenticating the bootstrap owner: %w", err)
	}
	identity, err := provisionControllerIdentity(ctx, h, owner, installationID)
	if err != nil {
		return bootstrapOutcome{}, err
	}
	if err := writeControllerIdentity(h.cfg.identityPath(), identity); err != nil {
		return bootstrapOutcome{}, err
	}
	return bootstrapOutcome{
		Identity: contract.Actor{PrincipalID: identity.PrincipalID, Kind: contract.KindService},
		Profile:  defaultProfile,
	}, nil
}

// provisionControllerIdentity creates the controller's service principal and
// its installation-wide grant as the owner, through the public operations.
// Submission keys are deterministic per installation so a retry after a
// crash replays the same commands instead of minting a second principal.
func provisionControllerIdentity(ctx context.Context, h *installationHandle, owner contract.Actor, installationID contract.ID) (controllerIdentity, error) {
	scope := contract.Scope{InstallationID: installationID}
	var created struct {
		Resource struct {
			ID contract.ID `json:"id"`
		} `json:"resource"`
	}
	if err := invokeInto(ctx, h, owner, "principal.create", "controller-identity/"+string(installationID)+"/principal", map[string]any{
		"scope": scope,
		"definition": map[string]any{
			"kind": contract.KindService, "name": controllerPrincipalName, "scope": scope, "revoked": false,
		},
	}, &created); err != nil {
		return controllerIdentity{}, fmt.Errorf("provisioning the controller principal: %w", err)
	}
	var granted struct {
		Resource struct {
			ID contract.ID `json:"id"`
		} `json:"resource"`
	}
	if err := invokeInto(ctx, h, owner, "grant.create", "controller-identity/"+string(installationID)+"/grant", map[string]any{
		"scope": scope,
		"definition": map[string]any{
			"principal_id": created.Resource.ID, "scope": scope,
			"capabilities": []string{"*"}, "destinations": []string{}, "denied": false,
		},
	}, &granted); err != nil {
		if faultCode(err) == contract.CodeReviewRequired {
			return controllerIdentity{}, &contract.Fault{
				Code: contract.CodePrerequisiteMissing,
				Message: "controller service identity cannot be granted authority: grant.create is a default review class " +
					"with no review path at bootstrap, and identity bootstrap creates no scoped service identity; " +
					"the controller principal " + string(created.Resource.ID) + " exists without authority (" + err.Error() + ")",
			}
		}
		return controllerIdentity{}, fmt.Errorf("granting the controller principal: %w", err)
	}
	return controllerIdentity{
		InstallationID: installationID,
		PrincipalID:    created.Resource.ID,
		GrantID:        granted.Resource.ID,
	}, nil
}

// invokeInto runs one keyed public mutation through the application and
// decodes its data.
func invokeInto(ctx context.Context, h *installationHandle, actor contract.Actor, operation, key string, input any, out any) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("encoding %s input: %w", operation, err)
	}
	res, err := h.app.Invoke(ctx, actor, operation, contract.Request{
		Schema: contract.SchemaRequest, SubmissionKey: key, Input: raw,
	})
	if err != nil {
		return err
	}
	if res.Status != contract.StatusCompleted {
		if res.Error != nil {
			return res.Error
		}
		return fmt.Errorf("%s returned status %s", operation, res.Status)
	}
	if err := json.Unmarshal(res.Data, out); err != nil {
		return fmt.Errorf("decoding %s data: %w", operation, err)
	}
	return nil
}

func faultCode(err error) string {
	var f *contract.Fault
	if errors.As(err, &f) {
		return f.Code
	}
	return ""
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
