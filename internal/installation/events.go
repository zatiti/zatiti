package installation

import (
	"context"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// Event emission. Storage stamps event identity, sequence, timestamp and
// scope; handlers supply the kind and the resource coordinates. Kinds follow
// owner.entity.transition with at least three dot segments.
const (
	eventInitialized      = "installation.state.initialized"
	eventMaintenanceEnter = "installation.state.maintenance_entered"
	eventPaused           = "installation.state.paused"
	eventResumed          = "installation.state.resumed"
	eventBackupFailed     = "installation.backup.failed"
	eventBackupCompleted  = "installation.backup.completed"
	eventRestoreRequested = "installation.restore.requested"
	eventRestoreRecorded  = "installation.restore.recorded"
)

// emitTransition appends one state-correlated event to the transaction
// outbox. A failure fails the handler and rolls back the mutation.
func emitTransition(ctx context.Context, unit contract.Unit, kind string, resourceID contract.ID, version contract.Version) error {
	if err := unit.Emit(ctx, contract.Event{
		Kind:            kind,
		ResourceID:      resourceID,
		ResourceVersion: version,
	}); err != nil {
		return fmt.Errorf("installation: emit %s: %w", kind, err)
	}
	return nil
}
