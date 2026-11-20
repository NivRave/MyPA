package workspace_gmail

import (
	"context"
	"strings"
	"testing"

	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/gmail"
	"github.com/nivik/mypa/internal/identity/identitytest"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
)

type mockGmailClient struct {
	searchedUser  string
	searchedQuery string
	readMessageID string
	draftedReply  string
	archivedIDs   []string
	deletedIDs    []string
	createdLabel  string
	appliedLabel  string
	unsubMessage  string
}

func (m *mockGmailClient) SearchEmails(ctx context.Context, userID string, query string, maxResults int64) ([]gmail.EmailSummary, error) {
	m.searchedUser = userID
	m.searchedQuery = query
	return []gmail.EmailSummary{
		{ID: "msg_1", From: "boss@example.com", Subject: "Important Update", Snippet: "Please read"},
	}, nil
}

func (m *mockGmailClient) ReadEmail(ctx context.Context, userID string, messageID string) (string, error) {
	m.readMessageID = messageID
	return "Hello, this is the email body content.", nil
}

func (m *mockGmailClient) DraftReply(ctx context.Context, userID string, messageID string, replyText string) error {
	m.draftedReply = replyText
	return nil
}

func (m *mockGmailClient) ArchiveEmail(ctx context.Context, userID string, messageID string) error {
	m.archivedIDs = append(m.archivedIDs, messageID)
	return nil
}

func (m *mockGmailClient) SoftDeleteEmail(ctx context.Context, userID string, messageID string) error {
	m.deletedIDs = append(m.deletedIDs, messageID)
	return nil
}

func (m *mockGmailClient) ListLabels(ctx context.Context, userID string) (map[string]string, error) {
	return map[string]string{"Work": "label_123"}, nil
}

func (m *mockGmailClient) ApplyLabel(ctx context.Context, userID, messageID, labelID string) error {
	m.appliedLabel = labelID
	return nil
}

func (m *mockGmailClient) CreateLabel(ctx context.Context, userID, labelName string) (string, error) {
	m.createdLabel = labelName
	return "label_new_id", nil
}

func (m *mockGmailClient) UnsubscribeEmail(ctx context.Context, userID, messageID string) error {
	m.unsubMessage = messageID
	return nil
}

func TestWorkspaceGmail_Operations(t *testing.T) {
	mockClient := &mockGmailClient{}
	bus := events.NewEventBus()
	receivedEvents := make(chan events.MutationEvent, 10)
	bus.SubscribeTopic("communication", func(ctx context.Context, ev events.MutationEvent) error {
		receivedEvents <- ev
		return nil
	})

	mod := NewModule(WithClient(mockClient), WithEventBus(bus))
	if mod.Name() != "workspace_gmail" {
		t.Errorf("expected name workspace_gmail, got %s", mod.Name())
	}
	if len(mod.Declarations()) != 9 {
		t.Errorf("expected 9 declarations, got %d", len(mod.Declarations()))
	}

	callCtx := orchestrator.CallContext{
		User: &models.User{
			PlatformID:  "tg_456",
			FamilyGroup: "family_alpha",
		},
		EventBus: bus,
		Scope:    identitytest.Scope(t, "family_alpha", "tg_456", "family"), // V4-30: the tenant
	}

	// 1. Search Emails
	res, err := mod.Execute(context.Background(), callCtx, "search_emails", map[string]interface{}{
		"query": "is:unread",
	})
	if err != nil || !strings.Contains(res, "Important Update") {
		t.Fatalf("search_emails failed: res=%s, err=%v", res, err)
	}

	// 2. Read Email (checks untrusted wrapping)
	res, err = mod.Execute(context.Background(), callCtx, "read_email", map[string]interface{}{
		"message_id": "msg_1",
	})
	if err != nil || !strings.Contains(res, "<untrusted_external_content") {
		t.Fatalf("read_email untrusted wrapping missing: res=%s, err=%v", res, err)
	}

	// 3. Draft Reply
	res, err = mod.Execute(context.Background(), callCtx, "draft_email_reply", map[string]interface{}{
		"message_id": "msg_1",
		"reply_text": "Sounds good!",
	})
	if err != nil || !strings.Contains(res, "Draft created successfully") {
		t.Fatalf("draft_email_reply failed: res=%s, err=%v", res, err)
	}

	// 4. Archive Emails
	res, err = mod.Execute(context.Background(), callCtx, "archive_emails", map[string]interface{}{
		"message_ids": []string{"msg_1", "msg_2"},
	})
	if err != nil || !strings.Contains(res, "2 emails archived") {
		t.Fatalf("archive_emails failed: res=%s, err=%v", res, err)
	}

	// 5. Create Label
	res, err = mod.Execute(context.Background(), callCtx, "create_email_label", map[string]interface{}{
		"label_name": "Invoices",
	})
	if err != nil || !strings.Contains(res, "label_new_id") {
		t.Fatalf("create_email_label failed: res=%s, err=%v", res, err)
	}

	// 6. Unsubscribe
	res, err = mod.Execute(context.Background(), callCtx, "unsubscribe_email", map[string]interface{}{
		"message_id": "msg_1",
	})
	if err != nil || !strings.Contains(res, "Successfully unsubscribed") {
		t.Fatalf("unsubscribe_email failed: res=%s, err=%v", res, err)
	}

	// Drain and verify events
	if len(receivedEvents) == 0 {
		t.Error("expected mutation events emitted on communication topic")
	}
}
