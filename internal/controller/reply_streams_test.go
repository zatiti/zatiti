package controller

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

func testRoute(recipient contract.ID) contract.ReplyRoute {
	return contract.ReplyRoute{
		Source: "msg-1", Scope: contract.Scope{InstallationID: "inst-1"}, Recipient: recipient,
		Conversation: "conv-1", Worker: "worker-1", Turn: "turn-1", VoiceSession: "voice-1",
	}
}

func onlyPreview(t *testing.T, h *ReplyHub, r contract.ReplyRoute) contract.ReplyPreview {
	t.Helper()
	got := h.Snapshot(r.Recipient, r.Scope.InstallationID, r.Conversation)
	if len(got) != 1 {
		t.Fatalf("snapshot has %d previews, want 1: %+v", len(got), got)
	}
	return got[0]
}

// A phrase index a client already spoke must keep its text as the reply
// grows. Re-chunking the whole text would merge the first paragraph into
// the second once the second passes the phrase bound.
func TestReplyPhrasesAreAppendOnlyAsTextGrows(t *testing.T) {
	h := NewReplyHub()
	r := testRoute("human-1")
	para1 := "Here is the short answer."
	sentence := "This sentence adds more detail to the second paragraph. "
	text := para1 + "\n\n"
	var seen []string
	for i := 0; i < 8; i++ {
		text += sentence
		h.Publish(r, "stream-1", text, "streaming")
		p := onlyPreview(t, h, r)
		if len(p.Phrases) < len(seen) || !slices.Equal(p.Phrases[:len(seen)], seen) {
			t.Fatalf("step %d: phrases changed from %q to %q", i, seen, p.Phrases)
		}
		seen = p.Phrases
	}
	if len(seen) < 2 || !strings.HasPrefix(seen[0], para1) {
		t.Fatalf("phrases = %q, want several, starting with the first paragraph", seen)
	}
	h.Publish(r, "stream-1", text+"Final words", "generated")
	p := onlyPreview(t, h, r)
	if !slices.Equal(p.Phrases[:len(seen)], seen) || p.Phrases[len(p.Phrases)-1] != "Final words" {
		t.Fatalf("generated phrases = %q, want previous phrases plus the unfinished tail", p.Phrases)
	}
	for _, phrase := range p.Phrases {
		if n := len([]rune(phrase)); n > maxPhraseRunes {
			t.Fatalf("phrase has %d runes, above the %d bound", n, maxPhraseRunes)
		}
	}
}

func TestReplyPhrasesHoldBackUnfinishedSentences(t *testing.T) {
	h := NewReplyHub()
	r := testRoute("human-1")
	h.Publish(r, "stream-1", "Sure. I am checking", "streaming")
	if p := onlyPreview(t, h, r); !reflect.DeepEqual(p.Phrases, []string{"Sure."}) {
		t.Fatalf("phrases = %q, want only the finished sentence", p.Phrases)
	}
	// A long run with no sentence boundary is released at a word break.
	long := "Sure. I am checking " + strings.Repeat("word ", 60)
	h.Publish(r, "stream-1", long, "streaming")
	p := onlyPreview(t, h, r)
	if len(p.Phrases) != 2 || strings.HasSuffix(p.Phrases[1], "wor") {
		t.Fatalf("phrases = %q, want a word-bounded second phrase", p.Phrases)
	}
}

func TestInterruptedReplyKeepsSpokenPhrasesButNotTheTail(t *testing.T) {
	h := NewReplyHub()
	r := testRoute("human-1")
	h.Publish(r, "stream-1", "One. Two", "streaming")
	h.Publish(r, "stream-1", "One. Two and", "interrupted")
	p := onlyPreview(t, h, r)
	if p.State != "interrupted" || !reflect.DeepEqual(p.Phrases, []string{"One."}) {
		t.Fatalf("preview = %+v, want interrupted with only the finished phrase", p)
	}
	// Terminal states are final: no further text and no resurrection.
	h.Publish(r, "stream-1", "One. Two and three.", "generated")
	if p := onlyPreview(t, h, r); p.State != "interrupted" || p.Text != "One. Two and" {
		t.Fatalf("preview after terminal = %+v, want unchanged", p)
	}
}

