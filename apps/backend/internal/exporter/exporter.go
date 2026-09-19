// Package exporter builds asynchronous organization exports requested from the web console.
package exporter

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"time"

	"actilens/backend/internal/filestore"
	"actilens/backend/internal/obs"
	"actilens/backend/internal/store"
)

// Service processes pending organization export jobs.
type Service struct {
	store *store.Store
	files *filestore.Store
}

func New(st *store.Store, files *filestore.Store) *Service {
	return &Service{store: st, files: files}
}

// StartWorker requeues jobs interrupted by a previous process shutdown, then
// processes new jobs until ctx is cancelled.
func (s *Service) StartWorker(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	go func() {
		if err := s.store.RequeueRunningOrganizationExports(ctx); err != nil {
			obs.Error("exporter: requeue running jobs failed", "err", err)
		}
		s.runPending(ctx)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.expire(ctx)
				s.runPending(ctx)
			}
		}
	}()
}

func (s *Service) runPending(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		job, err := s.store.ClaimNextOrganizationExport(ctx)
		if err != nil {
			obs.Error("exporter: claim job failed", "err", err)
			return
		}
		if job == nil {
			return
		}
		if err := s.process(ctx, *job); err != nil {
			obs.Error(
				"exporter: generation failed",
				"export_id", job.ID,
				"business_id", job.BusinessID,
				"kind", job.Kind,
				"err", err,
			)
			if markErr := s.store.FailOrganizationExport(ctx, job.ID, "generation_failed"); markErr != nil {
				obs.Error("exporter: mark failed job failed", "export_id", job.ID, "err", markErr)
			}
		}
	}
}

func (s *Service) expire(ctx context.Context) {
	paths, err := s.store.ExpireOrganizationExports(ctx)
	if err != nil {
		obs.Error("exporter: expire jobs failed", "err", err)
		return
	}
	for _, path := range paths {
		if err := s.files.Remove(path); err != nil {
			obs.Warn("exporter: remove expired file failed", "path", path, "err", err)
		}
	}
}

func (s *Service) process(ctx context.Context, job store.OrganizationExport) error {
	data, extension, err := s.generate(ctx, job)
	if err != nil {
		return err
	}
	rel, err := s.files.WriteExport(job.BusinessID, job.ID, extension, data)
	if err != nil {
		return err
	}
	// Export links are intentionally temporary. A new export can always be
	// requested after expiry. If the organization/job vanished while bytes were
	// being generated, remove the just-written file rather than leave an orphan.
	if err := s.store.CompleteOrganizationExport(
		ctx,
		job.ID,
		rel,
		time.Now().Add(7*24*time.Hour),
	); err != nil {
		if removeErr := s.files.Remove(rel); removeErr != nil {
			obs.Warn(
				"exporter: remove uncommitted export failed",
				"export_id", job.ID,
				"path", rel,
				"err", removeErr,
			)
		}
		return err
	}
	return nil
}

func (s *Service) generate(
	ctx context.Context,
	job store.OrganizationExport,
) ([]byte, string, error) {
	switch job.Kind {
	case store.ExportActivityCSV:
		rows, err := s.store.ExportActivity(ctx, job.BusinessID)
		if err != nil {
			return nil, "", err
		}
		return activityCSV(rows), "csv", nil
	case store.ExportActivityJSON:
		rows, err := s.store.ExportActivity(ctx, job.BusinessID)
		return jsonFile(rows, err)
	case store.ExportBrowserCSV:
		rows, err := s.store.ExportBrowser(ctx, job.BusinessID)
		if err != nil {
			return nil, "", err
		}
		return browserCSV(rows), "csv", nil
	case store.ExportBrowserJSON:
		rows, err := s.store.ExportBrowser(ctx, job.BusinessID)
		return jsonFile(rows, err)
	case store.ExportKeystrokesCSV:
		rows, err := s.store.ExportKeystrokes(ctx, job.BusinessID)
		if err != nil {
			return nil, "", err
		}
		return keystrokesCSV(rows), "csv", nil
	case store.ExportKeystrokesJSON:
		rows, err := s.store.ExportKeystrokes(ctx, job.BusinessID)
		return jsonFile(rows, err)
	case store.ExportAuditCSV:
		rows, err := s.store.ExportAudit(ctx, job.BusinessID)
		if err != nil {
			return nil, "", err
		}
		return auditCSV(rows), "csv", nil
	case store.ExportAuditJSON:
		rows, err := s.store.ExportAudit(ctx, job.BusinessID)
		return jsonFile(rows, err)
	case store.ExportScreenshotsArchive:
		data, err := s.screenshotArchive(ctx, job.BusinessID, false)
		return data, "zip", err
	case store.ExportFull:
		data, err := s.fullArchive(ctx, job.BusinessID)
		return data, "zip", err
	default:
		return nil, "", fmt.Errorf("unsupported export kind %q", job.Kind)
	}
}

