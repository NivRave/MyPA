package baby_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/domains/baby"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockBabyRepo is a thread-safe, in-memory implementation of db.BabyRepository.
type mockBabyRepo struct {
	mu           sync.RWMutex
	profiles     map[uint]*models.BabyProfile
	careLogs     map[uint]*models.BabyCareLog
	inventory    map[uint]*models.BabyInventoryItem
	nextProfID   uint
	nextLogID    uint
	nextInvID    uint
}

func newMockBabyRepo() *mockBabyRepo {
	return &mockBabyRepo{
		profiles:   make(map[uint]*models.BabyProfile),
		careLogs:   make(map[uint]*models.BabyCareLog),
		inventory:  make(map[uint]*models.BabyInventoryItem),
		nextProfID: 1,
		nextLogID:  1,
		nextInvID:  1,
	}
}

func (r *mockBabyRepo) UpsertBabyProfile(p *models.BabyProfile) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	p.Name = strings.TrimSpace(p.Name)
	p.FamilyGroup = strings.TrimSpace(p.FamilyGroup)
	p.UpdatedAt = time.Now()

	if p.ID == 0 {
		// Look up existing by familyGroup + Name
		for _, ex := range r.profiles {
			if ex.FamilyGroup == p.FamilyGroup && strings.EqualFold(ex.Name, p.Name) {
				p.ID = ex.ID
				break
			}
		}
	}

	if p.ID == 0 {
		p.ID = r.nextProfID
		r.nextProfID++
	}

	cp := *p
	r.profiles[p.ID] = &cp
	return nil
}

func (r *mockBabyRepo) GetBabyProfile(id uint) (*models.BabyProfile, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.profiles[id]
	if !ok {
		return nil, nil
	}
	cp := *p
	return &cp, nil
}

func (r *mockBabyRepo) GetBabyProfileByName(familyGroup, name string) (*models.BabyProfile, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, p := range r.profiles {
		if p.FamilyGroup == familyGroup && strings.EqualFold(p.Name, name) {
			cp := *p
			return &cp, nil
		}
	}
	return nil, nil
}

func (r *mockBabyRepo) GetBabyProfilesForFamily(familyGroup string) ([]models.BabyProfile, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var list []models.BabyProfile
	for _, p := range r.profiles {
		if p.FamilyGroup == familyGroup {
			list = append(list, *p)
		}
	}
	return list, nil
}

func (r *mockBabyRepo) DeleteBabyProfile(id uint) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.profiles, id)
	return nil
}

func (r *mockBabyRepo) CreateCareLog(log *models.BabyCareLog) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if log.ID == 0 {
		log.ID = r.nextLogID
		r.nextLogID++
	}
	if log.Timestamp.IsZero() {
		log.Timestamp = time.Now()
	}
	log.LogType = strings.ToLower(strings.TrimSpace(log.LogType))
	cp := *log
	r.careLogs[log.ID] = &cp
	return nil
}

func (r *mockBabyRepo) GetCareLogByID(id uint) (*models.BabyCareLog, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	l, ok := r.careLogs[id]
	if !ok {
		return nil, nil
	}
	cp := *l
	return &cp, nil
}

func (r *mockBabyRepo) GetCareLogs(babyID uint, familyGroup, logType string, start, end time.Time, limit int) ([]models.BabyCareLog, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var matches []models.BabyCareLog
	for _, l := range r.careLogs {
		if babyID > 0 && l.BabyID != babyID {
			continue
		}
		if familyGroup != "" && l.FamilyGroup != familyGroup {
			continue
		}
		if logType != "" && !strings.EqualFold(logType, "all") && !strings.EqualFold(l.LogType, logType) {
			continue
		}
		if !start.IsZero() && l.Timestamp.Before(start) {
			continue
		}
		if !end.IsZero() && l.Timestamp.After(end) {
			continue
		}
		matches = append(matches, *l)
	}

	// Reverse sort by timestamp (newest first)
	for i := 0; i < len(matches)-1; i++ {
		for j := i + 1; j < len(matches); j++ {
			if matches[i].Timestamp.Before(matches[j].Timestamp) {
				matches[i], matches[j] = matches[j], matches[i]
			}
		}
	}

	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	return matches, nil
}

