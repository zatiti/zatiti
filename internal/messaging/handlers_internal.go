package messaging

import (
	"context"
	"sort"

	"github.com/zatiti/zatiti/internal/contract"
)

// Internal peer operations. Their caller allowlists are enforced by the
// application dispatcher; these handlers own the domain fences.

// handleMessagingAdmit commits an internal message with its recipient
// inbox. Identity deduplication, disclosure gating and durable admission
// run in the shared pipeline. The message wire document is untrusted data:
// it never changes grants, and a worker-kind caller may only admit mail as
// itself.
func handleMessagingAdmit(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[struct {
		Message wireMessage `json:"message"`
	}](s, "_messaging.admit", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if in.Message.State != messageStateSubmitted {
		return contract.Payload{}, invalidInput("message state must be %s for admission", messageStateSubmitted)
	}
	if in.Message.Version != 1 {
		return contract.Payload{}, invalidInput("message version must be 1 for admission")
	}
	if in.Message.ID == "" {
		return contract.Payload{}, invalidInput("message id is required")
	}
	if in.Message.SenderID == "" {
		return contract.Payload{}, invalidInput("message sender_id is required")
	}
	if unit.Actor().Kind == contract.KindWorker && in.Message.SenderID != unit.Actor().PrincipalID {
		return contract.Payload{}, permissionDenied("a worker can only admit messages as itself")
	}
	wire, err := s.deliver(ctx, unit, &deliverParams{
		MessageID:      in.Message.ID,
		Scope:          in.Message.Scope,
		SenderID:       in.Message.SenderID,
		RecipientIDs:   in.Message.RecipientIDs,
		Body:           in.Message.Body,
		Attachments:    in.Message.Attachments,
		TaskIDs:        in.Message.TaskIDs,
		ConversationID: in.Message.ConversationID,
	})
	if err != nil {
		return contract.Payload{}, err
	}
	return s.completed(map[string]any{"resource": wire})
}

// handleMessagingBootstrap creates the pinned personal-chief direct
// conversation from committed identities. Bootstrap is idempotent by the
// stable key inside one installation: a replay with the same participants
// returns the existing conversation, and a replay with different
// participants is a surfaced conflict, never a second chief chat.
func handleMessagingBootstrap(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[struct {
		Scope   wireScope   `json:"scope"`
		OwnerID contract.ID `json:"owner_id"`
		ChiefID contract.ID `json:"chief_id"`
	}](s, "_messaging.bootstrap", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := checkUnitScope(unit, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	if in.OwnerID == "" || in.ChiefID == "" {
		return contract.Payload{}, invalidInput("bootstrap requires the owner and chief identities")
	}
	if in.OwnerID == in.ChiefID {
		return contract.Payload{}, invalidInput("bootstrap owner and chief must differ")
	}
	existing, err := getConversationByKey(ctx, unit, unit.Scope().InstallationID, bootstrapKey)
	if err != nil {
		return contract.Payload{}, err
	}
	if existing != nil {
		participants := mustIDList(existing.ParticipantIDsJSON)
		want := []contract.ID{in.OwnerID, in.ChiefID}
		sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
		if equalIDs(participants, want) {
			wire, err := existing.toWire()
			if err != nil {
				return contract.Payload{}, err
			}
			return s.completed(map[string]any{"resource": wire})
		}
		return contract.Payload{}, conflictFault(
			"bootstrap conversation already exists with different participants")
	}
	now := s.clock.Now()
	participants := []contract.ID{in.OwnerID, in.ChiefID}
	sort.Slice(participants, func(i, j int) bool { return participants[i] < participants[j] })
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
		Kind:               kindDirect,
		Title:              bootstrapTitle,
		Pinned:             true,
		Key:                bootstrapKey,
		ParticipantIDsJSON: participantsJSON,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := insertConversation(ctx, unit, row); err != nil {
		if isUniqueViolation(err) {
			return contract.Payload{}, conflictFault(
				"bootstrap conversation already exists with different participants")
		}
		return contract.Payload{}, err
	}
	if err := s.emitConversationEvent(ctx, unit, row, eventConversationBootstrapped); err != nil {
		return contract.Payload{}, err
	}
	wire, err := row.toWire()
	if err != nil {
		return contract.Payload{}, err
	}
	return s.completed(map[string]any{"resource": wire})
}

// handleMessagingPending lists a worker's unacknowledged admitted inbox
// oldest-first for safe-boundary injection or idle resume. A worker-kind
// caller can only read its own inbox.
func handleMessagingPending(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[struct {
		WorkerID contract.ID `json:"worker_id"`
		Limit    int64       `json:"limit"`
	}](s, "_messaging.pending", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if in.WorkerID == "" {
		return contract.Payload{}, invalidInput("worker_id is required")
	}
	if unit.Actor().Kind == contract.KindWorker && in.WorkerID != unit.Actor().PrincipalID {
		return contract.Payload{}, permissionDenied("a worker can only read its own pending mail")
	}
	limit := int(in.Limit)
	if limit > 100 {
		limit = 100
	}
	rows, err := listPending(ctx, unit, unit.Scope().InstallationID, in.WorkerID, limit)
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
	return s.completed(map[string]any{"items": items})
}

// equalIDs reports slice equality ignoring order.
func equalIDs(a, b []contract.ID) bool {
	if len(a) != len(b) {
		return false
	}
	left := make([]contract.ID, len(a))
	copy(left, a)
	sort.Slice(left, func(i, j int) bool { return left[i] < left[j] })
	right := make([]contract.ID, len(b))
	copy(right, b)
	sort.Slice(right, func(i, j int) bool { return right[i] < right[j] })
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
