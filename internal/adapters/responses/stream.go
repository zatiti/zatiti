package responses

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"github.com/zatiti/zatiti/internal/contract"
	"io"
	"strings"
	"unicode/utf8"
)

// replyPrefix only decodes the sealed reply tool's sole text field. It never
// forwards free model prose, reasoning, or another tool's partial arguments.
func replyPrefix(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" || s == "{" {
		return "", nil
	}
	if !strings.HasPrefix(s, "{") {
		return "", errors.New("invalid reply object")
	}
	s = strings.TrimSpace(s[1:])
	prefix := `"text"`
	if len(s) < len(prefix) {
		if strings.HasPrefix(prefix, s) {
			return "", nil
		}
		return "", errors.New("invalid reply key")
	}
	if !strings.HasPrefix(s, prefix) {
		return "", errors.New("invalid reply key")
	}
	s = strings.TrimSpace(s[len(prefix):])
	if s == "" {
		return "", nil
	}
	if s[0] != ':' {
		return "", errors.New("invalid reply separator")
	}
	s = strings.TrimSpace(s[1:])
	if s == "" {
		return "", nil
	}
	if s[0] != '"' {
		return "", errors.New("invalid reply text")
	}
	end := 1
	for end < len(s) {
		c := s[end]
		if c == '"' {
			var v struct {
				Text string `json:"text"`
			}
			if err := contract.DecodeStrict([]byte(raw), &v); err != nil { // closing brace may be in next delta
				if strings.TrimSpace(s[end+1:]) != "" {
					return "", err
				}
			}
			var text string
			err := json.Unmarshal([]byte(s[:end+1]), &text)
			return text, err
		}
		if c == '\\' {
			if end+1 >= len(s) {
				break
			}
			if s[end+1] == 'u' {
				if end+6 > len(s) {
					break
				}
				var u string
				if err := json.Unmarshal([]byte(`"`+s[end:end+6]+`"`), &u); err != nil {
					return "", err
				}
				if strings.HasPrefix(strings.ToLower(s[end:end+6]), `\ud8`) || strings.HasPrefix(strings.ToLower(s[end:end+6]), `\ud9`) || strings.HasPrefix(strings.ToLower(s[end:end+6]), `\uda`) || strings.HasPrefix(strings.ToLower(s[end:end+6]), `\udb`) {
					if end+12 > len(s) {
						break
					}
					end += 12
				} else {
					end += 6
				}
				continue
			}
			end += 2
			continue
		}
		if !utf8.FullRuneInString(s[end:]) {
			break
		}
		_, n := utf8.DecodeRuneInString(s[end:])
		end += n
	}
	var text string
	err := json.Unmarshal([]byte(s[:end]+`"`), &text)
	return text, err
}

type streamItem struct {
	args, text string
	id         string
	// broken stops disclosure of this item. A preview fault never fails
	// the model step: the terminal response stays authoritative.
	broken bool
}

// readReplyStream retains the exact bounded SSE evidence while returning the
// provider's terminal response to the existing outcome/accounting decoder.
// Only framing faults and a missing terminal response are read errors;
// anything wrong with a reply preview only withdraws that preview.
// A nil hub decodes the stream without publishing any preview.
func readReplyStream(r io.Reader, max int64, hub contract.ReplyStreams, route contract.ReplyRoute, attempt string) (body, wire []byte, err error) {
	publish := func(id, text, state string) {
		if hub != nil {
			hub.Publish(route, id, text, state)
		}
	}
	var captured bytes.Buffer
	scanner := bufio.NewScanner(io.TeeReader(io.LimitReader(r, max+1), &captured))
	scanner.Buffer(make([]byte, 4096), int(max+1))
	items := map[string]*streamItem{}
	terminal, completed := false, false
	var data strings.Builder
	defer func() {
		wire = append([]byte(nil), captured.Bytes()...)
		for _, v := range items {
			state := "generated"
			if err != nil || !completed || v.broken {
				state = "interrupted"
			}
			publish(v.id, v.text, state)
		}
	}()
	dispatch := func() error {
		if data.Len() == 0 {
			return nil
		}
		raw := strings.TrimSpace(data.String())
		data.Reset()
		if raw == "[DONE]" {
			return nil
		}
		var e struct {
			Type     string                                     `json:"type"`
			ItemID   string                                     `json:"item_id"`
			Delta    string                                     `json:"delta"`
			Item     struct{ ID, Type, Name, Arguments string } `json:"item"`
			Response json.RawMessage                            `json:"response"`
		}
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			return err
		}
		switch e.Type {
		case "response.output_item.added":
			if e.Item.Type == "function_call" && e.Item.Name == contract.LocalDecisionToolReply && items[e.Item.ID] == nil && len(items) < 4 {
				items[e.Item.ID] = &streamItem{id: attempt + ":" + e.Item.ID, args: e.Item.Arguments}
			}
		case "response.function_call_arguments.delta":
			v := items[e.ItemID]
			if v == nil || v.broken {
				return nil
			}
			v.args += e.Delta
			text, perr := replyPrefix(v.args)
			if perr != nil || len(v.args) > 65536 || len([]rune(text)) > 8192 || !strings.HasPrefix(text, v.text) {
				v.broken = true
				publish(v.id, v.text, "interrupted")
				return nil
			}
			v.text = text
			publish(v.id, text, "streaming")
		case "response.completed", "response.failed", "response.incomplete":
			if terminal {
				return errors.New("duplicate terminal response")
			}
			terminal = true
			completed = e.Type == "response.completed"
			body = e.Response
		case "error":
			return errors.New("provider stream reported an error")
		}
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err = dispatch(); err != nil {
				return
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(line, "data:"))
		}
	}
	if err = scanner.Err(); err != nil {
		return
	}
	if err = dispatch(); err != nil {
		return
	}
	if int64(captured.Len()) > max {
		err = errors.New("provider stream exceeded bound")
		return
	}
	if !terminal || len(body) == 0 {
		err = errors.New("provider stream ended without a terminal response")
		return
	}
	// Each disclosed preview must be a prefix of its final reply arguments;
	// the final text then completes it. A mismatch only withdraws the preview.
	var final struct {
		Output []struct{ ID, Type, Name, Arguments string } `json:"output"`
	}
	if json.Unmarshal(body, &final) != nil {
		for _, v := range items {
			v.broken = true
		}
		return
	}
	for key, v := range items {
		found := false
		for _, item := range final.Output {
			if item.ID != key || item.Type != "function_call" || item.Name != contract.LocalDecisionToolReply {
				continue
			}
			var reply struct {
				Text string `json:"text"`
			}
			if contract.DecodeStrict([]byte(item.Arguments), &reply) == nil && strings.HasPrefix(reply.Text, v.text) && len([]rune(reply.Text)) <= 8192 {
				v.text = reply.Text
				found = true
			}
		}
		if !found {
			v.broken = true
		}
	}
	return
}
