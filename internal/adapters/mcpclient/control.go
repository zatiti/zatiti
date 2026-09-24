package mcpclient

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/zatiti/zatiti/internal/contract"
)

func (a *Adapter) newCallState(ctx context.Context, kind string, secret []byte, limit int64, classification string) *callState {
	callCtx, cancel := context.WithCancel(ctx)
	return &callState{ctx: callCtx, cancel: cancel, kind: kind, secret: secret, blobs: a.deps.Blobs, controlReplyLimit: limit, classification: classification}
}

func (s *callState) abort(code string) {
	s.mu.Lock()
	if s.abortCode == "" {
		s.abortCode = code
	}
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *callState) aborted() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.abortCode
}

func (s *callState) unavailable(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.contextUnavailable == "" {
		s.contextUnavailable = reason
	}
}

// requestClass accepts one primary request, or each distinct declared handshake
// message once, plus explicitly budgeted method-not-found replies. It runs under
// rt.mu so simultaneous SDK replies cannot overdraw their admitted budget.
func (s *callState) requestClass(method string, body []byte) (string, error) {
	if method == http.MethodDelete {
		if s.kind != kindCloseSession {
			return "", internalError("mcpclient: unadmitted session termination")
		}
		if s.primarySeen != nil && s.primarySeen["DELETE"] {
			return "", internalError("mcpclient: repeated session termination was refused")
		}
		if s.primarySeen == nil {
			s.primarySeen = map[string]bool{}
		}
		s.primarySeen["DELETE"] = true
		return "main", nil
	}
	if method != http.MethodPost || s.kind == kindCloseSession {
		return "", internalError("mcpclient: unadmitted HTTP method")
	}
	rpcMethod := peekRPCMethod(body)
	if rpcMethod == "" {
		if !negativeControlReply(body) {
			return "", internalError("mcpclient: only JSON-RPC method-not-found control replies are admitted")
		}
		if s.controlReplies >= s.controlReplyLimit {
			return "", fault("control_reply_limit_exceeded", "mcpclient: control reply budget exhausted")
		}
		s.controlReplies++
		return "control_reply", nil
	}
	allowed := false
	class := "main"
	switch s.kind {
	case kindOpenSession:
		allowed = handshakeMessages[rpcMethod]
		class = "handshake"
	case kindListTools:
		allowed = rpcMethod == "tools/list"
	case kindCallTool:
		allowed = rpcMethod == "tools/call"
	}
	if !allowed {
		return "", internalError("mcpclient: SDK method is outside the admitted action")
	}
	if s.primarySeen == nil {
		s.primarySeen = map[string]bool{}
	}
	if s.primarySeen[rpcMethod] {
		return "", internalError("mcpclient: repeated primary request was refused")
	}
	s.primarySeen[rpcMethod] = true
	return class, nil
}

func negativeControlReply(body []byte) bool {
	var reply struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Error   *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if contract.DecodeStrict(body, &reply) != nil || reply.JSONRPC != "2.0" || reply.Error == nil || reply.Error.Code != -32601 {
		return false
	}
	id := strings.TrimSpace(string(reply.ID))
	if id == "" || id == "null" {
		return false
	}
	var value any
	if json.Unmarshal(reply.ID, &value) != nil {
		return false
	}
	switch value.(type) {
	case string, float64:
		return true
	}
	return false
}

func primaryRequestSent(captured []capturedRequest) string {
	sent := "no"
	for _, request := range captured {
		if request.class == "control_reply" {
			continue
		}
		if request.requestSent == "yes" {
			return "yes"
		}
		if request.requestSent == "unknown" {
			sent = "unknown"
		}
	}
	return sent
}

func (a *Adapter) finishSessionCall(entry *sessionEntry, handle string, state *callState) {
	entry.roundTripper.disarm()
	if state.aborted() != "" || state.ctx.Err() != nil {
		a.sessions.remove(handle)
		// Disarmed before SDK cleanup so it cannot issue an unadmitted DELETE.
		_ = entry.session.Close()
	}
}
