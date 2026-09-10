package application

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"

	"github.com/zatiti/zatiti/internal/contract"
)

// maxSubmissionKey is the wire limit for the submission key: 1..128
// printable ASCII characters. The internal evidence schema allows longer
// keys so retained commands replay verbatim, but a client never supplies
// more than 128.
const maxSubmissionKey = 128

// validateSubmissionKey enforces the wire convention: non-empty, at most 128
// characters, every byte printable ASCII (0x20..0x7E). One-time bootstrap
// mutations do not carry a submission key and never reach this check.
func validateSubmissionKey(key string) error {
	if key == "" {
		return invalidFault("submission_key is required for mutations")
	}
	if len(key) > maxSubmissionKey {
		return invalidFault("submission_key must be at most %d printable ASCII characters", maxSubmissionKey)
	}
	for i := 0; i < len(key); i++ {
		if key[i] < 0x20 || key[i] > 0x7e {
			return invalidFault("submission_key must contain only printable ASCII characters")
		}
	}
	return nil
}

// requestDigest binds the canonical input bytes to the command identity. The
// canonical form sorts keys, rejects duplicate keys and preserves exact
// integer values, so semantically identical requests hash identically while
// any value change produces a different digest.
func requestDigest(input []byte) (contract.Digest, error) {
	canonical, err := contract.Canonicalize(input)
	if err != nil {
		return "", invalidFault("input is not canonicalizable: %v", err)
	}
	sum := sha256.Sum256(canonical)
	return contract.Digest(hex.EncodeToString(sum[:])), nil
}

// submissionCommandInput builds the _evidence.command.begin input for a
// public mutation call.
type submissionCommandInput struct {
	PrincipalID      contract.ID     `json:"principal_id"`
	Operation        string          `json:"operation"`
	OperationVersion int64           `json:"operation_version"`
	SubmissionKey    string          `json:"submission_key"`
	RequestDigest    contract.Digest `json:"request_digest"`
}

// evidenceBeginOutput is the _evidence.command.begin output data.
type evidenceBeginOutput struct {
	CommandID contract.ID    `json:"command_id"`
	Existing  *storedCommand `json:"existing,omitempty"`
}

// storedCommand mirrors $defs/Command, the durable disposition record
// evidence retains for every public mutation.
type storedCommand struct {
	ID               contract.ID     `json:"id"`
	PrincipalID      contract.ID     `json:"principal_id"`
	Operation        string          `json:"operation"`
	OperationVersion int64           `json:"operation_version"`
	SubmissionKey    string          `json:"submission_key"`
	RequestDigest    contract.Digest `json:"request_digest"`
	Status           string          `json:"status"`
	Data             json.RawMessage `json:"data"`
	ErrorCode        string          `json:"error_code"`
	Result           *storedResult   `json:"result"`
}

// storedResult mirrors $defs/Result, the complete original result envelope
// evidence replays verbatim.
type storedResult struct {
	Schema     string          `json:"schema"`
	CommandID  contract.ID     `json:"command_id"`
	Status     string          `json:"status"`
	Data       json.RawMessage `json:"data"`
	Error      *contract.Fault `json:"error"`
	NextCursor *string         `json:"next_cursor"`
}

// replayDisposition converts a retained command into the original result
// envelope. The projection fields (status/data/error_code) must agree with
// the stored result; disagreement is an evidence-owner defect surfaced as
// internal_error rather than a silently different replay.
func replayDisposition(cmd *storedCommand) (contract.Result, error) {
	if cmd.Result == nil {
		return contract.Result{}, internalFault(
			"retained command %s has no result envelope", cmd.ID)
	}
	res := *cmd.Result
	if res.Schema != contract.SchemaResult {
		return contract.Result{}, internalFault(
			"retained command %s carries result schema %q", cmd.ID, res.Schema)
	}
	if res.CommandID != cmd.ID {
		return contract.Result{}, internalFault(
			"retained command %s carries result for command %s", cmd.ID, res.CommandID)
	}
	if res.Status != cmd.Status {
		return contract.Result{}, internalFault(
			"retained command %s status %q disagrees with result %q", cmd.ID, cmd.Status, res.Status)
	}
	if (res.Error == nil) == (cmd.Status == contract.StatusFailed) {
		// A failed command must carry its fault; a completed or accepted
		// command must not. Replaying a different error is forbidden.
		return contract.Result{}, internalFault(
			"retained command %s result envelope disagrees with its status", cmd.ID)
	}
	return contract.Result{
		Schema:    res.Schema,
		CommandID: res.CommandID,
		Payload: contract.Payload{
			Status:     res.Status,
			Data:       res.Data,
			Error:      res.Error,
			NextCursor: res.NextCursor,
		},
	}, nil
}

// itoa64 is a small helper keeping call sites tidy.
func itoa64(v int64) string { return strconv.FormatInt(v, 10) }
