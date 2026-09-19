package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"actilens/backend/internal/db"
	"actilens/backend/internal/auth"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
)

const integrationDatabaseEnv = "ACTILENS_TEST_DATABASE_URL"

func integrationStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()

	dsn := os.Getenv(integrationDatabaseEnv)
	if dsn == "" {
		t.Skipf("%s is not set", integrationDatabaseEnv)
	}
	if err := db.Migrate(dsn); err != nil {
		t.Fatalf("migrate integration database: %v", err)
	}

	ctx := context.Background()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect integration database: %v", err)
	}
	t.Cleanup(pool.Close)

	_, err = pool.Exec(ctx, `
		TRUNCATE TABLE
			enrollment_tokens,
			audit_events,
			screenshots,
			browser_visits,
			keystroke_buckets,
			activity_samples,
			devices,
			memberships,
			businesses,
			users
		RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("reset integration database: %v", err)
	}

	return New(pool), pool
}

func TestIntegrationManagedMemberLifecycle(t *testing.T) {
	st, pool := integrationStore(t)
	ctx := context.Background()

	owner, err := st.CreateUser(ctx, "owner@example.test", "", "hash", "Owner", "manager")
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	biz, err := st.CreateBusiness(ctx, owner.ID, "Primary team", "team")
	if err != nil {
		t.Fatalf("create business: %v", err)
	}

	consoleBusinesses, err := st.ListBusinessesForConsole(ctx, owner.ID)
	if err != nil {
		t.Fatalf("list console businesses: %v", err)
	}
	if len(consoleBusinesses) != 1 || consoleBusinesses[0].Business.ID != biz.ID || consoleBusinesses[0].Role != RoleOwner {
		t.Fatalf("unexpected console businesses: %+v", consoleBusinesses)
	}
	employee, _, err := st.CreateEmployee(ctx, owner.ID, &biz.ID, "", "alice", "hash", "Alice")
	if err != nil {
		t.Fatalf("create employee: %v", err)
	}

	managementRoster, err := st.ListEmployees(ctx, biz.ID)
	if err != nil {
		t.Fatalf("list management roster: %v", err)
	}
	if len(managementRoster) != 2 {
		t.Fatalf("management roster length = %d, want 2: %+v", len(managementRoster), managementRoster)
	}
	if managementRoster[0].ID != owner.ID || managementRoster[0].Role != RoleOwner {
		t.Fatalf("owner missing or not first in management roster: %+v", managementRoster)
	}
	if managementRoster[1].ID != employee.ID || managementRoster[1].Role != RoleEmployee {
		t.Fatalf("employee missing from management roster: %+v", managementRoster)
	}

	if _, err := st.CreateEnrollmentToken(ctx, owner.ID, biz.ID, employee.ID, "token-hash-1", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create enrollment token: %v", err)
	}
	grant, err := st.RedeemEnrollmentToken(ctx, "token-hash-1")
	if err != nil {
		t.Fatalf("redeem enrollment token: %v", err)
	}
	if grant.User.ID != employee.ID || grant.BusinessID != biz.ID || grant.AuthVersion != 1 {
		t.Fatalf("unexpected enrollment grant: %+v", grant)
	}
	if _, err := st.RedeemEnrollmentToken(ctx, "token-hash-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second enrollment redemption = %v, want ErrNotFound", err)
	}
	if _, err := st.CreateEnrollmentToken(ctx, owner.ID, biz.ID, employee.ID, "token-hash-2", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create replacement enrollment token: %v", err)
	}

	deviceID := uuid.NewString()
	activityID := uuid.NewString()
	keystrokeID := uuid.NewString()
	browserID := uuid.NewString()
	if err := st.SyncBatch(ctx, employee.ID, biz.ID, deviceID, DeviceMetadata{},
		[]ActivityRow{{ClientUUID: activityID, Ts: 100, AppName: "Editor", DurationS: 10, ClientUpdatedAt: 100}},
		[]KeystrokeRow{{ClientUUID: keystrokeID, TsBucket: 60, Count: 7, ClientUpdatedAt: 100}},
		[]BrowserRow{{ClientUUID: browserID, Ts: 100, URL: "https://example.test", DurationS: 10, ClientUpdatedAt: 100}},
	); err != nil {
		t.Fatalf("sync member data: %v", err)
	}
	if err := st.UpsertScreenshot(ctx, employee.ID, biz.ID, ScreenshotRow{
		ClientUUID:      uuid.NewString(),
		DeviceID:        deviceID,
		Ts:              100,
		FilePath:        "screenshots/example.webp",
		ByteSize:        42,
		ClientUpdatedAt: 100,
	}); err != nil {
		t.Fatalf("insert screenshot metadata: %v", err)
	}

	devices, err := st.ListEmployeeDevices(ctx, owner.ID, employee.ID, biz.ID)
	if err != nil {
		t.Fatalf("list employee devices in business: %v", err)
	}
	if len(devices) != 1 || devices[0].ID != deviceID {
		t.Fatalf("unexpected employee devices: %+v", devices)
	}

	admin, err := st.CreateUser(ctx, "admin@example.test", "", "hash", "Admin", "manager")
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO memberships (user_id, business_id, role) VALUES ($1, $2, 'admin')`,
		admin.ID, biz.ID); err != nil {
		t.Fatalf("add admin membership: %v", err)
	}
	if _, err := st.ListEmployeeDevices(ctx, admin.ID, employee.ID, biz.ID); err != nil {
		t.Fatalf("admin list employee devices in business: %v", err)
	}

	otherBiz, err := st.CreateBusiness(ctx, admin.ID, "Other team", "team")
	if err != nil {
		t.Fatalf("create unrelated business: %v", err)
	}
	if _, err := st.ListEmployeeDevices(ctx, admin.ID, employee.ID, otherBiz.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("list devices through unrelated business = %v, want ErrNotFound", err)
	}

	label := "Work laptop"
	updatedDevice, err := st.UpdateDevice(ctx, owner.ID, deviceID, &label, nil, biz.ID)
	if err != nil {
		t.Fatalf("rename device in business: %v", err)
	}
	if updatedDevice.Label != label {
		t.Fatalf("renamed device label = %q, want %q", updatedDevice.Label, label)
	}

	if err := st.UpdateMembershipMonitoring(ctx, owner.ID, biz.ID, employee.ID, false); err != nil {
		t.Fatalf("disable monitoring: %v", err)
	}
	if err := st.SyncBatch(ctx, employee.ID, biz.ID, deviceID, DeviceMetadata{}, nil, nil, nil); !errors.Is(err, ErrMembershipUnavailable) {
		t.Fatalf("sync while monitoring disabled = %v, want ErrMembershipUnavailable", err)
	}
	if err := st.UpdateMembershipMonitoring(ctx, owner.ID, biz.ID, employee.ID, true); err != nil {
		t.Fatalf("re-enable monitoring: %v", err)
	}

	removedFiles := false
	result, err := st.PurgeMemberFromBusiness(ctx, owner.ID, biz.ID, employee.ID, func() error {
		removedFiles = true
		return nil
	})
	if err != nil {
		t.Fatalf("purge member: %v", err)
	}
	if !removedFiles {
		t.Fatal("purge did not invoke screenshot tree removal")
	}
	if result.ActivityDeleted != 1 || result.KeystrokesDeleted != 1 || result.BrowserDeleted != 1 || result.ScreenshotsDeleted != 1 {
		t.Fatalf("unexpected purge counts: %+v", result)
	}
	if result.EnrollmentsDeleted != 2 || result.BytesFreed != 42 || !result.AccountTombstoned {
		t.Fatalf("unexpected purge metadata: %+v", result)
	}

	member, err := st.MemberBelongsToBusiness(ctx, employee.ID, biz.ID)
	if err != nil {
		t.Fatalf("check membership after purge: %v", err)
	}
	if member {
		t.Fatal("purged employee still belongs to business")
	}
	active, version, err := st.UserSecurity(ctx, employee.ID)
	if err != nil {
		t.Fatalf("load purged user security: %v", err)
	}
	if active || version <= 1 {
		t.Fatalf("purged account security = active:%v version:%d, want inactive and bumped version", active, version)
	}
	if err := st.SyncBatch(ctx, employee.ID, biz.ID, deviceID, DeviceMetadata{}, nil, nil, nil); !errors.Is(err, ErrMembershipUnavailable) {
		t.Fatalf("sync after purge = %v, want ErrMembershipUnavailable", err)
	}

	for _, table := range []string{"activity_samples", "keystroke_buckets", "browser_visits", "screenshots", "enrollment_tokens", "devices"} {
		var count int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s has %d rows after purge, want 0", table, count)
		}
	}

	events, err := st.ListAuditEvents(ctx, owner.ID, biz.ID, 100)
	if err != nil {
		t.Fatalf("list audit events: %v", err)
	}
	foundPurge := false
	for _, event := range events {
		if event.Action == "member.purged" && event.TargetID == employee.ID {
			foundPurge = true
			break
		}
	}
	if !foundPurge {
		t.Fatal("member.purged audit event was not retained")
	}
}