func (r *mockBabyRepo) DeleteCareLog(id uint) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.careLogs, id)
	return nil
}

func (r *mockBabyRepo) GetLastMedicationLog(babyID uint, familyGroup, medName string) (*models.BabyCareLog, error) {
	logs, err := r.GetCareLogs(babyID, familyGroup, "medication", time.Time{}, time.Time{}, 100)
	if err != nil || len(logs) == 0 {
		return nil, err
	}
	if strings.TrimSpace(medName) == "" {
		return &logs[0], nil
	}
	matched := baby.FindLastMedicationLogForCooldown(logs, medName)
	if matched != nil {
		cp := *matched
		return &cp, nil
	}
	return nil, nil
}

func (r *mockBabyRepo) GetCareStatsSummary(babyID uint, familyGroup string, start, end time.Time) (*models.BabyCareSummary, error) {
	logs, err := r.GetCareLogs(babyID, familyGroup, "all", start, end, 1000)
	if err != nil {
		return nil, err
	}

	summary := &models.BabyCareSummary{
		BabyID:         babyID,
		FamilyGroup:    familyGroup,
		StartTime:      start,
		EndTime:        end,
		LogCountByType: make(map[string]int),
	}

	for _, l := range logs {
		summary.LogCountByType[l.LogType]++
		switch l.LogType {
		case "feeding":
			summary.TotalFeedings++
			summary.TotalFeedingVolume += l.ValueNumeric
			if summary.FeedingVolumeUnit == "" && l.Unit != "" {
				summary.FeedingVolumeUnit = l.Unit
			}
		case "sleep":
			if strings.EqualFold(l.Unit, "hours") || strings.EqualFold(l.Unit, "h") {
				summary.TotalSleepMinutes += l.ValueNumeric * 60
			} else {
				summary.TotalSleepMinutes += l.ValueNumeric
			}
		case "diaper":
			summary.TotalDiaperChanges++
			detailLower := strings.ToLower(l.Details)
			if strings.Contains(detailLower, "both") || (strings.Contains(detailLower, "wet") && strings.Contains(detailLower, "dirty")) {
				summary.BothDiapers++
			} else if strings.Contains(detailLower, "dirty") || strings.Contains(detailLower, "poop") {
				summary.DirtyDiapers++
			} else {
				summary.WetDiapers++
			}
		case "temperature":
			if summary.LastTemperature == nil {
				val := l.ValueNumeric
				summary.LastTemperature = &val
				summary.LastTempUnit = l.Unit
			}
		case "medication":
			if summary.LastMedication == nil {
				cp := l
				summary.LastMedication = &cp
			}
		}
	}
	return summary, nil
}

func (r *mockBabyRepo) UpsertBabyInventoryItem(item *models.BabyInventoryItem) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	item.ItemName = strings.TrimSpace(item.ItemName)
	item.FamilyGroup = strings.TrimSpace(item.FamilyGroup)
	item.UpdatedAt = time.Now()

	if item.ID == 0 {
		for _, ex := range r.inventory {
			if ex.FamilyGroup == item.FamilyGroup && strings.EqualFold(ex.ItemName, item.ItemName) {
				item.ID = ex.ID
				break
			}
		}
	}

	if item.ID == 0 {
		item.ID = r.nextInvID
		r.nextInvID++
	}

	cp := *item
	r.inventory[item.ID] = &cp
	return nil
}

func (r *mockBabyRepo) GetBabyInventoryItem(id uint) (*models.BabyInventoryItem, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	it, ok := r.inventory[id]
	if !ok {
		return nil, nil
	}
	cp := *it
	return &cp, nil
}

