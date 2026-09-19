// Package lifecycle executes scheduled organization deletion after the cancellation window.
package lifecycle

import (
	"context"
	"time"

	"actilens/backend/internal/filestore"
	"actilens/backend/internal/obs"
	"actilens/backend/internal/store"
)

type Service struct {
	store *store.Store
	files *filestore.Store
}

func New(st *store.Store, files *filestore.Store) *Service {
	return &Service{store: st, files: files}
}

func (s *Service) StartWorker(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	go func() {
		s.Sweep(ctx)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.Sweep(ctx)
			}
		}
	}()
}

func (s *Service) Sweep(ctx context.Context) {
	ids, err := s.store.OrganizationsDueForDeletion(ctx)
	if err != nil {
		obs.Error("organization lifecycle: list due deletions failed", "err", err)
		return
	}
	for _, businessID := range ids {
		deletedAccounts, err := s.store.DeleteScheduledOrganization(
			ctx,
			businessID,
			func() error { return s.files.RemoveBusinessData(businessID) },
		)
		if err != nil {
			obs.Error(
				"organization lifecycle: deletion failed",
				"business_id", businessID,
				"err", err,
			)
			continue
		}
		obs.Info(
			"organization lifecycle: organization deleted",
			"business_id", businessID,
			"orphan_accounts_deleted", deletedAccounts,
		)
	}
}