func TestIntegrationPurgeIsOrganizationScoped(t *testing.T) {
	st, pool := integrationStore(t)
	ctx := context.Background()

	ownerA, err := st.CreateUser(ctx, "owner-a@example.test", "", "hash", "Owner A", "manager")
	if err != nil {
		t.Fatalf("create owner A: %v", err)
	}
	ownerB, err := st.CreateUser(ctx, "owner-b@example.test", "", "hash", "Owner B", "manager")
	if err != nil {
		t.Fatalf("create owner B: %v", err)
	}
	bizA, err := st.CreateBusiness(ctx, ownerA.ID, "Team A", "team")
	if err != nil {
		t.Fatalf("create business A: %v", err)
	}
	bizB, err := st.CreateBusiness(ctx, ownerB.ID, "Team B", "team")
	if err != nil {
		t.Fatalf("create business B: %v", err)
	}
	employee, _, err := st.CreateEmployee(ctx, ownerA.ID, &bizA.ID, "", "shared-member", "hash", "Shared Member")
	if err != nil {
		t.Fatalf("create shared employee: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO memberships (user_id, business_id, role) VALUES ($1, $2, 'employee')`,
		employee.ID, bizB.ID); err != nil {
		t.Fatalf("add second organization membership: %v", err)
	}

	deviceA := uuid.NewString()
	deviceB := uuid.NewString()
	if err := st.SyncBatch(ctx, employee.ID, bizA.ID, deviceA, DeviceMetadata{},
		[]ActivityRow{{ClientUUID: uuid.NewString(), Ts: 100, AppName: "A", DurationS: 1, ClientUpdatedAt: 100}}, nil, nil); err != nil {
		t.Fatalf("sync business A: %v", err)
	}
	if err := st.SyncBatch(ctx, employee.ID, bizB.ID, deviceB, DeviceMetadata{},
		[]ActivityRow{{ClientUUID: uuid.NewString(), Ts: 200, AppName: "B", DurationS: 1, ClientUpdatedAt: 200}}, nil, nil); err != nil {
		t.Fatalf("sync business B: %v", err)
	}

	result, err := st.PurgeMemberFromBusiness(ctx, ownerA.ID, bizA.ID, employee.ID, nil)
	if err != nil {
		t.Fatalf("purge from business A: %v", err)
	}
	if result.AccountTombstoned {
		t.Fatal("shared account was tombstoned despite remaining organization access")
	}

	memberA, err := st.MemberBelongsToBusiness(ctx, employee.ID, bizA.ID)
	if err != nil {
		t.Fatalf("check business A membership: %v", err)
	}
	memberB, err := st.MemberBelongsToBusiness(ctx, employee.ID, bizB.ID)
	if err != nil {
		t.Fatalf("check business B membership: %v", err)
	}
	if memberA || !memberB {
		t.Fatalf("membership scope after purge = A:%v B:%v, want A:false B:true", memberA, memberB)
	}

	active, version, err := st.UserSecurity(ctx, employee.ID)
	if err != nil {
		t.Fatalf("load shared user security: %v", err)
	}
	if !active || version <= 1 {
		t.Fatalf("shared account security = active:%v version:%d, want active and bumped version", active, version)
	}

	var countA, countB int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM activity_samples WHERE business_id = $1`, bizA.ID).Scan(&countA); err != nil {
		t.Fatalf("count business A activity: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM activity_samples WHERE business_id = $1`, bizB.ID).Scan(&countB); err != nil {
		t.Fatalf("count business B activity: %v", err)
	}
	if countA != 0 || countB != 1 {
		t.Fatalf("activity scope after purge = A:%d B:%d, want A:0 B:1", countA, countB)
	}

	var devices int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM devices WHERE user_id = $1`, employee.ID).Scan(&devices); err != nil {
		t.Fatalf("count shared user devices: %v", err)
	}
	if devices != 1 {
		t.Fatalf("shared user devices = %d, want 1", devices)
	}
}


func TestIntegrationCollectionPolicyEnforcedAtIngest(t *testing.T) {
	st, pool := integrationStore(t)
	ctx := context.Background()

	owner, err := st.CreateUser(ctx, "policy-owner@example.test", "", "hash", "Policy Owner", "manager")
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	biz, err := st.CreateBusiness(ctx, owner.ID, "Policy team", "team")
	if err != nil {
		t.Fatalf("create business: %v", err)
	}
	employee, _, err := st.CreateEmployee(ctx, owner.ID, &biz.ID, "", "policy-member", "hash", "Policy Member")
	if err != nil {
		t.Fatalf("create employee: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE businesses
		   SET collect_app_activity = false,
		       collect_window_titles = false,
		       collect_keystroke_counts = false,
		       collect_browser_activity = false,
		       collect_screenshots = false
		 WHERE id = $1`, biz.ID); err != nil {
		t.Fatalf("disable optional collection: %v", err)
	}

	title := "Sensitive document title"
	pid := 4242
	deviceID := uuid.NewString()
	activityID := uuid.NewString()
	if err := st.SyncBatch(ctx, employee.ID, biz.ID, deviceID, DeviceMetadata{},
		[]ActivityRow{{
			ClientUUID:      activityID,
			Ts:              100,
			AppName:         "Sensitive App",
			WindowTitle:     &title,
			Pid:             &pid,
			DurationS:       10,
			ClientUpdatedAt: 100,
		}},
		[]KeystrokeRow{{
			ClientUUID:      uuid.NewString(),
			TsBucket:        60,
			Count:           12,
			ClientUpdatedAt: 100,
		}},
		[]BrowserRow{{
			ClientUUID:      uuid.NewString(),
			Ts:              100,
			URL:             "https://secret.example.test/private",
			DurationS:       10,
			ClientUpdatedAt: 100,
		}},
	); err != nil {
		t.Fatalf("sync disabled categories: %v", err)
	}

	var appName string
	var storedTitle *string
	var storedPID *int
	if err := pool.QueryRow(ctx, `
		SELECT app_name, window_title, pid
		  FROM activity_samples
		 WHERE client_uuid = $1`, activityID).Scan(&appName, &storedTitle, &storedPID); err != nil {
		t.Fatalf("load redacted activity: %v", err)
	}
	if appName != "" || storedTitle != nil || storedPID != nil {
		t.Fatalf("activity identity leaked under disabled policy: app=%q title=%v pid=%v", appName, storedTitle, storedPID)
	}

	for _, table := range []string{"keystroke_buckets", "browser_visits"} {
		var count int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE business_id = $1", biz.ID).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s has %d rows with collection disabled, want 0", table, count)
		}
	}

	err = st.UpsertScreenshot(ctx, employee.ID, biz.ID, ScreenshotRow{
		ClientUUID: uuid.NewString(),
		DeviceID: deviceID,
		Ts: 100,
		FilePath: "screenshots/disabled.webp",
		ByteSize: 42,
		ClientUpdatedAt: 100,
	})
	if !errors.Is(err, ErrCollectionDisabled) {
		t.Fatalf("screenshot insert with collection disabled = %v, want ErrCollectionDisabled", err)
	}
	var screenshots int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM screenshots WHERE business_id = $1`, biz.ID).Scan(&screenshots); err != nil {
		t.Fatalf("count screenshots: %v", err)
	}
	if screenshots != 0 {
		t.Fatalf("screenshots has %d rows with collection disabled, want 0", screenshots)
	}
}


func TestIntegrationOrganizationExportJobs(t *testing.T) {
	st, pool := integrationStore(t)
	ctx := context.Background()

	owner, err := st.CreateUser(ctx, "export-owner@example.test", "", "hash", "Export Owner", "manager")
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	biz, err := st.CreateBusiness(ctx, owner.ID, "Export team", "team")
	if err != nil {
		t.Fatalf("create business: %v", err)
	}
	employee, _, err := st.CreateEmployee(ctx, owner.ID, &biz.ID, "", "export_member", "hash", "Export Member")
	if err != nil {
		t.Fatalf("create employee: %v", err)
	}

	if _, err := st.CreateOrganizationExport(ctx, employee.ID, biz.ID, ExportFull); !errors.Is(err, ErrForbidden) {
		t.Fatalf("employee export request = %v, want ErrForbidden", err)
	}
	if _, err := st.CreateOrganizationExport(ctx, owner.ID, biz.ID, "not_a_kind"); !errors.Is(err, ErrConflict) {
		t.Fatalf("invalid export kind = %v, want ErrConflict", err)
	}

	job, err := st.CreateOrganizationExport(ctx, owner.ID, biz.ID, ExportFull)
	if err != nil {
		t.Fatalf("create export job: %v", err)
	}
	if job.Status != "pending" || job.Kind != ExportFull {
		t.Fatalf("unexpected created export: %+v", job)
	}

	var auditCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_events
		  WHERE business_id = $1 AND action = 'data.export_requested' AND target_id = $2`,
		biz.ID, job.ID,
	).Scan(&auditCount); err != nil {
		t.Fatalf("query export audit: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("export audit count = %d, want 1", auditCount)
	}

	claimed, err := st.ClaimNextOrganizationExport(ctx)
	if err != nil {
		t.Fatalf("claim export: %v", err)
	}
	if claimed == nil || claimed.ID != job.ID || claimed.Status != "running" {
		t.Fatalf("unexpected claimed export: %+v", claimed)
	}
	none, err := st.ClaimNextOrganizationExport(ctx)
	if err != nil {
		t.Fatalf("claim empty queue: %v", err)
	}
	if none != nil {
		t.Fatalf("second claim = %+v, want nil", none)
	}

	expires := time.Now().Add(time.Hour)
	if err := st.CompleteOrganizationExport(ctx, job.ID, "exports/example.zip", expires); err != nil {
		t.Fatalf("complete export: %v", err)
	}
	got, err := st.OrganizationExportForActor(ctx, owner.ID, biz.ID, job.ID)
	if err != nil {
		t.Fatalf("get completed export: %v", err)
	}
	if got.Status != "ready" || got.FilePath == nil || *got.FilePath != "exports/example.zip" {
		t.Fatalf("unexpected completed export: %+v", got)
	}

	expiredJob, err := st.CreateOrganizationExport(ctx, owner.ID, biz.ID, ExportActivityJSON)
	if err != nil {
		t.Fatalf("create expiring export: %v", err)
	}
	claimed, err = st.ClaimNextOrganizationExport(ctx)
	if err != nil || claimed == nil || claimed.ID != expiredJob.ID {
		t.Fatalf("claim expiring export: %+v err=%v", claimed, err)
	}
	if err := st.CompleteOrganizationExport(
		ctx, expiredJob.ID, "exports/expired.json", time.Now().Add(-time.Minute),
	); err != nil {
		t.Fatalf("complete expiring export: %v", err)
	}
	paths, err := st.ExpireOrganizationExports(ctx)
	if err != nil {
		t.Fatalf("expire exports: %v", err)
	}
	if len(paths) != 1 || paths[0] != "exports/expired.json" {
		t.Fatalf("expired paths = %#v", paths)
	}
	got, err = st.OrganizationExportForActor(ctx, owner.ID, biz.ID, expiredJob.ID)
	if err != nil {
		t.Fatalf("get expired export: %v", err)
	}
	if got.Status != "expired" {
		t.Fatalf("expired status = %q, want expired", got.Status)
	}
}


