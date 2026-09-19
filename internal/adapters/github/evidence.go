package github

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"github.com/zatiti/zatiti/internal/contract"
)

// requestRecordSchema names the request record document below.
const requestRecordSchema = "zatiti.adapter.request-record/v1"

// requestRecordHeader is one permitted request header, in sorted order.
type requestRecordHeader struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

// requestRecord is the exact secret-free record of the one physical request
// an attempt makes: method, destination, the permitted headers and the body
// as sent. The Authorization header is added only after the record is
// built and is never part of it. The body is base64 so the record is exact
// whatever the body's encoding; GET requests record an empty body.
type requestRecord struct {
	Schema      string                `json:"schema"`
	Method      string                `json:"method"`
	Destination string                `json:"destination"`
	Headers     []requestRecordHeader `json:"headers"`
	BodyDigest  contract.Digest       `json:"body_digest"`
	BodySize    int64                 `json:"body_size"`
	BodyBase64  string                `json:"body_base64"`
}

// stageRequestContext stages the request record before any byte is sent
// and returns its StagedOutput (purpose context) plus the staged
// ArtifactLocator that names it as physical_call.request_context. The
// adapter has no IDSource, so it never fabricates an ArtifactRef and never
// reuses capability evidence as a stand-in. Any failure here means nothing
// is sent. A record containing the credential is refused rather than
// staged.
func stageRequestContext(ctx context.Context, blobs contract.BlobStore, secret []byte, method, destination string, permitted http.Header, body []byte) (wireStagedOutput, wireStagedLocator, error) {
	if blobs == nil {
		return wireStagedOutput{}, wireStagedLocator{}, prerequisiteMissing("github adapter requires a blob store dependency to stage the request context before sending")
	}
	record := requestRecord{
		Schema:      requestRecordSchema,
		Method:      method,
		Destination: destination,
		Headers:     make([]requestRecordHeader, 0, len(permitted)),
		BodyDigest:  contract.Hash(body),
		BodySize:    int64(len(body)),
		BodyBase64:  base64.StdEncoding.EncodeToString(body),
	}
	for name, values := range permitted {
		record.Headers = append(record.Headers, requestRecordHeader{Name: name, Values: values})
	}
	slices.SortFunc(record.Headers, func(a, b requestRecordHeader) int { return strings.Compare(a.Name, b.Name) })
	doc, err := json.Marshal(record)
	if err != nil {
		return wireStagedOutput{}, wireStagedLocator{}, internalError("encoding the github request record failed")
	}
	if len(secret) > 0 && (bytes.Contains(doc, secret) || bytes.Contains(body, secret)) {
		return wireStagedOutput{}, wireStagedLocator{}, internalError("the github request record would contain credential material; nothing was staged or sent")
	}
	stagingRef, digest, size, err := blobs.Stage(ctx, bytes.NewReader(doc), int64(len(doc)))
	if err != nil {
		return wireStagedOutput{}, wireStagedLocator{}, blobFault(err)
	}
	staged := wireStagedOutput{
		StagingRef:     stagingRef,
		Digest:         digest,
		Size:           size,
		MediaType:      "application/json",
		Classification: "internal",
		Purpose:        "context",
	}
	return staged, wireStagedLocator{Kind: "staged", StagingRef: stagingRef, Digest: digest}, nil
}

// noChargeUsage is the ProviderUsage this adapter reports: GitHub's
// documented REST API carries no per-call metered cost, so accounting is a
// known, exact zero rather than an unknown/advisory estimate.
func noChargeUsage() wireProviderUsage {
	return wireProviderUsage{
		Accounting: wireUsage{Currency: "USD"},
		Billing:    "no_charge",
	}
}

// stageProviderResponse stages the raw response body as provider_response
// evidence. Staging is best-effort: a nil BlobStore or empty body yields no
// staged output, and a Stage failure is returned to the caller to decide
// whether it is worth recording -- it must never erase an already-confirmed
// provider observation ("Publication failure ... does not erase a
// confirmed provider observation").
func stageProviderResponse(ctx context.Context, blobs contract.BlobStore, body []byte, contentType string) (*wireStagedOutput, error) {
	if len(body) == 0 || blobs == nil {
		return nil, nil
	}
	stagingRef, digest, size, err := blobs.Stage(ctx, bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return nil, blobFault(err)
	}
	mediaType := contentType
	if mediaType == "" {
		mediaType = "application/json"
	}
	return &wireStagedOutput{
		StagingRef:     stagingRef,
		Digest:         digest,
		Size:           size,
		MediaType:      mediaType,
		Classification: "internal",
		Purpose:        "provider_response",
	}, nil
}