func (r *mockBabyRepo) GetBabyInventoryItemByName(familyGroup, itemName string) (*models.BabyInventoryItem, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, it := range r.inventory {
		if it.FamilyGroup == familyGroup && strings.EqualFold(it.ItemName, itemName) {
			cp := *it
			return &cp, nil
		}
	}
	return nil, nil
}

func (r *mockBabyRepo) ListBabyInventoryItems(familyGroup string) ([]models.BabyInventoryItem, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var list []models.BabyInventoryItem
	for _, it := range r.inventory {
		if it.FamilyGroup == familyGroup {
			list = append(list, *it)
		}
	}
	return list, nil
}

func (r *mockBabyRepo) GetLowBabyInventoryItems(familyGroup string) ([]models.BabyInventoryItem, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var list []models.BabyInventoryItem
	for _, it := range r.inventory {
		if it.FamilyGroup == familyGroup && it.QuantityRemaining <= it.LowThreshold {
			list = append(list, *it)
		}
	}
	return list, nil
}

func (r *mockBabyRepo) DeleteBabyInventoryItem(id uint) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.inventory, id)
	return nil
}

func setupTestModule(t *testing.T) (*baby.Module, *mockBabyRepo, *events.EventBus) {
	repo := newMockBabyRepo()
	bus := events.NewEventBus()
	mod := baby.NewModule(repo, baby.WithEventBus(bus))
	return mod, repo, bus
}

func createTestCallContext(bus *events.EventBus) orchestrator.CallContext {
	return orchestrator.CallContext{
		User: &models.User{
			ID:          "user-101",
			FamilyGroup: "family-smith",
		},
		Message: models.Message{
			UserID: "user-101",
		},
		EventBus: bus,
	}
}

// 1. Declarations & Module Metadata
func TestModule_Declarations(t *testing.T) {
	mod, _, _ := setupTestModule(t)

	assert.Equal(t, "baby", mod.Name())

	decls := mod.Declarations()
	require.Len(t, decls, 5)

	expectedTools := map[string]bool{
		"upsert_baby_profile":            false,
		"log_baby_care":                  false,
		"query_baby_logs_and_trends":     false,
		"get_baby_milestone_suggestions": false,
		"manage_baby_inventory":          false,
	}

	for _, d := range decls {
		_, ok := expectedTools[d.Name]
		assert.True(t, ok, "unexpected tool declaration: %s", d.Name)
		expectedTools[d.Name] = true
		assert.NotEmpty(t, d.Description)
		assert.NotNil(t, d.Parameters)
	}

	for name, found := range expectedTools {
		assert.True(t, found, "missing tool declaration: %s", name)
	}
}

