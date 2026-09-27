package controller

import (
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/narrate-it/narrate/narration"
	"github.com/zatiti/zatiti/internal/contract"
)

// Hub bounds. Previews are provisional display state, so every bound
// drops new input rather than blocking worker generation.
const (
	maxReplyRoutes      = 512
	maxReplyPreviews    = 128
	maxReplyIDBytes     = 256
	maxReplyTextBytes   = 32768
	maxPhraseRunes      = 180
	replyRouteRetention = time.Hour
	replyRetention      = 10 * time.Minute
)

type replyKey struct{ actor, installation, conversation contract.ID }
type retainedReply struct {
	route   contract.ReplyRoute
	preview contract.ReplyPreview
	// consumed is the byte offset of preview.Text already covered by
	// preview.Phrases. Phrases are append-only: a phrase index a client has
	// already spoken never changes meaning as more text arrives.
	consumed int
	at       time.Time
}
type retainedRoute struct {
	route contract.ReplyRoute
	at    time.Time
}

// ReplyHub coalesces notifications instead of blocking worker generation on a
// slow client. A subscriber receives complete bounded snapshots, so coalescing
// never loses a fragment. Previews expire and never survive controller restart.
type ReplyHub struct {
	mu       sync.Mutex
	now      func() time.Time
	routes   map[contract.Digest]retainedRoute
	replies  map[string]retainedReply
	watchers map[replyKey]map[chan struct{}]bool
}

func NewReplyHub() *ReplyHub {
	return &ReplyHub{now: time.Now, routes: map[contract.Digest]retainedRoute{}, replies: map[string]retainedReply{}, watchers: map[replyKey]map[chan struct{}]bool{}}
}

func (h *ReplyHub) prune() {
	now := h.now()
	for k, v := range h.routes {
		if now.Sub(v.at) > replyRouteRetention {
			delete(h.routes, k)
		}
	}
	for k, v := range h.replies {
		if now.Sub(v.at) > replyRetention {
			delete(h.replies, k)
		}
	}
}

func (h *ReplyHub) Register(d contract.Digest, r contract.ReplyRoute) {
	if r.Recipient == "" || r.Conversation == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.prune()
	if len(h.routes) >= maxReplyRoutes {
		return
	}
	h.routes[d] = retainedRoute{r, h.now()}
}

func (h *ReplyHub) Route(d contract.Digest) (contract.ReplyRoute, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.prune()
	v, ok := h.routes[d]
	return v.route, ok
}

// Publish records the current preview of one reply item. Text may only
// grow, and only while the item is streaming; a terminal state is final.
func (h *ReplyHub) Publish(r contract.ReplyRoute, id, text, state string) {
	if len(id) == 0 || len(id) > maxReplyIDBytes || len(text) > maxReplyTextBytes || r.Recipient == "" || r.Conversation == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.prune()
	old, ok := h.replies[id]
	if ok && (old.route != r || !strings.HasPrefix(text, old.preview.Text) || old.preview.State != "streaming") {
		return
	}
	if !ok && len(h.replies) >= maxReplyPreviews {
		return
	}
	v := retainedReply{route: r, at: h.now()}
	if ok {
		v.consumed = old.consumed
		v.preview.Phrases = old.preview.Phrases
	}
	if state != "interrupted" {
		// An interrupted reply keeps the phrases it already had; its
		// unfinished tail is never promoted to a speakable phrase.
		added, consumed := appendPhrases(text, v.consumed, state == "generated")
		v.preview.Phrases = append(append([]string{}, v.preview.Phrases...), added...)
		v.consumed = consumed
	}
	if v.preview.Phrases == nil {
		v.preview.Phrases = []string{}
	}
	v.preview.Source, v.preview.ID, v.preview.Turn, v.preview.Worker = r.Source, id, r.Turn, r.Worker
	v.preview.VoiceSession, v.preview.Text, v.preview.State = r.VoiceSession, text, state
	h.replies[id] = v
	h.notify(r)
}

// notify wakes every watcher of r's recipient view without blocking; the
// buffered channel coalesces bursts into one snapshot read.
func (h *ReplyHub) notify(r contract.ReplyRoute) {
	for ch := range h.watchers[replyKey{r.Recipient, r.Scope.InstallationID, r.Conversation}] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (h *ReplyHub) Watch(actor, installation, conversation contract.ID) (<-chan struct{}, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	k := replyKey{actor, installation, conversation}
	ch := make(chan struct{}, 1)
	if h.watchers[k] == nil {
		h.watchers[k] = map[chan struct{}]bool{}
	}
	h.watchers[k][ch] = true
	ch <- struct{}{}
	return ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.watchers[k], ch)
		if len(h.watchers[k]) == 0 {
			delete(h.watchers, k)
		}
	}
}

// Snapshot returns only previews routed to exactly this recipient,
// installation and conversation.
func (h *ReplyHub) Snapshot(actor, installation, conversation contract.ID) []contract.ReplyPreview {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.prune()
	out := []contract.ReplyPreview{}
	for _, v := range h.replies {
		r := v.route
		if r.Recipient == actor && r.Scope.InstallationID == installation && r.Conversation == conversation {
			p := v.preview
			p.Phrases = append([]string{}, p.Phrases...)
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Commit marks a turn's generated preview committed once execution has
// recorded a reply proposal with exactly that text. It says nothing about
// durable conversation history.
func (h *ReplyHub) Commit(turn contract.ID, text string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, v := range h.replies {
		if v.route.Turn == turn && v.preview.State == "generated" && v.preview.Text == text {
			v.preview.State = "committed"
			h.replies[id] = v
			h.notify(v.route)
		}
	}
}

// appendPhrases returns the speakable phrases in text[from:] and the new
// consumed offset. Before the reply is done, only text up to the last
// sentence or paragraph boundary is eligible, so a phrase never ends
// mid-sentence; a long run with no boundary is released at a word break
// once it exceeds the phrase bound. Narrate's chunker then bounds each
// released span. Earlier phrases are never recomputed: the chunker merges
// pieces greedily, so re-chunking the whole text could change a phrase a
// client already spoke.
func appendPhrases(text string, from int, done bool) ([]string, int) {
	if from > len(text) {
		return nil, from
	}
	rest := text[from:]
	end := len(rest)
	if !done {
		end = releasable(rest)
	}
	if end == 0 {
		return nil, from
	}
	out := []string{}
	for _, c := range narration.NewChunker(maxPhraseRunes, 0).Chunk(rest[:end]) {
		if t := strings.TrimSpace(c.Text); t != "" {
			out = append(out, t)
		}
	}
	return out, from + end
}

// releasable is the byte length of the longest prefix of s that ends at a
// sentence or paragraph boundary, or at a word break when an unbounded
// sentence already exceeds the phrase bound.
func releasable(s string) int {
	end := 0
	for i, r := range s {
		next := i + utf8.RuneLen(r)
		switch {
		case r == '\n':
			end = next
		case (r == '.' || r == '!' || r == '?') && next < len(s):
			if n, _ := utf8.DecodeRuneInString(s[next:]); unicode.IsSpace(n) {
				end = next
			}
		}
	}
	if utf8.RuneCountInString(s[end:]) > maxPhraseRunes {
		tail := s[end:]
		cut := 0
		runes := 0
		for i, r := range tail {
			if runes >= maxPhraseRunes {
				break
			}
			if unicode.IsSpace(r) {
				cut = i
			}
			runes++
		}
		if cut > 0 {
			end += cut
		}
	}
	return end
}
