package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"actilens/backend/internal/auth"
	"actilens/backend/internal/filestore"
	"actilens/backend/internal/store"

	"github.com/gin-gonic/gin"
)

type ExportHandler struct {
	store *store.Store
	files *filestore.Store
}

func NewExportHandler(st *store.Store, files *filestore.Store) *ExportHandler {
	return &ExportHandler{store: st, files: files}
}

type createExportReq struct {
	Kind string `json:"kind"`
}

func (h *ExportHandler) Create(c *gin.Context) {
	actorID, _ := auth.UserID(c)
	var req createExportReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid export request")
		return
	}
	req.Kind = strings.TrimSpace(req.Kind)
	job, err := h.store.CreateOrganizationExport(
		c.Request.Context(), actorID, c.Param("id"), req.Kind,
	)
	switch {
	case err == nil:
		c.JSON(http.StatusAccepted, gin.H{"export": job})
	case errors.Is(err, store.ErrConflict):
		badRequest(c, "unsupported export kind")
	case errors.Is(err, store.ErrForbidden):
		forbidden(c, "insufficient permission")
	default:
		serverError(c, err)
	}
}

func (h *ExportHandler) List(c *gin.Context) {
	actorID, _ := auth.UserID(c)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "25"))
	jobs, err := h.store.ListOrganizationExports(
		c.Request.Context(), actorID, c.Param("id"), limit,
	)
	switch {
	case err == nil:
		c.JSON(http.StatusOK, gin.H{"exports": jobs})
	case errors.Is(err, store.ErrForbidden):
		forbidden(c, "insufficient permission")
	default:
		serverError(c, err)
	}
}

func (h *ExportHandler) Get(c *gin.Context) {
	actorID, _ := auth.UserID(c)
	job, err := h.store.OrganizationExportForActor(
		c.Request.Context(), actorID, c.Param("id"), c.Param("export_id"),
	)
	switch {
	case err == nil:
		c.JSON(http.StatusOK, gin.H{"export": job})
	case errors.Is(err, store.ErrForbidden):
		forbidden(c, "insufficient permission")
	case errors.Is(err, store.ErrNotFound):
		notFound(c, "export not found")
	default:
		serverError(c, err)
	}
}

func exportFileMetadata(kind, id string) (string, string) {
	extension := "json"
	contentType := "application/json; charset=utf-8"
	switch kind {
	case store.ExportActivityCSV, store.ExportBrowserCSV,
		store.ExportKeystrokesCSV, store.ExportAuditCSV:
		extension = "csv"
		contentType = "text/csv; charset=utf-8"
	case store.ExportScreenshotsArchive, store.ExportFull:
		extension = "zip"
		contentType = "application/zip"
	}
	return fmt.Sprintf("actilens-%s-%s.%s", kind, id, extension), contentType
}

func (h *ExportHandler) Download(c *gin.Context) {
	actorID, _ := auth.UserID(c)
	job, err := h.store.OrganizationExportForActor(
		c.Request.Context(), actorID, c.Param("id"), c.Param("export_id"),
	)
	switch {
	case errors.Is(err, store.ErrForbidden):
		forbidden(c, "insufficient permission")
		return
	case errors.Is(err, store.ErrNotFound):
		notFound(c, "export not found")
		return
	case err != nil:
		serverError(c, err)
		return
	}
	if job.Status != "ready" || job.FilePath == nil || *job.FilePath == "" {
		conflict(c, ErrCodeConflict, "export is not ready")
		return
	}

	file, err := h.files.Open(*job.FilePath)
	if err != nil {
		notFound(c, "export file is unavailable")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		serverError(c, err)
		return
	}
	name, contentType := exportFileMetadata(job.Kind, job.ID)
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	c.DataFromReader(http.StatusOK, info.Size(), contentType, file, nil)
}