// 2. Profile Upsert & Age Calculation
func TestModule_UpsertBabyProfile(t *testing.T) {
	mod, repo, bus := setupTestModule(t)
	callCtx := createTestCallContext(bus)

	var emittedEvents []events.MutationEvent
	var evMu sync.Mutex
	bus.SubscribeTopic("baby", func(ctx context.Context, ev events.MutationEvent) error {
		evMu.Lock()
		defer evMu.Unlock()
		emittedEvents = append(emittedEvents, ev)
		return nil
	})

	// Create profile
	args := map[string]interface{}{
		"name":                 "Maya",
		"date_of_birth":        "2026-06-15",
		"gender":               "female",
		"birth_weight_grams":   3200,
		"current_weight_grams": 5400,
		"height_cm":            58.5,
		"pediatrician_info":    "Dr. Cohen (Tel Aviv Children's Clinic)",
		"allergies_notes":      "None known",
	}

	out, err := mod.Execute(context.Background(), callCtx, "upsert_baby_profile", args)
	require.NoError(t, err)
	assert.Contains(t, out, "success")
	assert.Contains(t, out, "Maya")

	// Verify DB state
	profile, err := repo.GetBabyProfileByName("family-smith", "Maya")
	require.NoError(t, err)
	require.NotNil(t, profile)
	assert.Equal(t, "Maya", profile.Name)
	assert.Equal(t, 3200, profile.BirthWeightGrams)
	assert.Equal(t, 5400, profile.CurrentWeightGrams)
	assert.Equal(t, 58.5, profile.HeightCm)

	// Verify EventBus mutation event
	evMu.Lock()
	require.Len(t, emittedEvents, 1)
	assert.Equal(t, "baby", emittedEvents[0].Topic)
	assert.Equal(t, "create", emittedEvents[0].Action)
	assert.Equal(t, "BabyProfile", emittedEvents[0].EntityType)
	evMu.Unlock()

	// Update profile (weight & height update)
	updateArgs := map[string]interface{}{
		"id":                   int(profile.ID),
		"name":                 "Maya",
		"date_of_birth":        "2026-06-15",
		"current_weight_grams": 6100,
		"height_cm":            61.0,
	}

	outUpdate, err := mod.Execute(context.Background(), callCtx, "upsert_baby_profile", updateArgs)
	require.NoError(t, err)
	assert.Contains(t, outUpdate, "success")

	updatedProfile, err := repo.GetBabyProfile(profile.ID)
	require.NoError(t, err)
	assert.Equal(t, 6100, updatedProfile.CurrentWeightGrams)
	assert.Equal(t, 61.0, updatedProfile.HeightCm)
	assert.Equal(t, 3200, updatedProfile.BirthWeightGrams)
	assert.Equal(t, "Dr. Cohen (Tel Aviv Children's Clinic)", updatedProfile.PediatricianInfo)

	evMu.Lock()
	assert.Len(t, emittedEvents, 2)
	assert.Equal(t, "update", emittedEvents[1].Action)
	evMu.Unlock()
}

// 3. Log Baby Care (all log types) & Measurement auto-updating profile
func TestModule_LogBabyCare(t *testing.T) {
	mod, repo, bus := setupTestModule(t)
	callCtx := createTestCallContext(bus)

	// Seed baby profile
	profile := models.BabyProfile{
		FamilyGroup:        "family-smith",
		Name:               "Leo",
		DateOfBirth:        time.Now().AddDate(0, -3, 0),
		CurrentWeightGrams: 5000,
	}
	require.NoError(t, repo.UpsertBabyProfile(&profile))

	var emittedEvents []events.MutationEvent
	var evMu sync.Mutex
	bus.SubscribeTopic("baby", func(ctx context.Context, ev events.MutationEvent) error {
		evMu.Lock()
		defer evMu.Unlock()
		emittedEvents = append(emittedEvents, ev)
		return nil
	})

	// Feeding log
	out, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":       int(profile.ID),
		"log_type":      "feeding",
		"value_numeric": 150.0,
		"unit":          "ml",
		"details":       "formula stage 1",
	})
	require.NoError(t, err)
	assert.Contains(t, out, "Logged feeding for Leo: 150 ml (formula stage 1)")

	// Diaper log
	out, err = mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":  int(profile.ID),
		"log_type": "diaper",
		"details":  "wet and dirty",
	})
	require.NoError(t, err)
	assert.Contains(t, out, "Logged diaper change for Leo: wet and dirty")

	// Sleep log
	out, err = mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":       int(profile.ID),
		"log_type":      "sleep",
		"value_numeric": 1.5,
		"unit":          "hours",
		"details":       "morning nap in crib",
	})
	require.NoError(t, err)
	assert.Contains(t, out, "Logged sleep for Leo: 1.5 hours")

	// Temperature log
	out, err = mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":       int(profile.ID),
		"log_type":      "temperature",
		"value_numeric": 38.2,
		"unit":          "celsius",
		"details":       "rectal thermometer",
	})
	require.NoError(t, err)
	assert.Contains(t, out, "38.2 celsius")

	// Milestone log
	out, err = mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":  int(profile.ID),
		"log_type": "milestone",
		"details":  "rolled from tummy to back for the first time!",
	})
	require.NoError(t, err)
	assert.Contains(t, out, "rolled from tummy to back")

	// Measurement log: updates profile current weight
	out, err = mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":       int(profile.ID),
		"log_type":      "measurement",
		"value_numeric": 5.8,
		"unit":          "kg",
		"details":       "clinic weighing",
	})
	require.NoError(t, err)
	assert.Contains(t, out, "Logged growth measurement")

	// Verify profile weight was updated to 5800 grams
	updatedProf, err := repo.GetBabyProfile(profile.ID)
	require.NoError(t, err)
	assert.Equal(t, 5800, updatedProf.CurrentWeightGrams)

	evMu.Lock()
	assert.Equal(t, 6, len(emittedEvents))
	evMu.Unlock()
}

