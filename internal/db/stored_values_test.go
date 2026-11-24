package db

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm/schema"
)

// BL-43: GORM writes a field's tag default in place of its Go zero value on insert, so a model
// field whose default differs from its zero value cannot store the zero value. Where the zero
// value is a choice a person makes (false for "not shared", "inactive", "no view"), the field
// must carry no default. Fields listed here have a zero value nobody chooses: it means "not
// given", and the default is what it stands for.
var defaultMeansNotGiven = map[string]string{
	"SharedSpace.NotifyMode": "an empty notify mode is not a mode; 'instant' is what it means",
	"TopicMemory.Scope":      "an empty scope is not a scope; 'personal' is what it means",
}

func TestModels_NoDefaultReplacesAChosenZeroValue(t *testing.T) {
	RegisterWave1Models()
	RegisterWave2Models()
	cache := &sync.Map{}
	var offending []string
	for _, m := range schemaModels() {
		s, err := schema.Parse(m, cache, schema.NamingStrategy{})
		require.NoError(t, err)
		for _, f := range s.Fields {
			if !f.HasDefaultValue || f.PrimaryKey || f.DBName == "" {
				continue
			}
			if f.FieldType.Kind() == reflect.Pointer {
				continue // nil is "not given"; a pointer to the zero value is written as asked
			}
			name := s.Name + "." + f.Name
			if _, ok := defaultMeansNotGiven[name]; ok {
				continue
			}
			zero := fmt.Sprint(reflect.Zero(f.IndirectFieldType).Interface())
			if f.DefaultValueInterface == nil || fmt.Sprint(f.DefaultValueInterface) != zero {
				offending = append(offending, fmt.Sprintf("%s (default %q, zero %q)", name, f.DefaultValue, zero))
			}
		}
	}
	assert.Empty(t, offending, "a default that differs from the zero value stores the default when the zero value is asked for")
}

// The stored value is the one asked for, on every repository write of every field that had a
// non-zero default (BL-43, BL-62): a create with false, and an update from true to false and
// back. It holds on the schema the tests build from the models and on the one production
// builds with the migrations.
func TestStoredValues_AFalseIsStoredAsAsked(t *testing.T) {
	schemas := []struct {
		name string
		open func(t *testing.T) *Client
	}{
		{"models", func(t *testing.T) *Client {
			c, cleanup := setupTestDB(t)
			t.Cleanup(cleanup)
			return c
		}},
		{"migrations", func(t *testing.T) *Client {
			c, err := Open(newEmptyTestDatabase(t))
			require.NoError(t, err)
			t.Cleanup(func() { _ = c.Close() })
			sqlDB, err := c.DB.DB()
			require.NoError(t, err)
			require.NoError(t, Migrate(context.Background(), sqlDB))
			return c
		}},
	}
	for _, sc := range schemas {
		t.Run(sc.name, func(t *testing.T) { storedValuesRoundTrip(t, sc.open(t)) })
	}
}

// stored reads one column of one row as the database holds it: "true", "false" or "NULL".
func stored(t *testing.T, c *Client, table, column string, id uint) string {
	t.Helper()
	var v sql.NullBool
	require.NoError(t, c.DB.Raw(fmt.Sprintf("SELECT %s FROM %s WHERE id = ?", column, table), id).Row().Scan(&v))
	if !v.Valid {
		return "NULL"
	}
	return fmt.Sprint(v.Bool)
}

