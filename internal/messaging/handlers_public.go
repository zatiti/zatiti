package messaging

import (
	"context"

	"github.com/zatiti/zatiti/internal/contract"
)

// Public catalog operations. Every read and every filter runs under the
// authenticated principal's participant membership; server scope wins over
// any filter the caller asserts.

// conversationParticipantFence refuses access to a conversation the caller
// does not participate in. Group membership is conversation state, not
// organization membership.
func conversationParticipantFence(unit contract.Unit, row *conversationRow) error {
	if !containsID(mustIDList(row.ParticipantIDsJSON), unit.Actor().PrincipalID) {
		return permissionDenied("caller is not a participant of the conversation")
	}
	return nil
}

// handleConversationCreate creates a conversation. It changes neither the
// home organization, memory access nor tool grants: the only state written
// is conversation membership. Participants must be distinct and include
// the authenticated caller; direct conversations carry exactly two.
func handleConversationCreate(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[struct {
		Scope          wireScope     `json:"scope"`
		Kind           string        `json:"kind"`
		ParticipantIDs []contract.ID `json:"participant_ids"`
		Title          string        `json:"title"`
	}](s, "conversation.create", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := checkUnitScope(unit, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	if in.Kind != kindDirect && in.Kind != kindGroup {
		return contract.Payload{}, invalidInput("conversation kind %q is not direct or group", in.Kind)
	}
	if len(in.ParticipantIDs) == 0 {
		return contract.Payload{}, invalidInput("conversation requires participants")
	}
	participants, err := normalizeRecipients("", in.ParticipantIDs)
	if err != nil {
		return contract.Payload{}, err
	}
	if !containsID(participants, unit.Actor().PrincipalID) {
		return contract.Payload{}, permissionDenied("caller must participate in the conversation")
	}
	switch in.Kind {
	case kindDirect:
		if len(participants) != 2 {
			return contract.Payload{}, invalidInput("direct conversations carry exactly two participants")
		}
	case kindGroup:
		if len(participants) < 2 {
			return contract.Payload{}, invalidInput("group conversations require at least two participants")
		}
	}
	now := s.clock.Now()
	participantsJSON, err := idListJSON(participants)
	if err != nil {
		return contract.Payload{}, err
	}
	row := &conversationRow{
		ID:                 s.ids.New(),
		Version:            1,
		InstallationID:     unit.Scope().InstallationID,
		OrganizationID:     in.Scope.OrganizationID,
		ScopeJSON:          string(mustJSON(in.Scope)),
		Kind:               in.Kind,
		Title:              in.Title,
		ParticipantIDsJSON: participantsJSON,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := insertConversation(ctx, unit, row); err != nil {
		return contract.Payload{}, err
	}
	if err := s.emitConversationEvent(ctx, unit, row, eventConversationCreated); err != nil {
		return contract.Payload{}, err
	}
	wire, err := row.toWire()
	if err != nil {
		return contract.Payload{}, err
	}
	return s.completed(map[string]any{"resource": wire})
}

// handleConversationGet resolves one conversation under current
// authorization: not_found and permission_denied never leak cross-scope
// state.
func handleConversationGet(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[struct {
		Scope wireScope   `json:"scope"`
		ID    contract.ID `json:"id"`
	}](s, "conversation.get", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := checkUnitScope(unit, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	row, err := getConversation(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if row == nil || row.InstallationID != unit.Scope().InstallationID {
		return contract.Payload{}, notFound("conversation %s not found", in.ID)
	}
	if err := checkStoredConversationScope(unit, in.Scope, row); err != nil {
		return contract.Payload{}, err
	}
	if err := conversationParticipantFence(unit, row); err != nil {
		return contract.Payload{}, err
	}
	wire, err := row.toWire()
	if err != nil {
		return contract.Payload{}, err
	}
	return s.completed(map[string]any{"resource": wire})
}

// handleConversationList reads the caller's authorized conversation
// snapshot. Membership fences every row, filters apply before pagination,
// and the cursor binds principal, query and the evidence checkpoint.
func handleConversationList(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[struct {
		Scope  wireScope       `json:"scope"`
		Cursor string          `json:"cursor"`
		Limit  int64           `json:"limit"`
		Filter *wireListFilter `json:"filter"`
	}](s, "conversation.list", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := checkUnitScope(unit, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	f := conversationFilter{}
	if in.Filter != nil {
		if err := refuseUnsupportedFilters("conversation.list", in.Filter, false, true, true); err != nil {
			return contract.Payload{}, err
		}
		f.Key = in.Filter.Key
		f.WorkerID = in.Filter.WorkerID
		f.TaskID = in.Filter.TaskID
		f.OrganizationID = in.Filter.OrganizationID
		f.NeedsYou = in.Filter.NeedsYou != nil && *in.Filter.NeedsYou
	}
	fingerprint := cursorFingerprint("conversation.list",
		f.Key, string(f.WorkerID), string(f.TaskID), string(f.OrganizationID), boolText(f.NeedsYou))
	offset, snapshot, err := s.decodeCursor(unit, fingerprint, in.Cursor)
	if err != nil {
		return contract.Payload{}, err
	}
	if in.Cursor == "" {
		if snapshot, err = s.peerEvidenceSnapshot(ctx, unit, unit.Scope()); err != nil {
			return contract.Payload{}, err
		}
	}
	limit := limitOf(in.Limit)
	rows, err := listConversations(ctx, unit, unit.Actor().PrincipalID, f, limit, int(offset))
	if err != nil {
		return contract.Payload{}, err
	}
	items := make([]*wireConversation, 0, len(rows))
	for _, row := range rows {
		wire, err := row.toWire()
		if err != nil {
			return contract.Payload{}, err
		}
		items = append(items, wire)
	}
	out := map[string]any{"items": items}
	if len(items) == limit {
		next, err := s.encodeCursor(unit, fingerprint, snapshot, s.clock.Now(), offset+int64(limit))
		if err != nil {
			return contract.Payload{}, err
		}
		out["next_cursor"] = next
	}
	return s.completed(out)
}

// handleConversationUpdate applies versioned membership, title and pin
// changes. Membership changes write no historical disclosure: joining
// participants receive no rows for prior messages.
func handleConversationUpdate(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[struct {
		Scope           wireScope     `json:"scope"`
		ID              contract.ID   `json:"id"`
		ExpectedVersion int64         `json:"expected_version"`
		ParticipantIDs  []contract.ID `json:"participant_ids"`
		Title           *string       `json:"title"`
		Pinned          *bool         `json:"pinned"`
	}](s, "conversation.update", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := checkUnitScope(unit, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	row, err := getConversation(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if row == nil || row.InstallationID != unit.Scope().InstallationID {
		return contract.Payload{}, notFound("conversation %s not found", in.ID)
	}
	if err := checkStoredConversationScope(unit, in.Scope, row); err != nil {
		return contract.Payload{}, err
	}
	if err := conversationParticipantFence(unit, row); err != nil {
		return contract.Payload{}, err
	}
	if in.ExpectedVersion != row.Version {
		return contract.Payload{}, staleVersion("conversation %s version %d does not match expected %d",
			in.ID, row.Version, in.ExpectedVersion)
	}
	if row.Key == bootstrapKey && in.Pinned != nil && !*in.Pinned {
		return contract.Payload{}, conflictFault("the personal-chief conversation stays pinned")
	}
	if in.Title != nil {
		row.Title = *in.Title
	}
	if in.Pinned != nil {
		row.Pinned = *in.Pinned
	}
	if in.ParticipantIDs != nil {
		participants, err := normalizeRecipients("", in.ParticipantIDs)
		if err != nil {
			return contract.Payload{}, err
		}
		if !containsID(participants, unit.Actor().PrincipalID) {
			return contract.Payload{}, permissionDenied("caller must remain a conversation participant")
		}
		if row.Kind == kindDirect && len(participants) != 2 {
			return contract.Payload{}, invalidInput("direct conversations carry exactly two participants")
		}
		if row.Kind == kindGroup && len(participants) < 2 {
			return contract.Payload{}, invalidInput("group conversations require at least two participants")
		}
		participantsJSON, err := idListJSON(participants)
		if err != nil {
			return contract.Payload{}, err
		}
		row.ParticipantIDsJSON = participantsJSON
	}
	row.Version++
	row.UpdatedAt = s.clock.Now()
	if err := updateConversation(ctx, unit, row); err != nil {
		return contract.Payload{}, err
	}
	if err := s.emitConversationEvent(ctx, unit, row, eventConversationUpdated); err != nil {
		return contract.Payload{}, err
	}
	wire, err := row.toWire()
	if err != nil {
		return contract.Payload{}, err
	}
	return s.completed(map[string]any{"resource": wire})
}

// handleConversationMessageSend durably admits an authenticated message
// into a conversation. Attachments and participant disclosures are governed;
// task creation or assignment uses the versioned task operations, so task
// references are durable references only.
func handleConversationMessageSend(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[struct {
		Scope          wireScope         `json:"scope"`
		ConversationID contract.ID       `json:"conversation_id"`
		MessageID      contract.ID       `json:"message_id"`
		Body           string            `json:"body"`
		Attachments    []wireArtifactRef `json:"attachments"`
		TaskIDs        []contract.ID     `json:"task_ids"`
	}](s, "conversation.message.send", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if in.MessageID == "" {
		return contract.Payload{}, invalidInput("message_id is required")
	}
	wire, err := s.deliver(ctx, unit, &deliverParams{
		MessageID:      in.MessageID,
		Scope:          in.Scope,
		SenderID:       unit.Actor().PrincipalID,
		Body:           in.Body,
		Attachments:    in.Attachments,
		TaskIDs:        in.TaskIDs,
		ConversationID: in.ConversationID,
	})
	if err != nil {
		return contract.Payload{}, err
	}
	return s.completed(map[string]any{"resource": wire})
}

// handleMailboxAck acknowledges durable target admission at the recipient's
// own safe boundary. Only the recipient acknowledges; the expected version
// fences concurrent delivery transitions, and re-acknowledging committed
// mail with the current version is idempotent.
func handleMailboxAck(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[struct {
		Scope           wireScope   `json:"scope"`
		MessageID       contract.ID `json:"message_id"`
		RecipientID     contract.ID `json:"recipient_id"`
		ExpectedVersion int64       `json:"expected_version"`
	}](s, "mailbox.ack", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := checkUnitScope(unit, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	if unit.Actor().PrincipalID != in.RecipientID {
		return contract.Payload{}, permissionDenied("only the recipient may acknowledge a message")
	}
	message, err := getMessageInInstallation(ctx, unit, unit.Scope().InstallationID, in.MessageID)
	if err != nil {
		return contract.Payload{}, err
	}
	if message == nil {
		return contract.Payload{}, notFound("message %s not found", in.MessageID)
	}
	if err := checkStoredMessageScope(unit, in.Scope, message); err != nil {
		return contract.Payload{}, err
	}
	row, err := getRecipient(ctx, unit, in.MessageID, in.RecipientID)
	if err != nil {
		return contract.Payload{}, err
	}
	if row == nil {
		return contract.Payload{}, notFound("message %s is not in the inbox of %s", in.MessageID, in.RecipientID)
	}
	if row.State == recipientAcknowledged {
		if in.ExpectedVersion != message.Version {
			return contract.Payload{}, staleVersion("message %s version %d does not match expected %d",
				in.MessageID, message.Version, in.ExpectedVersion)
		}
		if err := hydrateRecipients(ctx, unit, []*messageRow{message}); err != nil {
			return contract.Payload{}, err
		}
		wire, err := message.toWire()
		if err != nil {
			return contract.Payload{}, err
		}
		return s.completed(map[string]any{"resource": wire})
	}
	if in.ExpectedVersion != message.Version {
		return contract.Payload{}, staleVersion("message %s version %d does not match expected %d",
			in.MessageID, message.Version, in.ExpectedVersion)
	}
	now := s.clock.Now()
	if err := updateRecipientState(ctx, unit, in.MessageID, in.RecipientID, recipientAcknowledged, now); err != nil {
		return contract.Payload{}, err
	}
	if err := insertReceipt(ctx, unit, s.ids.New(), in.MessageID, in.RecipientID, unit.Scope().InstallationID, now); err != nil {
		return contract.Payload{}, err
	}
	if message.ConversationID != "" {
		if err := upsertReadMarker(ctx, unit, message.ConversationID, in.RecipientID,
			unit.Scope().InstallationID, in.MessageID, now); err != nil {
			return contract.Payload{}, err
		}
	}
	remaining, err := countUnacknowledged(ctx, unit, in.MessageID)
	if err != nil {
		return contract.Payload{}, err
	}
	if remaining == 0 {
		message.State = messageStateAcknowledged
		message.Version++
		message.UpdatedAt = now
		if err := updateMessageState(ctx, unit, message); err != nil {
			return contract.Payload{}, err
		}
		if err := s.emitMessageEvent(ctx, unit, message, eventMessageAcknowledged); err != nil {
			return contract.Payload{}, err
		}
	}
	if err := hydrateRecipients(ctx, unit, []*messageRow{message}); err != nil {
		return contract.Payload{}, err
	}
	wire, err := message.toWire()
	if err != nil {
		return contract.Payload{}, err
	}
	return s.completed(map[string]any{"resource": wire})
}

// handleMailboxList reads one recipient's authorized inbox. The recipient
// is the authenticated principal: mail is private per principal and no
// caller can list another's inbox. Body text never confers authority.
func handleMailboxList(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[struct {
		Scope       wireScope       `json:"scope"`
		Cursor      string          `json:"cursor"`
		Limit       int64           `json:"limit"`
		Filter      *wireListFilter `json:"filter"`
		RecipientID contract.ID     `json:"recipient_id"`
	}](s, "mailbox.list", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := checkUnitScope(unit, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	if in.RecipientID == "" {
		return contract.Payload{}, invalidInput("recipient_id is required")
	}
	if in.RecipientID != unit.Actor().PrincipalID {
		return contract.Payload{}, permissionDenied("mailbox listing is private to the recipient")
	}
	f := mailboxFilter{}
	if in.Filter != nil {
		if err := refuseUnsupportedFilters("mailbox.list", in.Filter, true, false, false); err != nil {
			return contract.Payload{}, err
		}
		f.State = in.Filter.State
		f.WorkerID = in.Filter.WorkerID
		f.TaskID = in.Filter.TaskID
		f.OrganizationID = in.Filter.OrganizationID
	}
	fingerprint := cursorFingerprint("mailbox.list",
		string(in.RecipientID), f.State, string(f.WorkerID), string(f.TaskID),
		string(f.OrganizationID))
	offset, snapshot, err := s.decodeCursor(unit, fingerprint, in.Cursor)
	if err != nil {
		return contract.Payload{}, err
	}
	if in.Cursor == "" {
		if snapshot, err = s.peerEvidenceSnapshot(ctx, unit, unit.Scope()); err != nil {
			return contract.Payload{}, err
		}
	}
	limit := limitOf(in.Limit)
	rows, err := listMailbox(ctx, unit, unit.Scope().InstallationID, in.RecipientID, f, limit, int(offset))
	if err != nil {
		return contract.Payload{}, err
	}
	if err := hydrateRecipients(ctx, unit, rows); err != nil {
		return contract.Payload{}, err
	}
	items := make([]*wireMessage, 0, len(rows))
	for _, row := range rows {
		wire, err := row.toWire()
		if err != nil {
			return contract.Payload{}, err
		}
		items = append(items, wire)
	}
	out := map[string]any{"items": items}
	if len(items) == limit {
		next, err := s.encodeCursor(unit, fingerprint, snapshot, s.clock.Now(), offset+int64(limit))
		if err != nil {
			return contract.Payload{}, err
		}
		out["next_cursor"] = next
	}
	return s.completed(out)
}

// handleMailboxSend records an authenticated point-to-point message and
// admits it to the target inbox durably; receipt is only acknowledged
// afterwards by mailbox.ack. Redelivery deduplicates identity and surfaces
// changed content as a conflict.
func handleMailboxSend(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[struct {
		Scope       wireScope         `json:"scope"`
		MessageID   contract.ID       `json:"message_id"`
		RecipientID contract.ID       `json:"recipient_id"`
		Body        string            `json:"body"`
		Attachments []wireArtifactRef `json:"attachments"`
		TaskIDs     []contract.ID     `json:"task_ids"`
	}](s, "mailbox.send", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if in.MessageID == "" {
		return contract.Payload{}, invalidInput("message_id is required")
	}
	if in.RecipientID == "" {
		return contract.Payload{}, invalidInput("recipient_id is required")
	}
	wire, err := s.deliver(ctx, unit, &deliverParams{
		MessageID:    in.MessageID,
		Scope:        in.Scope,
		SenderID:     unit.Actor().PrincipalID,
		RecipientIDs: []contract.ID{in.RecipientID},
		Body:         in.Body,
		Attachments:  in.Attachments,
		TaskIDs:      in.TaskIDs,
	})
	if err != nil {
		return contract.Payload{}, err
	}
	return s.completed(map[string]any{"resource": wire})
}

// boolText renders a boolean for cursor fingerprint binding.
func boolText(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// shared helpers ---------------------------------------------------------------

// wireListFilter mirrors the structured filter object shared by the list
// operations. Booleans are pointers so absence stays distinguishable from
// false; every operation refuses the fields it does not support.
type wireListFilter struct {
	State          string      `json:"state,omitempty"`
	Key            string      `json:"key,omitempty"`
	ParentID       contract.ID `json:"parent_id,omitempty"`
	WorkerID       contract.ID `json:"worker_id,omitempty"`
	TaskID         contract.ID `json:"task_id,omitempty"`
	OrganizationID contract.ID `json:"organization_id,omitempty"`
	Descendants    *bool       `json:"descendants,omitempty"`
	NeedsYou       *bool       `json:"needs_you,omitempty"`
}

// refuseUnsupportedFilters enforces the per-resource filter support:
// conversation.list supports key/worker_id/task_id/organization_id/needs_you;
// mailbox.list supports state/worker_id/task_id/organization_id. parent_id
// and descendants are unsupported for both resources. Anything unsupported
// refuses invalid_input instead of being silently ignored.
func refuseUnsupportedFilters(op string, f *wireListFilter, stateSupported, keySupported, needsYouSupported bool) error {
	refuse := func(field string) error {
		return invalidInput("filter %s is unsupported for %s", field, op)
	}
	if !stateSupported && f.State != "" {
		return refuse("state")
	}
	if f.ParentID != "" {
		return refuse("parent_id")
	}
	if f.Descendants != nil {
		return refuse("descendants")
	}
	if !needsYouSupported && f.NeedsYou != nil {
		return refuse("needs_you")
	}
	if !keySupported && f.Key != "" {
		return refuse("key")
	}
	return nil
}

// checkStoredMessageScope verifies an asserted scope against a stored
// message without cross-scope disclosure.
func checkStoredMessageScope(unit contract.Unit, asserted wireScope, row *messageRow) error {
	us := unit.Scope()
	if us.InstallationID != "" && row.InstallationID != us.InstallationID {
		return permissionDenied("message is outside the authenticated installation")
	}
	stored, err := decodeScopeJSON(row.ScopeJSON)
	if err != nil {
		return err
	}
	if asserted.OrganizationID != "" && asserted.OrganizationID != stored.OrganizationID {
		return permissionDenied("message is outside the requested organization")
	}
	if asserted.ProjectID != "" && asserted.ProjectID != stored.ProjectID {
		return permissionDenied("message is outside the requested project")
	}
	if asserted.WorkerID != "" && asserted.WorkerID != stored.WorkerID {
		return permissionDenied("message is outside the requested worker scope")
	}
	return nil
}