// 4. Medication Cooldown Guardrail
func TestModule_MedicationCooldown(t *testing.T) {
	mod, repo, bus := setupTestModule(t)
	callCtx := createTestCallContext(bus)

	now := time.Now()
	// Baby is 4 months old
	dob := now.AddDate(0, -4, 0)
	profile := models.BabyProfile{
		FamilyGroup: "family-smith",
		Name:        "Noah",
		DateOfBirth: dob,
	}
	require.NoError(t, repo.UpsertBabyProfile(&profile))

	// Dose 1: Paracetamol at 12:00
	t1 := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, now.Location())
	out1, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Paracetamol 2.5ml",
		"timestamp": t1.Format(time.RFC3339),
	})
	require.NoError(t, err)
	assert.Contains(t, out1, "success")
	assert.NotContains(t, out1, "COOLDOWN ACTIVE")

	// Dose 2: Trying to give another dose at 14:15 (only 2h 15m later - min is 4h)
	t2 := time.Date(now.Year(), now.Month(), now.Day(), 14, 15, 0, 0, now.Location())
	out2, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Paracetamol 2.5ml",
		"timestamp": t2.Format(time.RFC3339),
	})
	require.NoError(t, err)
	assert.Contains(t, out2, "COOLDOWN ACTIVE")
	assert.Contains(t, out2, "Do NOT administer yet")
	assert.Contains(t, out2, "16:00") // 12:00 + 4 hours = 16:00

	var resp2 map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(out2), &resp2))
	cd2 := resp2["medication_cooldown"].(map[string]interface{})
	assert.Equal(t, false, cd2["is_safe_to_administer"])
	assert.Equal(t, "paracetamol", cd2["normalized_type"])

	// Dose 3: Giving dose at 16:30 (4h 30m later - safe!)
	t3 := time.Date(now.Year(), now.Month(), now.Day(), 16, 30, 0, 0, now.Location())
	out3, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Paracetamol 2.5ml",
		"timestamp": t3.Format(time.RFC3339),
	})
	require.NoError(t, err)
	assert.NotContains(t, out3, "COOLDOWN ACTIVE")
	var resp3 map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(out3), &resp3))
	cd3 := resp3["medication_cooldown"].(map[string]interface{})
	assert.Equal(t, true, cd3["is_safe_to_administer"])

	// Test Ibuprofen on a 4-month-old infant: should flag caution (< 6 months)
	outIbu, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Nurofen / Ibuprofen syrup",
		"timestamp": t3.Format(time.RFC3339),
	})
	require.NoError(t, err)
	assert.Contains(t, outIbu, "NOT recommended for infants under 6 months")
}

