package handlers

import (
	"errors"
	"net/http"
	"strings"

	"actilens/backend/internal/auth"
	"actilens/backend/internal/events"
	"actilens/backend/internal/store"

	"github.com/gin-gonic/gin"
)

type OrganizationLifecycleHandler struct {
	store     *store.Store
	tok       *auth.Manager
	publisher events.Publisher
}

func NewOrganizationLifecycleHandler(st *store.Store, tok *auth.Manager) *OrganizationLifecycleHandler {
	return &OrganizationLifecycleHandler{store: st, tok: tok, publisher: events.Discard{}}
}

func (h *OrganizationLifecycleHandler) SetEventPublisher(publisher events.Publisher) {
	if publisher != nil {
		h.publisher = publisher
	}
}

func (h *OrganizationLifecycleHandler) publish(c *gin.Context, event events.Event) {
	if h.publisher != nil {
		h.publisher.Publish(c.Request.Context(), event)
	}
}

func (h *OrganizationLifecycleHandler) requireReauth(c *gin.Context, raw string) bool {
	actorID, _ := auth.UserID(c)
	userID, version, err := h.tok.ParseReauthGrant(strings.TrimSpace(raw))
	if err != nil || userID != actorID {
		apiError(c, http.StatusUnauthorized, ErrCodeReauthRequired, "fresh reauthentication is required", nil)
		return false
	}
	active, currentVersion, err := h.store.UserSecurity(c.Request.Context(), actorID)
	if err != nil {
		serverError(c, err)
		return false
	}
	if !active || currentVersion != version {
		apiError(c, http.StatusUnauthorized, ErrCodeReauthRequired, "reauthentication grant is stale", nil)
		return false
	}
	return true
}

func (h *OrganizationLifecycleHandler) Archive(c *gin.Context) {
	actorID, _ := auth.UserID(c)
	business, err := h.store.ArchiveOrganization(c.Request.Context(), actorID, c.Param("id"))
	h.writeBusinessMutation(c, business, err)
}

func (h *OrganizationLifecycleHandler) Restore(c *gin.Context) {
	actorID, _ := auth.UserID(c)
	business, err := h.store.RestoreOrganization(c.Request.Context(), actorID, c.Param("id"))
	h.writeBusinessMutation(c, business, err)
}

type transferOwnershipReq struct {
	TargetUserID string `json:"target_user_id"`
	ReauthToken  string `json:"reauth_token"`
}

func (h *OrganizationLifecycleHandler) TransferOwnership(c *gin.Context) {
	actorID, _ := auth.UserID(c)
	var req transferOwnershipReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid body")
		return
	}
	req.TargetUserID = strings.TrimSpace(req.TargetUserID)
	if req.TargetUserID == "" {
		badRequest(c, "target_user_id is required")
		return
	}
	if !h.requireReauth(c, req.ReauthToken) {
		return
	}
	err := h.store.TransferOrganizationOwnership(
		c.Request.Context(), actorID, c.Param("id"), req.TargetUserID,
	)
	switch {
	case err == nil:
		h.publish(c, events.Event{
			Type: events.OwnershipTransferred,
			OrganizationID: c.Param("id"),
			ActorUserID: actorID,
			TargetUserID: req.TargetUserID,
		})
		c.JSON(http.StatusOK, gin.H{"status": "transferred", "reauth_required": true})
	case errors.Is(err, store.ErrForbidden):
		forbidden(c, "only the owner can transfer ownership")
	case errors.Is(err, store.ErrNotFound):
		notFound(c, "target member not found")
	case errors.Is(err, store.ErrConflict):
		conflict(c, ErrCodeConflict, "target must be an active administrator")
	case errors.Is(err, store.ErrOrganizationArchived):
		apiError(c, http.StatusConflict, ErrCodeOrganizationArchived, "organization is archived", nil)
	case errors.Is(err, store.ErrOrganizationDeletionPending):
		apiError(c, http.StatusConflict, ErrCodeOrganizationDeletionPending, "organization deletion is pending", nil)
	default:
		serverError(c, err)
	}
}

func (h *OrganizationLifecycleHandler) DeletionPreview(c *gin.Context) {
	actorID, _ := auth.UserID(c)
	preview, err := h.store.OrganizationDeletionPreview(
		c.Request.Context(), actorID, c.Param("id"),
	)
	switch {
	case err == nil:
		c.JSON(http.StatusOK, preview)
	case errors.Is(err, store.ErrForbidden):
		forbidden(c, "only the owner can preview organization deletion")
	default:
		serverError(c, err)
	}
}

type scheduleDeletionReq struct {
	ReauthToken      string `json:"reauth_token"`
	ConfirmationName string `json:"confirmation_name"`
}

func (h *OrganizationLifecycleHandler) ScheduleDeletion(c *gin.Context) {
	actorID, _ := auth.UserID(c)
	var req scheduleDeletionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid body")
		return
	}
	if !h.requireReauth(c, req.ReauthToken) {
		return
	}
	businessState, err := h.store.GetBusiness(c.Request.Context(), c.Param("id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			notFound(c, "organization not found")
		} else {
			serverError(c, err)
		}
		return
	}
	if strings.TrimSpace(req.ConfirmationName) != businessState.Name {
		badRequest(c, "confirmation_name must exactly match the organization name")
		return
	}
	business, err := h.store.ScheduleOrganizationDeletion(
		c.Request.Context(), actorID, c.Param("id"),
	)
	switch {
	case err == nil:
		if businessState.DeletionScheduledAt == nil {
			h.publish(c, events.Event{
				Type: events.OrganizationDeletionPending,
				OrganizationID: business.ID,
				ActorUserID: actorID,
				Details: map[string]any{
					"deletion_scheduled_at": business.DeletionScheduledAt,
				},
			})
		}
		c.JSON(http.StatusOK, gin.H{"business": business})
	case errors.Is(err, store.ErrForbidden):
		forbidden(c, "only the owner can schedule organization deletion")
	case errors.Is(err, store.ErrConflict):
		conflict(c, ErrCodeConflict, "organization must be archived before scheduling deletion")
	default:
		serverError(c, err)
	}
}

func (h *OrganizationLifecycleHandler) CancelDeletion(c *gin.Context) {
	actorID, _ := auth.UserID(c)
	previous, _ := h.store.GetBusiness(c.Request.Context(), c.Param("id"))
	business, err := h.store.CancelOrganizationDeletion(
		c.Request.Context(), actorID, c.Param("id"),
	)
	if err == nil && previous.DeletionScheduledAt != nil {
		h.publish(c, events.Event{
			Type: events.OrganizationDeletionCancelled,
			OrganizationID: business.ID,
			ActorUserID: actorID,
		})
	}
	h.writeBusinessMutation(c, business, err)
}

func (h *OrganizationLifecycleHandler) writeBusinessMutation(
	c *gin.Context,
	business store.Business,
	err error,
) {
	switch {
	case err == nil:
		c.JSON(http.StatusOK, gin.H{"business": business})
	case errors.Is(err, store.ErrForbidden):
		forbidden(c, "insufficient permission")
	case errors.Is(err, store.ErrNotFound):
		notFound(c, "organization not found")
	case errors.Is(err, store.ErrOrganizationDeletionPending):
		apiError(c, http.StatusConflict, ErrCodeOrganizationDeletionPending, "organization deletion is pending", nil)
	case errors.Is(err, store.ErrOrganizationArchived):
		apiError(c, http.StatusConflict, ErrCodeOrganizationArchived, "organization is archived", nil)
	default:
		serverError(c, err)
	}
}
