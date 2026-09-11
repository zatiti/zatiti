package messaging

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/zatiti/zatiti/internal/contract"
)

// Shared delivery pipeline for the three send paths: conversation.message.send,
// mailbox.send and _messaging.admit. Content is untrusted: the sender is the
// authenticated principal (public paths) or the caller-asserted committed
// identity (internal admission), and delivery authority comes from the
// policy disclosure check, never from message text. A message commits as
// admitted together with its recipient inbox rows in one transaction.

// deliverParams is the normalized content of one delivery.
type deliverParams struct {
	MessageID      contract.ID
	Scope          wireScope
	SenderID       contract.ID
	RecipientIDs   []contract.ID
	Body           string
	Attachments    []wireArtifactRef
	TaskIDs        []contract.ID
	ConversationID contract.ID
}

// contentDigestInput is the exact content identity that deduplication
// hashes. Recipients and attachments are normalized before hashing so a
// redelivery with reordered arrays deduplicates instead of conflicting.
// The conversation placement is part of the identity: the same content
// submitted into a different conversation is a changed submission.
type contentDigestInput struct {
	Scope          wireScope         `json:"scope"`
	SenderID       contract.ID       `json:"sender_id"`
	RecipientIDs   []contract.ID     `json:"recipient_ids"`
	Body           string            `json:"body"`
	Attachments    []wireArtifactRef `json:"attachments"`
	TaskIDs        []contract.ID     `json:"task_ids"`
	ConversationID contract.ID       `json:"conversation_id,omitempty"`
}

// normalizeRecipients deduplicates, sorts and drops the sender from the
// recipient set; a message with no other recipients is invalid.
func normalizeRecipients(sender contract.ID, in []contract.ID) ([]contract.ID, error) {
	seen := map[contract.ID]bool{}
	out := make([]contract.ID, 0, len(in))
	for _, id := range in {
		if id == "" || id == sender || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	if len(out) == 0 {
		return nil, invalidInput("message has no recipients besides the sender")
	}
	return out, nil
}

// normalizeAttachments deduplicates attachment references by id and sorts
// them; a conflicting duplicate digest under one id is invalid input.
func normalizeAttachments(in []wireArtifactRef) ([]wireArtifactRef, error) {
	seen := map[contract.ID]wireArtifactRef{}
	out := make([]wireArtifactRef, 0, len(in))
	for _, ref := range in {
		if ref.ID == "" {
			return nil, invalidInput("attachment reference id is empty")
		}
		if prior, ok := seen[ref.ID]; ok {
			if prior.Digest != ref.Digest {
				return nil, invalidInput("attachment %s is referenced with two digests", ref.ID)
			}
			continue
		}
		seen[ref.ID] = ref
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].Digest < out[j].Digest
	})
	return out, nil
}

