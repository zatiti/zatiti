package controller

import (
	"context"
	"encoding/json"

	"github.com/zatiti/zatiti/internal/contract"
)

// syncWorkerPrincipals backfills identity principals for configured workers
// once per controller lifetime (revision 21). Installations bootstrapped
// before workers were registered as principals gain them here; afterwards
// it is an idempotent no-op. A failure is noted, never fatal: it only
// leaves worker proposals refused exactly as they were before.
func (c *Controller) syncWorkerPrincipals(ctx context.Context, sess *session) {
	var out struct {
		Versions []json.RawMessage `json:"versions"`
	}
	err := c.write(func() error {
		return c.call(ctx, sess, "_configuration.worker.principals.sync",
			map[string]contract.ID{"installation_id": sess.scope.InstallationID}, &out)
	})
	if err != nil {
		c.note(err)
	}
}
