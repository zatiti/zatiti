package messaging

import (
	"context"
	"github.com/zatiti/zatiti/internal/contract"
)

// classifyLegacyRecipients uses only messaging-owned rows and the configured
// worker boundary. Closing the query before peer calls keeps one transaction
// available for both reads and writes. Classification never acknowledges mail.
func (s *Service) classifyLegacyRecipients(ctx context.Context, unit contract.Unit, limit int) error {
	rows, err := unit.QueryContext(ctx, `SELECT message_id, recipient_id FROM messaging_recipients WHERE installation_id=? AND turn_eligible IS NULL ORDER BY admitted_at, message_id, recipient_id LIMIT ?`, unit.Scope().InstallationID, limit)
	if err != nil {
		return err
	}
	type pair struct{ message, recipient contract.ID }
	var pending []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.message, &p.recipient); err != nil {
			_ = rows.Close()
			return err
		}
		pending = append(pending, p)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	eligible := make(map[contract.ID]bool)
	for _, p := range pending {
		worker, cached := eligible[p.recipient]
		if !cached {
			snapshot, err := s.peerConfigurationSnapshot(ctx, unit, contract.Scope{InstallationID: unit.Scope().InstallationID, WorkerID: p.recipient})
			if err != nil {
				return err
			}
			worker = snapshot.Resource.Worker != nil && snapshot.Resource.Worker.ID == p.recipient
			eligible[p.recipient] = worker
		}
		if _, err := unit.ExecContext(ctx, `UPDATE messaging_recipients SET turn_eligible=? WHERE message_id=? AND recipient_id=? AND installation_id=? AND turn_eligible IS NULL`, worker, p.message, p.recipient, unit.Scope().InstallationID); err != nil {
			return err
		}
	}
	return nil
}