func storedValuesRoundTrip(t *testing.T, c *Client) {
	ctx := context.Background()
	label := "bl43-" + uuid.NewString()
	s := scopeAs(t, c, label, "bl43-dana", "family")
	require.True(t, s.Valid())

	t.Run("financial_transactions.is_shared", func(t *testing.T) {
		for _, want := range []bool{false, true} {
			tx := &models.FinancialTransaction{UserID: "bl43-dana", Type: "expense", Category: "health",
				Merchant: "Clinic", Amount: 80, IsShared: want}
			require.NoError(t, c.CreateTransaction(ctx, s, tx))
			assert.Equal(t, fmt.Sprint(want), stored(t, c, "financial_transactions", "is_shared", tx.ID), "create")
			assert.Equal(t, want, tx.IsShared, "the caller's copy says what was stored")
		}
	})

	t.Run("recurring_bills.auto_renew", func(t *testing.T) {
		bill := &models.RecurringBill{Name: "Gym", Category: "health", ExpectedAmount: 50, AutoRenew: false}
		require.NoError(t, c.CreateRecurringBill(ctx, s, bill))
		assert.Equal(t, "false", stored(t, c, "recurring_bills", "auto_renew", bill.ID), "create")
		for _, want := range []bool{true, false} {
			bill.AutoRenew = want
			require.NoError(t, c.UpdateRecurringBill(ctx, s, bill))
			assert.Equal(t, fmt.Sprint(want), stored(t, c, "recurring_bills", "auto_renew", bill.ID), "update")
		}
	})

	t.Run("shared_spaces.can_view and can_edit", func(t *testing.T) {
		space := &models.SharedSpace{Topic: "finance", OwnerUserID: "bl43-dana", NotifyMode: "silent", CanView: false, CanEdit: false}
		require.NoError(t, c.UpsertSharedSpace(s, space))
		assert.Equal(t, "false", stored(t, c, "shared_spaces", "can_view", space.ID), "create")
		assert.Equal(t, "false", stored(t, c, "shared_spaces", "can_edit", space.ID), "create")
		for _, want := range []bool{true, false} {
			again := &models.SharedSpace{Topic: "finance", OwnerUserID: "bl43-dana", NotifyMode: "silent", CanView: want, CanEdit: want}
			require.NoError(t, c.UpsertSharedSpace(s, again))
			require.Equal(t, space.ID, again.ID, "the same space is updated")
			assert.Equal(t, fmt.Sprint(want), stored(t, c, "shared_spaces", "can_view", space.ID), "update")
			assert.Equal(t, fmt.Sprint(want), stored(t, c, "shared_spaces", "can_edit", space.ID), "update")
		}
	})

	t.Run("automation_rules.is_active", func(t *testing.T) {
		rule := &models.AutomationRule{UserID: "bl43-dana", Name: "Off from the start", TriggerType: "cron",
			TriggerTopic: "*", ConditionJSON: `{"cron": "0 9 * * *"}`, ActionInstruction: "notify: hi", IsActive: false}
		require.NoError(t, c.CreateAutomationRule(s, rule))
		assert.Equal(t, "false", stored(t, c, "automation_rules", "is_active", rule.ID), "create")
		assert.False(t, activeRule(t, c, rule.ID), "an inactive rule is not handed to the scheduler or the event runner (BL-62)")
		for _, want := range []bool{true, false} {
			rule.IsActive = want
			require.NoError(t, c.UpdateAutomationRule(s, rule))
			assert.Equal(t, fmt.Sprint(want), stored(t, c, "automation_rules", "is_active", rule.ID), "update")
			assert.Equal(t, want, activeRule(t, c, rule.ID))
		}
	})

	t.Run("schedule_events.sync_to_google", func(t *testing.T) {
		start := time.Now().Add(24 * time.Hour).Truncate(time.Second)
		ev := &models.ScheduleEvent{UserID: "bl43-dana", Topic: "health", Title: "Checkup",
			StartTime: start, EndTime: start.Add(time.Hour), SyncToGoogle: false}
		require.NoError(t, c.CreateScheduleEvent(s, ev))
		assert.Equal(t, "false", stored(t, c, "schedule_events", "sync_to_google", ev.ID), "create")
		for _, want := range []bool{true, false} {
			ev.SyncToGoogle = want
			require.NoError(t, c.UpdateScheduleEvent(s, ev))
			assert.Equal(t, fmt.Sprint(want), stored(t, c, "schedule_events", "sync_to_google", ev.ID), "update")
		}
	})

	t.Run("topic_calendar_configs.include_in_morning_briefing", func(t *testing.T) {
		cfg := &models.TopicCalendarConfig{UserID: "bl43-dana-" + uuid.NewString(), Topic: "health", IncludeInMorningBriefing: false}
		require.NoError(t, c.UpsertTopicCalendarConfig(cfg))
		assert.Equal(t, "false", stored(t, c, "topic_calendar_configs", "include_in_morning_briefing", cfg.ID), "create")
		for _, want := range []bool{true, false} {
			again := &models.TopicCalendarConfig{UserID: cfg.UserID, Topic: "health", IncludeInMorningBriefing: want}
			require.NoError(t, c.UpsertTopicCalendarConfig(again))
			require.Equal(t, cfg.ID, again.ID, "the same config is updated")
			assert.Equal(t, fmt.Sprint(want), stored(t, c, "topic_calendar_configs", "include_in_morning_briefing", cfg.ID), "update")
		}
	})
}