func TestIntegrationOrganizationLifecycle(t *testing.T) {
	st, pool := integrationStore(t)
	ctx := context.Background()

	owner, err := st.CreateUser(ctx, "lifecycle-owner@example.test", "", "hash", "Lifecycle Owner", "manager")
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	biz, err := st.CreateBusiness(ctx, owner.ID, "Lifecycle team", "team")
	if err != nil {
		t.Fatalf("create business: %v", err)
	}
	admin, err := st.CreateUser(ctx, "lifecycle-admin@example.test", "", "hash", "Lifecycle Admin", "manager")
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO memberships (user_id, business_id, role) VALUES ($1, $2, 'admin')`,
		admin.ID, biz.ID,
	); err != nil {
		t.Fatalf("add admin: %v", err)
	}
	employee, _, err := st.CreateEmployee(ctx, owner.ID, &biz.ID, "", "lifecycle_member", "hash", "Lifecycle Member")
	if err != nil {
		t.Fatalf("create employee: %v", err)
	}

	archived, err := st.ArchiveOrganization(ctx, admin.ID, biz.ID)
	if err != nil {
		t.Fatalf("admin archive: %v", err)
	}
	if archived.ArchivedAt == nil {
		t.Fatal("archive did not set archived_at")
	}
	if _, err := st.ScheduleOrganizationDeletion(ctx, admin.ID, biz.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("admin schedule deletion = %v, want ErrForbidden", err)
	}
	restored, err := st.RestoreOrganization(ctx, admin.ID, biz.ID)
	if err != nil {
		t.Fatalf("admin restore: %v", err)
	}
	if restored.ArchivedAt != nil {
		t.Fatal("restore did not clear archived_at")
	}

	if err := st.CreateAuthSession(
		ctx, owner.ID, uuid.NewString(), "lifecycle-owner-refresh", "web", "Owner browser",
		1, time.Now().Add(time.Hour),
	); err != nil {
		t.Fatalf("create owner session: %v", err)
	}
	adminSessionID := uuid.NewString()
	if err := st.CreateAuthSession(
		ctx, admin.ID, adminSessionID, "lifecycle-admin-refresh", "web", "Admin browser",
		1, time.Now().Add(time.Hour),
	); err != nil {
		t.Fatalf("create admin session: %v", err)
	}

	if err := st.TransferOrganizationOwnership(ctx, owner.ID, biz.ID, admin.ID); err != nil {
		t.Fatalf("transfer ownership: %v", err)
	}
	updated, err := st.GetBusiness(ctx, biz.ID)
	if err != nil {
		t.Fatalf("get transferred business: %v", err)
	}
	if updated.OwnerUserID != admin.ID {
		t.Fatalf("owner_user_id = %s, want %s", updated.OwnerUserID, admin.ID)
	}
	oldRole, err := st.MembershipRole(ctx, owner.ID, biz.ID)
	if err != nil || oldRole != RoleAdmin {
		t.Fatalf("previous owner role = %q err=%v, want admin", oldRole, err)
	}
	newRole, err := st.MembershipRole(ctx, admin.ID, biz.ID)
	if err != nil || newRole != RoleOwner {
		t.Fatalf("new owner role = %q err=%v, want owner", newRole, err)
	}
	sessionActive, err := st.SessionActive(ctx, admin.ID, adminSessionID)
	if err != nil {
		t.Fatalf("check new owner session: %v", err)
	}
	if sessionActive {
		t.Fatal("ownership transfer did not revoke new owner session")
	}

	if _, err := st.ArchiveOrganization(ctx, admin.ID, biz.ID); err != nil {
		t.Fatalf("new owner archive: %v", err)
	}
	preview, err := st.OrganizationDeletionPreview(ctx, admin.ID, biz.ID)
	if err != nil {
		t.Fatalf("deletion preview: %v", err)
	}
	if preview.Members != 3 {
		t.Fatalf("preview members = %d, want 3", preview.Members)
	}

	scheduled, err := st.ScheduleOrganizationDeletion(ctx, admin.ID, biz.ID)
	if err != nil {
		t.Fatalf("schedule deletion: %v", err)
	}
	if scheduled.DeletionScheduledAt == nil {
		t.Fatal("schedule deletion did not set deadline")
	}
	cancelled, err := st.CancelOrganizationDeletion(ctx, admin.ID, biz.ID)
	if err != nil {
		t.Fatalf("cancel deletion: %v", err)
	}
	if cancelled.DeletionScheduledAt != nil || cancelled.ArchivedAt == nil {
		t.Fatalf("cancelled state = %+v, want archived with no deletion deadline", cancelled)
	}
	if _, err := st.ScheduleOrganizationDeletion(ctx, admin.ID, biz.ID); err != nil {
		t.Fatalf("reschedule deletion: %v", err)
	}

	// Give the new owner another organization so hard deletion must preserve the account.
	if _, err := st.CreateBusiness(ctx, admin.ID, "Other lifecycle team", "team"); err != nil {
		t.Fatalf("create second business: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE businesses SET deletion_scheduled_at = now() - interval '1 minute' WHERE id = $1`,
		biz.ID,
	); err != nil {
		t.Fatalf("force deletion deadline: %v", err)
	}
	removedFiles := false
	deletedAccounts, err := st.DeleteScheduledOrganization(ctx, biz.ID, func() error {
		removedFiles = true
		return nil
	})
	if err != nil {
		t.Fatalf("hard delete organization: %v", err)
	}
	if !removedFiles {
		t.Fatal("hard delete did not remove organization files")
	}
	if deletedAccounts < 1 {
		t.Fatalf("deleted orphan accounts = %d, want at least 1", deletedAccounts)
	}
	if _, err := st.GetBusiness(ctx, biz.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted business lookup = %v, want ErrNotFound", err)
	}
	if _, err := st.GetUserByID(ctx, employee.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("orphan employee lookup = %v, want ErrNotFound", err)
	}
	if _, err := st.GetUserByID(ctx, admin.ID); err != nil {
		t.Fatalf("multi-organization owner was deleted: %v", err)
	}
}