// 5. Query Logs & Trends Aggregation
func TestModule_QueryBabyLogsAndTrends(t *testing.T) {
	mod, repo, bus := setupTestModule(t)
	callCtx := createTestCallContext(bus)

	profile := models.BabyProfile{
		FamilyGroup: "family-smith",
		Name:        "Chloe",
		DateOfBirth: time.Now().AddDate(0, -6, 0),
	}
	require.NoError(t, repo.UpsertBabyProfile(&profile))

	now := time.Now()

	// Seed 3 feeding logs (total 400 ml)
	require.NoError(t, repo.CreateCareLog(&models.BabyCareLog{
		BabyID: profile.ID, FamilyGroup: "family-smith", LogType: "feeding", ValueNumeric: 120, Unit: "ml", Timestamp: now.Add(-6 * time.Hour),
	}))
	require.NoError(t, repo.CreateCareLog(&models.BabyCareLog{
		BabyID: profile.ID, FamilyGroup: "family-smith", LogType: "feeding", ValueNumeric: 140, Unit: "ml", Timestamp: now.Add(-3 * time.Hour),
	}))
	require.NoError(t, repo.CreateCareLog(&models.BabyCareLog{
		BabyID: profile.ID, FamilyGroup: "family-smith", LogType: "feeding", ValueNumeric: 140, Unit: "ml", Timestamp: now.Add(-30 * time.Minute),
	}))

	// Seed 2 sleep logs (1.5h and 2.0h)
	require.NoError(t, repo.CreateCareLog(&models.BabyCareLog{
		BabyID: profile.ID, FamilyGroup: "family-smith", LogType: "sleep", ValueNumeric: 1.5, Unit: "hours", Timestamp: now.Add(-8 * time.Hour),
	}))
	require.NoError(t, repo.CreateCareLog(&models.BabyCareLog{
		BabyID: profile.ID, FamilyGroup: "family-smith", LogType: "sleep", ValueNumeric: 2.0, Unit: "hours", Timestamp: now.Add(-4 * time.Hour),
	}))

	// Seed 3 diaper logs
	require.NoError(t, repo.CreateCareLog(&models.BabyCareLog{
		BabyID: profile.ID, FamilyGroup: "family-smith", LogType: "diaper", Details: "wet", Timestamp: now.Add(-5 * time.Hour),
	}))
	require.NoError(t, repo.CreateCareLog(&models.BabyCareLog{
		BabyID: profile.ID, FamilyGroup: "family-smith", LogType: "diaper", Details: "dirty", Timestamp: now.Add(-2 * time.Hour),
	}))
	require.NoError(t, repo.CreateCareLog(&models.BabyCareLog{
		BabyID: profile.ID, FamilyGroup: "family-smith", LogType: "diaper", Details: "wet and dirty (both)", Timestamp: now.Add(-10 * time.Minute),
	}))

	// Query trends
	out, err := mod.Execute(context.Background(), callCtx, "query_baby_logs_and_trends", map[string]interface{}{
		"baby_id":    int(profile.ID),
		"time_range": "24h",
	})
	require.NoError(t, err)

	var res map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(out), &res))

	trends := res["trends"].(map[string]interface{})
	assert.Equal(t, float64(3), trends["total_feedings"])
	assert.Equal(t, float64(400), trends["total_feeding_volume"])
	assert.Equal(t, float64(210), trends["total_sleep_minutes"]) // (1.5 + 2.0) * 60 = 210
	assert.Equal(t, float64(3), trends["total_diaper_changes"])
	assert.Equal(t, float64(1), trends["both_diapers"])

	// Query filtered by log_type = feeding
	outFeed, err := mod.Execute(context.Background(), callCtx, "query_baby_logs_and_trends", map[string]interface{}{
		"baby_id":  int(profile.ID),
		"log_type": "feeding",
	})
	require.NoError(t, err)
	var resFeed map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(outFeed), &resFeed))
	assert.Equal(t, float64(3), resFeed["logs_returned"])
}

// 6. Milestone Suggestions & Leaps
func TestModule_MilestoneSuggestions(t *testing.T) {
	mod, repo, bus := setupTestModule(t)
	callCtx := createTestCallContext(bus)

	// Baby is 19 weeks old (around Leap 4, 4-month checkup/vaccine)
	dob := time.Now().AddDate(0, 0, -19*7)
	profile := models.BabyProfile{
		FamilyGroup: "family-smith",
		Name:        "Liam",
		DateOfBirth: dob,
	}
	require.NoError(t, repo.UpsertBabyProfile(&profile))

	out, err := mod.Execute(context.Background(), callCtx, "get_baby_milestone_suggestions", map[string]interface{}{
		"baby_id": int(profile.ID),
	})
	require.NoError(t, err)
	assert.Contains(t, out, "Liam")
	assert.Contains(t, out, "Leap 4")
	assert.Contains(t, out, "Events")
	assert.Contains(t, out, "4-Month Well-Baby Visit")
	assert.Contains(t, out, "Hexavalent")

	// Filter by category = feeding
	outFeed, err := mod.Execute(context.Background(), callCtx, "get_baby_milestone_suggestions", map[string]interface{}{
		"baby_id":  int(profile.ID),
		"category": "feeding",
	})
	require.NoError(t, err)
	assert.Contains(t, outFeed, "Solids Readiness")
}

