package db

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFinanceRepo_Transactions_CRUDAndIsolation(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewFinanceRepository(client.DB)
	ctx := context.Background()

	famA := "fam-" + uuid.New().String()
	famB := "fam-" + uuid.New().String()
	userA := "usr-" + uuid.New().String()
	userB := "usr-" + uuid.New().String()

	txDate := time.Now().Truncate(time.Hour)

	// 1. Create transactions for Family A
	txA1 := models.FinancialTransaction{
		UserID:          userA,
		FamilyGroup:     famA,
		Type:            models.TransactionTypeExpense,
		Category:        models.CategoryGroceries,
		Merchant:        "Whole Foods",
		Amount:          125.50,
		Currency:        "USD",
		TransactionDate: txDate,
		IsShared:        true,
	}
	require.NoError(t, repo.CreateTransaction(ctx, scopeFor(t, client, famA), &txA1))
	require.NotZero(t, txA1.ID)

	txA2 := models.FinancialTransaction{
		UserID:          userA,
		FamilyGroup:     famA,
		Type:            models.TransactionTypeIncome,
		Category:        "salary",
		Merchant:        "Tech Corp",
		Amount:          5000.0,
		Currency:        "USD",
		TransactionDate: txDate,
	}
	require.NoError(t, repo.CreateTransaction(ctx, scopeFor(t, client, famA), &txA2))
	require.NotZero(t, txA2.ID)

	// 2. Create transaction for Family B
	txB := models.FinancialTransaction{
		UserID:          userB,
		FamilyGroup:     famB,
		Type:            models.TransactionTypeExpense,
		Category:        models.CategoryDining,
		Merchant:        "Pizza Bistro",
		Amount:          45.0,
		Currency:        "USD",
		TransactionDate: txDate,
	}
	require.NoError(t, repo.CreateTransaction(ctx, scopeFor(t, client, famB), &txB))
	require.NotZero(t, txB.ID)

	// 3. Positive lookup
	gotA1, err := repo.GetTransaction(ctx, scopeFor(t, client, famA), txA1.ID)
	require.NoError(t, err)
	require.NotNil(t, gotA1)
	assert.Equal(t, "Whole Foods", gotA1.Merchant)
	assert.Equal(t, 125.50, gotA1.Amount)

	// 4. ListTransactions with cross-tenant isolation
	filterAll := TransactionFilter{Limit: 50}
	listA, err := repo.ListTransactions(ctx, scopeFor(t, client, famA), filterAll)
	require.NoError(t, err)
	require.Len(t, listA, 2)

	listB, err := repo.ListTransactions(ctx, scopeFor(t, client, famB), filterAll)
	require.NoError(t, err)
	require.Len(t, listB, 1)
	assert.Equal(t, txB.ID, listB[0].ID)

	// Filter by Category
	filterGroceries := TransactionFilter{Category: models.CategoryGroceries, Limit: 50}
	groceriesA, err := repo.ListTransactions(ctx, scopeFor(t, client, famA), filterGroceries)
	require.NoError(t, err)
	require.Len(t, groceriesA, 1)
	assert.Equal(t, txA1.ID, groceriesA[0].ID)

	// Filter by Type
	filterIncome := TransactionFilter{Type: models.TransactionTypeIncome, Limit: 50}
	incomeA, err := repo.ListTransactions(ctx, scopeFor(t, client, famA), filterIncome)
	require.NoError(t, err)
	require.Len(t, incomeA, 1)
	assert.Equal(t, 5000.0, incomeA[0].Amount)
}