func TestIntegrationProductionUpgradeToProductFoundation(t *testing.T) {
	dsn := os.Getenv(integrationDatabaseEnv)
	if dsn == "" {
		t.Skipf("%s is not set", integrationDatabaseEnv)
	}
	ctx := context.Background()

	// Start from the real latest schema, seed data through production store paths,
	// then roll back only Product Foundation v1. That leaves the exact v13 schema
	// and realistic rows that an existing installation would carry into migration 14.
	if err := db.Migrate(dsn); err != nil {
		t.Fatalf("migrate latest before upgrade fixture: %v", err)
	}
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect latest database: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		TRUNCATE TABLE
			organization_exports,
			mfa_recovery_codes,
			user_mfa,
			security_events,
			auth_sessions,
			privacy_rules,
			enrollment_tokens,
			audit_events,
			screenshots,
			browser_visits,
			keystroke_buckets,
			activity_samples,
			devices,
			memberships,
			businesses,
			users
		RESTART IDENTITY CASCADE`); err != nil {
		pool.Close()
		t.Fatalf("reset latest database: %v", err)
	}

	st := New(pool)
	owner, err := st.CreateUser(ctx, "upgrade-owner@example.test", "", "hash", "Upgrade Owner", "manager")
	if err != nil {
		pool.Close()
		t.Fatalf("create upgrade owner: %v", err)
	}
	biz, err := st.CreateBusiness(ctx, owner.ID, "Upgrade team", "team")
	if err != nil {
		pool.Close()
		t.Fatalf("create upgrade business: %v", err)
	}
	employee, _, err := st.CreateEmployee(
		ctx, owner.ID, &biz.ID, "", "upgrade_member", "hash", "Upgrade Member",
	)
	if err != nil {
		pool.Close()
		t.Fatalf("create upgrade employee: %v", err)
	}

	// These are legacy v13 settings. Migration 14 must preserve configured
	// retention, translate mode -> capture scope, import skip apps as rules, and
	// remove the old employee-override escape hatch.
	if _, err := pool.Exec(ctx, `
		UPDATE businesses
		   SET screenshot_mode = 'normal',
		       screenshot_skip_apps = ARRAY['Signal','Vault'],
		       screenshot_retention_days = 14,
		       allow_employee_override = true
		 WHERE id = $1`, biz.ID); err != nil {
		pool.Close()
		t.Fatalf("configure legacy business: %v", err)
	}

	deviceID := uuid.NewString()
	activityID := uuid.NewString()
	if err := st.SyncBatch(
		ctx, employee.ID, biz.ID, deviceID, DeviceMetadata{},
		[]ActivityRow{{
			ClientUUID: activityID, Ts: 100, AppName: "Legacy editor",
			DurationS: 15, ClientUpdatedAt: 100,
		}},
		nil, nil,
	); err != nil {
		pool.Close()
		t.Fatalf("seed legacy activity/device: %v", err)
	}
	screenshotID := uuid.NewString()
	if err := st.UpsertScreenshot(ctx, employee.ID, biz.ID, ScreenshotRow{
		ClientUUID: screenshotID,
		DeviceID: deviceID,
		Ts: 100,
		FilePath: "screenshots/legacy.webp",
		ByteSize: 123,
		ClientUpdatedAt: 100,
	}); err != nil {
		pool.Close()
		t.Fatalf("seed legacy screenshot: %v", err)
	}

	// A legacy device for a user with memberships in multiple organizations must
	// stay unbound after migration 14. Picking either organization would silently
	// cross an organization privacy boundary.
	secondBiz, err := st.CreateBusiness(ctx, owner.ID, "Upgrade second team", "team")
	if err != nil {
		pool.Close()
		t.Fatalf("create second upgrade business: %v", err)
	}
	multiUser, err := st.CreateUser(
		ctx, "upgrade-multi@example.test", "", "hash", "Upgrade Multi", "manager",
	)
	if err != nil {
		pool.Close()
		t.Fatalf("create multi-org upgrade user: %v", err)
	}
	for _, businessID := range []string{biz.ID, secondBiz.ID} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO memberships (user_id, business_id, role)
			 VALUES ($1, $2, 'employee')`,
			multiUser.ID, businessID,
		); err != nil {
			pool.Close()
			t.Fatalf("add multi-org membership: %v", err)
		}
	}
	multiDeviceID := uuid.NewString()
	if err := st.SyncBatch(
		ctx, multiUser.ID, biz.ID, multiDeviceID, DeviceMetadata{},
		nil, nil, nil,
	); err != nil {
		pool.Close()
		t.Fatalf("seed multi-org legacy device: %v", err)
	}
	pool.Close()

	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open goose database: %v", err)
	}
	if err := goose.SetDialect("postgres"); err != nil {
		sqlDB.Close()
		t.Fatalf("set goose dialect: %v", err)
	}
	// db.Migrate set goose's embedded BaseFS, so DownTo uses the same migration
	// source as production rather than a test-only copy.
	if err := goose.DownTo(sqlDB, "migrations", 13); err != nil {
		sqlDB.Close()
		t.Fatalf("roll back to production v13 schema: %v", err)
	}
	sqlDB.Close()

	// This is the production upgrade under test.
	if err := db.Migrate(dsn); err != nil {
		t.Fatalf("upgrade v13 -> product foundation: %v", err)
	}
	verify, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect upgraded database: %v", err)
	}
	defer verify.Close()

	var (
		name                  string
		timezone              string
		scope                 string
		retention             int
		allowOverride         bool
		weekStart             *int
		membershipStatus      string
		deviceBusinessID      *string
		multiDeviceBusinessID *string
		captureGroupID        *string
		activityCount         int
		privacyRuleCount      int
	)
	if err := verify.QueryRow(ctx, `
		SELECT name, timezone, screenshot_capture_scope, screenshot_retention_days,
		       allow_employee_override, week_starts_on
		  FROM businesses
		 WHERE id = $1`, biz.ID,
	).Scan(&name, &timezone, &scope, &retention, &allowOverride, &weekStart); err != nil {
		t.Fatalf("read upgraded business: %v", err)
	}
	if name != "Upgrade team" || timezone != "UTC" || scope != "active_display" ||
		retention != 14 || allowOverride || weekStart != nil {
		t.Fatalf(
			"unexpected upgraded business: name=%q timezone=%q scope=%q retention=%d override=%v week=%v",
			name, timezone, scope, retention, allowOverride, weekStart,
		)
	}

	if err := verify.QueryRow(ctx, `
		SELECT status FROM memberships
		 WHERE user_id = $1 AND business_id = $2`,
		employee.ID, biz.ID,
	).Scan(&membershipStatus); err != nil {
		t.Fatalf("read upgraded membership: %v", err)
	}
	if membershipStatus != MemberStatusActive {
		t.Fatalf("membership status = %q, want active", membershipStatus)
	}

	if err := verify.QueryRow(ctx,
		`SELECT business_id::text FROM devices WHERE id = $1`, deviceID,
	).Scan(&deviceBusinessID); err != nil {
		t.Fatalf("read upgraded device: %v", err)
	}
	if deviceBusinessID == nil || *deviceBusinessID != biz.ID {
		t.Fatalf("device business = %v, want %s", deviceBusinessID, biz.ID)
	}
	if err := verify.QueryRow(ctx,
		`SELECT business_id::text FROM devices WHERE id = $1`, multiDeviceID,
	).Scan(&multiDeviceBusinessID); err != nil {
		t.Fatalf("read upgraded multi-org device: %v", err)
	}
	if multiDeviceBusinessID != nil {
		t.Fatalf("multi-org legacy device was arbitrarily bound to %v", multiDeviceBusinessID)
	}

	if err := verify.QueryRow(ctx,
		`SELECT capture_group_id::text FROM screenshots WHERE client_uuid = $1`, screenshotID,
	).Scan(&captureGroupID); err != nil {
		t.Fatalf("read upgraded screenshot: %v", err)
	}
	if captureGroupID != nil {
		t.Fatalf("legacy screenshot capture_group_id = %v, want nil", captureGroupID)
	}

	if err := verify.QueryRow(ctx,
		`SELECT count(*) FROM activity_samples WHERE client_uuid = $1`, activityID,
	).Scan(&activityCount); err != nil {
		t.Fatalf("count upgraded activity: %v", err)
	}
	if activityCount != 1 {
		t.Fatalf("activity rows after upgrade = %d, want 1", activityCount)
	}

	if err := verify.QueryRow(ctx, `
		SELECT count(*)
		  FROM privacy_rules
		 WHERE business_id = $1
		   AND kind = 'app'
		   AND match_type = 'exact'
		   AND enabled = true
		   AND pattern IN ('Signal','Vault')`, biz.ID,
	).Scan(&privacyRuleCount); err != nil {
		t.Fatalf("count imported privacy rules: %v", err)
	}
	if privacyRuleCount != 2 {
		t.Fatalf("imported privacy rules = %d, want 2", privacyRuleCount)
	}
}


