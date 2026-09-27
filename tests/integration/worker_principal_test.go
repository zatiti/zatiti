package integration_test

import (
	"context"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// Revision 21: every configured worker is an identity principal of kind
// worker with the same id, so contract.WorkerOperator can resolve its own
// actor. These tests run against the real assembled owners.

// workerSend asks WorkerOperator to post one message as worker.
func (f *fixture) workerSend(worker, conversation contract.ID, body string) error {
	f.t.Helper()
	turnID, proposalID := contract.NewID(), "call_"+body
	_, err := f.app.ExecuteWorker(context.Background(), contract.WorkerRequest{
		TurnID: turnID, ProposalID: proposalID, WorkerID: worker,
		Scope:     contract.Scope{InstallationID: f.installationID, WorkerID: worker},
		Operation: "conversation.message.send", Version: 1,
		Input: mustJSON(map[string]any{
			"scope": f.scope(), "conversation_id": conversation, "message_id": contract.NewID(),
			"body": body, "attachments": []any{}, "task_ids": []contract.ID{},
		}),
		SubmissionKey: "worker-turn/" + string(turnID) + "/" + proposalID,
	})
	return err
}

func (f *fixture) directConversation(key string, members ...contract.ID) contract.ID {
	f.t.Helper()
	res := f.must(f.owner, "conversation.create", key, map[string]any{
		"scope": f.scope(), "kind": "direct", "participant_ids": members, "title": key,
	})
	var out struct {
		Resource struct {
			ID contract.ID `json:"id"`
		} `json:"resource"`
	}
	decode(f.t, res.Data, &out)
	return out.Resource.ID
}

func (f *fixture) messageBodies(conversation contract.ID) []string {
	f.t.Helper()
	res := f.must(f.owner, "conversation.message.list", "", map[string]any{"scope": f.scope(), "conversation_id": conversation})
	var out struct {
		Items []struct {
			Body     string      `json:"body"`
			SenderID contract.ID `json:"sender_id"`
		} `json:"items"`
	}
	decode(f.t, res.Data, &out)
	bodies := make([]string, 0, len(out.Items))
	for _, it := range out.Items {
		bodies = append(bodies, it.Body)
	}
	return bodies
}

type workerGrant struct {
	ID           contract.ID `json:"id"`
	Version      int64       `json:"version"`
	PrincipalID  contract.ID `json:"principal_id"`
	Capabilities []string    `json:"capabilities"`
}

func (f *fixture) grantsOf(principal contract.ID) []workerGrant {
	f.t.Helper()
	res := f.must(f.owner, "grant.list", "", map[string]any{"scope": f.scope(), "limit": 200})
	var out struct {
		Items []workerGrant `json:"items"`
	}
	decode(f.t, res.Data, &out)
	var mine []workerGrant
	for _, g := range out.Items {
		if g.PrincipalID == principal {
			mine = append(mine, g)
		}
	}
	return mine
}

func (f *fixture) principalRow(id contract.ID) (principalRow, bool) {
	for _, p := range f.principals() {
		if p.ID == id {
			return p, true
		}
	}
	return principalRow{}, false
}

// TestBootstrapChiefIsAWorkerPrincipalThatCanSendItsOwnReply closes the gap
// TestWorkerReplyDeliveryStopsAtMissingWorkerPrincipal (PR #77) documents:
// the bootstrap chief is a registered worker principal holding exactly the
// worker-visible allowlist, so WorkerOperator accepts its own message send
// and the owner reads it back.
func TestBootstrapChiefIsAWorkerPrincipalThatCanSendItsOwnReply(t *testing.T) {
	t.Parallel()
	f := newBootstrappedFixture(t)
	_, chief := f.rootOrganization()

	p, ok := f.principalRow(chief)
	if !ok || p.Kind != contract.KindWorker || p.Revoked {
		t.Fatalf("chief principal = %+v (found %v), want a live worker principal with the chief's id", p, ok)
	}
	grants := f.grantsOf(chief)
	if len(grants) != 1 {
		t.Fatalf("chief grants = %+v, want exactly the one standing grant", grants)
	}
	want := append(contract.WorkerVisibleOperations(), "messaging.disclosure.deliver")
	if strings.Join(grants[0].Capabilities, ",") != strings.Join(want, ",") {
		t.Fatalf("chief capabilities = %v, want exactly the worker-visible allowlist plus message delivery %v", grants[0].Capabilities, want)
	}

	conv := f.directConversation("chief-reply", f.owner.PrincipalID, chief)
	if err := f.workerSend(chief, conv, "acknowledged"); err != nil {
		t.Fatalf("chief reply send: %v", err)
	}
	if got := f.messageBodies(conv); len(got) != 1 || got[0] != "acknowledged" {
		t.Fatalf("owner history = %v, want the chief's one reply", got)
	}
}

// TestWorkerPrincipalRefusesAnythingBeyondItsGrant proves the principal's
// authority is exactly its standing grant: an operation off the allowlist
// is refused, and once the owner revokes the standing grant even the
// worker's own message send is refused and nothing is posted.
func TestWorkerPrincipalRefusesAnythingBeyondItsGrant(t *testing.T) {
	t.Parallel()
	f := newBootstrappedFixture(t)
	_, chief := f.rootOrganization()
	conv := f.directConversation("chief-bounded", f.owner.PrincipalID, chief)

	_, err := f.app.ExecuteWorker(context.Background(), contract.WorkerRequest{
		TurnID: contract.NewID(), ProposalID: "grant-self", WorkerID: chief,
		Scope:     contract.Scope{InstallationID: f.installationID, WorkerID: chief},
		Operation: "grant.create", Version: 1,
		Input: mustJSON(map[string]any{"scope": f.scope(), "definition": map[string]any{
			"principal_id": chief, "scope": f.scope(), "capabilities": []string{"*"}, "destinations": []string{}, "denied": false,
		}}),
	})
	if faultCode(err) != contract.CodePermissionDenied {
		t.Fatalf("worker grant.create err = %v, want permission_denied", err)
	}

	grants := f.grantsOf(chief)
	if len(grants) != 1 {
		t.Fatalf("chief grants = %+v, want one", grants)
	}
	f.must(f.owner, "grant.revoke", "revoke-chief-grant", map[string]any{
		"scope": f.scope(), "id": grants[0].ID, "expected_version": grants[0].Version,
	})
	if err := f.workerSend(chief, conv, "after-revoke"); faultCode(err) != contract.CodePermissionDenied {
		t.Fatalf("send after grant revocation err = %v, want permission_denied", err)
	}
	if got := f.messageBodies(conv); len(got) != 0 {
		t.Fatalf("owner history = %v, want nothing posted without authority", got)
	}
}

// TestWorkerLifecycleRegistersAndRetiresItsPrincipal covers worker.create
// and worker.archive through the configuration compiler.
func TestWorkerLifecycleRegistersAndRetiresItsPrincipal(t *testing.T) {
	t.Parallel()
	f := newBootstrappedFixture(t)
	org, _ := f.rootOrganization()
	f.activate("helper-worker", "worker.create", map[string]any{
		"definition": map[string]any{
			"organization_id": org, "key": "helper", "name": "Helper",
			"purpose": "integration helper", "instructions": "help",
			"skill_versions": []any{}, "bindings": []contract.ID{},
			"profile": nil, "limits": nil,
		},
	})
	worker := f.findWorkerByKey("helper")
	p, ok := f.principalRow(worker)
	if !ok || p.Kind != contract.KindWorker || p.Revoked || p.Scope.OrganizationID != org {
		t.Fatalf("new worker principal = %+v (found %v), want live worker principal in org %s", p, ok, org)
	}
	conv := f.directConversation("helper-conv", f.owner.PrincipalID, worker)
	if err := f.workerSend(worker, conv, "hello"); err != nil {
		t.Fatalf("new worker send: %v", err)
	}

	f.activate("helper-archive", "worker.archive", map[string]any{"id": worker, "expected_version": 1})
	p, _ = f.principalRow(worker)
	if !p.Revoked {
		t.Fatalf("archived worker principal = %+v, want revoked", p)
	}
	if err := f.workerSend(worker, conv, "after-archive"); faultCode(err) != contract.CodePermissionDenied {
		t.Fatalf("archived worker send err = %v, want permission_denied", err)
	}
}
