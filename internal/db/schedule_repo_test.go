package db

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScheduleRepo_Events_CRUDAndIsolation(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewScheduleRepository(client.DB)

	userA := "usr-" + uuid.New().String()
	famA := "fam-" + uuid.New().String()

	userB := "usr-" + uuid.New().String()
	famB := "fam-" + uuid.New().String()

	startTime := time.Now().Add(24 * time.Hour).Truncate(time.Second)
	endTime := startTime.Add(1 * time.Hour)

	// 1. Create event for Tenant A
	evA := models.ScheduleEvent{
		UserID:          userA,
		FamilyGroup:     famA,
		Topic:           "baby",
		Title:           "Pediatrician Visit",
		Description:     "6-month wellness checkup",
		StartTime:       startTime,
		EndTime:         endTime,
		IsShared:        true,
		SyncToGoogle:    true,
		CreatedByUserID: userA,
	}
	require.NoError(t, repo.CreateScheduleEvent(&evA))
	require.NotZero(t, evA.ID)

	// 2. Create event for Tenant B
	evB := models.ScheduleEvent{
		UserID:          userB,
		FamilyGroup:     famB,
		Topic:           "finance",
		Title:           "Budget Review",
		StartTime:       startTime,
		EndTime:         endTime,
		IsShared:        true,
		SyncToGoogle:    false,
		CreatedByUserID: userB,
	}
	require.NoError(t, repo.CreateScheduleEvent(&evB))
	require.NotZero(t, evB.ID)

	// 3. Positive read and tenant isolation
	gotA, err := repo.GetScheduleEvent(evA.ID, famA)
	require.NoError(t, err)
	require.NotNil(t, gotA)
	assert.Equal(t, "Pediatrician Visit", gotA.Title)
	assert.Equal(t, "baby", gotA.Topic)

	// Tenant B querying Tenant A's event returns nil
	gotLeak, err := repo.GetScheduleEvent(evA.ID, famB)
	require.NoError(t, err)
	assert.Nil(t, gotLeak, "Tenant B must not access Tenant A's event")

	// 4. ListScheduleEvents with isolation
	eventsA, err := repo.ListScheduleEvents(userA, famA, "all", startTime.Add(-1*time.Hour), endTime.Add(1*time.Hour))
	require.NoError(t, err)
	require.Len(t, eventsA, 1)
	assert.Equal(t, evA.ID, eventsA[0].ID)

	eventsB, err := repo.ListScheduleEvents(userB, famB, "all", startTime.Add(-1*time.Hour), endTime.Add(1*time.Hour))
	require.NoError(t, err)
	require.Len(t, eventsB, 1)
	assert.Equal(t, evB.ID, eventsB[0].ID)

	// Filter Tenant A by topic "finance" -> empty
	eventsFinanceA, err := repo.ListScheduleEvents(userA, famA, "finance", startTime.Add(-1*time.Hour), endTime.Add(1*time.Hour))
	require.NoError(t, err)
	assert.Empty(t, eventsFinanceA)

	// 5. Update event
	evA.Title = "Pediatrician 6mo Checkup"
	evA.Description = "Bring vaccination record"
	require.NoError(t, repo.UpdateScheduleEvent(&evA))

	recheckA, err := repo.GetScheduleEvent(evA.ID, famA)
	require.NoError(t, err)
	require.NotNil(t, recheckA)
	assert.Equal(t, "Pediatrician 6mo Checkup", recheckA.Title)
	assert.Equal(t, "Bring vaccination record", recheckA.Description)

	// 6. Delete event: attempt deletion with wrong tenant scope should not delete
	err = repo.DeleteScheduleEvent(evA.ID, famB)
	require.NoError(t, err)

	recheckStillThere, err := repo.GetScheduleEvent(evA.ID, famA)
	require.NoError(t, err)
	require.NotNil(t, recheckStillThere, "Event should still exist because deletion was attempted with wrong tenant")

	// Delete with correct tenant scope
	err = repo.DeleteScheduleEvent(evA.ID, famA)
	require.NoError(t, err)

	recheckDeleted, err := repo.GetScheduleEvent(evA.ID, famA)
	require.NoError(t, err)
	assert.Nil(t, recheckDeleted, "Event should be deleted")
}