// normalizeTaskIDs deduplicates and sorts task references.
func normalizeTaskIDs(in []contract.ID) []contract.ID {
	seen := map[contract.ID]bool{}
	out := make([]contract.ID, 0, len(in))
	for _, id := range in {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// contentDigest hashes the normalized content identity. Recipient order is
// tolerated; any content change under the same message id is a conflict.
func contentDigest(scope wireScope, p *deliverParams) string {
	input := contentDigestInput{
		Scope:          scope,
		SenderID:       p.SenderID,
		RecipientIDs:   p.RecipientIDs,
		Body:           p.Body,
		Attachments:    p.Attachments,
		TaskIDs:        p.TaskIDs,
		ConversationID: p.ConversationID,
	}
	raw := mustJSON(input)
	canonical, err := contract.Canonicalize(raw)
	if err != nil {
		// Canonicalization of freshly marshaled JSON cannot fail for
		// schema-bounded content; treat any failure as internal.
		panic("messaging: content canonicalization failed: " + err.Error())
	}
	return string(contract.Hash(canonical))
}

// deliver admits one message durably. The pipeline order is deliberate:
// conversation membership resolves first (it defines the recipient set),
// then content normalization and identity dedup (retries are idempotent
// without re-authorization), then the policy disclosure gate, attachment
// validation and durable admission.
func (s *Service) deliver(ctx context.Context, unit contract.Unit, p *deliverParams) (*wireMessage, error) {
	scope := p.Scope
	if err := checkUnitScope(unit, scope); err != nil {
		return nil, err
	}
	if p.SenderID == "" {
		return nil, internalError("delivery requires an authenticated sender")
	}
	if p.MessageID == "" {
		return nil, internalError("delivery requires a stable message id")
	}

	var conv *conversationRow
	if p.ConversationID != "" {
		var err error
		conv, err = getConversation(ctx, unit, p.ConversationID)
		if err != nil {
			return nil, err
		}
		if conv == nil {
			return nil, notFound("conversation %s not found", p.ConversationID)
		}
		if err := checkStoredConversationScope(unit, scope, conv); err != nil {
			return nil, err
		}
		participants := mustIDList(conv.ParticipantIDsJSON)
		if !containsID(participants, p.SenderID) {
			return nil, permissionDenied("sender is not a participant of the conversation")
		}
		for _, rid := range p.RecipientIDs {
			if !containsID(participants, rid) {
				return nil, invalidInput("recipient %s is not a conversation participant", rid)
			}
		}
		if len(p.RecipientIDs) == 0 {
			// Conversation delivery goes to every other participant; a
			// newly added participant never receives historical rows.
			participants, err = normalizeRecipients(p.SenderID, participants)
			if err != nil {
				return nil, err
			}
			p.RecipientIDs = participants
		} else {
			p.RecipientIDs, err = normalizeRecipients(p.SenderID, p.RecipientIDs)
			if err != nil {
				return nil, err
			}
		}
	} else {
		recipients, err := normalizeRecipients(p.SenderID, p.RecipientIDs)
		if err != nil {
			return nil, err
		}
		p.RecipientIDs = recipients
	}
	attachments, err := normalizeAttachments(p.Attachments)
	if err != nil {
		return nil, err
	}
	p.Attachments = attachments
	p.TaskIDs = normalizeTaskIDs(p.TaskIDs)
	digest := contentDigest(scope, p)

	// Identity dedup: a redelivery of committed mail is idempotent; the
	// same id with different content is a surfaced conflict.
	existing, err := getMessageInInstallation(ctx, unit, unit.Scope().InstallationID, p.MessageID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if existing.ContentDigest != digest {
			return nil, submissionConflict("message %s already exists with different content", p.MessageID)
		}
		if err := hydrateRecipients(ctx, unit, []*messageRow{existing}); err != nil {
			return nil, err
		}
		return existing.toWire()
	}

	// Disclosure gate: body and attachment delivery authority is policy's
	// decision, intersected over current grants for this scope. The content
	// digest is the candidate identity of what is being disclosed.
	if err := s.peerPolicyCheck(ctx, unit, scope.toContract(), disclosureCapability, digest); err != nil {
		return nil, err
	}
	if len(p.Attachments) > 0 {
		if err := s.validateArtifacts(ctx, unit, scope.toContract(), p.Attachments); err != nil {
			return nil, err
		}
	}
	meaningful, err := s.classifyMeaningful(ctx, unit, p)
	if err != nil {
		return nil, err
	}

	now := s.clock.Now()
	row := &messageRow{
		ID:              p.MessageID,
		Version:         1,
		InstallationID:  unit.Scope().InstallationID,
		OrganizationID:  scope.OrganizationID,
		ConversationID:  p.ConversationID,
		ScopeJSON:       string(mustJSON(scope)),
		SenderID:        p.SenderID,
		Body:            p.Body,
		AttachmentsJSON: string(mustJSON(p.Attachments)),
		TaskIDsJSON:     string(mustJSON(p.TaskIDs)),
		State:           messageStateAdmitted,
		Meaningful:      meaningful,
		ContentDigest:   digest,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	// The delivered wire carries the recipient roster; it is known here
	// without a read-back, and toWire requires it hydrated first.
	row.RecipientIDs = append([]contract.ID{}, p.RecipientIDs...)
	wire, err := row.toWire()
	if err != nil {
		return nil, err
	}
	if err := insertMessage(ctx, unit, row); err != nil {
		if isUniqueViolation(err) {
			return nil, submissionConflict("message %s already exists with different content", p.MessageID)
		}
		return nil, err
	}
	delivered := string(mustJSON(wire))
	for _, rid := range p.RecipientIDs {
		if err := insertRecipient(ctx, unit, &recipientRow{
			MessageID:      p.MessageID,
			RecipientID:    rid,
			InstallationID: unit.Scope().InstallationID,
			State:          recipientAdmitted,
			DeliveredJSON:  delivered,
			AdmittedAt:     now,
		}); err != nil {
			return nil, err
		}
	}
	if meaningful && conv != nil {
		if err := touchConversationMeaningful(ctx, unit, conv.ID, now); err != nil {
			return nil, err
		}
	}
	if conv != nil {
		if err := upsertReadMarker(ctx, unit, conv.ID, p.SenderID, unit.Scope().InstallationID, p.MessageID, now); err != nil {
			return nil, err
		}
	}
	if err := s.emitMessageEvent(ctx, unit, row, eventMessageAdmitted); err != nil {
		return nil, err
	}
	return wire, nil
}

// classifyMeaningful decides whether a delivery is a meaningful human-facing
// event or quiet coordination. Human and client-agent sends are meaningful.
// Worker-sourced messages are meaningful only when an explicit reporting
// binding granted to the sender lists the destination conversation among its
// destinations; routine coordination stays quiet and never reorders chats or
// marks them unread.
func (s *Service) classifyMeaningful(ctx context.Context, unit contract.Unit, p *deliverParams) (bool, error) {
	switch unit.Actor().Kind {
	case contract.KindHuman, contract.KindClientAgent:
		return true, nil
	default:
		if p.ConversationID == "" {
			return false, nil
		}
		snapshot, err := s.peerConfigurationSnapshot(ctx, unit, p.Scope.toContract())
		if err != nil {
			return false, err
		}
		for _, b := range reportingBindings(snapshot, p.SenderID) {
			if containsString(b.Destinations, string(p.ConversationID)) {
				return true, nil
			}
		}
		return false, nil
	}
}

// checkUnitScope verifies the authenticated unit scope covers the asserted
// scope: same installation, no contradiction on optional dimensions.
func checkUnitScope(unit contract.Unit, scope wireScope) error {
	us := unit.Scope()
	if us.InstallationID != "" && scope.InstallationID != "" && scope.InstallationID != us.InstallationID {
		return permissionDenied("scope installation %s does not match the authenticated installation", scope.InstallationID)
	}
	if us.OrganizationID != "" && scope.OrganizationID != "" && scope.OrganizationID != us.OrganizationID {
		return permissionDenied("scope organization does not match the authenticated scope")
	}
	if us.ProjectID != "" && scope.ProjectID != "" && scope.ProjectID != us.ProjectID {
		return permissionDenied("scope project does not match the authenticated scope")
	}
	return nil
}

// checkStoredConversationScope verifies an asserted scope against a stored
// conversation row without cross-scope disclosure.
func checkStoredConversationScope(unit contract.Unit, asserted wireScope, row *conversationRow) error {
	us := unit.Scope()
	if us.InstallationID != "" && row.InstallationID != us.InstallationID {
		return permissionDenied("conversation is outside the authenticated installation")
	}
	stored, err := decodeScopeJSON(row.ScopeJSON)
	if err != nil {
		return err
	}
	if asserted.OrganizationID != "" && asserted.OrganizationID != stored.OrganizationID {
		return permissionDenied("conversation is outside the requested organization")
	}
	if asserted.ProjectID != "" && asserted.ProjectID != stored.ProjectID {
		return permissionDenied("conversation is outside the requested project")
	}
	if asserted.WorkerID != "" && asserted.WorkerID != stored.WorkerID {
		return permissionDenied("conversation is outside the requested worker scope")
	}
	return nil
}

// containsID reports whether the sorted-or-unsorted id list contains id.
func containsID(ids []contract.ID, id contract.ID) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

// containsString reports membership in a string list.
func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// mustJSON marshals a wire value; schema-bounded content cannot fail, and a
// failure is a programming error, not caller input.
func mustJSON(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		panic("messaging: wire value encoding failed: " + err.Error())
	}
	return raw
}
