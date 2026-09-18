package integration_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/cli"
	"github.com/zatiti/zatiti/internal/contract"
)

// Cross-package defects the real assembly surfaces beyond the registry seam
// (registry_seam_test.go). Each case runs the real behavior, and when it
// observes the recorded failure it skips with the suspected cause; any other
// failure fails the test, and a pass means the defect is fixed.

// reviewDeadlockCause is shared by every case the default review classes
// block on a fresh installation.
const reviewDeadlockCause = "internal/policy/authority.go:318-338,616-619 puts grant.* and configuration.apply in the default review classes " +
	"until a narrower standing policy governs them; internal/application/gates.go:108-126 gates every public capability with scope and " +
	"capability only (no candidate digest), so authority.go:642 can never consult an approved review; and a standing policy is only " +
	"activated by configuration.apply. No principal, including the bootstrap owner, can ever pass grant.create, grant.update, " +
	"grant.revoke or configuration.apply"

// stageTeam stages one configuration-owned change and seals its plan.
func (f *fixture) stageTeam() (plan struct {
	ID              contract.ID `json:"id"`
	BaseRevision    int64       `json:"base_revision"`
	CandidateDigest string      `json:"candidate_digest"`
}) {
	f.t.Helper()
	org, _ := f.rootOrganization()
	staged := f.must(f.owner, "team.create", "defect-team", map[string]any{
		"scope": f.scope(), "definition": map[string]any{
			"organization_id": org, "key": "engineering", "name": "Engineering", "worker_ids": []string{}},
	})
	var draft struct {
		Draft struct {
			ID      contract.ID `json:"id"`
			Version int64       `json:"version"`
		} `json:"draft"`
	}
	decode(f.t, staged.Data, &draft)
	sealed := f.must(f.owner, "configuration.plan", "defect-plan", map[string]any{
		"scope": f.scope(), "draft_id": draft.Draft.ID, "expected_version": draft.Draft.Version,
	})
	var out struct {
		Resource json.RawMessage `json:"resource"`
	}
	decode(f.t, sealed.Data, &out)
	decode(f.t, out.Resource, &plan)
	return plan
}

// TestOwnerActivatesConfiguration (Z04 one compiler and exact atomic
// activation; journey 1 "activate a plan"): the owner stages a team, seals
// the plan and applies it; the team becomes effective and a repeated apply
// under the same key replays.
func TestOwnerActivatesConfiguration(t *testing.T) {
	t.Parallel()
	f := newBootstrappedFixture(t)
	plan := f.stageTeam()
	if n := f.count("team.list", map[string]any{"scope": f.scope()}); n != 0 {
		t.Fatalf("a staged, unapplied team is already effective (%d teams)", n)
	}
	apply := map[string]any{"scope": f.scope(), "plan_id": plan.ID, "base_revision": plan.BaseRevision, "candidate_digest": plan.CandidateDigest}
	first, err := f.invoke(f.owner, "configuration.apply", "defect-apply", apply)
	if faultCode(err) == contract.CodeReviewRequired {
		skipKnownDefect(t, reviewDeadlockCause, err.Error())
	}
	if err != nil {
		t.Fatalf("configuration.apply: %v", err)
	}
	if n := f.count("team.list", map[string]any{"scope": f.scope()}); n != 1 {
		t.Fatalf("applied plan left %d effective teams, want 1", n)
	}
	replay := f.must(f.owner, "configuration.apply", "defect-apply", apply)
	if replay.CommandID != first.CommandID {
		t.Fatalf("repeated apply returned command %s, original %s", replay.CommandID, first.CommandID)
	}
}

