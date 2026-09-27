package server

import (
	"encoding/json"
	"fmt"
	"github.com/zatiti/zatiti/internal/contract"
	"net/http"
	"time"
)

// A read-only HTTP event stream uses the same authentication and Application
// authorization as ordinary conversation reads. Reconnect obtains a current
// bounded snapshot; it never resubmits a command or replays speech.
func streamMux(h *operationHandler, hub contract.ReplyStreams) *http.ServeMux {
	mux := newMux(h)
	if hub == nil {
		return mux
	}
	mux.HandleFunc("POST /v1/replies/stream", func(w http.ResponseWriter, r *http.Request) {
		if f := checkContentType(r); f != nil {
			writeEnvelope(w, faultEnvelope(f))
			return
		}
		req, f := decodeRequest(r, h.maxBodyBytes)
		if f != nil {
			writeEnvelope(w, faultEnvelope(f))
			return
		}
		var in struct {
			Scope        contract.Scope `json:"scope"`
			Conversation contract.ID    `json:"conversation_id"`
		}
		if err := contract.DecodeStrict(req.Input, &in); err != nil || in.Conversation == "" || in.Scope.InstallationID == "" || req.SubmissionKey != "" {
			writeEnvelope(w, faultEnvelope(&contract.Fault{Code: contract.CodeInvalidInput, Message: "conversation stream input is invalid"}))
			return
		}
		actor, f := h.authenticate(r.Context(), r)
		if f != nil {
			writeEnvelope(w, faultEnvelope(f))
			return
		}
		if actor.Kind != contract.KindHuman {
			writeEnvelope(w, faultEnvelope(&contract.Fault{Code: contract.CodePermissionDenied, Message: "reply previews are human-only"}))
			return
		}
		input, _ := json.Marshal(map[string]any{"scope": in.Scope, "conversation_id": in.Conversation, "limit": 1})
		authorize := func() bool {
			current, f := h.authenticate(r.Context(), r)
			if f != nil || current != actor {
				return false
			}
			result := invoke(r.Context(), h.app, actor, "conversation.message.list", contract.Request{Schema: contract.SchemaRequest, Input: input})
			return result.Status == contract.StatusCompleted
		}
		if !authorize() {
			writeEnvelope(w, faultEnvelope(&contract.Fault{Code: contract.CodePermissionDenied, Message: "conversation stream unavailable"}))
			return
		}
		notify, closeWatch := hub.Watch(actor.PrincipalID, in.Scope.InstallationID, in.Conversation)
		defer closeWatch()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Accel-Buffering", "no")
		rc := http.NewResponseController(w)
		heartbeat := time.NewTicker(15 * time.Second)
		defer heartbeat.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-heartbeat.C:
			case <-notify:
			}
			if !authorize() {
				return
			}
			data, err := json.Marshal(hub.Snapshot(actor.PrincipalID, in.Scope.InstallationID, in.Conversation))
			if err != nil {
				return
			}
			_ = rc.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, err = fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return
			}
			if err = rc.Flush(); err != nil {
				return
			}
		}
	})
	return mux
}