func TestReplyPublishRejectsRewritesAndOtherRoutes(t *testing.T) {
	h := NewReplyHub()
	r := testRoute("human-1")
	h.Publish(r, "stream-1", "Hello there.", "streaming")
	h.Publish(r, "stream-1", "Goodbye.", "streaming")
	other := r
	other.Recipient = "human-2"
	h.Publish(other, "stream-1", "Hello there. More.", "streaming")
	if p := onlyPreview(t, h, r); p.Text != "Hello there." {
		t.Fatalf("text = %q, want the rewrite and cross-route update rejected", p.Text)
	}
}

func TestReplySnapshotIsScopedToRecipientInstallationAndConversation(t *testing.T) {
	h := NewReplyHub()
	r := testRoute("human-1")
	h.Publish(r, "stream-1", "Private reply.", "streaming")
	for name, key := range map[string][3]contract.ID{
		"other recipient":    {"human-2", "inst-1", "conv-1"},
		"other installation": {"human-1", "inst-2", "conv-1"},
		"other conversation": {"human-1", "inst-1", "conv-2"},
	} {
		if got := h.Snapshot(key[0], key[1], key[2]); len(got) != 0 {
			t.Fatalf("%s sees %d previews, want none", name, len(got))
		}
	}
	onlyPreview(t, h, r)
}

func TestReplyCommitRequiresTheRecordedText(t *testing.T) {
	h := NewReplyHub()
	r := testRoute("human-1")
	h.Publish(r, "stream-1", "Done.", "streaming")
	h.Commit(r.Turn, "Done.")
	if p := onlyPreview(t, h, r); p.State != "streaming" {
		t.Fatalf("state = %q, want a streaming preview never committed", p.State)
	}
	h.Publish(r, "stream-1", "Done.", "generated")
	h.Commit(r.Turn, "Something else.")
	if p := onlyPreview(t, h, r); p.State != "generated" {
		t.Fatalf("state = %q, want generated when recorded text differs", p.State)
	}
	h.Commit(r.Turn, "Done.")
	if p := onlyPreview(t, h, r); p.State != "committed" {
		t.Fatalf("state = %q, want committed", p.State)
	}
}

// A reconnecting watcher gets an immediate notification and then the same
// current snapshot; bursts coalesce into one pending notification.
func TestReplyWatchCoalescesAndReconnectReadsCurrentSnapshot(t *testing.T) {
	h := NewReplyHub()
	r := testRoute("human-1")
	notify, stop := h.Watch(r.Recipient, r.Scope.InstallationID, r.Conversation)
	<-notify
	h.Publish(r, "stream-1", "One.", "streaming")
	h.Publish(r, "stream-1", "One. Two.", "streaming")
	select {
	case <-notify:
	default:
		t.Fatal("no notification after publish")
	}
	select {
	case <-notify:
		t.Fatal("burst was not coalesced")
	default:
	}
	stop()
	again, stop2 := h.Watch(r.Recipient, r.Scope.InstallationID, r.Conversation)
	defer stop2()
	<-again
	// "Two." has no following space yet, so it is still held back.
	if p := onlyPreview(t, h, r); p.Text != "One. Two." || !reflect.DeepEqual(p.Phrases, []string{"One."}) {
		t.Fatalf("reconnect snapshot = %+v", p)
	}
}

func TestReplyPreviewsExpireAndStayBounded(t *testing.T) {
	now := time.Unix(0, 0)
	h := NewReplyHub()
	h.now = func() time.Time { return now }
	r := testRoute("human-1")
	for i := 0; i < maxReplyPreviews+5; i++ {
		h.Publish(r, "stream-"+string(rune('a'+i%26))+strings.Repeat("x", i), "Hi.", "streaming")
	}
	if got := len(h.Snapshot(r.Recipient, r.Scope.InstallationID, r.Conversation)); got != maxReplyPreviews {
		t.Fatalf("retained %d previews, want the %d bound", got, maxReplyPreviews)
	}
	now = now.Add(replyRetention + time.Second)
	if got := len(h.Snapshot(r.Recipient, r.Scope.InstallationID, r.Conversation)); got != 0 {
		t.Fatalf("retained %d expired previews, want none", got)
	}
}