func jsonFile[T any](value T, err error) ([]byte, string, error) {
	if err != nil {
		return nil, "", err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, "", err
	}
	data = append(data, '\n')
	return data, "json", nil
}

func str(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func intp(v *int) string {
	if v == nil {
		return ""
	}
	return strconv.Itoa(*v)
}

func csvBytes(header []string, rows [][]string) []byte {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write(header)
	for _, row := range rows {
		_ = w.Write(row)
	}
	w.Flush()
	return buf.Bytes()
}

func activityCSV(items []store.ExportActivityRow) []byte {
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{
			item.ClientUUID, item.UserID, item.DeviceID,
			strconv.FormatInt(item.Ts, 10), item.AppName, str(item.WindowTitle),
			strconv.Itoa(item.DurationS),
		})
	}
	return csvBytes(
		[]string{"client_uuid", "user_id", "device_id", "ts", "app_name", "window_title", "duration_s"},
		rows,
	)
}

func browserCSV(items []store.ExportBrowserRow) []byte {
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{
			item.ClientUUID, item.UserID, item.DeviceID,
			strconv.FormatInt(item.Ts, 10), item.URL, str(item.PageTitle), str(item.Browser),
			strconv.Itoa(item.DurationS),
		})
	}
	return csvBytes(
		[]string{"client_uuid", "user_id", "device_id", "ts", "url", "page_title", "browser", "duration_s"},
		rows,
	)
}

func keystrokesCSV(items []store.ExportKeystrokeRow) []byte {
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{
			item.ClientUUID, item.UserID, item.DeviceID,
			strconv.FormatInt(item.TsBucket, 10), strconv.Itoa(item.Count),
		})
	}
	return csvBytes(
		[]string{"client_uuid", "user_id", "device_id", "ts_bucket", "count"},
		rows,
	)
}

func auditCSV(items []store.AuditEvent) []byte {
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		details, _ := json.Marshal(item.Details)
		rows = append(rows, []string{
			strconv.FormatInt(item.ID, 10), item.BusinessID, item.ActorUserID,
			item.Action, item.TargetType, item.TargetID,
			string(details), strconv.FormatInt(item.CreatedAt, 10),
		})
	}
	return csvBytes(
		[]string{"id", "business_id", "actor_user_id", "action", "target_type", "target_id", "details", "created_at"},
		rows,
	)
}

func addJSONFile(zw *zip.Writer, name string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	if _, err := w.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

func (s *Service) addScreenshots(
	ctx context.Context,
	zw *zip.Writer,
	businessID string,
) error {
	items, err := s.store.ExportScreenshots(ctx, businessID)
	if err != nil {
		return err
	}
	if err := addJSONFile(zw, "screenshots/manifest.json", items); err != nil {
		return err
	}
	for _, item := range items {
		src, err := s.files.Open(item.FilePath)
		if err != nil {
			return fmt.Errorf("open screenshot %s: %w", item.ClientUUID, err)
		}
		dst, err := zw.Create("screenshots/" + item.ClientUUID + ".webp")
		if err != nil {
			src.Close()
			return err
		}
		_, copyErr := io.Copy(dst, src)
		closeErr := src.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func (s *Service) screenshotArchive(
	ctx context.Context,
	businessID string,
	_ bool,
) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if err := s.addScreenshots(ctx, zw, businessID); err != nil {
		_ = zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (s *Service) fullArchive(ctx context.Context, businessID string) ([]byte, error) {
	organization, err := s.store.ExportOrganization(ctx, businessID)
	if err != nil {
		return nil, err
	}
	members, err := s.store.ExportMembers(ctx, businessID)
	if err != nil {
		return nil, err
	}
	devices, err := s.store.ExportDevices(ctx, businessID)
	if err != nil {
		return nil, err
	}
	rules, err := s.store.ExportPrivacyRules(ctx, businessID)
	if err != nil {
		return nil, err
	}
	activity, err := s.store.ExportActivity(ctx, businessID)
	if err != nil {
		return nil, err
	}
	browser, err := s.store.ExportBrowser(ctx, businessID)
	if err != nil {
		return nil, err
	}
	keystrokes, err := s.store.ExportKeystrokes(ctx, businessID)
	if err != nil {
		return nil, err
	}
	audit, err := s.store.ExportAudit(ctx, businessID)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := []struct {
		name  string
		value any
	}{
		{"organization.json", organization},
		{"members.json", members},
		{"devices.json", devices},
		{"privacy_rules.json", rules},
		{"activity.json", activity},
		{"browser.json", browser},
		{"keystrokes.json", keystrokes},
		{"audit.json", audit},
	}
	for _, file := range files {
		if err := addJSONFile(zw, file.name, file.value); err != nil {
			_ = zw.Close()
			return nil, err
		}
	}
	if err := s.addScreenshots(ctx, zw, businessID); err != nil {
		_ = zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Keep helpers referenced so future optional fields can be rendered consistently.
var _ = intp
