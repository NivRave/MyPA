package graph

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	dbmodels "github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BL-66 on the dashboard: a person changes their own transaction and deletes it, and can undo
// both; another member and an owner are refused; a transaction made private leaves the other
// members' list.
func TestDashboard_APersonChangesAndDeletesTheirOwnTransaction(t *testing.T) {
	r, dur := undoResolver(t)
	fam := "fam-bl66-" + uuid.NewString()
	owner := scopeFor(t, r, fam, "owner-"+uuid.NewString(), "admin")
	b := scopeFor(t, r, fam, "adult-b-"+uuid.NewString(), "member")
	c := scopeFor(t, r, fam, "adult-c-"+uuid.NewString(), "member")
	ownerC, bC, cC := WithCaller(context.Background(), owner), WithCaller(context.Background(), b), WithCaller(context.Background(), c)

	tx, err := r.Mutation().LogFinancialTransaction(bC, "expense", "groceries", "Market BL66", 80, "USD", nil, ptrB(true))
	require.NoError(t, err)
	listed := func(ctx context.Context) map[string]bool {
		txs, err := r.Query().FinancialTransactions(ctx, nil, nil)
		require.NoError(t, err)
		out := map[string]bool{}
		for _, x := range txs {
			out[x.ID] = x.IsMine
		}
		return out
	}
	mine, ok := listed(ownerC)[tx.ID]
	require.True(t, ok, "shared: the owner sees it")
	assert.False(t, mine, "but it is not the owner's")
	assert.True(t, listed(bC)[tx.ID], "it is B's")

	// The owner and another adult may not change or delete it.
	for _, ctx := range []context.Context{ownerC, cC} {
		_, err := r.Mutation().UpdateFinancialTransaction(ctx, tx.ID, nil, nil, nil, ptrF(1), nil, nil, nil, ptrB(false))
		assert.Error(t, err)
		_, err = r.Mutation().DeleteFinancialTransaction(ctx, tx.ID)
		assert.Error(t, err)
	}
	var row dbmodels.FinancialTransaction
	require.NoError(t, r.DB.DB.First(&row, tx.ID).Error)
	assert.Equal(t, 80.0, row.Amount)
	assert.True(t, row.IsShared)

	// B changes it and makes it private: it leaves the others' list.
	day := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	got, err := r.Mutation().UpdateFinancialTransaction(bC, tx.ID, nil, ptr("dining"), ptr("Bistro BL66"), ptrF(95), nil, &day, nil, ptrB(false))
	require.NoError(t, err)
	assert.Equal(t, 95.0, got.Amount)
	assert.Equal(t, "dining", got.Category)
	assert.False(t, got.IsShared)
	assert.True(t, got.IsMine)
	_, ok = listed(ownerC)[tx.ID]
	assert.False(t, ok, "private: gone from the owner's list")
	_, ok = listed(cC)[tx.ID]
	assert.False(t, ok, "and from C's")

	res, err := dur.RollbackLast(context.Background(), b)
	require.NoError(t, err, "B undoes the change")
	assert.Equal(t, tx.ID, res.EntityID)
	require.NoError(t, r.DB.DB.First(&row, tx.ID).Error)
	assert.Equal(t, 80.0, row.Amount)
	assert.True(t, row.IsShared, "shared again")

	deleted, err := r.Mutation().DeleteFinancialTransaction(bC, tx.ID)
	require.NoError(t, err)
	assert.True(t, deleted)
	var n int64
	require.NoError(t, r.DB.DB.Model(&dbmodels.FinancialTransaction{}).Where("id = ?", tx.ID).Count(&n).Error)
	assert.Zero(t, n)
	_, err = dur.RollbackLast(context.Background(), b)
	require.NoError(t, err, "B undoes the delete")
	require.NoError(t, r.DB.DB.Model(&dbmodels.FinancialTransaction{}).Where("id = ?", tx.ID).Count(&n).Error)
	assert.Equal(t, int64(1), n)
}

// BL-68 on the dashboard: a new budget and a person's first preferences were logged as updates
// with no earlier state, so /undo could not reverse them. They are creates, and /undo removes them.
func TestDashboard_ANewBudgetAndFirstPreferencesAreUndone(t *testing.T) {
	r, dur := undoResolver(t)
	fam := "fam-bl68-" + uuid.NewString()
	key := "owner-" + uuid.NewString()
	owner := scopeFor(t, r, fam, key, "admin")
	ctx := WithCaller(context.Background(), owner)

	budget, err := r.Mutation().SetCategoryBudget(ctx, "dining", 300, "USD", nil)
	require.NoError(t, err)
	res, err := dur.RollbackLast(context.Background(), owner)
	require.NoError(t, err)
	assert.False(t, res.NotUndoable, res.Detail)
	var n int64
	require.NoError(t, r.DB.DB.Model(&dbmodels.CategoryBudget{}).Where("id = ?", budget.ID).Count(&n).Error)
	assert.Zero(t, n, "the new budget is gone")

	_, err = r.Mutation().SaveUserPreference(ctx, owner.PersonID(), nil, ptr("Warm"), nil, nil, nil, nil)
	require.NoError(t, err)
	res, err = dur.RollbackLast(context.Background(), owner)
	require.NoError(t, err)
	assert.False(t, res.NotUndoable, res.Detail)
	require.NoError(t, r.DB.DB.Model(&dbmodels.UserPreference{}).Count(&n).Error)
	assert.Zero(t, n, "the first preferences are gone")
}

func ptrF(f float64) *float64 { return &f }
func ptrB(b bool) *bool       { return &b }
