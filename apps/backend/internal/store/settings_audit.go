package store

import "context"

// RecordSettingsAudit writes an administrative audit event after re-checking the
// actor's organization settings permission in the same transaction.
func (s *Store) RecordSettingsAudit(
	ctx context.Context,
	actorID, businessID, action, targetType, targetID string,
	details map[string]any,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := requireBusinessPermissionTx(
		ctx,
		tx,
		actorID,
		businessID,
		CapabilitySettingsManage,
	); err != nil {
		return err
	}
	if err := insertAuditTx(
		ctx,
		tx,
		businessID,
		actorID,
		action,
		targetType,
		targetID,
		details,
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