func TestIntegrationFourRoleCapabilityMatrix(t *testing.T) {
	st, pool := integrationStore(t)
	ctx := context.Background()

	owner, err := st.CreateUser(ctx, "matrix-owner@example.test", "", "hash", "Matrix Owner", "manager")
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	biz, err := st.CreateBusiness(ctx, owner.ID, "Capability matrix", "team")
	if err != nil {
		t.Fatalf("create business: %v", err)
	}

	users := map[BusinessRole]User{RoleOwner: owner}
	for _, role := range []BusinessRole{RoleAdmin, RoleManager, RoleEmployee} {
		user, err := st.CreateUser(
			ctx,
			"matrix-"+string(role)+"@example.test",
			"",
			"hash",
			"Matrix "+string(role),
			"manager",
		)
		if err != nil {
			t.Fatalf("create %s: %v", role, err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO memberships (user_id, business_id, role)
			 VALUES ($1, $2, $3)`,
			user.ID, biz.ID, role,
		); err != nil {
			t.Fatalf("add %s membership: %v", role, err)
		}
		users[role] = user
	}

	all := []Capability{
		CapabilityReportsView,
		CapabilityMembersView,
		CapabilityMembersManage,
		CapabilityMembersPurge,
		CapabilityDevicesView,
		CapabilityDevicesManage,
		CapabilitySettingsView,
		CapabilitySettingsManage,
		CapabilityAuditView,
		CapabilityRolesManage,
		CapabilityOrganizationManage,
		CapabilityOrganizationTransfer,
		CapabilityOrganizationDelete,
	}
	expected := map[BusinessRole]map[Capability]bool{
		RoleOwner: {
			CapabilityReportsView: true, CapabilityMembersView: true,
			CapabilityMembersManage: true, CapabilityMembersPurge: true,
			CapabilityDevicesView: true, CapabilityDevicesManage: true,
			CapabilitySettingsView: true, CapabilitySettingsManage: true,
			CapabilityAuditView: true, CapabilityRolesManage: true,
			CapabilityOrganizationManage: true, CapabilityOrganizationTransfer: true,
			CapabilityOrganizationDelete: true,
		},
		RoleAdmin: {
			CapabilityReportsView: true, CapabilityMembersView: true,
			CapabilityMembersManage: true,
			CapabilityDevicesView: true, CapabilityDevicesManage: true,
			CapabilitySettingsView: true, CapabilitySettingsManage: true,
			CapabilityAuditView: true, CapabilityOrganizationManage: true,
		},
		RoleManager: {
			CapabilityReportsView: true,
			CapabilityMembersView: true,
		},
		RoleEmployee: {},
	}

	for role, user := range users {
		for _, capability := range all {
			got, err := st.HasBusinessPermission(ctx, user.ID, biz.ID, capability)
			if err != nil {
				t.Fatalf("%s %s permission error: %v", role, capability, err)
			}
			want := expected[role][capability]
			if got != want {
				t.Fatalf("%s capability %s = %v, want %v", role, capability, got, want)
			}
		}
	}

	// Membership lifecycle state also gates all role capabilities.
	manager := users[RoleManager]
	if _, err := pool.Exec(ctx,
		`UPDATE memberships SET status = 'blocked' WHERE user_id = $1 AND business_id = $2`,
		manager.ID, biz.ID,
	); err != nil {
		t.Fatalf("block manager fixture: %v", err)
	}
	if _, err := st.HasBusinessPermission(
		ctx, manager.ID, biz.ID, CapabilityReportsView,
	); !errors.Is(err, ErrForbidden) {
		t.Fatalf("blocked manager permission error = %v, want ErrForbidden", err)
	}
}


func TestIntegrationOrganizationDeviceLimit(t *testing.T) {
	st, _ := integrationStore(t)
	ctx := context.Background()

	owner, err := st.CreateUser(ctx, "limit-owner@example.test", "", "hash", "Limit Owner", "manager")
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	biz, err := st.CreateBusiness(ctx, owner.ID, "Limited devices", "team")
	if err != nil {
		t.Fatalf("create business: %v", err)
	}
	if err := st.UpdateBusinessSettingsAudited(
		ctx, owner.ID, biz.ID, map[string]any{"device_limit": 1},
	); err != nil {
		t.Fatalf("set device limit: %v", err)
	}

	first, _, err := st.CreateEmployee(
		ctx, owner.ID, &biz.ID, "", "limit_first", "hash", "First Member",
	)
	if err != nil {
		t.Fatalf("create first member: %v", err)
	}
	second, _, err := st.CreateEmployee(
		ctx, owner.ID, &biz.ID, "", "limit_second", "hash", "Second Member",
	)
	if err != nil {
		t.Fatalf("create second member: %v", err)
	}

	firstDevice := uuid.NewString()
	secondDevice := uuid.NewString()
	if err := st.TouchDevice(ctx, first.ID, biz.ID, firstDevice, DeviceMetadata{}); err != nil {
		t.Fatalf("enroll first device: %v", err)
	}
	if err := st.TouchDevice(ctx, second.ID, biz.ID, secondDevice, DeviceMetadata{}); !errors.Is(err, ErrDeviceLimitReached) {
		t.Fatalf("second organization device = %v, want ErrDeviceLimitReached", err)
	}

	revoke := true
	if _, err := st.UpdateDevice(ctx, owner.ID, firstDevice, nil, &revoke, biz.ID); err != nil {
		t.Fatalf("revoke first device: %v", err)
	}
	if err := st.TouchDevice(ctx, second.ID, biz.ID, secondDevice, DeviceMetadata{}); err != nil {
		t.Fatalf("enroll second device after revoke: %v", err)
	}

	restore := false
	if _, err := st.UpdateDevice(ctx, owner.ID, firstDevice, nil, &restore, biz.ID); !errors.Is(err, ErrDeviceLimitReached) {
		t.Fatalf("restore while slot occupied = %v, want ErrDeviceLimitReached", err)
	}

	if _, err := st.UpdateDevice(ctx, owner.ID, secondDevice, nil, &revoke, biz.ID); err != nil {
		t.Fatalf("revoke second device: %v", err)
	}
	restored, err := st.UpdateDevice(ctx, owner.ID, firstDevice, nil, &restore, biz.ID)
	if err != nil {
		t.Fatalf("restore first device after slot freed: %v", err)
	}
	if restored.RevokedAt != nil {
		t.Fatalf("restored device still revoked: %+v", restored)
	}
}


func TestIntegrationMFARecoveryAndManagedReset(t *testing.T) {
	st, pool := integrationStore(t)
	ctx := context.Background()

	owner, err := st.CreateUser(ctx, "mfa-owner@example.test", "", "hash", "MFA Owner", "manager")
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	biz, err := st.CreateBusiness(ctx, owner.ID, "MFA team", "team")
	if err != nil {
		t.Fatalf("create business: %v", err)
	}

	createMember := func(email, name string, role BusinessRole) User {
		t.Helper()
		user, err := st.CreateUser(ctx, email, "", "hash", name, "manager")
		if err != nil {
			t.Fatalf("create %s: %v", role, err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO memberships (user_id, business_id, role)
			 VALUES ($1, $2, $3)`,
			user.ID, biz.ID, role,
		); err != nil {
			t.Fatalf("add %s membership: %v", role, err)
		}
		return user
	}

	admin := createMember("mfa-admin@example.test", "MFA Admin", RoleAdmin)
	peerAdmin := createMember("mfa-peer-admin@example.test", "MFA Peer Admin", RoleAdmin)
	manager := createMember("mfa-manager@example.test", "MFA Manager", RoleManager)
	employee := createMember("mfa-employee@example.test", "MFA Employee", RoleEmployee)

	enable := func(userID string, hashes []string) {
		t.Helper()
		if err := st.BeginMFASetup(ctx, userID, []byte("encrypted-test-secret")); err != nil {
			t.Fatalf("begin mfa setup for %s: %v", userID, err)
		}
		if err := st.EnableMFA(ctx, userID, hashes); err != nil {
			t.Fatalf("enable mfa for %s: %v", userID, err)
		}
		state, err := st.MFAState(ctx, userID)
		if err != nil {
			t.Fatalf("mfa state for %s: %v", userID, err)
		}
		if !state.Enabled || state.EnabledAt == nil {
			t.Fatalf("mfa not enabled for %s: %+v", userID, state)
		}
	}

	oldOne := auth.HashRecoveryCode("AAAAA-BBBBB")
	oldTwo := auth.HashRecoveryCode("CCCCC-DDDDD")
	enable(employee.ID, []string{oldOne, oldTwo})

	used, err := st.ConsumeRecoveryCode(ctx, employee.ID, oldOne)
	if err != nil || !used {
		t.Fatalf("consume first recovery code: used=%v err=%v", used, err)
	}
	used, err = st.ConsumeRecoveryCode(ctx, employee.ID, oldOne)
	if err != nil {
		t.Fatalf("consume used recovery code: %v", err)
	}
	if used {
		t.Fatal("recovery code was accepted twice")
	}

	newOne := auth.HashRecoveryCode("EEEEE-FFFFF")
	newTwo := auth.HashRecoveryCode("GGGGG-HHHHH")
	if err := st.ReplaceRecoveryCodes(ctx, employee.ID, []string{newOne, newTwo}); err != nil {
		t.Fatalf("replace recovery codes: %v", err)
	}
	used, err = st.ConsumeRecoveryCode(ctx, employee.ID, oldTwo)
	if err != nil {
		t.Fatalf("consume invalidated old recovery code: %v", err)
	}
	if used {
		t.Fatal("old recovery code survived regeneration")
	}
	used, err = st.ConsumeRecoveryCode(ctx, employee.ID, newOne)
	if err != nil || !used {
		t.Fatalf("consume regenerated recovery code: used=%v err=%v", used, err)
	}

	_, beforeVersion, err := st.UserSecurity(ctx, employee.ID)
	if err != nil {
		t.Fatalf("employee security before reset: %v", err)
	}
	sessionID := uuid.NewString()
	if err := st.CreateAuthSession(
		ctx,
		employee.ID,
		sessionID,
		"mfa-employee-refresh",
		"web",
		"Employee browser",
		beforeVersion,
		time.Now().Add(time.Hour),
	); err != nil {
		t.Fatalf("create employee session: %v", err)
	}

	if err := st.ResetManagedMemberMFA(ctx, admin.ID, biz.ID, employee.ID); err != nil {
		t.Fatalf("admin reset employee mfa: %v", err)
	}
	state, err := st.MFAState(ctx, employee.ID)
	if err != nil {
		t.Fatalf("employee mfa state after reset: %v", err)
	}
	if state.Enabled || state.EnabledAt != nil {
		t.Fatalf("employee mfa still enabled after reset: %+v", state)
	}
	if _, err := st.MFASecret(ctx, employee.ID, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("employee mfa secret after reset = %v, want ErrNotFound", err)
	}
	active, afterVersion, err := st.UserSecurity(ctx, employee.ID)
	if err != nil {
		t.Fatalf("employee security after reset: %v", err)
	}
	if !active || afterVersion <= beforeVersion {
		t.Fatalf("security after reset active=%v version=%d, before=%d", active, afterVersion, beforeVersion)
	}
	sessionActive, err := st.SessionActive(ctx, employee.ID, sessionID)
	if err != nil {
		t.Fatalf("check employee session after reset: %v", err)
	}
	if sessionActive {
		t.Fatal("mfa reset did not revoke employee sessions")
	}
	var auditCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_events
		 WHERE business_id = $1
		   AND action = 'member.mfa_reset'
		   AND target_id = $2`,
		biz.ID, employee.ID,
	).Scan(&auditCount); err != nil {
		t.Fatalf("query mfa reset audit: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("mfa reset audit count = %d, want 1", auditCount)
	}

	enable(manager.ID, []string{auth.HashRecoveryCode("MMMMM-NNNNN")})
	if err := st.ResetManagedMemberMFA(ctx, admin.ID, biz.ID, manager.ID); err != nil {
		t.Fatalf("admin reset manager mfa: %v", err)
	}

	enable(peerAdmin.ID, []string{auth.HashRecoveryCode("PPPPP-QQQQQ")})
	if err := st.ResetManagedMemberMFA(ctx, admin.ID, biz.ID, peerAdmin.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("admin reset peer admin = %v, want ErrForbidden", err)
	}
	if err := st.ResetManagedMemberMFA(ctx, owner.ID, biz.ID, peerAdmin.ID); err != nil {
		t.Fatalf("owner reset admin mfa: %v", err)
	}

	enable(owner.ID, []string{auth.HashRecoveryCode("RRRRR-SSSSS")})
	if err := st.ResetManagedMemberMFA(ctx, owner.ID, biz.ID, owner.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("owner self-reset mfa = %v, want ErrForbidden", err)
	}
}


func TestIntegrationArchivedOrganizationIsReadOnly(t *testing.T) {
	st, _ := integrationStore(t)
	ctx := context.Background()

	owner, err := st.CreateUser(ctx, "readonly-owner@example.test", "", "hash", "Readonly Owner", "manager")
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	biz, err := st.CreateBusiness(ctx, owner.ID, "Readonly team", "team")
	if err != nil {
		t.Fatalf("create business: %v", err)
	}
	admin, _, err := st.CreateEmployee(ctx, owner.ID, &biz.ID, "readonly-admin@example.test", "", "hash", "Readonly Admin")
	if err != nil {
		t.Fatalf("create admin account fixture: %v", err)
	}
	if err := st.UpdateMembershipRole(ctx, owner.ID, biz.ID, admin.ID, RoleAdmin); err != nil {
		t.Fatalf("promote admin fixture: %v", err)
	}
	employee, _, err := st.CreateEmployee(ctx, owner.ID, &biz.ID, "", "readonly_employee", "hash", "Readonly Employee")
	if err != nil {
		t.Fatalf("create employee: %v", err)
	}
	deviceID := uuid.NewString()
	if err := st.TouchDevice(ctx, employee.ID, biz.ID, deviceID, DeviceMetadata{}); err != nil {
		t.Fatalf("seed employee device: %v", err)
	}

	if _, err := st.ArchiveOrganization(ctx, owner.ID, biz.ID); err != nil {
		t.Fatalf("archive organization: %v", err)
	}

	assertArchived := func(name string, err error) {
		t.Helper()
		if !errors.Is(err, ErrOrganizationArchived) {
			t.Fatalf("%s = %v, want ErrOrganizationArchived", name, err)
		}
	}

	assertArchived("role change", st.UpdateMembershipRole(ctx, owner.ID, biz.ID, employee.ID, RoleManager))
	assertArchived("monitoring change", st.UpdateMembershipMonitoring(ctx, owner.ID, biz.ID, employee.ID, false))
	assertArchived("member removal", st.RemoveMember(ctx, owner.ID, biz.ID, employee.ID))
	assertArchived("password reset", st.ResetManagedMemberPassword(ctx, owner.ID, biz.ID, employee.ID, "new-hash"))
	assertArchived("mfa reset", st.ResetManagedMemberMFA(ctx, owner.ID, biz.ID, employee.ID))
	_, err = st.UpdateManagedMemberIdentity(
		ctx, owner.ID, biz.ID, employee.ID, nil, nil, func() *string {
			v := "Changed Name"
			return &v
		}(),
	)
	assertArchived("identity change", err)
	revoke := true
	_, err = st.UpdateDevice(ctx, owner.ID, deviceID, nil, &revoke, biz.ID)
	assertArchived("device change", err)
	_, err = st.CreatePrivacyRule(ctx, owner.ID, biz.ID, "app", "exact", "Private App")
	assertArchived("privacy rule create", err)
	_, err = st.UpdateDefaultMonitoring(ctx, owner.ID, biz.ID, false, true)
	assertArchived("default monitoring change", err)
	assertArchived(
		"settings change",
		st.UpdateBusinessSettingsAudited(ctx, owner.ID, biz.ID, map[string]any{
			"collect_browser_activity": false,
		}),
	)
	_, _, err = st.CreateEmployee(
		ctx, owner.ID, &biz.ID, "", "blocked_new_member", "hash", "Blocked New Member",
	)
	assertArchived("member creation", err)
	_, err = st.PurgeMemberFromBusiness(ctx, owner.ID, biz.ID, employee.ID, nil)
	assertArchived("member purge", err)

	// Archive is read-only for mutations, but export remains intentionally available.
	exportJob, err := st.CreateOrganizationExport(ctx, owner.ID, biz.ID, ExportFull)
	if err != nil {
		t.Fatalf("create export while archived: %v", err)
	}
	if exportJob.BusinessID != biz.ID || exportJob.Status != "pending" {
		t.Fatalf("unexpected archived export job: %+v", exportJob)
	}

	// Admins can still read historical data/audit while archived.
	if _, err := st.ListAuditEvents(ctx, admin.ID, biz.ID, 20); err != nil {
		t.Fatalf("admin read audit while archived: %v", err)
	}
	if _, err := st.ListPrivacyRules(ctx, admin.ID, biz.ID); err != nil {
		t.Fatalf("admin read privacy rules while archived: %v", err)
	}
}


func TestIntegrationArchivedRetentionIsFrozen(t *testing.T) {
	st, _ := integrationStore(t)
	ctx := context.Background()

	owner, err := st.CreateUser(ctx, "retention-freeze-owner@example.test", "", "hash", "Retention Freeze Owner", "manager")
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	biz, err := st.CreateBusiness(ctx, owner.ID, "Retention freeze", "team")
	if err != nil {
		t.Fatalf("create business: %v", err)
	}
	member, _, err := st.CreateEmployee(ctx, owner.ID, &biz.ID, "", "retention_freeze_member", "hash", "Retention Member")
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	deviceID := uuid.NewString()
	if err := st.SyncBatch(
		ctx, member.ID, biz.ID, deviceID, DeviceMetadata{},
		[]ActivityRow{{ClientUUID: uuid.NewString(), Ts: 100, AppName: "Old app", DurationS: 10, ClientUpdatedAt: 100}},
		[]KeystrokeRow{{ClientUUID: uuid.NewString(), TsBucket: 100, Count: 3, ClientUpdatedAt: 100}},
		[]BrowserRow{{ClientUUID: uuid.NewString(), Ts: 100, URL: "https://old.example", DurationS: 5, ClientUpdatedAt: 100}},
	); err != nil {
		t.Fatalf("seed retention rows: %v", err)
	}

	if _, err := st.ArchiveOrganization(ctx, owner.ID, biz.ID); err != nil {
		t.Fatalf("archive organization: %v", err)
	}
	if err := st.EnsureBusinessMutable(ctx, biz.ID); !errors.Is(err, ErrOrganizationArchived) {
		t.Fatalf("mutable check = %v, want ErrOrganizationArchived", err)
	}
	policies, err := st.BusinessesWithRetentionPolicies(ctx)
	if err != nil {
		t.Fatalf("list retention policies: %v", err)
	}
	for _, policy := range policies {
		if policy.ID == biz.ID {
			t.Fatal("archived organization remained eligible for automatic retention")
		}
	}

	cutoff := int64(1_000_000)
	if _, err := st.DeleteActivityBefore(ctx, biz.ID, cutoff); !errors.Is(err, ErrOrganizationArchived) {
		t.Fatalf("activity delete = %v, want ErrOrganizationArchived", err)
	}
	if _, err := st.DeleteBrowserBefore(ctx, biz.ID, cutoff); !errors.Is(err, ErrOrganizationArchived) {
		t.Fatalf("browser delete = %v, want ErrOrganizationArchived", err)
	}
	if _, err := st.DeleteKeystrokesBefore(ctx, biz.ID, cutoff); !errors.Is(err, ErrOrganizationArchived) {
		t.Fatalf("keystroke delete = %v, want ErrOrganizationArchived", err)
	}

	activity, err := st.CountActivityBefore(ctx, biz.ID, cutoff)
	if err != nil || activity != 1 {
		t.Fatalf("activity count after blocked cleanup = %d err=%v, want 1", activity, err)
	}
	browser, err := st.CountBrowserBefore(ctx, biz.ID, cutoff)
	if err != nil || browser != 1 {
		t.Fatalf("browser count after blocked cleanup = %d err=%v, want 1", browser, err)
	}
	keys, err := st.CountKeystrokesBefore(ctx, biz.ID, cutoff)
	if err != nil || keys != 1 {
		t.Fatalf("keystroke count after blocked cleanup = %d err=%v, want 1", keys, err)
	}

	if _, err := st.ScheduleOrganizationDeletion(ctx, owner.ID, biz.ID); err != nil {
		t.Fatalf("schedule deletion: %v", err)
	}
	if err := st.EnsureBusinessMutable(ctx, biz.ID); !errors.Is(err, ErrOrganizationDeletionPending) {
		t.Fatalf("mutable check while deletion pending = %v, want ErrOrganizationDeletionPending", err)
	}
}


func TestIntegrationPolicyStopsOnOrganizationLifecycle(t *testing.T) {
	st, _ := integrationStore(t)
	ctx := context.Background()

	owner, err := st.CreateUser(ctx, "policy-lifecycle-owner@example.test", "", "hash", "Policy Owner", "manager")
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	biz, err := st.CreateBusiness(ctx, owner.ID, "Policy lifecycle", "team")
	if err != nil {
		t.Fatalf("create business: %v", err)
	}
	member, _, err := st.CreateEmployee(
		ctx, owner.ID, &biz.ID, "", "policy_lifecycle_member", "hash", "Policy Member",
	)
	if err != nil {
		t.Fatalf("create member: %v", err)
	}

	policy, err := st.PolicyForUserInBusiness(ctx, member.ID, biz.ID)
	if err != nil {
		t.Fatalf("active policy: %v", err)
	}
	if policy == nil || !policy.Managed || policy.BusinessID != biz.ID {
		t.Fatalf("unexpected active policy: %+v", policy)
	}
	enabled, err := st.MembershipMonitoringEnabled(ctx, member.ID, biz.ID)
	if err != nil || !enabled {
		t.Fatalf("active monitoring state enabled=%v err=%v", enabled, err)
	}

	if _, err := st.ArchiveOrganization(ctx, owner.ID, biz.ID); err != nil {
		t.Fatalf("archive organization: %v", err)
	}
	if _, err := st.PolicyForUserInBusiness(ctx, member.ID, biz.ID); !errors.Is(err, ErrOrganizationArchived) {
		t.Fatalf("archived policy error = %v, want ErrOrganizationArchived", err)
	}
	if _, err := st.MembershipMonitoringEnabled(ctx, member.ID, biz.ID); !errors.Is(err, ErrOrganizationArchived) {
		t.Fatalf("archived monitoring error = %v, want ErrOrganizationArchived", err)
	}

	if _, err := st.RestoreOrganization(ctx, owner.ID, biz.ID); err != nil {
		t.Fatalf("restore organization: %v", err)
	}
	if _, err := st.PolicyForUserInBusiness(ctx, member.ID, biz.ID); err != nil {
		t.Fatalf("policy after restore: %v", err)
	}

	if _, err := st.ArchiveOrganization(ctx, owner.ID, biz.ID); err != nil {
		t.Fatalf("archive for deletion: %v", err)
	}
	if _, err := st.ScheduleOrganizationDeletion(ctx, owner.ID, biz.ID); err != nil {
		t.Fatalf("schedule deletion: %v", err)
	}
	if _, err := st.PolicyForUserInBusiness(ctx, member.ID, biz.ID); !errors.Is(err, ErrOrganizationDeletionPending) {
		t.Fatalf("deletion-pending policy error = %v, want ErrOrganizationDeletionPending", err)
	}
	if _, err := st.MembershipMonitoringEnabled(ctx, member.ID, biz.ID); !errors.Is(err, ErrOrganizationDeletionPending) {
		t.Fatalf("deletion-pending monitoring error = %v, want ErrOrganizationDeletionPending", err)
	}
}


func TestIntegrationCleanupDateRangeBoundaries(t *testing.T) {
	st, _ := integrationStore(t)
	ctx := context.Background()

	owner, err := st.CreateUser(ctx, "range-owner@example.test", "", "hash", "Range Owner", "manager")
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	biz, err := st.CreateBusiness(ctx, owner.ID, "Range cleanup", "team")
	if err != nil {
		t.Fatalf("create business: %v", err)
	}
	member, _, err := st.CreateEmployee(
		ctx, owner.ID, &biz.ID, "", "range_member", "hash", "Range Member",
	)
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	deviceID := uuid.NewString()

	activity := []ActivityRow{}
	keys := []KeystrokeRow{}
	browser := []BrowserRow{}
	for _, ts := range []int64{100, 200, 300} {
		activity = append(activity, ActivityRow{
			ClientUUID: uuid.NewString(), Ts: ts, AppName: "Range App",
			DurationS: 5, ClientUpdatedAt: ts,
		})
		keys = append(keys, KeystrokeRow{
			ClientUUID: uuid.NewString(), TsBucket: ts, Count: 2, ClientUpdatedAt: ts,
		})
		browser = append(browser, BrowserRow{
			ClientUUID: uuid.NewString(), Ts: ts, URL: "https://range.example",
			DurationS: 3, ClientUpdatedAt: ts,
		})
	}
	if err := st.SyncBatch(
		ctx, member.ID, biz.ID, deviceID, DeviceMetadata{},
		activity, keys, browser,
	); err != nil {
		t.Fatalf("seed structured range rows: %v", err)
	}
	for i, ts := range []int64{100, 200, 300} {
		if err := st.UpsertScreenshot(ctx, member.ID, biz.ID, ScreenshotRow{
			ClientUUID: uuid.NewString(),
			DeviceID: deviceID,
			Ts: ts,
			FilePath: "screenshots/" + uuid.NewString() + ".webp",
			ByteSize: (i + 1) * 10,
			ClientUpdatedAt: ts,
		}); err != nil {
			t.Fatalf("seed screenshot %d: %v", ts, err)
		}
	}

	const fromTs, toTs = int64(150), int64(300)
	if count, err := st.CountActivityRange(ctx, biz.ID, fromTs, toTs); err != nil || count != 1 {
		t.Fatalf("activity preview count=%d err=%v, want 1", count, err)
	}
	if count, err := st.CountBrowserRange(ctx, biz.ID, fromTs, toTs); err != nil || count != 1 {
		t.Fatalf("browser preview count=%d err=%v, want 1", count, err)
	}
	if count, err := st.CountKeystrokesRange(ctx, biz.ID, fromTs, toTs); err != nil || count != 1 {
		t.Fatalf("keystroke preview count=%d err=%v, want 1", count, err)
	}
	if count, bytes, err := st.ScreenshotStatsRange(ctx, biz.ID, fromTs, toTs); err != nil || count != 1 || bytes != 20 {
		t.Fatalf("screenshot preview count=%d bytes=%d err=%v, want 1/20", count, bytes, err)
	}
	files, err := st.ScreenshotsRange(ctx, biz.ID, fromTs, toTs)
	if err != nil {
		t.Fatalf("list screenshot range: %v", err)
	}
	if len(files) != 1 || files[0].ByteSize != 20 {
		t.Fatalf("screenshot range files=%+v, want middle screenshot only", files)
	}

	if deleted, err := st.DeleteActivityRange(ctx, biz.ID, fromTs, toTs); err != nil || deleted != 1 {
		t.Fatalf("delete activity range=%d err=%v, want 1", deleted, err)
	}
	if deleted, err := st.DeleteBrowserRange(ctx, biz.ID, fromTs, toTs); err != nil || deleted != 1 {
		t.Fatalf("delete browser range=%d err=%v, want 1", deleted, err)
	}
	if deleted, err := st.DeleteKeystrokesRange(ctx, biz.ID, fromTs, toTs); err != nil || deleted != 1 {
		t.Fatalf("delete keystroke range=%d err=%v, want 1", deleted, err)
	}
	if deleted, err := st.DeleteScreenshotsByIDs(ctx, []int64{files[0].ID}); err != nil || deleted != 1 {
		t.Fatalf("delete screenshot range metadata=%d err=%v, want 1", deleted, err)
	}

	if count, err := st.CountActivityBefore(ctx, biz.ID, 1_000); err != nil || count != 2 {
		t.Fatalf("remaining activity=%d err=%v, want 2", count, err)
	}
	if count, err := st.CountBrowserBefore(ctx, biz.ID, 1_000); err != nil || count != 2 {
		t.Fatalf("remaining browser=%d err=%v, want 2", count, err)
	}
	if count, err := st.CountKeystrokesBefore(ctx, biz.ID, 1_000); err != nil || count != 2 {
		t.Fatalf("remaining keystrokes=%d err=%v, want 2", count, err)
	}
	if count, bytes, err := st.ScreenshotStatsBefore(ctx, biz.ID, 1_000); err != nil || count != 2 || bytes != 40 {
		t.Fatalf("remaining screenshots=%d bytes=%d err=%v, want 2/40", count, bytes, err)
	}
}


func TestIntegrationFormerMemberHistoryAndCaptureGroups(t *testing.T) {
	st, pool := integrationStore(t)
	ctx := context.Background()

	owner, err := st.CreateUser(ctx, "former-owner@example.test", "", "hash", "Former Owner", "manager")
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	biz, err := st.CreateBusiness(ctx, owner.ID, "Former history", "team")
	if err != nil {
		t.Fatalf("create business: %v", err)
	}
	member, _, err := st.CreateEmployee(
		ctx, owner.ID, &biz.ID, "", "former_member", "hash", "Former Member",
	)
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	admin, err := st.CreateUser(ctx, "former-admin@example.test", "", "hash", "Former Admin", "manager")
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	manager, err := st.CreateUser(ctx, "former-manager@example.test", "", "hash", "Former Manager", "manager")
	if err != nil {
		t.Fatalf("create manager: %v", err)
	}
	for _, row := range []struct {
		user User
		role BusinessRole
	}{{admin, RoleAdmin}, {manager, RoleManager}} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO memberships (user_id, business_id, role) VALUES ($1, $2, $3)`,
			row.user.ID, biz.ID, row.role,
		); err != nil {
			t.Fatalf("add %s membership: %v", row.role, err)
		}
	}

	deviceID := uuid.NewString()
	if err := st.SyncBatch(
		ctx, member.ID, biz.ID, deviceID, DeviceMetadata{},
		[]ActivityRow{{
			ClientUUID: uuid.NewString(), Ts: 200, AppName: "Historical App",
			DurationS: 15, ClientUpdatedAt: 200,
		}},
		nil, nil,
	); err != nil {
		t.Fatalf("seed former activity: %v", err)
	}
	groupID := uuid.NewString()
	if err := st.UpsertScreenshot(ctx, member.ID, biz.ID, ScreenshotRow{
		ClientUUID: uuid.NewString(),
		DeviceID: deviceID,
		Ts: 200,
		FilePath: "screenshots/former.webp",
		ByteSize: 50,
		DisplayID: func() *int { v := 1; return &v }(),
		CaptureGroupID: &groupID,
		ClientUpdatedAt: 200,
	}); err != nil {
		t.Fatalf("seed former screenshot: %v", err)
	}

	if err := st.RemoveMember(ctx, owner.ID, biz.ID, member.ID); err != nil {
		t.Fatalf("remove member: %v", err)
	}
	former, err := st.ListFormerMembers(ctx, biz.ID)
	if err != nil {
		t.Fatalf("list former members: %v", err)
	}
	if len(former) != 1 || former[0].ID != member.ID {
		t.Fatalf("former members = %+v, want removed member", former)
	}

	ownerAccess, err := st.ReportAccessInBusiness(ctx, owner.ID, member.ID, biz.ID)
	if err != nil || ownerAccess.TargetStatus != MemberStatusRemoved {
		t.Fatalf("owner former report access = %+v err=%v", ownerAccess, err)
	}
	adminAccess, err := st.ReportAccessInBusiness(ctx, admin.ID, member.ID, biz.ID)
	if err != nil || adminAccess.TargetStatus != MemberStatusRemoved {
		t.Fatalf("admin former report access = %+v err=%v", adminAccess, err)
	}
	if _, err := st.ReportAccessInBusiness(ctx, manager.ID, member.ID, biz.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("manager former report access = %v, want ErrForbidden", err)
	}

	activity, _, err := st.ActivityReportInBusiness(ctx, member.ID, biz.ID, 0, 1_000)
	if err != nil || len(activity) != 1 || activity[0].AppName != "Historical App" {
		t.Fatalf("former activity = %+v err=%v", activity, err)
	}
	shots, err := st.ScreenshotsReportInBusiness(ctx, member.ID, biz.ID, 0, 1_000, 20, 0)
	if err != nil {
		t.Fatalf("former screenshots: %v", err)
	}
	if len(shots) != 1 || shots[0].CaptureGroupID == nil || *shots[0].CaptureGroupID != groupID {
		t.Fatalf("former screenshot capture group = %+v, want %s", shots, groupID)
	}
}


