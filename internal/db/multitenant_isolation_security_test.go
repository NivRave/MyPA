package db

import (
	"context"
	"fmt"
	"github.com/nivik/mypa/internal/identity"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/models"
	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMultiTenant_AdversarialSQLInjectionAndWildcards tests that adversarial
// tenant strings (wildcards %, empty "", SQL fragments ' OR 1=1 --) cannot bypass tenant boundaries
// in any of the 8 database repositories.
func TestMultiTenant_AdversarialSQLInjectionAndWildcards(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	legitFamily := "fam-legit-" + uuid.New().String()
	legitUser := "usr-legit-" + uuid.New().String()
	legit := scopeAs(t, client, legitFamily, legitUser, "family")

	// 1. Populate legitimate data across all 8 repos
	// Repo 1: Durability
	require.NoError(t, client.InsertMutationLog(legit, models.MutationLog{
		ID:          uuid.New().String(),
		UserID:      legitUser,
		FamilyGroup: legitFamily,
		Topic:       "house",
		Action:      "create",
		EntityType:  "contract",
		EntityID:    "1",
		Summary:     "Legit Contract",
	}))

	// Repo 2: Memory V2
	vec := pgvector.NewVector(make([]float32, 3072))
	mem := models.TopicMemory{
		UserID:      legitUser,
		FamilyGroup: legitFamily,
		Topic:       "secret_topic",
		Fact:        "Legitimate Family Secret Fact",
		Scope:       "family",
		Embedding:   vec,
		CreatedAt:   time.Now(),
	}
	require.NoError(t, client.MemoryV2().SaveTopicMemory(ctx, legit, &mem))

	// Repo 3: Shared Space
	require.NoError(t, NewSharedRepository(client.DB).UpsertSharedSpace(legit, &models.SharedSpace{
		FamilyGroup: legitFamily,
		Topic:       "secret_topic",
		OwnerUserID: legitUser,
	}))

	// Repo 4: Schedule
	schedRepo := NewScheduleRepository(client.DB)
	require.NoError(t, schedRepo.CreateScheduleEvent(legit, &models.ScheduleEvent{
		UserID:          legitUser,
		FamilyGroup:     legitFamily,
		Topic:           "secret_topic",
		Title:           "Top Secret Event",
		StartTime:       time.Now(),
		EndTime:         time.Now().Add(time.Hour),
		CreatedByUserID: legitUser,
	}))

	// Repo 5: Baby
	babyRepo := NewBabyRepository(client.DB)
	require.NoError(t, babyRepo.UpsertBabyProfile(legit, &models.BabyProfile{
		FamilyGroup: legitFamily,
		Name:        "BabySecret",
		DateOfBirth: time.Now(),
	}))

	// Repo 6: House
	houseRepo := NewHouseRepository(client.DB)
	require.NoError(t, houseRepo.CreateContract(legit, &models.HouseContractRecord{
		FamilyGroup:     legitFamily,
		RecordType:      models.HouseRecordLease,
		Title:           "Secret Home Lease",
		CreatedByUserID: legitUser,
	}))

	// Repo 7: Finance
	finRepo := NewFinanceRepository(client.DB)
	require.NoError(t, finRepo.CreateTransaction(ctx, legit, &models.FinancialTransaction{
		UserID:          legitUser,
		FamilyGroup:     legitFamily,
		Type:            models.TransactionTypeExpense,
		Category:        models.CategoryGroceries,
		Merchant:        "Secret Merchant",
		Amount:          999.99,
		TransactionDate: time.Now(),
	}))

	// Repo 8: Automation
	autoRepo := NewAutomationRepository(client.DB)
	require.NoError(t, autoRepo.CreateAPIConnector(legit, &models.ExternalAPIConnector{
		UserID:      legitUser,
		FamilyGroup: legitFamily,
		Name:        "SecretAPI",
		BaseURL:     "https://secret.local",
	}))

	// Adversarial input probes (non-empty SQL injection and wildcard vectors)
	adversarialProbes := []string{
		"%",
		"*",
		"' OR '1'='1",
		"' OR 1=1 --",
		"\" OR \"\"=\"",
		"fam-legit%",
		"admin'--",
		"\\",
	}

	for _, probe := range adversarialProbes {
		t.Run(fmt.Sprintf("Probe_%s", probe), func(t *testing.T) {
			// V4-30: the probe is a family label; it becomes a household of its own.
			probeScope := scopeAs(t, client, probe, probe, "family")
			// Durability
			logs, err := client.GetMutationLogsByTopic(probeScope, "all", 50)
			require.NoError(t, err)
			assert.Empty(t, logs, "Probe %q must not leak mutation logs", probe)

			// Memory V2
			mems, err := client.MemoryV2().ListTopicMemories(ctx, probeScope, "", false)
			require.NoError(t, err)
			assert.Empty(t, mems, "Probe %q must not leak topic memories", probe)

			// Shared
			spaces, err := NewSharedRepository(client.DB).GetSharedSpacesForFamily(probeScope)
			require.NoError(t, err)
			assert.Empty(t, spaces, "Probe %q must not leak shared spaces", probe)

			// Schedule
			events, err := schedRepo.ListScheduleEvents(probeScope, "all", time.Now().Add(-24*time.Hour), time.Now().Add(24*time.Hour))
			require.NoError(t, err)
			assert.Empty(t, events, "Probe %q must not leak schedule events", probe)

			// Baby
			profiles, err := babyRepo.GetBabyProfilesForFamily(probeScope)
			require.NoError(t, err)
			assert.Empty(t, profiles, "Probe %q must not leak baby profiles", probe)

			// House
			contracts, err := houseRepo.ListContracts(probeScope, "all", nil)
			require.NoError(t, err)
			assert.Empty(t, contracts, "Probe %q must not leak house contracts", probe)

			// Finance
			txs, err := finRepo.ListTransactions(ctx, probeScope, TransactionFilter{Limit: 50})
			require.NoError(t, err)
			assert.Empty(t, txs, "Probe %q must not leak transactions", probe)

			// Automation
			conns, err := autoRepo.ListAPIConnectors(probeScope)
			require.NoError(t, err)
			assert.Empty(t, conns, "Probe %q must not leak API connectors", probe)
		})
	}
}

// TestMultiTenant_EmptyScopeLeakVulnerability documented the SEC-04 fail-open behaviour when
// callers omitted familyGroup and userID (empty strings ""). Since V4-30 a repository takes an
// identity.Scope, and the only way to omit the tenant is the zero Scope: every repository refuses
// it with identity.ErrNoScope and returns nothing. The four paths that failed OPEN are now
// asserted closed (DP-26).
func TestMultiTenant_EmptyScopeLeakVulnerability(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	legitFamily := "fam-legit-" + uuid.New().String()
	legitUser := "usr-legit-" + uuid.New().String()
	legit := scopeAs(t, client, legitFamily, legitUser, "family")
	var empty identity.Scope // the tenant omitted

	// Seed records under legitFamily
	vec := pgvector.NewVector(make([]float32, 3072))
	require.NoError(t, client.MemoryV2().SaveTopicMemory(ctx, legit, &models.TopicMemory{
		UserID:      legitUser,
		FamilyGroup: legitFamily,
		Topic:       "secret_topic",
		Fact:        "Secret Fact",
		Scope:       "family",
		Embedding:   vec,
		CreatedAt:   time.Now(),
	}))

	schedRepo := NewScheduleRepository(client.DB)
	require.NoError(t, schedRepo.CreateScheduleEvent(legit, &models.ScheduleEvent{
		UserID:          legitUser,
		FamilyGroup:     legitFamily,
		Topic:           "secret_topic",
		Title:           "Secret Event",
		StartTime:       time.Now(),
		EndTime:         time.Now().Add(time.Hour),
		CreatedByUserID: legitUser,
	}))

	houseRepo := NewHouseRepository(client.DB)
	require.NoError(t, houseRepo.CreateContract(legit, &models.HouseContractRecord{
		FamilyGroup:     legitFamily,
		RecordType:      models.HouseRecordLease,
		Title:           "Secret Lease",
		CreatedByUserID: legitUser,
	}))

	autoRepo := NewAutomationRepository(client.DB)
	require.NoError(t, autoRepo.CreateAPIConnector(legit, &models.ExternalAPIConnector{
		UserID:      legitUser,
		FamilyGroup: legitFamily,
		Name:        "SecretAPI",
		BaseURL:     "https://secret.local",
	}))

	// 1. Durability fails closed (no leak)
	logs, err := client.GetMutationLogsByTopic(empty, "all", 50)
	require.ErrorIs(t, err, identity.ErrNoScope)
	assert.Empty(t, logs, "Durability correctly fails closed on empty tenant")

	// 2. Finance fails closed (no leak)
	txs, err := NewFinanceRepository(client.DB).ListTransactions(ctx, empty, TransactionFilter{Limit: 50})
	require.ErrorIs(t, err, identity.ErrNoScope)
	assert.Empty(t, txs, "Finance correctly fails closed on empty tenant")

	// 3. Shared fails closed (no leak)
	spaces, err := NewSharedRepository(client.DB).GetSharedSpacesForFamily(empty)
	require.ErrorIs(t, err, identity.ErrNoScope)
	assert.Empty(t, spaces, "Shared correctly fails closed on empty tenant")

	// 4. Baby fails closed (no leak)
	profiles, err := NewBabyRepository(client.DB).GetBabyProfilesForFamily(empty)
	require.ErrorIs(t, err, identity.ErrNoScope)
	assert.Empty(t, profiles, "Baby correctly fails closed on empty tenant")

	// 5. Memory V2 ListTopicMemories failed OPEN before V4-30; now closed.
	leakedMems, err := client.MemoryV2().ListTopicMemories(ctx, empty, "", false)
	require.ErrorIs(t, err, identity.ErrNoScope)
	assert.Empty(t, leakedMems, "Memory fails closed on empty tenant")

	// 6. Schedule ListScheduleEvents failed OPEN before V4-30; now closed.
	leakedEvents, err := schedRepo.ListScheduleEvents(empty, "all", time.Now().Add(-24*time.Hour), time.Now().Add(24*time.Hour))
	require.ErrorIs(t, err, identity.ErrNoScope)
	assert.Empty(t, leakedEvents, "Schedule fails closed on empty tenant")

	// 7. House ListContracts failed OPEN before V4-30; now closed.
	leakedContracts, err := houseRepo.ListContracts(empty, "all", nil)
	require.ErrorIs(t, err, identity.ErrNoScope)
	assert.Empty(t, leakedContracts, "House fails closed on empty tenant")

	// 8. Automation ListAPIConnectors failed OPEN before V4-30; now closed.
	leakedConns, err := autoRepo.ListAPIConnectors(empty)
	require.ErrorIs(t, err, identity.ErrNoScope)
	assert.Empty(t, leakedConns, "Automation fails closed on empty tenant")
}

// TestMultiTenant_ConcurrentStress tests concurrent reads and writes across
// 8 distinct tenants executing parallel operations across all 8 domain tables without cross-contamination.
func TestMultiTenant_ConcurrentStress(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	const numTenants = 8
	var wg sync.WaitGroup
	wg.Add(numTenants)

	errChan := make(chan error, numTenants*8)

	for i := 0; i < numTenants; i++ {
		tenantIdx := i
		fam := fmt.Sprintf("fam-stress-%d-%s", tenantIdx, uuid.New().String())
		usr := fmt.Sprintf("usr-stress-%d-%s", tenantIdx, uuid.New().String())
		own := scopeAs(t, client, fam, usr, "family") // V4-30: each tenant is a household

		go func() {
			defer wg.Done()
			ctx := context.Background()

			// 1. Durability
			err := client.InsertMutationLog(own, models.MutationLog{
				ID:          uuid.New().String(),
				UserID:      usr,
				FamilyGroup: fam,
				Topic:       "durability",
				Action:      "create",
				EntityType:  "stress_entity",
				EntityID:    fmt.Sprintf("%d", tenantIdx),
				Summary:     fmt.Sprintf("Stress log %d", tenantIdx),
			})
			if err != nil {
				errChan <- fmt.Errorf("tenant %d durability write: %w", tenantIdx, err)
				return
			}

			// 2. Memory V2
			vec := pgvector.NewVector(make([]float32, 3072))
			err = client.MemoryV2().SaveTopicMemory(ctx, own, &models.TopicMemory{
				UserID:      usr,
				FamilyGroup: fam,
				Topic:       "stress_topic",
				Fact:        fmt.Sprintf("Stress fact %d", tenantIdx),
				Scope:       "family",
				Embedding:   vec,
				CreatedAt:   time.Now(),
			})
			if err != nil {
				errChan <- fmt.Errorf("tenant %d memory write: %w", tenantIdx, err)
				return
			}

			// 3. Shared
			sharedRepo := NewSharedRepository(client.DB)
			err = sharedRepo.UpsertSharedSpace(own, &models.SharedSpace{
				FamilyGroup: fam,
				Topic:       "stress_topic",
				OwnerUserID: usr,
			})
			if err != nil {
				errChan <- fmt.Errorf("tenant %d shared write: %w", tenantIdx, err)
				return
			}

			// 4. Schedule
			schedRepo := NewScheduleRepository(client.DB)
			err = schedRepo.CreateScheduleEvent(own, &models.ScheduleEvent{
				UserID:          usr,
				FamilyGroup:     fam,
				Topic:           "stress_topic",
				Title:           fmt.Sprintf("Event %d", tenantIdx),
				StartTime:       time.Now(),
				EndTime:         time.Now().Add(time.Hour),
				CreatedByUserID: usr,
			})
			if err != nil {
				errChan <- fmt.Errorf("tenant %d schedule write: %w", tenantIdx, err)
				return
			}

			// 5. Baby
			babyRepo := NewBabyRepository(client.DB)
			err = babyRepo.UpsertBabyProfile(own, &models.BabyProfile{
				FamilyGroup: fam,
				Name:        fmt.Sprintf("Baby_%d", tenantIdx),
				DateOfBirth: time.Now(),
			})
			if err != nil {
				errChan <- fmt.Errorf("tenant %d baby write: %w", tenantIdx, err)
				return
			}

			// 6. House
			houseRepo := NewHouseRepository(client.DB)
			err = houseRepo.CreateContract(own, &models.HouseContractRecord{
				FamilyGroup:     fam,
				RecordType:      models.HouseRecordLease,
				Title:           fmt.Sprintf("Lease %d", tenantIdx),
				CreatedByUserID: usr,
			})
			if err != nil {
				errChan <- fmt.Errorf("tenant %d house write: %w", tenantIdx, err)
				return
			}

			// 7. Finance
			finRepo := NewFinanceRepository(client.DB)
			err = finRepo.CreateTransaction(ctx, own, &models.FinancialTransaction{
				UserID:          usr,
				FamilyGroup:     fam,
				Type:            models.TransactionTypeExpense,
				Category:        models.CategoryGroceries,
				Merchant:        fmt.Sprintf("Merchant_%d", tenantIdx),
				Amount:          100.0 + float64(tenantIdx),
				TransactionDate: time.Now(),
			})
			if err != nil {
				errChan <- fmt.Errorf("tenant %d finance write: %w", tenantIdx, err)
				return
			}

			// 8. Automation
			autoRepo := NewAutomationRepository(client.DB)
			err = autoRepo.CreateAPIConnector(own, &models.ExternalAPIConnector{
				UserID:      usr,
				FamilyGroup: fam,
				Name:        fmt.Sprintf("Connector_%d", tenantIdx),
				BaseURL:     fmt.Sprintf("https://api%d.local", tenantIdx),
			})
			if err != nil {
				errChan <- fmt.Errorf("tenant %d automation write: %w", tenantIdx, err)
				return
			}

			// Readback and assert strict 1:1 containment (no leakage from other 7 tenants)
			logs, err := client.GetMutationLogsByTopic(own, "all", 50)
			if err != nil || len(logs) != 1 {
				errChan <- fmt.Errorf("tenant %d unexpected logs count: %d, err: %v", tenantIdx, len(logs), err)
				return
			}

			mems, err := client.MemoryV2().ListTopicMemories(ctx, own, "", false)
			if err != nil || len(mems) != 1 {
				errChan <- fmt.Errorf("tenant %d unexpected mems count: %d, err: %v", tenantIdx, len(mems), err)
				return
			}

			spaces, err := sharedRepo.GetSharedSpacesForFamily(own)
			if err != nil || len(spaces) != 1 {
				errChan <- fmt.Errorf("tenant %d unexpected spaces count: %d, err: %v", tenantIdx, len(spaces), err)
				return
			}

			events, err := schedRepo.ListScheduleEvents(own, "all", time.Now().Add(-24*time.Hour), time.Now().Add(24*time.Hour))
			if err != nil || len(events) != 1 {
				errChan <- fmt.Errorf("tenant %d unexpected events count: %d, err: %v", tenantIdx, len(events), err)
				return
			}

			profiles, err := babyRepo.GetBabyProfilesForFamily(own)
			if err != nil || len(profiles) != 1 {
				errChan <- fmt.Errorf("tenant %d unexpected profiles count: %d, err: %v", tenantIdx, len(profiles), err)
				return
			}

			contracts, err := houseRepo.ListContracts(own, "all", nil)
			if err != nil || len(contracts) != 1 {
				errChan <- fmt.Errorf("tenant %d unexpected contracts count: %d, err: %v", tenantIdx, len(contracts), err)
				return
			}

			txs, err := finRepo.ListTransactions(ctx, own, TransactionFilter{Limit: 50})
			if err != nil || len(txs) != 1 {
				errChan <- fmt.Errorf("tenant %d unexpected txs count: %d, err: %v", tenantIdx, len(txs), err)
				return
			}

			conns, err := autoRepo.ListAPIConnectors(own)
			if err != nil || len(conns) != 1 {
				errChan <- fmt.Errorf("tenant %d unexpected conns count: %d, err: %v", tenantIdx, len(conns), err)
				return
			}
		}()
	}
	wg.Wait()
	close(errChan)

	for err := range errChan {
		require.NoError(t, err)
	}
}