func activeRule(t *testing.T, c *Client, id uint) bool {
	t.Helper()
	rules, err := c.ListActiveAutomationRules("*")
	require.NoError(t, err)
	for _, r := range rules {
		if r.ID == id {
			return true
		}
	}
	return false
}

// BL-44: a member reads the household's shared transactions and their own unshared ones, on
// every finance read (the rule the dashboard has had since V4-35, ADR 0002 Decision 1): the
// list, one transaction, the summary, the month's category totals, the budget check and the
// anomaly average. Another household's are never read.
func TestFinanceRepo_AMemberReadsSharedAndTheirOwnTransactions(t *testing.T) {
	c, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	label := "bl44-" + uuid.NewString()
	dana := scopeAs(t, c, label, "bl44-dana", "admin")
	omer := scopeAs(t, c, label, "bl44-omer", "family")
	other := scopeAs(t, c, "bl44-other-"+uuid.NewString(), "bl44-ran", "admin")
	require.Equal(t, dana.HouseholdID(), omer.HouseholdID())

	now := time.Now().UTC()
	add := func(s identity.Scope, person, merchant string, amount float64, shared bool) *models.FinancialTransaction {
		tx := &models.FinancialTransaction{UserID: person, Type: "expense", Category: "health", Merchant: merchant,
			Amount: amount, TransactionDate: now, IsShared: shared}
		require.NoError(t, c.CreateTransaction(ctx, s, tx))
		return tx
	}
	add(dana, "bl44-dana", "Dana shared", 10, true)
	add(dana, "bl44-dana", "Dana private", 20, false)
	add(omer, "bl44-omer", "Omer shared", 100, true)
	omerPrivate := add(omer, "bl44-omer", "Omer private", 1000, false)
	add(other, "bl44-ran", "Other household", 5000, true)

	merchants := func(s identity.Scope) []string {
		txs, err := c.ListTransactions(ctx, s, TransactionFilter{})
		require.NoError(t, err)
		var out []string
		for _, tx := range txs {
			out = append(out, tx.Merchant)
		}
		return out
	}
	assert.ElementsMatch(t, []string{"Dana shared", "Dana private", "Omer shared"}, merchants(dana))
	assert.ElementsMatch(t, []string{"Dana shared", "Omer shared", "Omer private"}, merchants(omer))

	got, err := c.GetTransaction(ctx, dana, omerPrivate.ID)
	require.NoError(t, err)
	assert.Nil(t, got, "another member's unshared transaction is not found")
	got, err = c.GetTransaction(ctx, omer, omerPrivate.ID)
	require.NoError(t, err)
	require.NotNil(t, got, "its person reads it")

	sum, err := c.GetFinancialSummary(ctx, dana, "", now.AddDate(0, 0, -1), now.AddDate(0, 0, 1))
	require.NoError(t, err)
	assert.Equal(t, 130.0, sum.TotalExpense)
	assert.Equal(t, 3, sum.TransactionCount)
	assert.Equal(t, 100.0, sum.UserSummary["bl44-omer"], "another member's spending shows only what they shared")
	sum, err = c.GetFinancialSummary(ctx, dana, "bl44-omer", now.AddDate(0, 0, -1), now.AddDate(0, 0, 1))
	require.NoError(t, err)
	assert.Equal(t, 100.0, sum.TotalExpense, "asking for another member does not reach their unshared rows")

	month, err := c.GetMonthlyCategorySpend(ctx, dana, "health", now.Year(), now.Month())
	require.NoError(t, err)
	assert.Equal(t, 130.0, month)
	totals, err := c.GetMonthlyTotalsByCategory(ctx, omer, now.Year(), now.Month())
	require.NoError(t, err)
	assert.Equal(t, 1110.0, totals["health"])

	avg, err := c.GetHistoricalAverage(ctx, dana, "health", "Omer private", 0)
	require.NoError(t, err)
	assert.Equal(t, "category", avg.MatchedOn, "another member's unshared merchant is not matched")
	assert.InDelta(t, 130.0/3, avg.Average, 0.001)
}