func TestIntegrationOrganizationMetadataChangesPreserveHistory(t *testing.T) {
	st, pool := integrationStore(t)
	ctx := context.Background()

	owner, err := st.CreateUser(
		ctx, "metadata-owner@example.test", "", "hash", "Metadata Owner", "manager",
	)
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	biz, err := st.CreateBusinessConfigured(
		ctx, owner.ID, "Original Org", "team", "UTC", nil,
	)
	if err != nil {
		t.Fatalf("create business: %v", err)
	}
	member, _, err := st.CreateEmployee(
		ctx, owner.ID, &biz.ID, "", "metadata_member", "hash", "Metadata Member",
	)
	if err != nil {
		t.Fatalf("create member: %v", err)
	}

	deviceID := uuid.NewString()
	activityID := uuid.NewString()
	if err := st.SyncBatch(
		ctx, member.ID, biz.ID, deviceID, DeviceMetadata{Hostname: func() *string {
			v := "metadata-device"
			return &v
		}()},
		[]ActivityRow{{
			ClientUUID: activityID,
			Ts: 200,
			AppName: "Metadata App",
			DurationS: 15,
			ClientUpdatedAt: 200,
		}},
		nil, nil,
	); err != nil {
		t.Fatalf("seed history: %v", err)
	}
	screenshotID := uuid.NewString()
	if err := st.UpsertScreenshot(ctx, member.ID, biz.ID, ScreenshotRow{
		ClientUUID: screenshotID,
		DeviceID: deviceID,
		Ts: 200,
		FilePath: "screenshots/metadata.webp",
		ByteSize: 64,
		ClientUpdatedAt: 200,
	}); err != nil {
		t.Fatalf("seed screenshot: %v", err)
	}

	newName := "Renamed Org"
	newKind := "other"
	newTimezone := "Europe/Berlin"
	weekStart := 1
	updated, err := st.UpdateOrganization(ctx, owner.ID, biz.ID, OrganizationPatch{
		Name: &newName,
		Kind: &newKind,
		Timezone: &newTimezone,
		WeekStartsOnSet: true,
		WeekStartsOn: &weekStart,
	})
	if err != nil {
		t.Fatalf("update organization metadata: %v", err)
	}
	if updated.ID != biz.ID ||
		updated.Name != newName ||
		updated.Kind != newKind ||
		updated.Timezone != newTimezone ||
		updated.WeekStartsOn == nil ||
		*updated.WeekStartsOn != weekStart {
		t.Fatalf("unexpected updated business: %+v", updated)
	}

	var (
		membershipCount int
		deviceBusinessID string
		activityBusinessID string
		screenshotBusinessID string
	)
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		  FROM memberships
		 WHERE business_id = $1
		   AND user_id IN ($2, $3)`,
		biz.ID, owner.ID, member.ID,
	).Scan(&membershipCount); err != nil {
		t.Fatalf("count preserved memberships: %v", err)
	}
	if membershipCount != 2 {
		t.Fatalf("memberships after metadata change = %d, want 2", membershipCount)
	}
	if err := pool.QueryRow(
		ctx, `SELECT business_id::text FROM devices WHERE id = $1`, deviceID,
	).Scan(&deviceBusinessID); err != nil {
		t.Fatalf("read preserved device: %v", err)
	}
	if err := pool.QueryRow(
		ctx, `SELECT business_id::text FROM activity_samples WHERE client_uuid = $1`, activityID,
	).Scan(&activityBusinessID); err != nil {
		t.Fatalf("read preserved activity: %v", err)
	}
	if err := pool.QueryRow(
		ctx, `SELECT business_id::text FROM screenshots WHERE client_uuid = $1`, screenshotID,
	).Scan(&screenshotBusinessID); err != nil {
		t.Fatalf("read preserved screenshot: %v", err)
	}
	for label, businessID := range map[string]string{
		"device": deviceBusinessID,
		"activity": activityBusinessID,
		"screenshot": screenshotBusinessID,
	} {
		if businessID != biz.ID {
			t.Fatalf("%s history rebound to %q, want %q", label, businessID, biz.ID)
		}
	}

	var auditCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		  FROM audit_events
		 WHERE business_id = $1
		   AND action IN (
		       'organization.renamed',
		       'organization.kind_changed',
		       'organization.timezone_changed',
		       'organization.week_start_changed'
		   )`, biz.ID,
	).Scan(&auditCount); err != nil {
		t.Fatalf("count organization metadata audit: %v", err)
	}
	if auditCount != 4 {
		t.Fatalf("organization metadata audit count = %d, want 4", auditCount)
	}
}


