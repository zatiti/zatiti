package messaging

import (
	"github.com/zatiti/zatiti/internal/contract"
)

// ownerName is the domain owner identity used in descriptors and events.
const ownerName = "messaging"

// migrationV1 creates the messaging-owned tables. All objects live inside
// the messaging_ namespace: conversations carry membership and the
// meaningful-activity projection, messages carry the durable mail records,
// recipients carry the per-recipient inbox admission with the exact
// delivered content, receipts carry durable acknowledgements and read
// markers carry per-participant read positions.
const migrationV1 = `CREATE TABLE messaging_conversations (
	id TEXT PRIMARY KEY,
	version INTEGER NOT NULL,
	installation_id TEXT NOT NULL,
	organization_id TEXT NOT NULL DEFAULT '',
	scope_json TEXT NOT NULL,
	kind TEXT NOT NULL,
	title TEXT NOT NULL DEFAULT '',
	pinned INTEGER NOT NULL DEFAULT 0,
	key TEXT NOT NULL DEFAULT '',
	participant_ids_json TEXT NOT NULL,
	last_meaningful_event TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE INDEX messaging_conversations_installation ON messaging_conversations (installation_id, pinned, last_meaningful_event);
CREATE INDEX messaging_conversations_org ON messaging_conversations (organization_id);
CREATE UNIQUE INDEX messaging_conversations_key ON messaging_conversations (installation_id, key) WHERE key != '';
CREATE TABLE messaging_messages (
	id TEXT PRIMARY KEY,
	version INTEGER NOT NULL,
	installation_id TEXT NOT NULL,
	organization_id TEXT NOT NULL DEFAULT '',
	conversation_id TEXT NOT NULL DEFAULT '',
	scope_json TEXT NOT NULL,
	sender_id TEXT NOT NULL,
	body TEXT NOT NULL DEFAULT '',
	attachments_json TEXT NOT NULL,
	task_ids_json TEXT NOT NULL,
	state TEXT NOT NULL,
	meaningful INTEGER NOT NULL DEFAULT 0,
	content_digest TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE INDEX messaging_messages_conversation ON messaging_messages (conversation_id, created_at);
CREATE INDEX messaging_messages_sender ON messaging_messages (sender_id);
CREATE TABLE messaging_recipients (
	message_id TEXT NOT NULL,
	recipient_id TEXT NOT NULL,
	installation_id TEXT NOT NULL,
	state TEXT NOT NULL,
	delivered_json TEXT NOT NULL,
	admitted_at TEXT NOT NULL,
	acknowledged_at TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (message_id, recipient_id)
);
CREATE INDEX messaging_recipients_inbox ON messaging_recipients (recipient_id, state, admitted_at);
CREATE TABLE messaging_receipts (
	id TEXT PRIMARY KEY,
	message_id TEXT NOT NULL,
	recipient_id TEXT NOT NULL,
	installation_id TEXT NOT NULL,
	acknowledged_at TEXT NOT NULL,
	UNIQUE (message_id, recipient_id)
);
CREATE INDEX messaging_receipts_recipient ON messaging_receipts (recipient_id, acknowledged_at);
CREATE TABLE messaging_read_markers (
	conversation_id TEXT NOT NULL,
	principal_id TEXT NOT NULL,
	installation_id TEXT NOT NULL,
	last_read_message_id TEXT NOT NULL DEFAULT '',
	last_read_at TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL,
	PRIMARY KEY (conversation_id, principal_id)
);`

// migrationV2 adds the durable message-to-turn admission link: revision 3's
// _messaging.processed records exactly one turn per (message_id,
// recipient_id) pair here, in the same transaction as the execution owner's
// turn admission/context commit. _messaging.ready's bounded fair scan reads
// messaging_recipients left-joined against this table: a recipient row with
// no matching link is still awaiting a durable worker turn. The added index
// on messaging_recipients supports that scan, which is not scoped to one
// recipient_id and so cannot use the existing (recipient_id, state,
// admitted_at) index.
const migrationV2 = `CREATE TABLE messaging_turn_links (
	message_id TEXT NOT NULL,
	recipient_id TEXT NOT NULL,
	installation_id TEXT NOT NULL,
	turn_id TEXT NOT NULL,
	context_artifact_json TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	PRIMARY KEY (message_id, recipient_id)
);
CREATE INDEX messaging_turn_links_recipient ON messaging_turn_links (recipient_id);
CREATE INDEX messaging_recipients_ready ON messaging_recipients (state, admitted_at);`

// messagingMigrations returns the owned migration list.
func messagingMigrations() []contract.Migration {
	return []contract.Migration{
		{
			Owner:   ownerName,
			Version: 1,
			SQL:     migrationV1,
			SHA256:  contract.Hash([]byte(migrationV1)),
		},
		{
			Owner:   ownerName,
			Version: 2,
			SQL:     migrationV2,
			SHA256:  contract.Hash([]byte(migrationV2)),
		},
	}
}
