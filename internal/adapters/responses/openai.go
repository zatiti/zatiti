package responses

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// openaiProtocolRevision is the exact protocol revision string a profile's
// capability_evidence.protocol_revision must carry to select this
// protocol. It names the published OpenAPI document this file was written
// against; PROTOCOL.md records the URL, commit and date.
const openaiProtocolRevision = "openai-openapi/2.3.0@ddface9b"

// Documented constants this protocol relies on. Every one is cited in
// PROTOCOL.md.
const (
	// openaiFlatTierMaxInputTokens is the largest input the flat published
	// rates cover: prompts above it are billed at a higher tier for the
	// whole request, which the profile's two rates cannot express.
	openaiFlatTierMaxInputTokens = 272000
	// openaiMinOutputTokens is the documented minimum of max_output_tokens.
	openaiMinOutputTokens = 16
	// openaiReconcilePageLimit is the documented maximum page size of the
	// conversation items list.
	openaiReconcilePageLimit = 100
	// openaiServiceTier is the only service tier the profile's rates price.
	openaiServiceTier = "default"
)

// capInputTokenBoundBytes is the capability string a profile's
// capability_evidence.capabilities must carry before this protocol claims
// an input token bound. The bound (see openaiProtocol.inputTokenBound) is
// an assumption about the provider's tokenizer that only live
// qualification can establish; without the string the protocol reports no
// bound and an enforced-cost profile refuses to send.
const capInputTokenBoundBytes = "input_token_bound:utf8_bytes"

// openaiProtocol is the wire protocol for the OpenAI Responses API as
// documented in the pinned OpenAPI revision. It is stateless.
type openaiProtocol struct{}

func (openaiProtocol) limits() protocolLimits {
	return protocolLimits{
		BoundsOutputTokens:   true, // max_output_tokens is documented as an upper bound including reasoning tokens
		SupportsContinuation: false,
		MinOutputTokens:      openaiMinOutputTokens,
		SupportsReconcile:    true,
	}
}

// validateProfile holds the profile to what this protocol can price and
// address: the endpoint must be the responses resource (the conversations
// resource is derived from it), and the input ceiling must stay inside the
// flat pricing tier.
func (openaiProtocol) validateProfile(p protocolProfile) error {
	if _, err := openaiConversationsURL(p.Endpoint); err != nil {
		return err
	}
	if p.MaxInputTokens > openaiFlatTierMaxInputTokens {
		return fmt.Errorf("max_input_tokens %d exceeds the %d-token flat pricing tier; the profile's two rates cannot price a larger prompt", p.MaxInputTokens, openaiFlatTierMaxInputTokens)
	}
	return nil
}

// openaiConversationsURL derives the conversations resource from the
// profile's responses endpoint: the same origin and prefix, with the final
// path segment "responses" replaced by "conversations".
func openaiConversationsURL(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("endpoint is not a URL")
	}
	const suffix = "/responses"
	if !strings.HasSuffix(u.Path, suffix) {
		return "", fmt.Errorf("endpoint path %q does not end in %s; the conversations resource cannot be derived", u.Path, suffix)
	}
	u.Path = strings.TrimSuffix(u.Path, suffix) + "/conversations"
	return u.String(), nil
}

// ---------- request shapes (documented CreateResponse subset) ----------

type openaiInputText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type openaiMessageItem struct {
	Type    string            `json:"type"`
	Role    string            `json:"role"`
	Content []openaiInputText `json:"content"`
}

