package contract

import (
	"encoding/json"
	"testing"
	"time"
)

func TestAdapterDependenciesBrowserIsOptional(t *testing.T) {
	var deps AdapterDependencies
	if deps.Browser != nil {
		t.Fatal("an unconfigured browser channel must be absent")
	}
}

func TestBrowserExchangeJSONRoundTrip(t *testing.T) {
	want := BrowserExchange{
		ConnectionID: NewID(), OperationID: NewID(), AttemptID: NewID(),
		Origin: "https://example.test", Generation: 7,
		Deadline: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		Command:  json.RawMessage(`{"op":"click","args":{"integer":9007199254740993}}`),
	}
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got BrowserExchange
	if err := DecodeStrict(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.ConnectionID != want.ConnectionID || got.OperationID != want.OperationID || got.AttemptID != want.AttemptID || got.Generation != want.Generation || !got.Deadline.Equal(want.Deadline) || string(got.Command) != string(want.Command) {
		t.Fatalf("exchange identity or opaque command changed: %+v", got)
	}
}
