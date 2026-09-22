package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/client"
	"github.com/zatiti/zatiti/internal/contract"
)

// eventPage is one event.list page as a wire client sees it.
type eventPage struct {
	ids    []contract.ID
	cursor *string
}

func (f *fixture) eventsPage(op contract.Operator, limit int, cursor *string) (eventPage, contract.Result, error) {
	f.t.Helper()
	input := map[string]any{"scope": f.scope(), "limit": limit}
	if cursor != nil {
		input["cursor"] = *cursor
	}
	res, err := op.Call(context.Background(), "event.list", contract.Request{Schema: contract.SchemaRequest, Input: mustJSON(input)})
	if err != nil {
		return eventPage{}, res, err
	}
	var out struct {
		Items []struct {
			ID contract.ID `json:"id"`
		} `json:"items"`
	}
	decode(f.t, res.Data, &out)
	page := eventPage{cursor: res.NextCursor}
	for _, e := range out.Items {
		page.ids = append(page.ids, e.ID)
	}
	return page, res, nil
}

// TestExpiredCursorDemandsSnapshotAndReplaysWithoutGap (Z16 cursor expiry;
// R8.4-006; desktop contract): an event.list cursor that outlives its
// window fails cursor_expired with snapshot_required=true through
// internal/client (as *client.CursorExpiredError) and over the raw wire (HTTP
// 410 with the detail in the envelope); the client's recovery, a fresh
// snapshot, presents the complete history including what happened during
// the gap, and an unexpired cursor still pages exactly.
func TestExpiredCursorDemandsSnapshotAndReplaysWithoutGap(t *testing.T) {
	t.Parallel()
	f := newBootstrappedFixture(t)
	f.provisionTransportCredential(transportToken)
	sock := f.serve()
	op := newClient(t, sock, staticCredential(transportToken))

	first, _, err := f.eventsPage(op, 2, nil)
	if err != nil || len(first.ids) != 2 || first.cursor == nil {
		t.Fatalf("first page: %+v err %v, want two events and a cursor", first, err)
	}
	second, _, err := f.eventsPage(op, 2, first.cursor)
	if err != nil || len(second.ids) != 2 || second.cursor == nil || second.ids[0] == first.ids[0] {
		t.Fatalf("second page: %+v err %v, want the next two events and a cursor", second, err)
	}

	// Something happens while the client holds its cursor, then the cursor
	// outlives its 15-minute window.
	f.must(f.owner, "principal.create", "cursor-gap", principalInput(f, "cursor-agent"))
	f.clock.Advance(16 * time.Minute)

	_, _, err = f.eventsPage(op, 2, second.cursor)
	var expired *client.CursorExpiredError
	if !errors.As(err, &expired) || !expired.SnapshotRequired || faultCode(err) != contract.CodeCursorExpired {
		t.Fatalf("expired cursor through the client: %v (%T), want *client.CursorExpiredError with snapshot_required", err, err)
	}
	status, wire, werr := (wireTransport{sock: sock, token: transportToken}).post(t, "event.list", contract.Request{
		Schema: contract.SchemaRequest, Input: mustJSON(map[string]any{"scope": f.scope(), "limit": 2, "cursor": *second.cursor}),
	})
	if werr != nil || status != http.StatusGone || wire.Error == nil || wire.Error.Code != contract.CodeCursorExpired {
		t.Fatalf("expired cursor over the wire: http %d %+v err %v, want 410 cursor_expired", status, wire, werr)
	}
	var details struct {
		SnapshotRequired bool `json:"snapshot_required"`
	}
	if err := json.Unmarshal(wire.Error.Details, &details); err != nil || !details.SnapshotRequired {
		t.Fatalf("wire cursor_expired details %s, want snapshot_required=true", wire.Error.Details)
	}

	// Recovery: a fresh snapshot, paged from the start, is the complete
	// history; the pages the client already had are its prefix and the gap
	// event is present.
	// P34 mints a resumable cursor even on a drained page (see
	// internal/evidence/handlers.go's eventList: "the position it resumed
	// from ... rather than no cursor at all"), so a nil cursor is never the
	// drain signal. A short page (fewer than the requested limit) is.
	var snapshot []contract.ID
	var cursor *string
	for i := 0; ; i++ {
		if i > 200 {
			t.Fatalf("snapshot paging did not drain after %d pages; event.list is not advancing", i)
		}
		page, _, err := f.eventsPage(op, 5, cursor)
		if err != nil {
			t.Fatalf("snapshot page: %v", err)
		}
		snapshot = append(snapshot, page.ids...)
		cursor = page.cursor
		if len(page.ids) < 5 {
			break
		}
	}
	full, _, err := f.eventsPage(op, 500, nil)
	if err != nil {
		t.Fatalf("full listing: %v", err)
	}
	if len(snapshot) != len(full.ids) || full.cursor == nil {
		t.Fatalf("paged snapshot has %d events, one-page listing %d (cursor %v)", len(snapshot), len(full.ids), full.cursor)
	}
	for i, id := range snapshot {
		if id != full.ids[i] {
			t.Fatalf("paged snapshot diverges from the one-page listing at %d: %s vs %s", i, id, full.ids[i])
		}
	}
	for i, id := range append(first.ids, second.ids...) {
		if snapshot[i] != id {
			t.Fatalf("snapshot position %d is %s, the client's earlier page had %s", i, snapshot[i], id)
		}
	}
	if len(snapshot) <= 4 {
		t.Fatalf("snapshot holds %d events, the gap mutation must have appended more", len(snapshot))
	}
}

// TestRestartInvalidatesCursors (Z16 transport-independent recovery): a
// cursor minted before a controller restart is refused with a code that
// tells the client to take a fresh snapshot, never served from a stale
// position.
func TestRestartInvalidatesCursors(t *testing.T) {
	t.Parallel()
	f := newBootstrappedFixture(t)
	token := f.ownerToken()
	res := f.must(f.owner, "event.list", "", map[string]any{"scope": f.scope(), "limit": 2})
	if res.NextCursor == nil {
		t.Fatal("no cursor to carry across the restart")
	}
	cursor := *res.NextCursor
	installation, stateDir, keyRef := f.installationID, f.stateDir, f.keyRef
	f.close()

	g, err := assemble(t, fixtureOptions{stateDir: stateDir, keyRef: keyRef})
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	g.installationID = installation
	g.owner = g.authenticate(token)
	_, err = g.invoke(g.owner, "event.list", "", map[string]any{"scope": g.scope(), "limit": 2, "cursor": cursor})
	switch faultCode(err) {
	case contract.CodeCursorExpired, contract.CodeInvalidInput:
		// Either code refuses the stale position; the client re-snapshots.
	default:
		t.Fatalf("cursor from before the restart: %v, want a refusal", err)
	}
	// P34 mints a resumable cursor even on a drained page, so NextCursor's
	// mere presence no longer signals more data; a short page under the
	// requested limit does.
	after := g.must(g.owner, "event.list", "", map[string]any{"scope": g.scope(), "limit": 500})
	var afterItems struct {
		Items []struct {
			ID contract.ID `json:"id"`
		} `json:"items"`
	}
	decode(t, after.Data, &afterItems)
	if len(afterItems.Items) >= 500 {
		t.Fatal("a fresh snapshot after restart did not fit one page")
	}
}