func TestFinanceRepo_AggregationsAndSummary(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewFinanceRepository(client.DB)
	ctx := context.Background()

	fam := "fam-" + uuid.New().String()
	user := "usr-" + uuid.New().String()

	now := time.Now()
	currentYear := now.Year()
	currentMonth := now.Month()

	// 1. Seed transactions
	t1 := models.FinancialTransaction{
		UserID:          user,
		FamilyGroup:     fam,
		Type:            models.TransactionTypeExpense,
		Category:        models.CategoryGroceries,
		Merchant:        "Trader Joe's",
		Amount:          80.0,
		TransactionDate: now,
	}
	require.NoError(t, repo.CreateTransaction(ctx, scopeFor(t, client, fam), &t1))

	t2 := models.FinancialTransaction{
		UserID:          user,
		FamilyGroup:     fam,
		Type:            models.TransactionTypeExpense,
		Category:        models.CategoryGroceries,
		Merchant:        "Trader Joe's",
		Amount:          120.0,
		TransactionDate: now,
	}
	require.NoError(t, repo.CreateTransaction(ctx, scopeFor(t, client, fam), &t2))

	t3 := models.FinancialTransaction{
		UserID:          user,
		FamilyGroup:     fam,
		Type:            models.TransactionTypeExpense,
		Category:        models.CategoryUtilities,
		Merchant:        "Electric Co",
		Amount:          150.0,
		TransactionDate: now,
	}
	require.NoError(t, repo.CreateTransaction(ctx, scopeFor(t, client, fam), &t3))

	tIncome := models.FinancialTransaction{
		UserID:          user,
		FamilyGroup:     fam,
		Type:            models.TransactionTypeIncome,
		Category:        "salary",
		Amount:          3000.0,
		TransactionDate: now,
	}
	require.NoError(t, repo.CreateTransaction(ctx, scopeFor(t, client, fam), &tIncome))

	// 2. GetMonthlyCategorySpend
	spend, err := repo.GetMonthlyCategorySpend(ctx, scopeFor(t, client, fam), models.CategoryGroceries, currentYear, currentMonth)
	require.NoError(t, err)
	assert.Equal(t, 200.0, spend) // 80 + 120

	// 3. GetMonthlyTotalsByCategory
	totals, err := repo.GetMonthlyTotalsByCategory(ctx, scopeFor(t, client, fam), currentYear, currentMonth)
	require.NoError(t, err)
	assert.Equal(t, 200.0, totals[models.CategoryGroceries])
	assert.Equal(t, 150.0, totals[models.CategoryUtilities])

	// 4. GetFinancialSummary
	startMonth := time.Date(currentYear, currentMonth, 1, 0, 0, 0, 0, time.UTC)
	endMonth := startMonth.AddDate(0, 1, 0)
	summary, err := repo.GetFinancialSummary(ctx, scopeFor(t, client, fam), user, startMonth, endMonth)
	require.NoError(t, err)
	require.NotNil(t, summary)
	assert.Equal(t, 350.0, summary.TotalExpense) // 200 + 150
	assert.Equal(t, 3000.0, summary.TotalIncome)
	assert.Equal(t, 2650.0, summary.NetSavings)
	assert.Equal(t, 4, summary.TransactionCount)

	// 5. GetHistoricalAverage for merchant anomaly detection
	avgResult, err := repo.GetHistoricalAverage(ctx, scopeFor(t, client, fam), models.CategoryGroceries, "Trader Joe's", 0)
	require.NoError(t, err)
	require.NotNil(t, avgResult)
	assert.Equal(t, 100.0, avgResult.Average) // (80 + 120) / 2
	assert.Equal(t, int64(2), avgResult.Count)
	assert.Equal(t, "merchant", avgResult.MatchedOn)
}

