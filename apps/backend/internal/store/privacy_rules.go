package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type PrivacyRule struct {
	ID         string `json:"id"`
	BusinessID string `json:"business_id"`
	Kind       string `json:"kind"`
	MatchType  string `json:"match_type"`
	Pattern    string `json:"pattern"`
	Enabled    bool   `json:"enabled"`
}

func validatePrivacyRule(kind, matchType, pattern string) (string, string, string, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	matchType = strings.ToLower(strings.TrimSpace(matchType))
	pattern = strings.TrimSpace(pattern)
	if pattern == "" || len([]rune(pattern)) > 200 {
		return "", "", "", ErrConflict
	}
	switch kind {
	case "app":
		if matchType != "exact" && matchType != "contains" {
			return "", "", "", ErrConflict
		}
	case "window_title":
		if matchType != "contains" {
			return "", "", "", ErrConflict
		}
	default:
		return "", "", "", ErrConflict
	}
	return kind, matchType, pattern, nil
}

func (s *Store) privacyRulesForBusiness(ctx context.Context, businessID string) ([]PrivacyRule, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, business_id::text, kind, match_type, pattern, enabled
		  FROM privacy_rules
		 WHERE business_id = $1
		 ORDER BY kind, lower(pattern), created_at`,
		businessID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []PrivacyRule{}
	for rows.Next() {
		var rule PrivacyRule
		if err := rows.Scan(
			&rule.ID, &rule.BusinessID, &rule.Kind, &rule.MatchType,
			&rule.Pattern, &rule.Enabled,
		); err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	return out, rows.Err()
}

func (s *Store) ListPrivacyRules(
	ctx context.Context,
	actorID, businessID string,
) ([]PrivacyRule, error) {
	if err := s.BusinessPermissionOrForbidden(
		ctx, actorID, businessID, CapabilitySettingsView,
	); err != nil {
		return nil, err
	}
	return s.privacyRulesForBusiness(ctx, businessID)
}

func syncLegacySkipAppsTx(ctx context.Context, tx pgx.Tx, businessID string) error {
	_, err := tx.Exec(ctx, `
		UPDATE businesses b
		   SET screenshot_skip_apps = COALESCE((
		       SELECT array_agg(pattern ORDER BY lower(pattern))
		         FROM privacy_rules
		        WHERE business_id = b.id
		          AND enabled = true
		          AND kind = 'app'
		          AND match_type = 'exact'
		   ), ARRAY[]::text[]),
		       updated_at = now()
		 WHERE b.id = $1`,
		businessID,
	)
	return err
}

func lockMutableOrganizationTx(ctx context.Context, tx pgx.Tx, businessID string) error {
	var archivedAt, deletionScheduledAt *time.Time
	err := tx.QueryRow(ctx, `
		SELECT archived_at, deletion_scheduled_at
		  FROM businesses
		 WHERE id = $1
		 FOR UPDATE`,
		businessID,
	).Scan(&archivedAt, &deletionScheduledAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if deletionScheduledAt != nil {
		return ErrOrganizationDeletionPending
	}
	if archivedAt != nil {
		return ErrOrganizationArchived
	}
	return nil
}

func (s *Store) CreatePrivacyRule(
	ctx context.Context,
	actorID, businessID, kind, matchType, pattern string,
) (PrivacyRule, error) {
	kind, matchType, pattern, err := validatePrivacyRule(kind, matchType, pattern)
	if err != nil {
		return PrivacyRule{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PrivacyRule{}, err
	}
	defer tx.Rollback(ctx)

	if _, err := requireBusinessPermissionTx(
		ctx, tx, actorID, businessID, CapabilitySettingsManage,
	); err != nil {
		return PrivacyRule{}, err
	}
	if err := lockMutableOrganizationTx(ctx, tx, businessID); err != nil {
		return PrivacyRule{}, err
	}

	id := uuid.NewString()
	var rule PrivacyRule
	err = tx.QueryRow(ctx, `
		INSERT INTO privacy_rules (id, business_id, kind, match_type, pattern)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id::text, business_id::text, kind, match_type, pattern, enabled`,
		id, businessID, kind, matchType, pattern,
	).Scan(
		&rule.ID, &rule.BusinessID, &rule.Kind, &rule.MatchType,
		&rule.Pattern, &rule.Enabled,
	)
	if err != nil {
		return PrivacyRule{}, err
	}
	if err := syncLegacySkipAppsTx(ctx, tx, businessID); err != nil {
		return PrivacyRule{}, err
	}
	if err := insertAuditTx(ctx, tx, businessID, actorID, "settings.privacy_rule_created", "privacy_rule", id, map[string]any{
		"kind": kind, "match_type": matchType, "pattern": pattern,
	}); err != nil {
		return PrivacyRule{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PrivacyRule{}, err
	}
	return rule, nil
}

func (s *Store) UpdatePrivacyRule(
	ctx context.Context,
	actorID, businessID, ruleID string,
	kind, matchType, pattern *string,
	enabled *bool,
) (PrivacyRule, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PrivacyRule{}, err
	}
	defer tx.Rollback(ctx)

	if _, err := requireBusinessPermissionTx(
		ctx, tx, actorID, businessID, CapabilitySettingsManage,
	); err != nil {
		return PrivacyRule{}, err
	}
	if err := lockMutableOrganizationTx(ctx, tx, businessID); err != nil {
		return PrivacyRule{}, err
	}

	var current PrivacyRule
	err = tx.QueryRow(ctx, `
		SELECT id::text, business_id::text, kind, match_type, pattern, enabled
		  FROM privacy_rules
		 WHERE id = $1 AND business_id = $2
		 FOR UPDATE`,
		ruleID, businessID,
	).Scan(
		&current.ID, &current.BusinessID, &current.Kind, &current.MatchType,
		&current.Pattern, &current.Enabled,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return PrivacyRule{}, ErrNotFound
	}
	if err != nil {
		return PrivacyRule{}, err
	}

	nextKind := current.Kind
	nextMatch := current.MatchType
	nextPattern := current.Pattern
	nextEnabled := current.Enabled
	if kind != nil {
		nextKind = *kind
	}
	if matchType != nil {
		nextMatch = *matchType
	}
	if pattern != nil {
		nextPattern = *pattern
	}
	if enabled != nil {
		nextEnabled = *enabled
	}
	nextKind, nextMatch, nextPattern, err = validatePrivacyRule(
		nextKind, nextMatch, nextPattern,
	)
	if err != nil {
		return PrivacyRule{}, err
	}

	var updated PrivacyRule
	err = tx.QueryRow(ctx, `
		UPDATE privacy_rules
		   SET kind = $1, match_type = $2, pattern = $3, enabled = $4, updated_at = now()
		 WHERE id = $5 AND business_id = $6
		RETURNING id::text, business_id::text, kind, match_type, pattern, enabled`,
		nextKind, nextMatch, nextPattern, nextEnabled, ruleID, businessID,
	).Scan(
		&updated.ID, &updated.BusinessID, &updated.Kind, &updated.MatchType,
		&updated.Pattern, &updated.Enabled,
	)
	if err != nil {
		return PrivacyRule{}, err
	}
	if err := syncLegacySkipAppsTx(ctx, tx, businessID); err != nil {
		return PrivacyRule{}, err
	}
	if err := insertAuditTx(ctx, tx, businessID, actorID, "settings.privacy_rule_changed", "privacy_rule", ruleID, map[string]any{
		"kind": nextKind, "match_type": nextMatch, "pattern": nextPattern, "enabled": nextEnabled,
	}); err != nil {
		return PrivacyRule{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PrivacyRule{}, err
	}
	return updated, nil
}

func (s *Store) DeletePrivacyRule(
	ctx context.Context,
	actorID, businessID, ruleID string,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := requireBusinessPermissionTx(
		ctx, tx, actorID, businessID, CapabilitySettingsManage,
	); err != nil {
		return err
	}
	if err := lockMutableOrganizationTx(ctx, tx, businessID); err != nil {
		return err
	}

	var kind, matchType, pattern string
	err = tx.QueryRow(ctx, `
		DELETE FROM privacy_rules
		 WHERE id = $1 AND business_id = $2
		RETURNING kind, match_type, pattern`,
		ruleID, businessID,
	).Scan(&kind, &matchType, &pattern)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if err := syncLegacySkipAppsTx(ctx, tx, businessID); err != nil {
		return err
	}
	if err := insertAuditTx(ctx, tx, businessID, actorID, "settings.privacy_rule_deleted", "privacy_rule", ruleID, map[string]any{
		"kind": kind, "match_type": matchType, "pattern": pattern,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
