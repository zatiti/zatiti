// Package messaging is the conversations and mailboxes domain owner.
//
// Messaging owns durable conversations (direct and group), stable message
// identity, recipient inbox admission, acknowledgements, receipts, read
// markers and the meaningful-activity projection. Group membership is
// conversation state, not organization membership, grant or memory binding;
// creating or joining a conversation changes no authority.
//
// Delivery is disclosure-governed: before any body or attachment is
// admitted, the policy owner intersects current grants for the
// messaging.disclosure.deliver capability, and attachments resolve through
// the artifacts owner. Content is untrusted data — it never changes grants
// or verifier contracts. Acknowledgement is recorded only after durable
// target inbox admission, redelivery deduplicates message identity with a
// content digest, and the same identity with different content surfaces a
// conflict. Human and client-agent sends are meaningful human-facing events;
// worker-sourced messages are meaningful only under an explicit reporting
// binding whose destinations list the target conversation, so routine
// coordination stays quiet and never reorders chats or marks them unread.
//
// Conversations fence visibility by participant membership; the server
// scopes every list and hierarchy filter, cursors bind principal, query and
// the evidence checkpoint, and worker identities persist across tasks and
// conversations. User requests and chief assignments converge on the
// versioned task operations owned by the tasks owner — messages reference
// tasks by id and never schedule work themselves.
package messaging
