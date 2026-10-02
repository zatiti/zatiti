package contract

import (
	"context"
	"encoding/json"
	"time"
)

// BrowserChannel transports one already-governed browser command. Exchange
// never retries. Receipt reads journal evidence and never executes commands.
type BrowserChannel interface {
	Exchange(context.Context, BrowserExchange) (BrowserExchangeResult, error)
	Receipt(context.Context, BrowserReceiptQuery) (BrowserReceiptResult, error)
}

type BrowserExchange struct {
	ConnectionID ID              `json:"connection_id"`
	OperationID  ID              `json:"operation_id"`
	AttemptID    ID              `json:"attempt_id"`
	Origin       string          `json:"origin"`
	Generation   int64           `json:"generation"`
	Deadline     time.Time       `json:"deadline"`
	Command      json.RawMessage `json:"command"`
}

// Delivered is authoritative hand-out evidence, independent of reply codes.
// Outcome is replied, refused or uncertain. Delivered without a terminal
// receipt remains uncertain even after disconnect, re-pair or supersession.
type BrowserExchangeResult struct {
	Delivered     bool            `json:"delivered"`
	Outcome       string          `json:"outcome"`
	BootID        string          `json:"boot_id"`
	Generation    int64           `json:"generation"`
	Epoch         string          `json:"epoch"`
	DeliveredAtMS int64           `json:"delivered_at_ms"`
	Code          string          `json:"code,omitempty"`
	Reply         json.RawMessage `json:"reply,omitempty"`
}

// Exactly one of AttemptID or OperationID selects the journal lookup.
type BrowserReceiptQuery struct {
	ConnectionID ID     `json:"connection_id"`
	AttemptID    ID     `json:"attempt_id,omitempty"`
	OperationID  ID     `json:"operation_id,omitempty"`
	BootID       string `json:"boot_id"`
	Epoch        string `json:"epoch"`
}

// State is completed, refused, received, started, not_found, never_delivered,
// offline, boot_mismatch or epoch_mismatch. Only same-boot hand-out absence
// can prove never_delivered; journal absence alone never proves nonexecution.
type BrowserReceiptResult struct {
	State            string          `json:"state"`
	Epoch            string          `json:"epoch"`
	EvictedThroughMS int64           `json:"evicted_through_ms"`
	Code             string          `json:"code,omitempty"`
	Reply            json.RawMessage `json:"reply,omitempty"`
	Records          json.RawMessage `json:"records,omitempty"`
}
