package orchestrator_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/testutil"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type spyTelegramClient struct {
	mu           sync.Mutex
	sentMessages []string
}

func (s *spyTelegramClient) SendMessage(ctx context.Context, chatID string, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sentMessages = append(s.sentMessages, fmt.Sprintf("%s:%s", chatID, text))
	return nil
}

func (s *spyTelegramClient) DownloadFile(ctx context.Context, filePath string) ([]byte, error) {
	return nil, nil
}

func (s *spyTelegramClient) GetFile(ctx context.Context, fileID string) (string, error) {
	return "", nil
}

func newTestDBClient(t *testing.T) *db.Client {
	t.Helper()
	client, cleanup := testutil.NewTestDB(t)
	t.Cleanup(cleanup)
	return client
}

// TestAdversarial_SharedTaskRemindersDelivery tests BUG-04:
// - Past DueTime with IsSent=false -> dispatched and IsSent=true
// - Future DueTime -> not dispatched
// - Completed task -> not dispatched
// - Already sent -> not dispatched
// - Empty AssignedToUserID -> falls back to CreatedByUserID
func TestAdversarial_SharedTaskRemindersDelivery(t *testing.T) {
	client := newTestDBClient(t)

	testDad := "adv_dad_user"
	testMom := "adv_mom_user"
	famGroup := "adv_shared_family"

	// Cleanup
	defer func() {
		client.DB.Where("family_group = ?", famGroup).Delete(&models.SharedTaskReminder{})
		client.DB.Where("user_id IN ?", []string{testDad, testMom}).Delete(&models.AuditSession{})
	}()
	client.DB.Where("family_group = ?", famGroup).Delete(&models.SharedTaskReminder{})
	client.DB.Where("user_id IN ?", []string{testDad, testMom}).Delete(&models.AuditSession{})

	// Insert audit sessions so Engine knows chat ID and source for dispatch
	dadSession := models.AuditSession{
		ID:        "00000000-0000-0000-0000-000000000001",
		UserID:    testDad,
		ChatID:    "chat-dad-123",
		Source:    "telegram",
		StartTime: time.Now(),
		Status:    "success",
	}
	require.NoError(t, client.DB.Create(&dadSession).Error)

	momSession := models.AuditSession{
		ID:        "00000000-0000-0000-0000-000000000002",
		UserID:    testMom,
		ChatID:    "chat-mom-456",
		Source:    "telegram",
		StartTime: time.Now(),
		Status:    "success",
	}
	require.NoError(t, client.DB.Create(&momSession).Error)

	spyTg := &spyTelegramClient{}
	engine := orchestrator.NewEngine(
		nil, nil, client, nil, spyTg, nil, nil, nil, nil, nil, nil, nil, nil, "UTC",
	)

	// Scenario 1: Past Due Reminder (Due 15 minutes ago, IsSent=false) -> MUST BE DISPATCHED
	pastDue := time.Now().Add(-15 * time.Minute)
	remDue := models.SharedTaskReminder{
		FamilyGroup:      famGroup,
		Topic:            "baby",
		Title:            "Buy Baby Vitamin D drops",
		DueTime:          &pastDue,
		CreatedByUserID:  testMom,
		AssignedToUserID: testDad,
		IsCompleted:      false,
		IsSent:           false,
		CreatedAt:        time.Now().Add(-1 * time.Hour),
	}
	require.NoError(t, client.CreateSharedReminder(&remDue))

	// Scenario 2: Future Due Reminder (Due in 2 hours) -> MUST NOT BE DISPATCHED
	futureDue := time.Now().Add(2 * time.Hour)
	remFuture := models.SharedTaskReminder{
		FamilyGroup:      famGroup,
		Topic:            "house",
		Title:            "Change AC filter",
		DueTime:          &futureDue,
		CreatedByUserID:  testMom,
		AssignedToUserID: testDad,
		IsCompleted:      false,
		IsSent:           false,
		CreatedAt:        time.Now(),
	}
	require.NoError(t, client.CreateSharedReminder(&remFuture))

	// Scenario 3: Completed Reminder (Due in past, but IsCompleted=true) -> MUST NOT BE DISPATCHED
	completedPast := time.Now().Add(-30 * time.Minute)
	remCompleted := models.SharedTaskReminder{
		FamilyGroup:       famGroup,
		Topic:             "finance",
		Title:             "Pay Electric Bill",
		DueTime:           &completedPast,
		CreatedByUserID:   testMom,
		AssignedToUserID:  testDad,
		IsCompleted:       true,
		CompletedByUserID: testDad,
		IsSent:            false,
		CreatedAt:         time.Now().Add(-2 * time.Hour),
	}
	require.NoError(t, client.CreateSharedReminder(&remCompleted))

	// Scenario 4: Already Sent Reminder (Due in past, but IsSent=true) -> MUST NOT BE DISPATCHED AGAIN
	remAlreadySent := models.SharedTaskReminder{
		FamilyGroup:      famGroup,
		Topic:            "baby",
		Title:            "Call Pediatrician",
		DueTime:          &pastDue,
		CreatedByUserID:  testMom,
		AssignedToUserID: testDad,
		IsCompleted:      false,
		IsSent:           true,
		CreatedAt:        time.Now().Add(-3 * time.Hour),
	}
	require.NoError(t, client.CreateSharedReminder(&remAlreadySent))

	// Scenario 5: Fallback to CreatedByUserID when AssignedToUserID is empty
	remFallback := models.SharedTaskReminder{
		FamilyGroup:      famGroup,
		Topic:            "baby",
		Title:            "Prep formula bottles",
		DueTime:          &pastDue,
		CreatedByUserID:  testMom,
		AssignedToUserID: "", // empty -> route to testMom
		IsCompleted:      false,
		IsSent:           false,
		CreatedAt:        time.Now().Add(-20 * time.Minute),
	}
	require.NoError(t, client.CreateSharedReminder(&remFallback))

	// Trigger CheckAndSendReminders
	engine.CheckAndSendReminders()

	// Verify dispatched messages
	spyTg.mu.Lock()
	messages := make([]string, len(spyTg.sentMessages))
	copy(messages, spyTg.sentMessages)
	spyTg.mu.Unlock()

	// We expect exactly 2 dispatched messages: remDue (to dad) and remFallback (to mom)
	require.Len(t, messages, 2, "Expected exactly 2 reminders dispatched")

	// Message 1 to dad
	assert.Contains(t, messages[0], "chat-dad-123", "remDue must be sent to assigned user chat")
	assert.Contains(t, messages[0], "Buy Baby Vitamin D drops")
	assert.Contains(t, messages[0], "Shared Task Reminder")

	// Message 2 to mom (fallback)
	assert.Contains(t, messages[1], "chat-mom-456", "remFallback must fall back to createdBy user chat")
	assert.Contains(t, messages[1], "Prep formula bottles")

	// Verify database state for remDue
	var refreshedDue models.SharedTaskReminder
	err := client.DB.Where("id = ?", remDue.ID).First(&refreshedDue).Error
	require.NoError(t, err)
	assert.True(t, refreshedDue.IsSent, "remDue.IsSent must be true in DB")
	assert.NotNil(t, refreshedDue.RemindedAt, "remDue.RemindedAt must not be nil")

	// Verify database state for remFallback
	var refreshedFallback models.SharedTaskReminder
	err = client.DB.Where("id = ?", remFallback.ID).First(&refreshedFallback).Error
	require.NoError(t, err)
	assert.True(t, refreshedFallback.IsSent, "remFallback.IsSent must be true in DB")
	assert.NotNil(t, refreshedFallback.RemindedAt, "remFallback.RemindedAt must not be nil")

	// Verify database state for remFuture (remains unsent)
	var refreshedFuture models.SharedTaskReminder
	err = client.DB.Where("id = ?", remFuture.ID).First(&refreshedFuture).Error
	require.NoError(t, err)
	assert.False(t, refreshedFuture.IsSent, "remFuture.IsSent must remain false")

	// Verify database state for remCompleted (remains unsent)
	var refreshedCompleted models.SharedTaskReminder
	err = client.DB.Where("id = ?", remCompleted.ID).First(&refreshedCompleted).Error
	require.NoError(t, err)
	assert.False(t, refreshedCompleted.IsSent, "remCompleted.IsSent must remain false")

	// Verify subsequent invocation sends NOTHING (idempotency check)
	spyTg.mu.Lock()
	spyTg.sentMessages = nil
	spyTg.mu.Unlock()

	engine.CheckAndSendReminders()

	spyTg.mu.Lock()
	countSecondRun := len(spyTg.sentMessages)
	spyTg.mu.Unlock()
	assert.Equal(t, 0, countSecondRun, "Subsequent check must not re-dispatch already sent reminders")
}