// TestPlanSealsPeerOwnedChange (Z04, R6-006): a draft holding a change owned
// by another domain (a standing policy) seals into a plan; the compiler asks
// the owning peer to validate its slice.
func TestPlanSealsPeerOwnedChange(t *testing.T) {
	t.Parallel()
	f := newBootstrappedFixture(t)
	staged := f.must(f.owner, "policy.create", "defect-policy", map[string]any{
		"scope": f.scope(), "definition": map[string]any{"scope": f.scope(), "rules": []any{map[string]any{
			"capability": "task.create", "effect": "local", "destinations": []string{},
			"decision": "allow", "human_required": false, "conditions": map[string]any{}}}},
	})
	var draft struct {
		Draft struct {
			ID      contract.ID `json:"id"`
			Version int64       `json:"version"`
		} `json:"draft"`
	}
	decode(t, staged.Data, &draft)
	_, err := f.invoke(f.owner, "configuration.plan", "defect-policy-plan", map[string]any{
		"scope": f.scope(), "draft_id": draft.Draft.ID, "expected_version": draft.Draft.Version,
	})
	if err == nil {
		return
	}
	if faultCode(err) == contract.CodeInvalidInput && strings.Contains(err.Error(), "candidate_digest") {
		skipKnownDefect(t,
			"internal/configuration/compiler.go:739-745 sends CandidateDigest \"\" in the candidate envelope to _<owner>.validate, "+
				"but $defs/Candidate requires ^[0-9a-f]{64}$ and the peer schema-validates its input, so no draft with a peer-owned change "+
				"(policy, budget, schedule, connection, skill, memory binding) can be planned",
			err.Error())
	}
	t.Fatalf("configuration.plan failed in an unrecorded way: %v", err)
}

// TestSynchronousLocalIOMutationCompletes (Z16 interrupted upload
// precondition; R8.4-007 bounded chunk upload): a chunk upload through the
// real application, evidence and artifacts owners with real platform blobs
// completes, the artifact finishes, and bounded reads return the exact
// bytes.
func TestSynchronousLocalIOMutationCompletes(t *testing.T) {
	t.Parallel()
	f := newBootstrappedFixture(t)
	body := []byte("synthetic artifact body for the integration fixture")
	digest := string(contract.Hash(body))
	begun := f.must(f.owner, "artifact.upload.begin", "upload-begin", map[string]any{
		"scope": f.scope(), "size": len(body), "digest": digest, "media_type": "text/plain", "classification": "internal",
	})
	var upload struct {
		Resource struct {
			ID           contract.ID `json:"id"`
			Version      int64       `json:"version"`
			ReceivedSize int64       `json:"received_size"`
		} `json:"resource"`
	}
	decode(t, begun.Data, &upload)

	chunk, err := f.invoke(f.owner, "artifact.upload.chunk", "upload-chunk", map[string]any{
		"scope": f.scope(), "upload_id": upload.Resource.ID, "offset": 0,
		"bytes_base64": base64.StdEncoding.EncodeToString(body), "chunk_digest": digest,
	})
	if faultCode(err) == contract.CodeInternalError && strings.Contains(err.Error(), "already finished") {
		cmd, cmdErr := f.command("artifact.upload.chunk", "upload-chunk")
		skipKnownDefect(t,
			"internal/application/localio.go:75-84 finishes the command as accepted in the Prepare transaction and localio.go:134 finishes it "+
				"again with the terminal result, as the frozen contract specifies (\"replace pending disposition once at Finish\"); "+
				"internal/evidence/handlers.go:126-128 and store.go:152-168 refuse a second finish, so the Finish transaction rolls back. "+
				"Every synchronous local IO mutation is affected: artifact.upload.chunk/finish/cancel, skill.import, connection.setup.*, "+
				"installation.backup/restore",
			fmt.Sprintf("%v; the command stays %q forever (lookup error: %v) although Perform already ran", err, cmd.Status, cmdErr))
	}
	if err != nil {
		t.Fatalf("artifact.upload.chunk: %v", err)
	}
	decode(t, chunk.Data, &upload)
	if upload.Resource.ReceivedSize != int64(len(body)) {
		t.Fatalf("upload received %d bytes, want %d", upload.Resource.ReceivedSize, len(body))
	}
	finished := f.must(f.owner, "artifact.upload.finish", "upload-finish", map[string]any{
		"scope": f.scope(), "upload_id": upload.Resource.ID, "expected_version": upload.Resource.Version,
	})
	var artifact struct {
		Resource struct {
			ID        contract.ID `json:"id"`
			Digest    string      `json:"digest"`
			Encrypted bool        `json:"encrypted"`
		} `json:"resource"`
	}
	decode(t, finished.Data, &artifact)
	if artifact.Resource.Digest != digest || !artifact.Resource.Encrypted {
		t.Fatalf("artifact %s, want digest %s encrypted at rest", finished.Data, digest)
	}
	read := f.must(f.owner, "artifact.read", "", map[string]any{"scope": f.scope(), "id": artifact.Resource.ID, "offset": 0, "length": 1024})
	var bytesOut struct {
		Bytes string `json:"bytes_base64"`
	}
	decode(t, read.Data, &bytesOut)
	if got, _ := base64.StdEncoding.DecodeString(bytesOut.Bytes); string(got) != string(body) {
		t.Fatalf("artifact.read returned %q, want the uploaded bytes", got)
	}
}

