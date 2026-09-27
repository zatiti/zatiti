package contract

// ReplyRoute is trusted execution context, never provider-selected routing.
// Preview disclosure is restricted to the initiating principal; durable replies
// continue through Messaging's normal participant disclosure rules.
type ReplyRoute struct {
	Source       ID
	Scope        Scope
	Recipient    ID
	Conversation ID
	Worker       ID
	Turn         ID
	VoiceSession ID
}

type ReplyPreview struct {
	Source       ID       `json:"source_message_id"`
	Phrases      []string `json:"phrases"`
	ID           string   `json:"id"`
	Turn         ID       `json:"turn_id"`
	Worker       ID       `json:"worker_id"`
	VoiceSession ID       `json:"voice_session_id,omitempty"`
	Text         string   `json:"text"`
	State        string   `json:"state"` // streaming | generated | interrupted
}

// ReplyStreams is an ephemeral, bounded preview channel. It is not a durable
// message store or an authorization capability. Readers must reauthorize the
// conversation through Application before every disclosure.
type ReplyStreams interface {
	Register(Digest, ReplyRoute)
	// Commit marks a turn's generated preview committed once execution has
	// recorded a reply proposal with exactly that text.
	Commit(ID, string)
	Route(Digest) (ReplyRoute, bool)
	Publish(ReplyRoute, string, string, string)
	Watch(ID, ID, ID) (<-chan struct{}, func())
	Snapshot(ID, ID, ID) []ReplyPreview
}