type openaiFunctionCallItem struct {
	Type      string `json:"type"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openaiFunctionCallOutputItem struct {
	Type   string `json:"type"`
	CallID string `json:"call_id"`
	Output string `json:"output"`
}

type openaiFunctionTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      bool            `json:"strict"`
}

type openaiPromptCacheOptions struct {
	Mode string `json:"mode"`
}

type openaiCreateResponse struct {
	Model              string                   `json:"model"`
	Conversation       string                   `json:"conversation,omitempty"`
	Input              []any                    `json:"input"`
	Tools              []openaiFunctionTool     `json:"tools,omitempty"`
	ToolChoice         string                   `json:"tool_choice,omitempty"`
	MaxOutputTokens    int64                    `json:"max_output_tokens"`
	Store              bool                     `json:"store"`
	Background         bool                     `json:"background"`
	Stream             bool                     `json:"stream"`
	ServiceTier        string                   `json:"service_tier"`
	PromptCacheOptions openaiPromptCacheOptions `json:"prompt_cache_options"`
	Metadata           map[string]string        `json:"metadata"`
}

type openaiCreateConversation struct {
	Metadata map[string]string `json:"metadata"`
}

// ---------- response shapes (documented subset this protocol reads) ----------

type openaiError struct {
	Code    *string `json:"code"`
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Param   *string `json:"param"`
}

type openaiErrorResponse struct {
	Error *openaiError `json:"error"`
}

type openaiResponseError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type openaiIncompleteDetails struct {
	Reason string `json:"reason"`
}

type openaiContentPart struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Refusal string `json:"refusal"`
}

// openaiItem is the union of the output/conversation item fields this
// protocol reads; unknown item types are ignored.
type openaiItem struct {
	Type      string              `json:"type"`
	ID        string              `json:"id"`
	Role      string              `json:"role"`
	Status    string              `json:"status"`
	Content   []openaiContentPart `json:"content"`
	CallID    string              `json:"call_id"`
	Name      string              `json:"name"`
	Arguments string              `json:"arguments"`
}

type openaiUsage struct {
	InputTokens        int64 `json:"input_tokens"`
	InputTokensDetails *struct {
		CachedTokens     int64 `json:"cached_tokens"`
		CacheWriteTokens int64 `json:"cache_write_tokens"`
	} `json:"input_tokens_details"`
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
}

type openaiResponse struct {
	ID                string                   `json:"id"`
	Object            string                   `json:"object"`
	Status            string                   `json:"status"`
	Error             *openaiResponseError     `json:"error"`
	IncompleteDetails *openaiIncompleteDetails `json:"incomplete_details"`
	Output            []openaiItem             `json:"output"`
	Usage             *openaiUsage             `json:"usage"`
	ServiceTier       *string                  `json:"service_tier"`
	Conversation      *struct {
		ID string `json:"id"`
	} `json:"conversation"`
}

type openaiConversation struct {
	ID     string `json:"id"`
	Object string `json:"object"`
}

type openaiConversationItemList struct {
	Object  string       `json:"object"`
	Data    []openaiItem `json:"data"`
	HasMore bool         `json:"has_more"`
}

// ---------- prepare: the conversation the step is journaled under ----------

func openaiMetadata(in protocolRequest) map[string]string {
	return map[string]string{
		"zatiti_operation_id": in.OperationID,
		"zatiti_attempt_id":   in.AttemptID,
	}
}

func (openaiProtocol) prepare(in protocolRequest) (*protocolCall, error) {
	destination, err := openaiConversationsURL(in.Profile.Endpoint)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(openaiCreateConversation{Metadata: openaiMetadata(in)})
	if err != nil {
		return nil, err
	}
	return &protocolCall{
		Method:      http.MethodPost,
		Destination: destination,
		Header:      http.Header{"Content-Type": []string{"application/json"}},
		Body:        body,
	}, nil
}

var openaiHandlePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)

func (openaiProtocol) decodePrepare(status int, _ http.Header, body []byte) (string, error) {
	if status < 200 || status >= 300 {
		return "", errors.New(openaiErrorText(body, status))
	}
	var conv openaiConversation
	if err := json.Unmarshal(body, &conv); err != nil {
		return "", fmt.Errorf("conversation body is not JSON: %w", err)
	}
	if conv.Object != "conversation" || !openaiHandlePattern.MatchString(conv.ID) {
		return "", fmt.Errorf("conversation body is not a conversation object with a usable id")
	}
	return conv.ID, nil
}

// openaiErrorText renders a documented error body as "type/code: message",
// or a status-only note when the body is not one.
func openaiErrorText(body []byte, status int) string {
	var er openaiErrorResponse
	if json.Unmarshal(body, &er) == nil && er.Error != nil && er.Error.Message != "" {
		code := er.Error.Type
		if er.Error.Code != nil && *er.Error.Code != "" {
			code += "/" + *er.Error.Code
		}
		return code + ": " + er.Error.Message
	}
	return fmt.Sprintf("HTTP %d without a documented error body", status)
}

// ---------- encode: the model step ----------

func (openaiProtocol) authorize(h http.Header, secret []byte) {
	h.Set("Authorization", "Bearer "+string(secret))
}

func (openaiProtocol) encode(in protocolRequest, handle string) (protocolCall, error) {
	items, tools, err := openaiItems(in.Context)
	if err != nil {
		return protocolCall{}, err
	}
	req := openaiCreateResponse{
		Model:              in.Profile.Model,
		Conversation:       handle,
		Input:              items,
		Tools:              tools,
		MaxOutputTokens:    in.MaxOutputTokens,
		Store:              true,
		Background:         false,
		Stream:             false,
		ServiceTier:        openaiServiceTier,
		PromptCacheOptions: openaiPromptCacheOptions{Mode: "explicit"},
		Metadata:           openaiMetadata(in),
	}
	if len(tools) > 0 {
		req.ToolChoice = "auto"
	}
	body, err := json.Marshal(req)
	if err != nil {
		return protocolCall{}, err
	}
	return protocolCall{
		Method:      http.MethodPost,
		Destination: in.Profile.Endpoint,
		Header:      http.Header{"Content-Type": []string{"application/json"}},
		Body:        body,
	}, nil
}

// openaiItems translates the persisted context into input items in exact
// semantic order, plus the function tools the context declares. A part the
// documented item shapes cannot carry is an error, never dropped.
func openaiItems(doc *contextDocument) ([]any, []openaiFunctionTool, error) {
	toolNames := make(map[wireVersionRef]string, len(doc.Document.Tools))
	tools := make([]openaiFunctionTool, 0, len(doc.Document.Tools))
	for _, t := range doc.Document.Tools {
		toolNames[t.Tool] = t.Name
		tools = append(tools, openaiFunctionTool{Type: "function", Name: t.Name, Description: t.Description, Parameters: t.InputSchema, Strict: false})
	}

	items := make([]any, 0, len(doc.Messages))
	for _, msg := range doc.Messages {
		var pending []openaiInputText
		flush := func() {
			if len(pending) > 0 {
				items = append(items, openaiMessageItem{Type: "message", Role: msg.Role, Content: pending})
				pending = nil
			}
		}
		for _, part := range msg.DecodedParts {
			switch part.Kind {
			case partText:
				pending = append(pending, openaiInputText{Type: "input_text", Text: part.Text.Text})
			case partMemoryExcerpt:
				pending = append(pending, openaiInputText{Type: "input_text", Text: part.MemoryExcerpt.Text})
			case partArtifact:
				if !strings.HasPrefix(part.Artifact.MediaType, "text/") {
					return nil, nil, fmt.Errorf("artifact part %s has media type %q; only text/* artifacts are carried as input_text", part.Artifact.Artifact.ID, part.Artifact.MediaType)
				}
				pending = append(pending, openaiInputText{Type: "input_text", Text: string(part.Bytes)})
			case partToolCall:
				flush()
				name, ok := toolNames[part.ToolCall.Proposal.Tool]
				if !ok {
					return nil, nil, fmt.Errorf("tool call %s names tool %s version %d, which the context does not declare", part.ToolCall.Proposal.ID, part.ToolCall.Proposal.Tool.ID, part.ToolCall.Proposal.Tool.Version)
				}
				items = append(items, openaiFunctionCallItem{Type: "function_call", CallID: part.ToolCall.Proposal.ID, Name: name, Arguments: string(part.ToolCall.Proposal.Input)})
			case partToolResult:
				flush()
				items = append(items, openaiFunctionCallOutputItem{Type: "function_call_output", CallID: part.ToolResult.ProposalID, Output: string(part.Bytes)})
			default:
				return nil, nil, fmt.Errorf("context part kind %q has no documented item shape", part.Kind)
			}
		}
		if msg.Role == "tool" && len(pending) > 0 {
			return nil, nil, fmt.Errorf("message %s has role tool with text content; tool results are carried only as function_call_output items", msg.ID)
		}
		flush()
	}
	return items, tools, nil
}

// inputTokenBound is the qualified assumption that the provider bills at
// most one input token per UTF-8 byte of model-visible text, plus a fixed
// per-item and per-tool overhead. It is claimed only when the profile's
// capability evidence carries capInputTokenBoundBytes, i.e. after live
// qualification compared reported input_tokens against it. The adapter
// core additionally flags any observed usage above the bound.
func (openaiProtocol) inputTokenBound(in protocolRequest) *int64 {
	claimed := false
	for _, c := range in.Profile.Capabilities {
		if c == capInputTokenBoundBytes {
			claimed = true
			break
		}
	}
	if !claimed || in.Context == nil {
		return nil
	}
	const perItem, perTool, fixed = 8, 8, 32
	var n int64 = fixed
	for _, msg := range in.Context.Messages {
		n += perItem
		for _, part := range msg.DecodedParts {
			switch part.Kind {
			case partText:
				n += int64(len(part.Text.Text))
			case partMemoryExcerpt:
				n += int64(len(part.MemoryExcerpt.Text))
			case partArtifact, partToolResult:
				n += perItem + int64(len(part.Bytes))
			case partToolCall:
				n += perItem + int64(len(part.ToolCall.Proposal.Input)) + 128
			}
		}
	}
	for _, t := range in.Context.Document.Tools {
		n += perTool + int64(len(t.Name)+len(t.Description)+len(t.InputSchema))
	}
	return &n
}

// ---------- decode: the model step's response ----------

func (openaiProtocol) decode(status int, _ http.Header, body []byte) (protocolResult, error) {
	if status < 200 || status >= 300 {
		var er openaiErrorResponse
		r := protocolResult{}
		if json.Unmarshal(body, &er) == nil && er.Error != nil {
			r.ErrorCode = er.Error.Type
			if er.Error.Code != nil && *er.Error.Code != "" {
				r.ErrorCode = *er.Error.Code
			}
			r.ErrorMessage = er.Error.Message
		}
		return r, nil
	}
	var resp openaiResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return protocolResult{}, fmt.Errorf("response body is not JSON: %w", err)
	}
	if resp.Object != "response" || resp.ID == "" {
		return protocolResult{}, fmt.Errorf("response body is not a response object")
	}
	r := protocolResult{ResponseID: resp.ID, UsageReference: resp.ID}
	if resp.Conversation != nil {
		r.ContinuationReference = resp.Conversation.ID
	}
	switch resp.Status {
	case "completed", "incomplete":
		r.State = stateCompleted
	case "failed":
		r.State = stateFailed
		r.ErrorCode = "failed"
		if resp.Error != nil {
			r.ErrorCode, r.ErrorMessage = resp.Error.Code, resp.Error.Message
		}
	case "cancelled":
		r.State = stateFailed
		r.ErrorCode = "cancelled"
	case "in_progress", "queued":
		r.State = stateAccepted
	default:
		return protocolResult{}, fmt.Errorf("response status %q is not documented", resp.Status)
	}
	texts, refusals, calls := openaiOutputs(resp.Output)
	r.Texts, r.ToolCalls = texts, calls
	r.Refusal = strings.Join(refusals, "\n")
	if r.State == stateCompleted {
		r.FinishReason = openaiFinishReason(resp, len(texts), len(refusals), len(calls))
	}
	if resp.Usage != nil {
		r.Usage = &protocolTokenUsage{InputTokens: resp.Usage.InputTokens, OutputTokens: resp.Usage.OutputTokens}
		if d := resp.Usage.InputTokensDetails; d != nil && (d.CachedTokens > 0 || d.CacheWriteTokens > 0) {
			r.UsageUnpriceable = fmt.Sprintf("prompt cache activity (cached %d, cache writes %d) is priced at rates the profile does not carry", d.CachedTokens, d.CacheWriteTokens)
		}
	}
	if resp.ServiceTier != nil && *resp.ServiceTier != openaiServiceTier {
		r.UsageUnpriceable = fmt.Sprintf("service tier %q is priced differently from %q", *resp.ServiceTier, openaiServiceTier)
	}
	return r, nil
}

// openaiOutputs collects, in order, the output_text texts, refusal texts and
// function calls from documented output items. Other item types (reasoning,
// built-in tool calls) carry nothing this contract records.
func openaiOutputs(items []openaiItem) (texts, refusals []string, calls []protocolToolCall) {
	for _, item := range items {
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				switch part.Type {
				case "output_text":
					texts = append(texts, part.Text)
				case "refusal":
					refusals = append(refusals, part.Refusal)
				}
			}
		case "function_call":
			args := json.RawMessage(item.Arguments)
			if !json.Valid(args) {
				quoted, _ := json.Marshal(item.Arguments)
				args = quoted
			}
			calls = append(calls, protocolToolCall{ID: item.CallID, Name: item.Name, Arguments: args})
		}
	}
	return texts, refusals, calls
}

func openaiFinishReason(resp openaiResponse, texts, refusals, calls int) string {
	switch {
	case calls > 0:
		return "tool_calls"
	case resp.Status == "incomplete":
		reason := ""
		if resp.IncompleteDetails != nil {
			reason = resp.IncompleteDetails.Reason
		}
		switch reason {
		case "max_output_tokens":
			return "length_limit"
		case "content_filter":
			return "refused"
		default:
			return "interrupted"
		}
	case refusals > 0 && texts == 0:
		return "refused"
	default:
		return "completed"
	}
}

// ---------- reconcile: the documented authoritative lookup ----------

func (openaiProtocol) reconcile(p protocolProfile, handle string) (protocolCall, error) {
	if !openaiHandlePattern.MatchString(handle) {
		return protocolCall{}, fmt.Errorf("provider key %q is not a conversation id", handle)
	}
	base, err := openaiConversationsURL(p.Endpoint)
	if err != nil {
		return protocolCall{}, err
	}
	return protocolCall{
		Method:      http.MethodGet,
		Destination: fmt.Sprintf("%s/%s/items?limit=%d&order=asc", base, handle, openaiReconcilePageLimit),
		Header:      http.Header{},
	}, nil
}

func (openaiProtocol) decodeReconcile(status int, _ http.Header, body []byte) (protocolResult, error) {
	if status < 200 || status >= 300 {
		return protocolResult{ErrorMessage: openaiErrorText(body, status)}, nil
	}
	var list openaiConversationItemList
	if err := json.Unmarshal(body, &list); err != nil {
		return protocolResult{}, fmt.Errorf("items body is not JSON: %w", err)
	}
	if list.Object != "list" {
		return protocolResult{}, fmt.Errorf("items body is not a list object")
	}
	// Items are added only after the response completes, so an empty
	// list proves nothing either way.
	if len(list.Data) == 0 {
		return protocolResult{State: stateUnresolved}, nil
	}
	var outputs []openaiItem
	for _, item := range list.Data {
		if item.Type == "function_call" || (item.Type == "message" && item.Role == "assistant") {
			outputs = append(outputs, item)
		}
	}
	texts, refusals, calls := openaiOutputs(outputs)
	r := protocolResult{State: stateCompleted, Texts: texts, ToolCalls: calls, Refusal: strings.Join(refusals, "\n")}
	switch {
	case len(calls) > 0:
		r.FinishReason = "tool_calls"
	case len(refusals) > 0 && len(texts) == 0:
		r.FinishReason = "refused"
	case len(outputs) == 0:
		r.FinishReason = "unknown" // input items present, no assistant output on this page
	default:
		r.FinishReason = "completed"
	}
	if list.HasMore {
		r.ErrorCode = "reconcile_items_truncated"
		r.ErrorMessage = fmt.Sprintf("the conversation has more than %d items; outputs beyond the first page are not recorded", openaiReconcilePageLimit)
	}
	return r, nil
}
