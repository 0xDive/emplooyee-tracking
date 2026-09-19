package events

import (
	"context"
	"sync"
	"time"
)

// Type is a stable internal domain-event identifier. Delivery adapters should
// depend on these identifiers rather than on HTTP routes or audit-log wording.
type Type string

const (
	DeviceOutdated                 Type = "device.outdated"
	OrganizationArchived           Type = "organization.archived"
	OrganizationRestored           Type = "organization.restored"
	OrganizationDeletionPending    Type = "organization.deletion_pending"
	OrganizationDeletionCancelled  Type = "organization.deletion_cancelled"
	OwnershipTransferred           Type = "organization.ownership_transferred"
	MFAEnabled                     Type = "security.mfa_enabled"
	MFADisabled                    Type = "security.mfa_disabled"
	MFARecoveryCodesRegenerated    Type = "security.mfa_recovery_regenerated"
	MFAReset                       Type = "security.mfa_reset"
	PasswordChanged                Type = "security.password_changed"
	SessionsRevoked                Type = "security.sessions_revoked"
)

// Event is deliberately transport-neutral. Details must contain only values that
// are safe for an internal notification adapter to inspect; secrets and raw MFA
// recovery codes must never be placed here.
type Event struct {
	Type           Type           `json:"type"`
	OrganizationID string         `json:"organization_id,omitempty"`
	UserID         string         `json:"user_id,omitempty"`
	ActorUserID    string         `json:"actor_user_id,omitempty"`
	TargetUserID   string         `json:"target_user_id,omitempty"`
	OccurredAt     time.Time      `json:"occurred_at"`
	Details        map[string]any `json:"details,omitempty"`
}

// Publisher is the dependency domain handlers need. Publishing is best-effort:
// notification delivery must never make an already-committed domain mutation fail.
type Publisher interface {
	Publish(context.Context, Event)
}

// Sink is implemented by future notification delivery adapters.
type Sink interface {
	Handle(context.Context, Event)
}

type SinkFunc func(context.Context, Event)

func (f SinkFunc) Handle(ctx context.Context, event Event) {
	f(ctx, event)
}

// Bus is an in-process fan-out bus. It is intentionally small; durable delivery can
// later subscribe through an outbox-backed Sink without changing publishers.
type Bus struct {
	mu    sync.RWMutex
	sinks []Sink
}

func NewBus() *Bus {
	return &Bus{}
}

func (b *Bus) Subscribe(sink Sink) {
	if sink == nil {
		return
	}
	b.mu.Lock()
	b.sinks = append(b.sinks, sink)
	b.mu.Unlock()
}

func (b *Bus) Publish(ctx context.Context, event Event) {
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	b.mu.RLock()
	sinks := append([]Sink(nil), b.sinks...)
	b.mu.RUnlock()
	for _, sink := range sinks {
		sink.Handle(ctx, event)
	}
}

// Discard is the default publisher for handlers constructed in tests or embedded
// contexts that do not wire a notification bus.
type Discard struct{}

func (Discard) Publish(context.Context, Event) {}
