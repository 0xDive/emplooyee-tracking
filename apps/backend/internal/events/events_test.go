package events

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestBusPublishesTypedEventsToAllSubscribers(t *testing.T) {
	bus := NewBus()

	var mu sync.Mutex
	received := []Event{}
	for i := 0; i < 2; i++ {
		bus.Subscribe(SinkFunc(func(_ context.Context, event Event) {
			mu.Lock()
			received = append(received, event)
			mu.Unlock()
		}))
	}

	bus.Publish(context.Background(), Event{
		Type: OrganizationDeletionPending,
		OrganizationID: "org-1",
		ActorUserID: "user-1",
		Details: map[string]any{"reason": "test"},
	})

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 2 {
		t.Fatalf("received %d events, want 2", len(received))
	}
	for _, event := range received {
		if event.Type != OrganizationDeletionPending ||
			event.OrganizationID != "org-1" ||
			event.ActorUserID != "user-1" {
			t.Fatalf("unexpected event: %+v", event)
		}
		if event.OccurredAt.IsZero() {
			t.Fatal("bus did not stamp occurred_at")
		}
		if time.Since(event.OccurredAt) > time.Minute {
			t.Fatalf("unexpected occurred_at: %v", event.OccurredAt)
		}
	}
}

func TestDiscardPublisherIsSafe(t *testing.T) {
	Discard{}.Publish(context.Background(), Event{Type: MFAEnabled})
}