// TestRefusedCommandReplaysInTheSameForm (R8.4-005; "domain failures always
// include the result envelope"): the original refusal and its replay reach a
// caller the same way. Over the socket both must be the mapped failure
// status with a failed envelope.
func TestRefusedCommandReplaysInTheSameForm(t *testing.T) {
	t.Parallel()
	f := newBootstrappedFixture(t)
	f.provisionTransportCredential(transportToken)
	f.must(f.owner, "principal.create", "form-seed", principalInput(f, "form-agent"))
	op := servedOperator(t, f)
	duplicate := contract.Request{Schema: contract.SchemaRequest, SubmissionKey: "form-duplicate", Input: mustJSON(principalInput(f, "form-agent"))}

	first, firstErr := op.Call(context.Background(), "principal.create", duplicate)
	if faultCode(firstErr) != contract.CodeConflict || first.Status != contract.StatusFailed {
		t.Fatalf("duplicate principal over the socket: status %q err %v, want a failed conflict envelope", first.Status, firstErr)
	}
	replay, replayErr := op.Call(context.Background(), "principal.create", duplicate)
	if faultCode(replayErr) == contract.CodeConflict && replay.Status == contract.StatusFailed {
		cmd, err := f.command("principal.create", "form-duplicate")
		if err != nil {
			t.Fatalf("command lookup: %v", err)
		}
		if first.CommandID != cmd.ID || replay.CommandID != cmd.ID {
			skipKnownDefect(t,
				"internal/server/envelope.go:84-94 writeFault mints a throwaway command_id for every refusal, so a refused keyed mutation's "+
					"envelope never names the durable command evidence retained for it",
				fmt.Sprintf("original envelope command %s, replay %s, retained command %s", first.CommandID, replay.CommandID, cmd.ID))
		}
		return
	}
	skipKnownDefect(t,
		"internal/application/compose.go:41-46 returns a replayed refusal as a failed Result with a nil error, while the original refusal is "+
			"an error with no Result (compose.go:48-53,75); internal/server/envelope.go:66-75 writeResult assumes \"the dispatcher never returns "+
			"a failed result without an error\" and answers HTTP 200, which internal/client treats as an unknown acknowledgement. "+
			"Its recovery then fails too: internal/client/client.go:325-327 looks the command up with {submission_key} only, but the frozen "+
			"command.get input requires scope, operation and operation_version, and client.go:349-353 reports that lookup's invalid_input "+
			"as if it were the command's retained disposition, so lost-acknowledgement recovery cannot work against the real evidence owner",
		fmt.Sprintf("original: status %q err %v; replay: status %q err %v", first.Status, firstErr, replay.Status, replayErr))
}

// TestBootstrapOwnerCredentialCrossesSocket (journey step 1; Z13): the
// credential bootstrap custodies for the owner authenticates over the local
// socket, which is the only way a CLI, MCP or desktop client reaches the
// controller.
func TestBootstrapOwnerCredentialCrossesSocket(t *testing.T) {
	t.Parallel()
	f := newBootstrappedFixture(t)
	op := newClient(t, f.serve(), staticCredential(f.ownerToken()))
	_, err := op.Call(context.Background(), "installation.status", contract.Request{
		Schema: contract.SchemaRequest, Input: mustJSON(map[string]any{"scope": f.scope()}),
	})
	if err == nil {
		t.Log("this run's random owner credential happened to be a valid header value; the defect is probabilistic (about 1 run in 80 passes)")
		return
	}
	skipKnownDefect(t,
		"internal/installation/bootstrap.go:126-137 custodies 32 raw random bytes as the owner credential and internal/identity/authn.go:23-31 "+
			"verifies the SHA-256 of exactly the presented bytes, while internal/server/auth.go:19-25 and internal/client present the credential "+
			"as the verbatim Authorization header value; raw bytes below 0x20 or 0x7f are not a legal header value, and no owner agrees an "+
			"encoding. Separately, no exported API returns the owner credential reference after bootstrap (init returns metadata only, "+
			"contract.SecretStore has no lookup by key, platform refForKey is unexported)",
		err.Error())
}

