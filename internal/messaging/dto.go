package messaging

import (
	"github.com/zatiti/zatiti/internal/contract"
)

// Wire DTOs mirror the frozen local schema definitions one to one. Fields
// use json tags exactly as the schemas name them; strict decoding rejects
// anything else. Optional UUID and string fields stay empty when absent.

// wireScope mirrors #/$defs/Scope.
type wireScope struct {
	InstallationID contract.ID `json:"installation_id"`
	OrganizationID contract.ID `json:"organization_id,omitempty"`
	ProjectID      contract.ID `json:"project_id,omitempty"`
	WorkerID       contract.ID `json:"worker_id,omitempty"`
	TaskID         contract.ID `json:"task_id,omitempty"`
}

// scopeFromContract converts the unit scope to its wire shape.
func scopeFromContract(sc contract.Scope) wireScope {
	return wireScope{
		InstallationID: sc.InstallationID,
		OrganizationID: sc.OrganizationID,
		ProjectID:      sc.ProjectID,
		WorkerID:       sc.WorkerID,
		TaskID:         sc.TaskID,
	}
}

// toContract converts a wire scope to the contract shape.
func (w wireScope) toContract() contract.Scope {
	return contract.Scope{
		InstallationID: w.InstallationID,
		OrganizationID: w.OrganizationID,
		ProjectID:      w.ProjectID,
		WorkerID:       w.WorkerID,
		TaskID:         w.TaskID,
	}
}

// wireArtifactRef mirrors #/$defs/ArtifactRef.
type wireArtifactRef struct {
	ID     contract.ID     `json:"id"`
	Digest contract.Digest `json:"digest"`
}

// wireMessage mirrors #/$defs/Message, the durable mail record.
type wireMessage struct {
	ID             contract.ID       `json:"id"`
	Version        contract.Version  `json:"version"`
	SenderID       contract.ID       `json:"sender_id"`
	RecipientIDs   []contract.ID     `json:"recipient_ids"`
	Scope          wireScope         `json:"scope"`
	TaskIDs        []contract.ID     `json:"task_ids"`
	Body           string            `json:"body"`
	Attachments    []wireArtifactRef `json:"attachments"`
	State          string            `json:"state"`
	CreatedAt      string            `json:"created_at"`
	ConversationID contract.ID       `json:"conversation_id,omitempty"`
}

// wireConversation mirrors #/$defs/Conversation.
type wireConversation struct {
	ID                  contract.ID      `json:"id"`
	Version             contract.Version `json:"version"`
	Scope               wireScope        `json:"scope"`
	Kind                string           `json:"kind"`
	ParticipantIDs      []contract.ID    `json:"participant_ids"`
	Title               string           `json:"title"`
	Pinned              bool             `json:"pinned"`
	LastMeaningfulEvent string           `json:"last_meaningful_event,omitempty"`
}

// Conversation kinds.
const (
	kindDirect = "direct"
	kindGroup  = "group"
)

// Message delivery states. Messages commit as admitted (recipient inboxes
// are durable in the same transaction); acknowledged is the terminal
// delivery state after the recipient's ack.
const (
	messageStateSubmitted    = "submitted"
	messageStateAdmitted     = "admitted"
	messageStateAcknowledged = "acknowledged"
)

// Recipient inbox row states.
const (
	recipientAdmitted     = "admitted"
	recipientAcknowledged = "acknowledged"
)

// bootstrapKey is the stable key of the pinned personal-chief conversation
// inside one installation. Bootstrap is idempotent by this key.
const bootstrapKey = "personal-chief"

// bootstrapTitle is the wire title of the pinned personal-chief conversation.
const bootstrapTitle = "Personal chief"

// disclosureCapability is the policy capability messaging requires before
// any body/attachment disclosure is delivered to its recipients.
const disclosureCapability = "messaging.disclosure.deliver"

// snapshotBindingReporting is the configuration binding kind that authorizes
// a worker's report to a human-facing conversation. Binding kind values are
// frozen by the shared configuration contract.
const bindingKindReporting = "reporting"
