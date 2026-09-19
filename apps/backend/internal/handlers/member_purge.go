package handlers

import (
	"errors"
	"net/http"

	"actilens/backend/internal/auth"
	"actilens/backend/internal/filestore"
	"actilens/backend/internal/store"

	"github.com/gin-gonic/gin"
)

// MemberPurgeHandler owns the irreversible organization-scoped member deletion
// flow because it must coordinate both Postgres rows and screenshot files.
type MemberPurgeHandler struct {
	store *store.Store
	files *filestore.Store
}

func NewMemberPurgeHandler(s *store.Store, files *filestore.Store) *MemberPurgeHandler {
	return &MemberPurgeHandler{store: s, files: files}
}

// Purge permanently removes a non-owner member's monitoring data from one
// organization. Audit events remain intact by design.
func (h *MemberPurgeHandler) Purge(c *gin.Context) {
	actorID, ok := auth.UserID(c)
	if !ok {
		unauthorized(c, "unauthenticated")
		return
	}
	businessID := c.Param("id")
	targetUserID := c.Param("user_id")

	result, err := h.store.PurgeMemberFromBusiness(
		c.Request.Context(),
		actorID,
		businessID,
		targetUserID,
		func() error {
			return h.files.RemoveMemberScreenshots(businessID, targetUserID)
		},
	)
	switch {
	case errors.Is(err, store.ErrForbidden):
		forbidden(c, "only the organization owner can permanently delete this member")
	case errors.Is(err, store.ErrNotFound):
		notFound(c, "member not found")
	case errors.Is(err, store.ErrOrganizationArchived):
		apiError(c, http.StatusConflict, ErrCodeOrganizationArchived, "organization is archived", nil)
	case errors.Is(err, store.ErrOrganizationDeletionPending):
		apiError(c, http.StatusConflict, ErrCodeOrganizationDeletionPending, "organization deletion is pending", nil)
	case err != nil:
		// Screenshot deletion happens before DB row deletion while the membership
		// is exclusively locked. RemoveAll is idempotent, so retry is safe even
		// if a later database operation failed after the directory was removed.
		serverError(c, err)
	default:
		c.JSON(http.StatusOK, gin.H{
			"status":      "purged",
			"bytes_freed": result.BytesFreed,
			"result":      result,
		})
	}
}