func TestFinanceRepo_CategoryBudgets_CRUDAndIsolation(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewFinanceRepository(client.DB)
	ctx := context.Background()

	famA := "fam-" + uuid.New().String()
	famB := "fam-" + uuid.New().String()

	// 1. Set budgets for Family A
	bA1 := models.CategoryBudget{
		FamilyGroup:       famA,
		Category:          models.CategoryGroceries,
		MonthlyLimit:      800.0,
		Currency:          "USD",
		AlertThresholdPct: 80.0,
	}
	require.NoError(t, repo.SetCategoryBudget(ctx, scopeFor(t, client, famA), &bA1))
	require.NotZero(t, bA1.ID)

	bA2 := models.CategoryBudget{
		FamilyGroup:       famA,
		Category:          models.CategoryDining,
		MonthlyLimit:      300.0,
		Currency:          "USD",
		AlertThresholdPct: 85.0,
	}
	require.NoError(t, repo.SetCategoryBudget(ctx, scopeFor(t, client, famA), &bA2))

	// 2. Set budget for Family B
	bB := models.CategoryBudget{
		FamilyGroup:       famB,
		Category:          models.CategoryGroceries,
		MonthlyLimit:      500.0,
		Currency:          "USD",
		AlertThresholdPct: 75.0,
	}
	require.NoError(t, repo.SetCategoryBudget(ctx, scopeFor(t, client, famB), &bB))

	// 3. Positive lookup and isolation
	gotA, err := repo.GetCategoryBudget(ctx, scopeFor(t, client, famA), models.CategoryGroceries)
	require.NoError(t, err)
	require.NotNil(t, gotA)
	assert.Equal(t, 800.0, gotA.MonthlyLimit)

	gotB, err := repo.GetCategoryBudget(ctx, scopeFor(t, client, famB), models.CategoryGroceries)
	require.NoError(t, err)
	require.NotNil(t, gotB)
	assert.Equal(t, 500.0, gotB.MonthlyLimit)

	// ListCategoryBudgets
	budgetsA, err := repo.ListCategoryBudgets(ctx, scopeFor(t, client, famA))
	require.NoError(t, err)
	require.Len(t, budgetsA, 2)

	budgetsB, err := repo.ListCategoryBudgets(ctx, scopeFor(t, client, famB))
	require.NoError(t, err)
	require.Len(t, budgetsB, 1)

	// 4. Update in place via SetCategoryBudget
	bA1.MonthlyLimit = 900.0
	require.NoError(t, repo.SetCategoryBudget(ctx, scopeFor(t, client, famA), &bA1))

	recheckA, err := repo.GetCategoryBudget(ctx, scopeFor(t, client, famA), models.CategoryGroceries)
	require.NoError(t, err)
	require.NotNil(t, recheckA)
	assert.Equal(t, 900.0, recheckA.MonthlyLimit)

	// 5. Delete budget
	require.NoError(t, repo.DeleteCategoryBudget(ctx, scopeFor(t, client, famA), bA1.ID))
	deletedA, err := repo.GetCategoryBudget(ctx, scopeFor(t, client, famA), models.CategoryGroceries)
	require.NoError(t, err)
	assert.Nil(t, deletedA)
}

func TestFinanceRepo_RecurringBills_CRUDAndIsolation(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewFinanceRepository(client.DB)
	ctx := context.Background()

	famA := "fam-" + uuid.New().String()
	famB := "fam-" + uuid.New().String()

	// 1. Create bills for Family A
	billA := models.RecurringBill{
		FamilyGroup:    famA,
		Name:           "Fiber Internet",
		Category:       models.CategoryUtilities,
		ExpectedAmount: 70.0,
		Currency:       "USD",
		BillingCycle:   models.BillingCycleMonthly,
		NextDueDay:     15,
		AutoRenew:      true,
	}
	require.NoError(t, repo.CreateRecurringBill(ctx, scopeFor(t, client, famA), &billA))
	require.NotZero(t, billA.ID)

	// 2. Create bill for Family B
	billB := models.RecurringBill{
		FamilyGroup:    famB,
		Name:           "Cloud Storage",
		Category:       models.CategoryOther,
		ExpectedAmount: 9.99,
		Currency:       "USD",
		BillingCycle:   models.BillingCycleMonthly,
		NextDueDay:     1,
	}
	require.NoError(t, repo.CreateRecurringBill(ctx, scopeFor(t, client, famB), &billB))
	require.NotZero(t, billB.ID)

	// 3. Positive lookup and isolation
	gotA, err := repo.GetRecurringBill(ctx, scopeFor(t, client, famA), billA.ID)
	require.NoError(t, err)
	require.NotNil(t, gotA)
	assert.Equal(t, "Fiber Internet", gotA.Name)

	listA, err := repo.ListRecurringBills(ctx, scopeFor(t, client, famA))
	require.NoError(t, err)
	require.Len(t, listA, 1)
	assert.Equal(t, "Fiber Internet", listA[0].Name)

	listB, err := repo.ListRecurringBills(ctx, scopeFor(t, client, famB))
	require.NoError(t, err)
	require.Len(t, listB, 1)
	assert.Equal(t, "Cloud Storage", listB[0].Name)

	// 4. Update bill
	billA.ExpectedAmount = 75.0
	require.NoError(t, repo.UpdateRecurringBill(ctx, scopeFor(t, client, famA), &billA))

	recheckA, err := repo.GetRecurringBill(ctx, scopeFor(t, client, famA), billA.ID)
	require.NoError(t, err)
	require.NotNil(t, recheckA)
	assert.Equal(t, 75.0, recheckA.ExpectedAmount)

	// 5. Delete bill
	require.NoError(t, repo.DeleteRecurringBill(ctx, scopeFor(t, client, famA), billA.ID))
	deletedA, err := repo.GetRecurringBill(ctx, scopeFor(t, client, famA), billA.ID)
	require.NoError(t, err)
	assert.Nil(t, deletedA)
}