// 7. Baby Supplies & Inventory Tracking
func TestModule_ManageBabyInventory(t *testing.T) {
	mod, _, bus := setupTestModule(t)
	callCtx := createTestCallContext(bus)

	var emittedEvents []events.MutationEvent
	var evMu sync.Mutex
	bus.SubscribeTopic("baby", func(ctx context.Context, ev events.MutationEvent) error {
		evMu.Lock()
		defer evMu.Unlock()
		emittedEvents = append(emittedEvents, ev)
		return nil
	})

	// 1. Set stock for Formula
	outSet, err := mod.Execute(context.Background(), callCtx, "manage_baby_inventory", map[string]interface{}{
		"action":        "set",
		"item_name":     "Formula Stage 1",
		"quantity":      6.0,
		"unit":          "cans",
		"low_threshold": 2.0,
	})
	require.NoError(t, err)
	assert.Contains(t, outSet, "Formula Stage 1")

	// 2. Set stock for Diapers Size 2
	_, err = mod.Execute(context.Background(), callCtx, "manage_baby_inventory", map[string]interface{}{
		"action":        "set",
		"item_name":     "Diapers Size 2",
		"quantity":      3.0,
		"unit":          "packs",
		"low_threshold": 2.0,
	})
	require.NoError(t, err)

	// 3. List inventory
	outList, err := mod.Execute(context.Background(), callCtx, "manage_baby_inventory", map[string]interface{}{
		"action": "list",
	})
	require.NoError(t, err)
	assert.Contains(t, outList, "Formula Stage 1")
	assert.Contains(t, outList, "Diapers Size 2")

	// 4. Adjust: consume 2 packs of Diapers (delta: -2)
	outAdj, err := mod.Execute(context.Background(), callCtx, "manage_baby_inventory", map[string]interface{}{
		"action":    "adjust",
		"item_name": "Diapers Size 2",
		"delta":     -2.0,
	})
	require.NoError(t, err)
	assert.Contains(t, outAdj, "LOW STOCK ALERT")
	assert.Contains(t, outAdj, "1.0 packs left")

	// 5. Check low items
	outLow, err := mod.Execute(context.Background(), callCtx, "manage_baby_inventory", map[string]interface{}{
		"action": "check_low",
	})
	require.NoError(t, err)
	assert.Contains(t, outLow, "Diapers Size 2")
	assert.NotContains(t, outLow, "Formula Stage 1") // 6 cans remaining > 2 threshold

	evMu.Lock()
	assert.True(t, len(emittedEvents) >= 3)
	evMu.Unlock()
}

