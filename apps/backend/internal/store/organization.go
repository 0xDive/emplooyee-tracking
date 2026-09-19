package store

import (
	"context"
	"errors"
	"strings"
	"time"

)

type OrganizationPatch struct {
	Name            *string
	Kind            *string
	Timezone        *string
	WeekStartsOnSet bool
	WeekStartsOn    *int
}

func ValidBusinessKind(kind string) bool {
	switch kind {
	case "team", "family", "other":
		return true
	default:
		return false
	}
}

func validateTimezone(value string) bool {
	if value == "" || len(value) > 100 {
		return false
	}
	_, err := time.LoadLocation(value)
	return err == nil
}

func (s *Store) UpdateOrganization(
	ctx context.Context,
	actorID, businessID string,
	patch OrganizationPatch,
) (Business, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Business{}, err
	}
	defer tx.Rollback(ctx)

	actorRole, err := requireBusinessPermissionTx(
		ctx, tx, actorID, businessID, CapabilityOrganizationManage,
	)
	if err != nil {
		return Business{}, err
	}

	current, err := getBusiness(ctx, tx, businessID)
	if err != nil {
		return Business{}, err
	}
	if current.DeletionScheduledAt != nil {
		return Business{}, ErrOrganizationDeletionPending
	}
	if current.ArchivedAt != nil {
		return Business{}, ErrOrganizationArchived
	}

	nextName := current.Name
	nextKind := current.Kind
	nextTimezone := current.Timezone
	nextWeek := current.WeekStartsOn

	type pendingAudit struct {
		action  string
		details map[string]any
	}
	events := []pendingAudit{}

	if patch.Name != nil {
		value := strings.TrimSpace(*patch.Name)
		if value == "" || len([]rune(value)) > 120 {
			return Business{}, ErrConflict
		}
		if value != current.Name {
			nextName = value
			events = append(events, pendingAudit{
				action:  "organization.renamed",
				details: map[string]any{"from": current.Name, "to": value},
			})
		}
	}

	if patch.Kind != nil {
		value := strings.ToLower(strings.TrimSpace(*patch.Kind))
		if !ValidBusinessKind(value) {
			return Business{}, ErrConflict
		}
		if actorRole != RoleOwner {
			return Business{}, ErrForbidden
		}
		if value != current.Kind {
			nextKind = value
			events = append(events, pendingAudit{
				action:  "organization.kind_changed",
				details: map[string]any{"from": current.Kind, "to": value},
			})
		}
	}

	if patch.Timezone != nil {
		value := strings.TrimSpace(*patch.Timezone)
		if !validateTimezone(value) {
			return Business{}, ErrConflict
		}
		if value != current.Timezone {
			nextTimezone = value
			events = append(events, pendingAudit{
				action:  "organization.timezone_changed",
				details: map[string]any{"from": current.Timezone, "to": value},
			})
		}
	}

	if patch.WeekStartsOnSet {
		if patch.WeekStartsOn != nil && (*patch.WeekStartsOn < 0 || *patch.WeekStartsOn > 6) {
			return Business{}, ErrConflict
		}
		changed := (current.WeekStartsOn == nil) != (patch.WeekStartsOn == nil)
		if !changed && current.WeekStartsOn != nil && patch.WeekStartsOn != nil {
			changed = *current.WeekStartsOn != *patch.WeekStartsOn
		}
		if changed {
			nextWeek = patch.WeekStartsOn
			events = append(events, pendingAudit{
				action: "organization.week_start_changed",
				details: map[string]any{
					"from": current.WeekStartsOn,
					"to":   patch.WeekStartsOn,
				},
			})
		}
	}

	if len(events) == 0 {
		if err := tx.Commit(ctx); err != nil {
			return Business{}, err
		}
		return current, nil
	}

	_, err = tx.Exec(ctx, `
		UPDATE businesses
		   SET name = $1, kind = $2, timezone = $3, week_starts_on = $4, updated_at = now()
		 WHERE id = $5`,
		nextName, nextKind, nextTimezone, nextWeek, businessID,
	)
	if err != nil {
		return Business{}, err
	}

	for _, event := range events {
		if err := insertAuditTx(
			ctx, tx, businessID, actorID, event.action, "organization", businessID, event.details,
		); err != nil {
			return Business{}, err
		}
	}

	updated, err := getBusiness(ctx, tx, businessID)
	if err != nil {
		return Business{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Business{}, err
	}
	return updated, nil
}

func (s *Store) GetOrganizationForActor(
	ctx context.Context,
	actorID, businessID string,
) (BusinessAccess, error) {
	role, err := s.MembershipRole(ctx, actorID, businessID)
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrForbidden) {
		return BusinessAccess{}, ErrForbidden
	}
	if err != nil {
		return BusinessAccess{}, err
	}
	if !roleAllows(role, CapabilityMembersView) && !roleAllows(role, CapabilitySettingsView) {
		return BusinessAccess{}, ErrForbidden
	}
	business, err := s.GetBusiness(ctx, businessID)
	if err != nil {
		return BusinessAccess{}, err
	}
	return BusinessAccess{Business: business, Role: role}, nil
}

func (s *Store) CreateBusinessConfigured(
	ctx context.Context,
	ownerID, name, kind, timezone string,
	weekStartsOn *int,
) (Business, error) {
	name = strings.TrimSpace(name)
	kind = strings.ToLower(strings.TrimSpace(kind))
	timezone = strings.TrimSpace(timezone)
	if name == "" || len([]rune(name)) > 120 {
		return Business{}, ErrConflict
	}
	if kind == "" {
		kind = "team"
	}
	if !ValidBusinessKind(kind) {
		return Business{}, ErrConflict
	}
	if timezone == "" {
		timezone = "UTC"
	}
	if !validateTimezone(timezone) {
		return Business{}, ErrConflict
	}
	if weekStartsOn != nil && (*weekStartsOn < 0 || *weekStartsOn > 6) {
		return Business{}, ErrConflict
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Business{}, err
	}
	defer tx.Rollback(ctx)

	biz, err := createBusinessTx(ctx, tx, ownerID, name, kind)
	if err != nil {
		return Business{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE businesses
		   SET timezone = $1, week_starts_on = $2, updated_at = now()
		 WHERE id = $3`,
		timezone, weekStartsOn, biz.ID,
	); err != nil {
		return Business{}, err
	}
	if err := insertAuditTx(ctx, tx, biz.ID, ownerID, "organization.created", "organization", biz.ID, map[string]any{
		"name":     name,
		"kind":     kind,
		"timezone": timezone,
	}); err != nil {
		return Business{}, err
	}
	biz, err = getBusiness(ctx, tx, biz.ID)
	if err != nil {
		return Business{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Business{}, err
	}
	return biz, nil
}