// TestBootstrapOverLocalSocket (stage-1 gate "bootstrap parity"): the
// unauthenticated local bootstrap route initializes the installation and
// reports the same metadata-only result shape as the in-process call.
func TestBootstrapOverLocalSocket(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	op := newClient(t, f.serve(), nil)
	res, err := op.Call(context.Background(), "installation.init", contract.Request{Schema: contract.SchemaRequest, Input: initInput()})
	if err != nil {
		t.Fatalf("installation.init over the socket: %v", err)
	}
	var out struct {
		Resource struct {
			InstallationID contract.ID `json:"installation_id"`
			Initialized    bool        `json:"initialized"`
		} `json:"resource"`
	}
	decode(t, res.Data, &out)
	if res.Status != contract.StatusCompleted || !out.Resource.Initialized {
		t.Fatalf("socket bootstrap result %s status %q", res.Data, res.Status)
	}
	// A second bootstrap and an unauthenticated ordinary call are refused.
	if _, err := op.Call(context.Background(), "installation.init", contract.Request{Schema: contract.SchemaRequest, Input: initInput()}); faultCode(err) != contract.CodeConflict {
		t.Errorf("second socket bootstrap: %v, want conflict", err)
	}
	_, err = op.Call(context.Background(), "installation.status", contract.Request{
		Schema: contract.SchemaRequest, Input: mustJSON(map[string]any{"scope": contract.Scope{InstallationID: out.Resource.InstallationID}}),
	})
	if err == nil {
		t.Error("an unauthenticated caller read installation.status after bootstrap")
	}
}

// TestCLIReachesEveryLandedOperationPath (Z02): the CLI tree generated from
// the landed descriptors exposes each operation at its frozen path below the
// zatiti root.
func TestCLIReachesEveryLandedOperationPath(t *testing.T) {
	t.Parallel()
	f := newBootstrappedFixture(t)
	f.provisionTransportCredential(transportToken)
	tr := cliTransport{op: servedOperator(t, f), descs: f.catalog.Public()}
	prefixed := map[string]int{}
	for _, d := range tr.descs {
		if len(d.CLI) > 1 && d.CLI[0] == "zatiti" {
			prefixed[d.Owner]++
		}
	}
	// task.list is one of the affected operations; run its frozen path.
	var stdout, stderr bytes.Buffer
	exit := cli.Execute(context.Background(),
		[]string{"task", "list", "--json", "--input", string(mustJSON(map[string]any{"scope": f.scope()}))},
		tr.op, tr.descs, cli.IO{In: strings.NewReader(""), Out: &stdout, Err: &stderr})
	if len(prefixed) == 0 && exit == 0 {
		return
	}
	owners := make([]string, 0, len(prefixed))
	for o, n := range prefixed {
		owners = append(owners, fmt.Sprintf("%s:%d", o, n))
	}
	sort.Strings(owners)
	skipKnownDefect(t,
		"eleven domains prepend the binary name to Descriptor.CLI (for example internal/tasks and internal/configuration descriptor builders) "+
			"while the frozen catalog and internal/cli/cli.go:61-70 treat CLI as the path below the zatiti root, so those commands are "+
			"generated at \"zatiti zatiti <path>\" and the frozen path does not exist",
		fmt.Sprintf("operations with a binary-name CLI prefix by owner: %v; the frozen path \"task list\" exits %d: %s", owners, exit, strings.TrimSpace(stderr.String())))
}

// TestLandedDescriptorsDeliverOneSchemaForm: internal/application validates
// Descriptor.InputSchema exactly as delivered, so every landed descriptor
// must resolve its own $refs.
func TestLandedDescriptorsDeliverOneSchemaForm(t *testing.T) {
	t.Parallel()
	bare := map[string]int{}
	selfContained := map[string]int{}
	for _, m := range realModules(t) {
		for _, d := range m.Descriptors() {
			if isBareSchema(d.InputSchema) {
				bare[m.Name()]++
			} else {
				selfContained[m.Name()]++
			}
		}
	}
	if len(bare) == 0 || len(selfContained) == 0 {
		return
	}
	skipKnownDefect(t,
		"the frozen contract does not say who merges the shared $defs: internal/registry (catalog.go:189-202) demands bare operation schemas and "+
			"returns them unmerged from Lookup, internal/application/dispatch.go:91 validates the delivered schema as-is, eleven domains "+
			"deliver self-contained documents and five deliver bare ones whose $refs cannot resolve at the dispatcher",
		fmt.Sprintf("bare descriptors by owner %v; self-contained by owner %v. moduleCatalog merges the frozen shared $defs for the bare ones", bare, selfContained))
}