func TestIntegrationMultiOrgPolicyNeverInfersArbitraryBusiness(t *testing.T) {
	st, pool := integrationStore(t)
	ctx := context.Background()

	ownerA, err := st.CreateUser(ctx, "scope-owner-a@example.test", "", "hash", "Scope Owner A", "manager")
	if err != nil {
		t.Fatalf("create owner A: %v", err)
	}
	ownerB, err := st.CreateUser(ctx, "scope-owner-b@example.test", "", "hash", "Scope Owner B", "manager")
	if err != nil {
		t.Fatalf("create owner B: %v", err)
	}
	bizA, err := st.CreateBusiness(ctx, ownerA.ID, "Scope A", "team")
	if err != nil {
		t.Fatalf("create business A: %v", err)
	}
	bizB, err := st.CreateBusiness(ctx, ownerB.ID, "Scope B", "team")
	if err != nil {
		t.Fatalf("create business B: %v", err)
	}
	member, _, err := st.CreateEmployee(
		ctx, ownerA.ID, &bizA.ID, "", "scope_member", "hash", "Scope Member",
	)
	if err != nil {
		t.Fatalf("create shared member: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO memberships (user_id, business_id, role)
		 VALUES ($1, $2, 'employee')`,
		member.ID, bizB.ID,
	); err != nil {
		t.Fatalf("add second membership: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE businesses SET collect_browser_activity = false WHERE id = $1`,
		bizA.ID,
	); err != nil {
		t.Fatalf("differentiate policy A: %v", err)
	}

	legacy, err := st.PolicyForUser(ctx, member.ID)
	if err == nil && legacy != nil {
		t.Fatalf(
			"legacy multi-org policy inferred business %q; want no arbitrary policy",
			legacy.BusinessID,
		)
	}

	policyA, err := st.PolicyForUserInBusiness(ctx, member.ID, bizA.ID)
	if err != nil {
		t.Fatalf("scoped policy A: %v", err)
	}
	policyB, err := st.PolicyForUserInBusiness(ctx, member.ID, bizB.ID)
	if err != nil {
		t.Fatalf("scoped policy B: %v", err)
	}
	if policyA.BusinessID != bizA.ID || policyB.BusinessID != bizB.ID {
		t.Fatalf("scoped policies rebound: A=%q B=%q", policyA.BusinessID, policyB.BusinessID)
	}
	if policyA.CollectBrowserActivity || !policyB.CollectBrowserActivity {
		t.Fatalf(
			"scoped policy values crossed organizations: A=%v B=%v",
			policyA.CollectBrowserActivity,
			policyB.CollectBrowserActivity,
		)
	}
}
