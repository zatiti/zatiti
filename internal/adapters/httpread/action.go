package httpread

import (
	"encoding/json"
	"net/url"

	"github.com/zatiti/zatiti/internal/contract"
)

// kindRead is the only action kind the frozen zatiti.httpread.action/v1
// schema accepts.
const kindRead = "read"

// action is the decoded, parsed Dispatch.Action.
type action struct {
	wireHTTPReadParameters
	parsedURL *url.URL
	origin    string
}

// decodeAction validates raw against the composed zatiti.httpread.action/v1
// schema, strict-decodes it, and runs the Go-level validation the frozen
// JSON Schema cannot express: the URL must actually parse, and it must not
// carry embedded userinfo credentials -- the permitted header set already
// excludes raw secret input, and a URL-embedded credential would smuggle
// exactly that back in.
func decodeAction(raw json.RawMessage) (*action, error) {
	schema, err := parametersSchema()
	if err != nil {
		return nil, internalError("httpread parameters schema composition failed: %v", err)
	}
	if err := contract.ValidateSchema(schema, raw); err != nil {
		return nil, invalidInput("httpread action does not match the zatiti.httpread.action/v1 schema: %v", err)
	}

	var w wireHTTPReadParameters
	if err := contract.DecodeStrict(raw, &w); err != nil {
		return nil, invalidInput("httpread action decode failed: %v", err)
	}

	parsed, err := url.Parse(w.URL)
	if err != nil {
		return nil, invalidInput("httpread action url %q does not parse: %v", w.URL, err)
	}
	if parsed.Host == "" {
		return nil, invalidInput("httpread action url %q has no host", w.URL)
	}
	if parsed.User != nil {
		return nil, invalidInput("httpread action url must not carry embedded userinfo credentials")
	}

	return &action{
		wireHTTPReadParameters: w,
		parsedURL:              parsed,
		origin:                 parsed.Scheme + "://" + parsed.Host,
	}, nil
}
