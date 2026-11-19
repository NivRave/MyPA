package workspace_contacts

import (
	"context"
	"strings"
	"testing"

	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/identity/identitytest"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
)

type mockContactsClient struct {
	searchedUser  string
	searchedQuery string
	createdName   string
}

func (m *mockContactsClient) SearchContacts(ctx context.Context, userID, query string) ([]models.Contact, error) {
	m.searchedUser = userID
	m.searchedQuery = query
	return []models.Contact{
		{Name: "Alice Smith", Email: "alice@example.com", Phone: "123-456-7890"},
	}, nil
}

func (m *mockContactsClient) CreateContact(ctx context.Context, userID, name, email, phone string) error {
	m.createdName = name
	return nil
}

func TestWorkspaceContacts_Operations(t *testing.T) {
	mockClient := &mockContactsClient{}
	bus := events.NewEventBus()
	receivedEvents := make(chan events.MutationEvent, 5)
	bus.SubscribeTopic("contacts", func(ctx context.Context, ev events.MutationEvent) error {
		receivedEvents <- ev
		return nil
	})

	mod := NewModule(WithClient(mockClient), WithEventBus(bus))
	if mod.Name() != "workspace_contacts" {
		t.Errorf("expected name workspace_contacts, got %s", mod.Name())
	}
	if len(mod.Declarations()) != 2 {
		t.Errorf("expected 2 declarations, got %d", len(mod.Declarations()))
	}

	callCtx := orchestrator.CallContext{
		User: &models.User{
			PlatformID:  "tg_contact",
			FamilyGroup: "family_contacts",
		},
		EventBus: bus,
		Scope:    identitytest.Scope(t, "family_contacts", "tg_contact", "family"), // V4-30: the tenant
	}

	// 1. Search Contacts
	res, err := mod.Execute(context.Background(), callCtx, "search_contacts", map[string]interface{}{
		"query": "Alice",
	})
	if err != nil || !strings.Contains(res, "Alice Smith") {
		t.Fatalf("search_contacts failed: res=%s, err=%v", res, err)
	}

	// 2. Create Contact
	res, err = mod.Execute(context.Background(), callCtx, "create_contact", map[string]interface{}{
		"name":  "Bob Builder",
		"email": "bob@example.com",
	})
	if err != nil || !strings.Contains(res, "Contact created successfully") {
		t.Fatalf("create_contact failed: res=%s, err=%v", res, err)
	}
	if mockClient.createdName != "Bob Builder" {
		t.Errorf("expected Bob Builder, got %s", mockClient.createdName)
	}

	select {
	case ev := <-receivedEvents:
		if ev.Topic != "contacts" || ev.Action != "create" || ev.ActorUserID != "tg_contact" {
			t.Errorf("unexpected event: %+v", ev)
		}
	default:
		t.Error("expected mutation event on create_contact")
	}
}
