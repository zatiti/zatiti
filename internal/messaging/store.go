package messaging

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Store layer for the messaging_ tables. Rows scan through one decoder per
// table; JSON columns marshal and unmarshal at the edges. All queries run
// inside the caller's unit (one transaction). Conversation visibility is
// fenced by participant membership; filter values are always bound
// parameters, never interpolated SQL.

const timeLayout = time.RFC3339Nano

type rowScanner interface {
	Scan(dest ...any) error
}

// conversationRow is one durable conversation with its membership.
type conversationRow struct {
	ID                  contract.ID
	Version             int64
	InstallationID      contract.ID
	OrganizationID      contract.ID
	ScopeJSON           string
	Kind                string
	Title               string
	Pinned              bool
	Key                 string
	ParticipantIDsJSON  string
	LastMeaningfulEvent string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// messageRow is one durable mail record. RecipientIDs is not a column; it
// is hydrated by hydrateRecipients before wire conversion.
type messageRow struct {
	ID              contract.ID
	Version         int64
	InstallationID  contract.ID
	OrganizationID  contract.ID
	ConversationID  contract.ID
	ScopeJSON       string
	SenderID        contract.ID
	Body            string
	AttachmentsJSON string
	TaskIDsJSON     string
	State           string
	Meaningful      bool
	ContentDigest   string
	RecipientIDs    []contract.ID
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// recipientRow is one per-recipient inbox admission.
type recipientRow struct {
	MessageID      contract.ID
	RecipientID    contract.ID
	InstallationID contract.ID
	State          string
	DeliveredJSON  string
	AdmittedAt     time.Time
	AcknowledgedAt time.Time
}

// conversation columns -------------------------------------------------------

const conversationColumns = `id, version, installation_id, organization_id, scope_json, kind, title, pinned, key, participant_ids_json, last_meaningful_event, created_at, updated_at`

func scanConversation(row rowScanner) (*conversationRow, error) {
	var r conversationRow
	var pinned int64
	var created, updated string
	err := row.Scan(&r.ID, &r.Version, &r.InstallationID, &r.OrganizationID, &r.ScopeJSON,
		&r.Kind, &r.Title, &pinned, &r.Key, &r.ParticipantIDsJSON, &r.LastMeaningfulEvent,
		&created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.Pinned = pinned != 0
	if r.CreatedAt, err = time.Parse(timeLayout, created); err != nil {
		return nil, err
	}
	if r.UpdatedAt, err = time.Parse(timeLayout, updated); err != nil {
		return nil, err
	}
	return &r, nil
}

// getConversation fetches one conversation by id; nil when absent.
func getConversation(ctx context.Context, unit contract.Unit, id contract.ID) (*conversationRow, error) {
	row := unit.QueryRowContext(ctx,
		`SELECT `+conversationColumns+` FROM messaging_conversations WHERE id = ?`, string(id))
	return scanConversation(row)
}

// getConversationByKey resolves the pinned keyed conversation (bootstrap)
// inside one installation; nil when absent.
func getConversationByKey(ctx context.Context, unit contract.Unit, installation contract.ID, key string) (*conversationRow, error) {
	row := unit.QueryRowContext(ctx,
		`SELECT `+conversationColumns+` FROM messaging_conversations WHERE installation_id = ? AND key = ?`,
		string(installation), key)
	return scanConversation(row)
}

func insertConversation(ctx context.Context, unit contract.Unit, r *conversationRow) error {
	pinned := int64(0)
	if r.Pinned {
		pinned = 1
	}
	_, err := unit.ExecContext(ctx, `INSERT INTO messaging_conversations
		(id, version, installation_id, organization_id, scope_json, kind, title, pinned, key,
		 participant_ids_json, last_meaningful_event, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(r.ID), r.Version, string(r.InstallationID), string(r.OrganizationID), r.ScopeJSON,
		r.Kind, r.Title, pinned, r.Key, r.ParticipantIDsJSON, r.LastMeaningfulEvent,
		r.CreatedAt.Format(timeLayout), r.UpdatedAt.Format(timeLayout))
	return err
}

// updateConversation writes the mutable conversation fields after a
// versioned update. Scope, kind, installation and key never change.
func updateConversation(ctx context.Context, unit contract.Unit, r *conversationRow) error {
	pinned := int64(0)
	if r.Pinned {
		pinned = 1
	}
	_, err := unit.ExecContext(ctx, `UPDATE messaging_conversations SET
		version = ?, title = ?, pinned = ?, participant_ids_json = ?, last_meaningful_event = ?, updated_at = ?
		WHERE id = ?`,
		r.Version, r.Title, pinned, r.ParticipantIDsJSON, r.LastMeaningfulEvent,
		r.UpdatedAt.Format(timeLayout), string(r.ID))
	return err
}

// touchConversationMeaningful advances the meaningful-activity projection of
// a conversation to one timestamp. Quiet messages never call it.
func touchConversationMeaningful(ctx context.Context, unit contract.Unit, id contract.ID, at time.Time) error {
	_, err := unit.ExecContext(ctx,
		`UPDATE messaging_conversations SET last_meaningful_event = ? WHERE id = ?`,
		at.Format(timeLayout), string(id))
	return err
}

// message columns ------------------------------------------------------------

const messageColumns = `id, version, installation_id, organization_id, conversation_id, scope_json, sender_id, body, attachments_json, task_ids_json, state, meaningful, content_digest, created_at, updated_at`

// messageColumnsJoined prefixes the message columns for join queries where
// recipient state would otherwise be ambiguous.
const messageColumnsJoined = `msg.id, msg.version, msg.installation_id, msg.organization_id, msg.conversation_id, msg.scope_json, msg.sender_id, msg.body, msg.attachments_json, msg.task_ids_json, msg.state, msg.meaningful, msg.content_digest, msg.created_at, msg.updated_at`

func scanMessage(row rowScanner) (*messageRow, error) {
	var r messageRow
	var meaningful int64
	var created, updated string
	err := row.Scan(&r.ID, &r.Version, &r.InstallationID, &r.OrganizationID, &r.ConversationID,
		&r.ScopeJSON, &r.SenderID, &r.Body, &r.AttachmentsJSON, &r.TaskIDsJSON,
		&r.State, &meaningful, &r.ContentDigest, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.Meaningful = meaningful != 0
	if r.CreatedAt, err = time.Parse(timeLayout, created); err != nil {
		return nil, err
	}
	if r.UpdatedAt, err = time.Parse(timeLayout, updated); err != nil {
		return nil, err
	}
	return &r, nil
}

// getMessageInInstallation resolves a message id inside one installation.
// Deduplication is scoped so a caller cannot probe another installation's
// mail by guessing ids: a foreign id behaves as absent.
func getMessageInInstallation(ctx context.Context, unit contract.Unit, installation, id contract.ID) (*messageRow, error) {
	row := unit.QueryRowContext(ctx,
		`SELECT `+messageColumns+` FROM messaging_messages WHERE id = ? AND installation_id = ?`,
		string(id), string(installation))
	return scanMessage(row)
}

func insertMessage(ctx context.Context, unit contract.Unit, r *messageRow) error {
	meaningful := int64(0)
	if r.Meaningful {
		meaningful = 1
	}
	_, err := unit.ExecContext(ctx, `INSERT INTO messaging_messages
		(id, version, installation_id, organization_id, conversation_id, scope_json, sender_id,
		 body, attachments_json, task_ids_json, state, meaningful, content_digest, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(r.ID), r.Version, string(r.InstallationID), string(r.OrganizationID),
		string(r.ConversationID), r.ScopeJSON, string(r.SenderID), r.Body, r.AttachmentsJSON,
		r.TaskIDsJSON, r.State, meaningful, r.ContentDigest,
		r.CreatedAt.Format(timeLayout), r.UpdatedAt.Format(timeLayout))
	return err
}

// updateMessageState writes the delivery state after acknowledgements.
func updateMessageState(ctx context.Context, unit contract.Unit, r *messageRow) error {
	_, err := unit.ExecContext(ctx,
		`UPDATE messaging_messages SET version = ?, state = ?, updated_at = ? WHERE id = ?`,
		r.Version, r.State, r.UpdatedAt.Format(timeLayout), string(r.ID))
	return err
}

// recipient columns ----------------------------------------------------------

const recipientColumns = `message_id, recipient_id, installation_id, state, delivered_json, admitted_at, acknowledged_at`

func scanRecipient(row rowScanner) (*recipientRow, error) {
	var r recipientRow
	var admitted, acknowledged string
	err := row.Scan(&r.MessageID, &r.RecipientID, &r.InstallationID, &r.State, &r.DeliveredJSON,
		&admitted, &acknowledged)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if r.AdmittedAt, err = time.Parse(timeLayout, admitted); err != nil {
		return nil, err
	}
	if r.AcknowledgedAt, err = time.Parse(timeLayout, acknowledged); err != nil {
		return nil, err
	}
	return &r, nil
}

// getRecipient fetches one inbox row; nil when absent.
func getRecipient(ctx context.Context, unit contract.Unit, messageID, recipientID contract.ID) (*recipientRow, error) {
	row := unit.QueryRowContext(ctx,
		`SELECT `+recipientColumns+` FROM messaging_recipients WHERE message_id = ? AND recipient_id = ?`,
		string(messageID), string(recipientID))
	return scanRecipient(row)
}

func insertRecipient(ctx context.Context, unit contract.Unit, r *recipientRow) error {
	_, err := unit.ExecContext(ctx, `INSERT INTO messaging_recipients
		(message_id, recipient_id, installation_id, state, delivered_json, admitted_at, acknowledged_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		string(r.MessageID), string(r.RecipientID), string(r.InstallationID), r.State,
		r.DeliveredJSON, r.AdmittedAt.Format(timeLayout), r.AcknowledgedAt.Format(timeLayout))
	return err
}

// updateRecipientState advances one inbox row to acknowledged.
func updateRecipientState(ctx context.Context, unit contract.Unit, messageID, recipientID contract.ID, state string, at time.Time) error {
	_, err := unit.ExecContext(ctx,
		`UPDATE messaging_recipients SET state = ?, acknowledged_at = ?
		 WHERE message_id = ? AND recipient_id = ?`,
		state, at.Format(timeLayout), string(messageID), string(recipientID))
	return err
}

// countUnacknowledgedRecipients counts inbox rows still awaiting ack.
func countUnacknowledged(ctx context.Context, unit contract.Unit, messageID contract.ID) (int64, error) {
	var n int64
	err := unit.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messaging_recipients WHERE message_id = ? AND state != ?`,
		string(messageID), recipientAcknowledged).Scan(&n)
	return n, err
}

// receipts and read markers --------------------------------------------------

func insertReceipt(ctx context.Context, unit contract.Unit, id contract.ID, messageID, recipientID, installation contract.ID, at time.Time) error {
	_, err := unit.ExecContext(ctx, `INSERT INTO messaging_receipts
		(id, message_id, recipient_id, installation_id, acknowledged_at) VALUES (?, ?, ?, ?, ?)`,
		string(id), string(messageID), string(recipientID), string(installation), at.Format(timeLayout))
	return err
}

// upsertReadMarker advances a participant's read position to one message.
// Senders and acknowledgers both record progress; nothing reads it back
// over the wire in v1, but the projection stays durable in owned state.
func upsertReadMarker(ctx context.Context, unit contract.Unit, conversationID, principalID, installation contract.ID, messageID contract.ID, at time.Time) error {
	_, err := unit.ExecContext(ctx, `INSERT INTO messaging_read_markers
		(conversation_id, principal_id, installation_id, last_read_message_id, last_read_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (conversation_id, principal_id) DO UPDATE SET
		  last_read_message_id = excluded.last_read_message_id,
		  last_read_at = excluded.last_read_at,
		  updated_at = excluded.updated_at`,
		string(conversationID), string(principalID), string(installation),
		string(messageID), at.Format(timeLayout), at.Format(timeLayout))
	return err
}

// filters --------------------------------------------------------------------

// conversationFilter is the structured exact-match filter of
// conversation.list after unsupported fields were refused.
type conversationFilter struct {
	Key            string
	WorkerID       contract.ID
	TaskID         contract.ID
	OrganizationID contract.ID
	NeedsYou       bool
}

// mailboxFilter is the structured exact-match filter of mailbox.list after
// unsupported fields were refused.
type mailboxFilter struct {
	State          string
	WorkerID       contract.ID
	TaskID         contract.ID
	OrganizationID contract.ID
}

// participantToken builds the exact-match LIKE parameter for membership
// inside a JSON id array. UUIDs never contain quotes, so the quoted token
// cannot collide with a substring of another id.
func participantToken(id string) string {
	return `%"` + id + `"%`
}

// listConversations lists conversations where the caller is a participant.
// Membership is the visibility fence: organization scope narrows it, never
// widens it. NeedsYou gathers conversations with an unacknowledged
// meaningful task-bearing message addressed to the caller.
func listConversations(ctx context.Context, unit contract.Unit, principal contract.ID, f conversationFilter, limit, offset int) ([]*conversationRow, error) {
	where := []string{"installation_id = ?", "participant_ids_json LIKE ?"}
	args := []any{string(unit.Scope().InstallationID), participantToken(string(principal))}
	if f.Key != "" {
		where = append(where, "key = ?")
		args = append(args, f.Key)
	}
	if f.WorkerID != "" {
		where = append(where, "json_extract(scope_json, '$.worker_id') = ?")
		args = append(args, string(f.WorkerID))
	}
	if f.OrganizationID != "" {
		where = append(where, "organization_id = ?")
		args = append(args, string(f.OrganizationID))
	}
	if f.TaskID != "" {
		where = append(where, `EXISTS (SELECT 1 FROM messaging_messages tm
			WHERE tm.conversation_id = messaging_conversations.id AND tm.task_ids_json LIKE ?)`)
		args = append(args, participantToken(string(f.TaskID)))
	}
	if f.NeedsYou {
		where = append(where, `EXISTS (SELECT 1 FROM messaging_messages nm
			JOIN messaging_recipients nr ON nr.message_id = nm.id AND nr.recipient_id = ?
			WHERE nm.conversation_id = messaging_conversations.id AND nm.meaningful = 1
			  AND nm.task_ids_json != '[]' AND nr.state = ?
			  AND NOT EXISTS (SELECT 1 FROM messaging_receipts nc
			    WHERE nc.message_id = nm.id AND nc.recipient_id = ?))`)
		args = append(args, string(principal), recipientAdmitted, string(principal))
	}
	query := `SELECT ` + conversationColumns + ` FROM messaging_conversations WHERE ` + strings.Join(where, " AND ") +
		` ORDER BY pinned DESC, last_meaningful_event DESC, id LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := unit.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*conversationRow
	for rows.Next() {
		r, err := scanConversation(rows)
		if err != nil {
			return nil, err
		}
		if r != nil {
			out = append(out, r)
		}
	}
	return out, rows.Err()
}

// listMailbox lists one recipient's inbox messages with structured filters.
// Acknowledged mail stays listed; the state filter narrows it.
func listMailbox(ctx context.Context, unit contract.Unit, installation, recipient contract.ID, f mailboxFilter, limit, offset int) ([]*messageRow, error) {
	where := []string{"msg.installation_id = ?", "r.recipient_id = ?"}
	args := []any{string(installation), string(recipient)}
	if f.State != "" {
		where = append(where, "msg.state = ?")
		args = append(args, f.State)
	}
	if f.WorkerID != "" {
		where = append(where, "json_extract(msg.scope_json, '$.worker_id') = ?")
		args = append(args, string(f.WorkerID))
	}
	if f.OrganizationID != "" {
		where = append(where, "msg.organization_id = ?")
		args = append(args, string(f.OrganizationID))
	}
	if f.TaskID != "" {
		where = append(where, "msg.task_ids_json LIKE ?")
		args = append(args, participantToken(string(f.TaskID)))
	}
	query := `SELECT ` + messageColumnsJoined + ` FROM messaging_messages msg
		JOIN messaging_recipients r ON r.message_id = msg.id
		WHERE ` + strings.Join(where, " AND ") +
		` ORDER BY r.admitted_at DESC, msg.id LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := unit.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*messageRow
	for rows.Next() {
		r, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		if r != nil {
			out = append(out, r)
		}
	}
	return out, rows.Err()
}

// listPending returns a worker's unacknowledged admitted inbox oldest-first
// for safe-boundary injection and idle resume.
func listPending(ctx context.Context, unit contract.Unit, installation, workerID contract.ID, limit int) ([]*messageRow, error) {
	rows, err := unit.QueryContext(ctx, `SELECT `+messageColumnsJoined+` FROM messaging_messages msg
		JOIN messaging_recipients r ON r.message_id = msg.id
		WHERE msg.installation_id = ? AND r.recipient_id = ? AND r.state = ?
		ORDER BY r.admitted_at ASC, msg.id LIMIT ?`,
		string(installation), string(workerID), recipientAdmitted, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*messageRow
	for rows.Next() {
		r, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		if r != nil {
			out = append(out, r)
		}
	}
	return out, rows.Err()
}

// wire conversion ------------------------------------------------------------

// toWire converts a stored conversation row into its wire representation.
func (r *conversationRow) toWire() (*wireConversation, error) {
	scope, err := decodeScopeJSON(r.ScopeJSON)
	if err != nil {
		return nil, err
	}
	participants := []contract.ID{}
	if err := decodeJSON("participant ids", r.ParticipantIDsJSON, &participants); err != nil {
		return nil, err
	}
	return &wireConversation{
		ID:                  r.ID,
		Version:             contract.Version(r.Version),
		Scope:               scope,
		Kind:                r.Kind,
		ParticipantIDs:      participants,
		Title:               r.Title,
		Pinned:              r.Pinned,
		LastMeaningfulEvent: r.LastMeaningfulEvent,
	}, nil
}

// toWire converts a stored message row into its wire representation.
// RecipientIDs must be hydrated first.
func (r *messageRow) toWire() (*wireMessage, error) {
	scope, err := decodeScopeJSON(r.ScopeJSON)
	if err != nil {
		return nil, err
	}
	attachments := []wireArtifactRef{}
	if err := decodeJSON("attachments", r.AttachmentsJSON, &attachments); err != nil {
		return nil, err
	}
	taskIDs := []contract.ID{}
	if err := decodeJSON("task ids", r.TaskIDsJSON, &taskIDs); err != nil {
		return nil, err
	}
	recipients := r.RecipientIDs
	if recipients == nil {
		recipients = []contract.ID{}
	}
	return &wireMessage{
		ID:             r.ID,
		Version:        contract.Version(r.Version),
		SenderID:       r.SenderID,
		RecipientIDs:   recipients,
		Scope:          scope,
		TaskIDs:        taskIDs,
		Body:           r.Body,
		Attachments:    attachments,
		State:          r.State,
		CreatedAt:      r.CreatedAt.Format(timeLayout),
		ConversationID: r.ConversationID,
	}, nil
}

// hydrateRecipients fills RecipientIDs for every listed message with one
// batched query so list pages avoid per-row round trips.
func hydrateRecipients(ctx context.Context, unit contract.Unit, rows []*messageRow) error {
	if len(rows) == 0 {
		return nil
	}
	ids := make([]contract.ID, 0, len(rows))
	seen := map[contract.ID]bool{}
	for _, r := range rows {
		if !seen[r.ID] {
			seen[r.ID] = true
			ids = append(ids, r.ID)
		}
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, string(id))
	}
	query := `SELECT message_id, recipient_id FROM messaging_recipients
		WHERE message_id IN (` + placeholders + `) ORDER BY message_id, recipient_id`
	res, err := unit.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer func() { _ = res.Close() }()
	byMessage := map[contract.ID][]contract.ID{}
	for res.Next() {
		var messageID, recipientID string
		if err := res.Scan(&messageID, &recipientID); err != nil {
			return err
		}
		mid := contract.ID(messageID)
		byMessage[mid] = append(byMessage[mid], contract.ID(recipientID))
	}
	if err := res.Err(); err != nil {
		return err
	}
	for _, r := range rows {
		r.RecipientIDs = byMessage[r.ID]
	}
	return nil
}

// row decoding helpers -------------------------------------------------------
func decodeJSON(column string, raw string, out any) error {
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return internalError("stored %s decoding failed: %v", column, err)
	}
	return nil
}

// decodeScope parses the stored scope JSON.
func decodeScopeJSON(raw string) (wireScope, error) {
	var sc wireScope
	if err := decodeJSON("scope", raw, &sc); err != nil {
		return sc, err
	}
	return sc, nil
}

// idListJSON encodes an id slice for storage with a stable empty form.
func idListJSON(ids []contract.ID) (string, error) {
	if ids == nil {
		ids = []contract.ID{}
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return "", internalError("id list encoding failed: %v", err)
	}
	return string(raw), nil
}

// isUniqueViolation reports an SQLite unique-constraint failure so explicit
// dedup checks can translate the race window into an exact fault.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// limitOf normalizes the list limit: default 50, maximum 200.
func limitOf(limit int64) int {
	switch {
	case limit <= 0:
		return 50
	case limit > 200:
		return 200
	default:
		return int(limit)
	}
}