// 8. SystemPromptSection injection
func TestModule_SystemPromptSection(t *testing.T) {
	mod, repo, bus := setupTestModule(t)
	callCtx := createTestCallContext(bus)

	// When no profiles exist, returns empty string
	promptEmpty := mod.SystemPromptSection(context.Background(), callCtx)
	assert.Empty(t, promptEmpty)

	// Seed baby profile
	now := time.Now()
	dob := now.AddDate(0, -4, 0)
	profile := models.BabyProfile{
		FamilyGroup: "family-smith",
		Name:        "Emma",
		DateOfBirth: dob,
	}
	require.NoError(t, repo.UpsertBabyProfile(&profile))

	// Add recent medication log (only 1h ago)
	medTime := now.Add(-1 * time.Hour)
	require.NoError(t, repo.CreateCareLog(&models.BabyCareLog{
		BabyID:      profile.ID,
		FamilyGroup: "family-smith",
		LogType:     "medication",
		Details:     "Paracetamol 2ml",
		Timestamp:   medTime,
	}))

	// Add low inventory item
	require.NoError(t, repo.UpsertBabyInventoryItem(&models.BabyInventoryItem{
		FamilyGroup:       "family-smith",
		ItemName:          "Vitamin D Drops",
		QuantityRemaining: 1.0,
		Unit:              "bottles",
		LowThreshold:      2.0,
	}))

	prompt := mod.SystemPromptSection(context.Background(), callCtx)
	assert.Contains(t, prompt, "Baby & Family Care Tracker")
	assert.Contains(t, prompt, "Emma")
	assert.Contains(t, prompt, "4 months")
	assert.Contains(t, prompt, "Medication Cooldown: Paracetamol")
	assert.Contains(t, prompt, "Low Baby Supplies")
	assert.Contains(t, prompt, "Vitamin D Drops")
}

// 9. Error handling cases
func TestModule_ErrorHandling(t *testing.T) {
	mod, repo, bus := setupTestModule(t)
	callCtx := createTestCallContext(bus)

	// Unknown tool
	_, err := mod.Execute(context.Background(), callCtx, "non_existent_tool", nil)
	assert.Error(t, err)

	// upsert missing name
	_, err = mod.Execute(context.Background(), callCtx, "upsert_baby_profile", map[string]interface{}{
		"date_of_birth": "2026-06-15",
	})
	assert.Error(t, err)

	// upsert missing date of birth
	_, err = mod.Execute(context.Background(), callCtx, "upsert_baby_profile", map[string]interface{}{
		"name": "Alex",
	})
	assert.Error(t, err)

	// log care missing log_type
	_, err = mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"details": "something",
	})
	assert.Error(t, err)

	// log care with no baby profile registered
	_, err = mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"log_type": "feeding",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no baby profiles registered")

	// seed 2 babies, then try logging without specifying which baby
	p1 := models.BabyProfile{FamilyGroup: "family-smith", Name: "Twin1", DateOfBirth: time.Now()}
	p2 := models.BabyProfile{FamilyGroup: "family-smith", Name: "Twin2", DateOfBirth: time.Now()}
	require.NoError(t, repo.UpsertBabyProfile(&p1))
	require.NoError(t, repo.UpsertBabyProfile(&p2))

	_, err = mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"log_type": "feeding",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "multiple babies found")
}

// 10. Age Calculation & Preemie Edge Cases
func TestCalculateAge_EdgeCases(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

	// Newborn 3 days old
	dobNewborn := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	ageNewborn := baby.CalculateAge(dobNewborn, nil, now)
	assert.Equal(t, 3, ageNewborn.TotalDays)
	assert.Equal(t, 0, ageNewborn.Weeks)
	assert.Contains(t, ageNewborn.FormattedAge, "3 days old")

	// Baby born 4 weeks premature
	dobPreemie := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	duePreemie := time.Date(2026, 6, 29, 0, 0, 0, 0, time.UTC) // 4 weeks later
	agePreemie := baby.CalculateAge(dobPreemie, &duePreemie, now)
	assert.True(t, agePreemie.IsPremature)
	assert.Contains(t, agePreemie.AdjustedFormatted, "adjusted")

	// Toddler 1 year, 2 months
	dobToddler := time.Date(2025, 9, 3, 0, 0, 0, 0, time.UTC)
	ageToddler := baby.CalculateAge(dobToddler, nil, now)
	assert.Equal(t, 1, ageToddler.Years)
	assert.Equal(t, 14, ageToddler.Months)
	assert.Contains(t, ageToddler.FormattedAge, "1 year, 2 months old")

	// Future birth date
	dobFuture := time.Date(2026, 12, 25, 0, 0, 0, 0, time.UTC)
	ageFuture := baby.CalculateAge(dobFuture, nil, now)
	assert.Contains(t, ageFuture.FormattedAge, "Not yet born")
}
