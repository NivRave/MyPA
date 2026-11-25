package graph

import (
	"testing"
	"time"

	"github.com/google/uuid"
	dbmodels "github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BL-38: logBabyCare loads the baby through the caller's household first. A baby ID of another
// household is refused, and no care log is written anywhere.
func TestLogBabyCare_BabyOfAnotherHouseholdIsRefused(t *testing.T) {
	resolver, _, cleanup := setupTestResolver(t)
	defer cleanup()
	famA := "fam-" + uuid.NewString()
	famB := "fam-" + uuid.NewString()
	households(t, resolver.DB, famA, famB)
	ctxA := signedIn(t, resolver, famA, "usr-"+uuid.NewString(), "member")
	ctxB := signedIn(t, resolver, famB, "usr-"+uuid.NewString(), "member")

	theirs, err := resolver.Mutation().UpsertBabyProfile(ctxB, "Their baby", time.Now().AddDate(0, -3, 0), "female", nil, nil, nil, nil)
	require.NoError(t, err)

	_, err = resolver.Mutation().LogBabyCare(ctxA, theirs.ID, "feeding", nil, nil, nil, nil)
	assert.Error(t, err, "a baby of another household is refused")
	_, err = resolver.Mutation().LogBabyCare(ctxA, "987654", "feeding", nil, nil, nil, nil)
	assert.Error(t, err, "a missing baby is refused")
	var n int64
	require.NoError(t, resolver.DB.DB.Model(&dbmodels.BabyCareLog{}).Count(&n).Error)
	assert.Zero(t, n, "nothing is written")

	// The household's own baby still works.
	mine, err := resolver.Mutation().UpsertBabyProfile(ctxA, "Our baby", time.Now().AddDate(0, -2, 0), "male", nil, nil, nil, nil)
	require.NoError(t, err)
	_, err = resolver.Mutation().LogBabyCare(ctxA, mine.ID, "feeding", nil, nil, nil, nil)
	require.NoError(t, err)
}

// BL-26: the dashboard's saveTopicMemory stores the scope as save_topic_memory does: "Family" is
// family, and another member of the household sees it; any other value is personal.
func TestSaveTopicMemoryMutation_ScopeIsNormalised(t *testing.T) {
	resolver, _, cleanup := setupTestResolver(t)
	defer cleanup()
	fam := "fam-" + uuid.NewString()
	households(t, resolver.DB, fam)
	dana := signedIn(t, resolver, fam, "usr-dana-"+uuid.NewString(), "member")
	omer := signedIn(t, resolver, fam, "usr-omer-"+uuid.NewString(), "member")

	family, other := " Family ", "Shared"
	saved, err := resolver.Mutation().SaveTopicMemory(dana, "house", "boiler", "The boiler is serviced every November.", nil, &family)
	require.NoError(t, err)
	assert.Equal(t, "family", saved.Scope)
	private, err := resolver.Mutation().SaveTopicMemory(dana, "health", "allergy", "Dana is allergic to peanuts.", nil, &other)
	require.NoError(t, err)
	assert.Equal(t, "personal", private.Scope)

	seen, err := resolver.Query().TopicMemories(omer, nil, nil)
	require.NoError(t, err)
	facts := map[string]bool{}
	for _, m := range seen {
		facts[m.Fact] = true
	}
	assert.True(t, facts["The boiler is serviced every November."], "another member sees the family memory")
	assert.False(t, facts["Dana is allergic to peanuts."], "a personal memory stays private")
}
