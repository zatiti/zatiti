package httpread

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sort"

	"github.com/zatiti/zatiti/internal/contract"
)

// requestRecord is the exact secret-free record of the one GET this adapter
// is about to send: method, requested URL, the headers as sent (action
// validation already excludes Authorization, Cookie and raw credentials, so
// nothing here is redacted after the fact) and the validated address the
// connection is pinned to. It is staged before any request byte is written
// and named by physical_call.request_context.
type requestRecord struct {
	Schema        string           `json:"schema"`
	Method        string           `json:"method"`
	URL           string           `json:"url"`
	Headers       []wireReadHeader `json:"headers"`
	PinnedAddress string           `json:"pinned_address"`
}

const requestRecordSchema = "zatiti.httpread.request/v1"

// stageRequestRecord stages the request record and returns the staged
// locator physical_call.request_context carries together with its one
// matching StagedOutput (purpose context). Unlike the response body, this
// staging is not best-effort: the shared contract requires the request
// record to be persisted before sending, so a nil BlobStore or a Stage
// failure is a fault and the caller must send nothing.
func stageRequestRecord(ctx context.Context, blobs contract.BlobStore, req *http.Request, record requestRecord) (wireArtifactLocator, wireStagedOutput, error) {
	if blobs == nil {
		return wireArtifactLocator{}, wireStagedOutput{}, prerequisiteMissing("httpread adapter requires a blob store dependency to stage the request record before sending")
	}
	for name, values := range req.Header {
		for _, v := range values {
			record.Headers = append(record.Headers, wireReadHeader{Name: name, Value: v})
		}
	}
	sort.Slice(record.Headers, func(i, j int) bool {
		if record.Headers[i].Name == record.Headers[j].Name {
			return record.Headers[i].Value < record.Headers[j].Value
		}
		return record.Headers[i].Name < record.Headers[j].Name
	})
	if record.Headers == nil {
		record.Headers = []wireReadHeader{}
	}
	data, err := json.Marshal(record)
	if err != nil {
		return wireArtifactLocator{}, wireStagedOutput{}, internalError("encoding httpread request record failed: %v", err)
	}
	stagingRef, digest, size, err := blobs.Stage(ctx, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return wireArtifactLocator{}, wireStagedOutput{}, blobFault(err)
	}
	return wireArtifactLocator{Kind: "staged", StagingRef: stagingRef, Digest: digest}, wireStagedOutput{
		StagingRef:     stagingRef,
		Digest:         digest,
		Size:           size,
		MediaType:      "application/json",
		Classification: "internal",
		Purpose:        "context",
	}, nil
}

// noChargeUsage is the ProviderUsage this adapter reports: a bounded public
// HTTP GET carries no per-call metered provider cost, so accounting is a
// known, exact zero rather than an unknown/advisory estimate.
func noChargeUsage() wireProviderUsage {
	return wireProviderUsage{
		Accounting: wireUsage{Currency: "USD"},
		Billing:    "no_charge",
	}
}

// stageContent stages the bounded response body as public_source evidence.
// Staging is best-effort: a nil BlobStore or empty body yields no staged
// output, and a Stage failure is returned to the caller to decide whether
// it is worth recording -- it must never erase an already-confirmed
// physical-call observation ("Publication failure ... does not erase a
// confirmed provider observation").
func stageContent(ctx context.Context, blobs contract.BlobStore, body []byte, mediaType string) (*wireStagedOutput, error) {
	if len(body) == 0 || blobs == nil {
		return nil, nil
	}
	stagingRef, digest, size, err := blobs.Stage(ctx, bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return nil, blobFault(err)
	}
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	return &wireStagedOutput{
		StagingRef:     stagingRef,
		Digest:         digest,
		Size:           size,
		MediaType:      mediaType,
		Classification: "public",
		Purpose:        "public_source",
	}, nil
}