func TestScheduleRepo_TopicCalendarConfig(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewScheduleRepository(client.DB)

	userA := "usr-" + uuid.New().String()
	userB := "usr-" + uuid.New().String()

	syncTrue := true
	syncFalse := false

	// 1. Create configs for User A
	cfgA1 := models.TopicCalendarConfig{
		UserID:                   userA,
		Topic:                    "baby",
		ColorID:                  "1",
		PrefixTag:                "BabyCare",
		TargetCalendarID:         "cal-baby@google.com",
		IncludeInMorningBriefing: true,
		SyncToGoogleDefault:      &syncTrue,
	}
	require.NoError(t, repo.UpsertTopicCalendarConfig(&cfgA1))
	require.NotZero(t, cfgA1.ID)

	cfgA2 := models.TopicCalendarConfig{
		UserID:                   userA,
		Topic:                    "house",
		ColorID:                  "2",
		PrefixTag:                "Home",
		TargetCalendarID:         "cal-home@google.com",
		IncludeInMorningBriefing: false,
		SyncToGoogleDefault:      &syncFalse,
	}
	require.NoError(t, repo.UpsertTopicCalendarConfig(&cfgA2))
	require.NotZero(t, cfgA2.ID)

	// 2. Create config for User B
	cfgB := models.TopicCalendarConfig{
		UserID:                   userB,
		Topic:                    "baby",
		ColorID:                  "9",
		PrefixTag:                "Nursery",
		TargetCalendarID:         "cal-nursery@google.com",
		IncludeInMorningBriefing: true,
		SyncToGoogleDefault:      &syncTrue,
	}
	require.NoError(t, repo.UpsertTopicCalendarConfig(&cfgB))
	require.NotZero(t, cfgB.ID)

	// 3. Positive lookup and user isolation
	gotA1, err := repo.GetTopicCalendarConfig(userA, "baby")
	require.NoError(t, err)
	require.NotNil(t, gotA1)
	assert.Equal(t, "BabyCare", gotA1.PrefixTag)

	gotB, err := repo.GetTopicCalendarConfig(userB, "baby")
	require.NoError(t, err)
	require.NotNil(t, gotB)
	assert.Equal(t, "Nursery", gotB.PrefixTag)

	// Non-existent topic returns (nil, nil)
	gotNone, err := repo.GetTopicCalendarConfig(userA, "nonexistent")
	require.NoError(t, err)
	assert.Nil(t, gotNone)

	// 4. ListTopicCalendarConfigs
	configsA, err := repo.ListTopicCalendarConfigs(userA)
	require.NoError(t, err)
	require.Len(t, configsA, 2)

	configsB, err := repo.ListTopicCalendarConfigs(userB)
	require.NoError(t, err)
	require.Len(t, configsB, 1)
	assert.Equal(t, "baby", configsB[0].Topic)

	// 5. Update existing config in-place
	cfgA1Updated := models.TopicCalendarConfig{
		UserID:                   userA,
		Topic:                    "baby",
		ColorID:                  "3",
		PrefixTag:                "BabyRoutine",
		TargetCalendarID:         "cal-baby-updated@google.com",
		IncludeInMorningBriefing: false,
	}
	require.NoError(t, repo.UpsertTopicCalendarConfig(&cfgA1Updated))
	assert.Equal(t, cfgA1.ID, cfgA1Updated.ID, "ID should be preserved on upsert")

	recheckA1, err := repo.GetTopicCalendarConfig(userA, "baby")
	require.NoError(t, err)
	require.NotNil(t, recheckA1)
	assert.Equal(t, "BabyRoutine", recheckA1.PrefixTag)
	assert.Equal(t, "3", recheckA1.ColorID)
}
