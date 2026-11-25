package house

import (
	"context"
	"fmt"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/identity/identitytest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testHouseholds holds the households tests create tenants in (V4-30): a family label becomes a
// household with an ID derived from the label, and scopes are built by the production constructor.
var testHouseholds = identitytest.NewStore()

// withScope gives a call context the scope of its user. The user's family label becomes a
// household; a user without a label is their own tenant, keyed by their platform ID (DE-2), as
// the migration maps them.
func withScope(cc orchestrator.CallContext) orchestrator.CallContext {
	label, key := "", cc.Message.Sender
	if cc.User != nil {
		label = cc.User.FamilyGroup
		if cc.User.PlatformID != "" {
			key = cc.User.PlatformID
		}
	}
	if strings.TrimSpace(label) == "" {
		label = key
	}
	h := testHouseholds.Household(label)
	s, err := identity.ForUser(context.Background(), testHouseholds, &models.User{PlatformID: key, Role: "family", HouseholdID: h.ID})
	if err != nil {
		panic(err)
	}
	cc.Scope = s
	return cc
}

// fakeHouseRepo provides a thread-safe, in-memory implementation of db.HouseRepository for unit testing.
type fakeHouseRepo struct {
	mu           sync.RWMutex
	contracts    map[uint]*models.HouseContractRecord
	maintenance  map[uint]*models.HomeMaintenanceItem
	vehicles     map[uint]*models.VehicleAsset
	nextContract uint
	nextMaint    uint
	nextVehicle  uint
}

func newFakeHouseRepo() *fakeHouseRepo {
	return &fakeHouseRepo{
		contracts:    make(map[uint]*models.HouseContractRecord),
		maintenance:  make(map[uint]*models.HomeMaintenanceItem),
		vehicles:     make(map[uint]*models.VehicleAsset),
		nextContract: 1,
		nextMaint:    1,
		nextVehicle:  1,
	}
}

func (r *fakeHouseRepo) CreateContract(s identity.Scope, c *models.HouseContractRecord) error {
	c.HouseholdID, c.FamilyGroup = s.HouseholdID(), s.LegacyLabel()
	r.mu.Lock()
	defer r.mu.Unlock()
	c.ID = r.nextContract
	r.nextContract++
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now()
	}
	cp := *c
	r.contracts[c.ID] = &cp
	return nil
}

func (r *fakeHouseRepo) GetContract(s identity.Scope, id uint) (*models.HouseContractRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.contracts[id]
	if !ok {
		return nil, nil
	}
	cp := *c
	return &cp, nil
}

func (r *fakeHouseRepo) UpdateContract(s identity.Scope, c *models.HouseContractRecord) error {
	c.HouseholdID, c.FamilyGroup = s.HouseholdID(), s.LegacyLabel()
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *c
	r.contracts[c.ID] = &cp
	return nil
}

func (r *fakeHouseRepo) DeleteContract(_ identity.Scope, id uint) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.contracts, id)
	return nil
}

func (r *fakeHouseRepo) ListContracts(s identity.Scope, recordType string, expiringBefore *time.Time) ([]models.HouseContractRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var res []models.HouseContractRecord
	recordType = strings.ToLower(strings.TrimSpace(recordType))
	for _, c := range r.contracts {
		if c.HouseholdID != s.HouseholdID() {
			continue
		}
		if recordType != "" && recordType != "all" && c.RecordType != recordType {
			continue
		}
		if expiringBefore != nil {
			if c.ExpirationOrRenewalDate == nil || c.ExpirationOrRenewalDate.After(*expiringBefore) {
				continue
			}
		}
		res = append(res, *c)
	}
	return res, nil
}

func (r *fakeHouseRepo) ListExpiringContracts(s identity.Scope, withinDays int) ([]models.HouseContractRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	now := time.Now()
	cutoff := now.AddDate(0, 0, withinDays)
	var res []models.HouseContractRecord
	for _, c := range r.contracts {
		if c.HouseholdID != s.HouseholdID() {
			continue
		}
		if c.ExpirationOrRenewalDate == nil {
			continue
		}
		if c.ExpirationOrRenewalDate.After(now.Add(-24*time.Hour)) && c.ExpirationOrRenewalDate.Before(cutoff) {
			res = append(res, *c)
		}
	}
	return res, nil
}

func (r *fakeHouseRepo) CreateMaintenanceItem(s identity.Scope, m *models.HomeMaintenanceItem) error {
	m.HouseholdID, m.FamilyGroup = s.HouseholdID(), s.LegacyLabel()
	r.mu.Lock()
	defer r.mu.Unlock()
	m.ID = r.nextMaint
	r.nextMaint++
	cp := *m
	r.maintenance[m.ID] = &cp
	return nil
}

func (r *fakeHouseRepo) GetMaintenanceItem(s identity.Scope, id uint) (*models.HomeMaintenanceItem, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.maintenance[id]
	if !ok {
		return nil, nil
	}
	if m.HouseholdID != s.HouseholdID() {
		return nil, nil
	}
	cp := *m
	return &cp, nil
}

func (r *fakeHouseRepo) UpdateMaintenanceItem(s identity.Scope, m *models.HomeMaintenanceItem) error {
	m.HouseholdID, m.FamilyGroup = s.HouseholdID(), s.LegacyLabel()
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *m
	r.maintenance[m.ID] = &cp
	return nil
}

func (r *fakeHouseRepo) DeleteMaintenanceItem(_ identity.Scope, id uint) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.maintenance, id)
	return nil
}

func (r *fakeHouseRepo) ListMaintenanceItems(s identity.Scope, category string, overdueOnly bool) ([]models.HomeMaintenanceItem, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	now := time.Now()
	category = strings.ToLower(strings.TrimSpace(category))
	var res []models.HomeMaintenanceItem
	for _, m := range r.maintenance {
		if m.HouseholdID != s.HouseholdID() {
			continue
		}
		if category != "" && category != "all" && m.Category != category {
			continue
		}
		if overdueOnly && !now.After(m.NextDueAt) {
			continue
		}
		res = append(res, *m)
	}
	return res, nil
}

func (r *fakeHouseRepo) CompleteMaintenanceItem(s identity.Scope, id uint, completedAt time.Time, userID string) (*models.HomeMaintenanceItem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.maintenance[id]
	if !ok {
		return nil, assert.AnError
	}
	if m.HouseholdID != s.HouseholdID() {
		return nil, fmt.Errorf("maintenance item %d not found in family %q", id, s.LegacyLabel())
	}
	m.MarkCompleted(completedAt, userID)
	cp := *m
	return &cp, nil
}

func (r *fakeHouseRepo) CreateVehicleAsset(s identity.Scope, v *models.VehicleAsset) error {
	v.HouseholdID, v.FamilyGroup = s.HouseholdID(), s.LegacyLabel()
	r.mu.Lock()
	defer r.mu.Unlock()
	v.ID = r.nextVehicle
	r.nextVehicle++
	cp := *v
	r.vehicles[v.ID] = &cp
	return nil
}

func (r *fakeHouseRepo) GetVehicleAsset(_ identity.Scope, id uint) (*models.VehicleAsset, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.vehicles[id]
	if !ok {
		return nil, nil
	}
	cp := *v
	return &cp, nil
}

func (r *fakeHouseRepo) UpdateVehicleAsset(s identity.Scope, v *models.VehicleAsset) error {
	v.HouseholdID, v.FamilyGroup = s.HouseholdID(), s.LegacyLabel()
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *v
	r.vehicles[v.ID] = &cp
	return nil
}

func (r *fakeHouseRepo) DeleteVehicleAsset(_ identity.Scope, id uint) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.vehicles, id)
	return nil
}

func (r *fakeHouseRepo) ListVehicleAssets(s identity.Scope) ([]models.VehicleAsset, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var res []models.VehicleAsset
	for _, v := range r.vehicles {
		if v.HouseholdID != s.HouseholdID() {
			continue
		}
		res = append(res, *v)
	}
	return res, nil
}

func (r *fakeHouseRepo) UpdateVehicleMileage(_ identity.Scope, id uint, mileageKm int) (*models.VehicleAsset, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.vehicles[id]
	if !ok {
		return nil, assert.AnError
	}
	v.CurrentMileageKm = mileageKm
	cp := *v
	return &cp, nil
}

var _ db.HouseRepository = (*fakeHouseRepo)(nil)

// TestHouseModels verifies model helper methods and date calculations.
func TestHouseModels(t *testing.T) {
	now := time.Now()
	expiry := now.AddDate(0, 0, 45) // 45 days in future

	// 1. HouseContractRecord
	contract := models.HouseContractRecord{
		RecordType:              models.HouseRecordLease,
		Title:                   "Downtown Apartment",
		NoticePeriodDays:        30,
		ExpirationOrRenewalDate: &expiry,
	}

	require.NoError(t, contract.SetKeyTerms(map[string]interface{}{"deposit": 3000, "pets": false}))
	terms := contract.KeyTerms()
	assert.Equal(t, float64(3000), terms["deposit"])
	assert.Equal(t, false, terms["pets"])

	deadline := contract.NoticeDeadline()
	require.NotNil(t, deadline)
	assert.Equal(t, expiry.AddDate(0, 0, -30).Format("2006-01-02"), deadline.Format("2006-01-02"))

	assert.True(t, contract.IsExpiringSoon(now, 60))
	assert.False(t, contract.IsExpiringSoon(now, 10))

	// 2. HomeMaintenanceItem
	duePast := now.AddDate(0, 0, -5)
	item := models.HomeMaintenanceItem{
		TaskName:     "HVAC filter",
		Category:     models.MaintenanceHVAC,
		IntervalDays: 90,
		NextDueAt:    duePast,
	}
	assert.True(t, item.IsOverdue(now))

	completedAt := now
	item.MarkCompleted(completedAt, "user-alice")
	assert.False(t, item.IsOverdue(now))
	assert.Equal(t, "user-alice", item.UpdatedByUserID)
	require.NotNil(t, item.LastCompletedAt)
	assert.Equal(t, completedAt.AddDate(0, 0, 90).Format("2006-01-02"), item.NextDueAt.Format("2006-01-02"))

	// 3. VehicleAsset
	inspDate := now.AddDate(0, 0, 20)
	insDate := now.AddDate(0, 0, 15)
	vehicle := models.VehicleAsset{
		Name:                 "Mazda CX-5",
		CurrentMileageKm:     52000,
		NextServiceMileageKm: 50000,
		AnnualTestExpiry:     &inspDate,
		InsuranceExpiry:      &insDate,
	}
	assert.True(t, vehicle.IsServiceDue())
	assert.True(t, vehicle.IsInspectionDue(now, 30))
	assert.False(t, vehicle.IsInspectionDue(now, 10))
	assert.True(t, vehicle.IsInsuranceExpiring(now, 20))
	assert.False(t, vehicle.IsInsuranceExpiring(now, 5))
}

// TestModule_Declarations verifies module name and tool schemas.
func TestModule_Declarations(t *testing.T) {
	repo := newFakeHouseRepo()
	mod := NewModule(repo)

	assert.Equal(t, "house", mod.Name())

	decls := mod.Declarations()
	require.Len(t, decls, 4)

	toolNames := make(map[string]bool)
	for _, d := range decls {
		toolNames[d.Name] = true
	}
	assert.True(t, toolNames["save_house_contract_or_receipt"])
	assert.True(t, toolNames["list_house_contracts"])
	assert.True(t, toolNames["manage_home_maintenance"])
	assert.True(t, toolNames["manage_vehicle_asset"])
}

// TestModule_SaveAndListContracts tests creating, updating, and querying contracts.
func TestModule_SaveAndListContracts(t *testing.T) {
	ctx := context.Background()
	repo := newFakeHouseRepo()
	eventBus := events.NewEventBus()

	var capturedEvents []events.MutationEvent
	eventBus.SubscribeTopic("house", func(ctx context.Context, ev events.MutationEvent) error {
		capturedEvents = append(capturedEvents, ev)
		return nil
	})

	mod := NewModule(repo, WithEventBus(eventBus))
	callCtx := withScope(orchestrator.CallContext{
		User: &models.User{
			ID:          "user-bob",
			FamilyGroup: "family-smith",
		},
		EventBus: eventBus,
	})

	// 1. Save lease contract
	saveArgs := map[string]interface{}{
		"record_type":        "lease",
		"title":              "Smith Apartment Lease",
		"vendor_or_landlord": "Greenfield Realty",
		"contact_details":    "office@greenfield.com",
		"amount":             4800.0,
		"currency":           "USD",
		"start_date":         "2026-01-01",
		"expiration_date":    "2026-12-31",
		"notice_period_days": 60,
		"key_terms":          "No smoking, 2 months deposit",
	}

	out, err := mod.Execute(ctx, callCtx, "save_house_contract_or_receipt", saveArgs)
	require.NoError(t, err)
	assert.Contains(t, out, "Smith Apartment Lease")
	assert.Contains(t, out, "Greenfield Realty")
	assert.Contains(t, out, "Notice Deadline")

	// 2. Save refrigerator warranty
	saveWarranty := map[string]interface{}{
		"record_type":        "warranty",
		"title":              "Samsung Fridge 5-Year Warranty",
		"vendor_or_landlord": "Samsung Support",
		"expiration_date":    time.Now().AddDate(0, 0, 20).Format("2006-01-02"),
		"amount":             120.0,
	}
	outWarranty, err := mod.Execute(ctx, callCtx, "save_house_contract_or_receipt", saveWarranty)
	require.NoError(t, err)
	assert.Contains(t, outWarranty, "Samsung Fridge 5-Year Warranty")

	// Verify events
	require.Len(t, capturedEvents, 2)
	assert.Equal(t, "house", capturedEvents[0].Topic)
	assert.Equal(t, "create", capturedEvents[0].Action, "BL-68: logged as the change undo reverses")
	assert.Equal(t, "contract_saved", capturedEvents[0].Label())

	// 3. Update lease contract
	updateArgs := map[string]interface{}{
		"contract_id": 1,
		"record_type": "lease",
		"title":       "Smith Apartment Lease (Renewed)",
		"amount":      5000.0,
	}
	outUpdate, err := mod.Execute(ctx, callCtx, "save_house_contract_or_receipt", updateArgs)
	require.NoError(t, err)
	assert.Contains(t, outUpdate, "Smith Apartment Lease (Renewed)")
	assert.Contains(t, outUpdate, "5000.00 USD")

	// 4. List all contracts
	listOut, err := mod.Execute(ctx, callCtx, "list_house_contracts", map[string]interface{}{})
	require.NoError(t, err)
	assert.Contains(t, listOut, "Smith Apartment Lease (Renewed)")
	assert.Contains(t, listOut, "Samsung Fridge 5-Year Warranty")

	// 5. List with type filter
	listTypeOut, err := mod.Execute(ctx, callCtx, "list_house_contracts", map[string]interface{}{"record_type": "warranty"})
	require.NoError(t, err)
	assert.Contains(t, listTypeOut, "Samsung Fridge 5-Year Warranty")
	assert.NotContains(t, listTypeOut, "Smith Apartment Lease")

	// 6. List with expiring_within_days filter
	listExpOut, err := mod.Execute(ctx, callCtx, "list_house_contracts", map[string]interface{}{"expiring_within_days": 30})
	require.NoError(t, err)
	assert.Contains(t, listExpOut, "Samsung Fridge 5-Year Warranty")

	// 7. Search query
	listSearchOut, err := mod.Execute(ctx, callCtx, "list_house_contracts", map[string]interface{}{"search_query": "samsung"})
	require.NoError(t, err)
	assert.Contains(t, listSearchOut, "Samsung Fridge 5-Year Warranty")
	assert.NotContains(t, listSearchOut, "Smith Apartment Lease")
}

// TestModule_HomeMaintenance tests creating, listing, updating, completing, deleting maintenance items and seasonal suggestions.
func TestModule_HomeMaintenance(t *testing.T) {
	ctx := context.Background()
	repo := newFakeHouseRepo()
	eventBus := events.NewEventBus()

	var eventsLogged []events.MutationEvent
	eventBus.SubscribeTopic("house", func(ctx context.Context, ev events.MutationEvent) error {
		eventsLogged = append(eventsLogged, ev)
		return nil
	})

	mod := NewModule(repo, WithEventBus(eventBus))
	callCtx := withScope(orchestrator.CallContext{
		User: &models.User{
			ID:          "user-carol",
			FamilyGroup: "family-smith",
		},
		EventBus: eventBus,
	})

	// 1. Create maintenance item
	createArgs := map[string]interface{}{
		"action":          "create",
		"task_name":       "Clean HVAC Filters",
		"category":        "hvac",
		"interval_days":   90,
		"contractor_info": "Avi HVAC (052-1234567)",
		"notes":           "Replace 16x25x1 pleated filter in hallway unit",
	}

	outCreate, err := mod.Execute(ctx, callCtx, "manage_home_maintenance", createArgs)
	require.NoError(t, err)
	assert.Contains(t, outCreate, "Clean HVAC Filters")
	assert.Contains(t, outCreate, "Every 90 days")

	// 2. List maintenance items
	outList, err := mod.Execute(ctx, callCtx, "manage_home_maintenance", map[string]interface{}{"action": "list"})
	require.NoError(t, err)
	assert.Contains(t, outList, "Clean HVAC Filters")
	assert.Contains(t, outList, "HVAC")

	// 3. Update maintenance item
	outUpdate, err := mod.Execute(ctx, callCtx, "manage_home_maintenance", map[string]interface{}{
		"action":        "update",
		"item_id":       1,
		"interval_days": 60,
		"task_name":     "Clean & Disinfect HVAC Filters",
	})
	require.NoError(t, err)
	assert.Contains(t, outUpdate, "Clean & Disinfect HVAC Filters")
	assert.Contains(t, outUpdate, "60 days")

	// 4. Complete maintenance item
	outComp, err := mod.Execute(ctx, callCtx, "manage_home_maintenance", map[string]interface{}{
		"action":  "complete",
		"item_id": 1,
	})
	require.NoError(t, err)
	assert.Contains(t, outComp, "Marked Maintenance Task as Completed")
	assert.Contains(t, outComp, "Next Due Date Rolled Forward")

	// 5. Add an overdue item and test list overdue_only
	pastDate := time.Now().AddDate(0, 0, -10).Format("2006-01-02")
	_, err = mod.Execute(ctx, callCtx, "manage_home_maintenance", map[string]interface{}{
		"action":      "create",
		"task_name":   "Descale Water Heater",
		"category":    "plumbing",
		"next_due_at": pastDate,
	})
	require.NoError(t, err)

	outOverdue, err := mod.Execute(ctx, callCtx, "manage_home_maintenance", map[string]interface{}{
		"action":       "list",
		"overdue_only": true,
	})
	require.NoError(t, err)
	assert.Contains(t, outOverdue, "Descale Water Heater")
	assert.NotContains(t, outOverdue, "Clean & Disinfect HVAC Filters") // rolled forward, so not overdue

	// 6. Seasonal suggestions
	outSugg, err := mod.Execute(ctx, callCtx, "manage_home_maintenance", map[string]interface{}{
		"action": "suggest_seasonal",
	})
	require.NoError(t, err)
	assert.Contains(t, outSugg, "Seasonal Home Maintenance Suggestions")
	assert.Contains(t, outSugg, "Upkeep Tip")

	// 7. Delete task
	outDel, err := mod.Execute(ctx, callCtx, "manage_home_maintenance", map[string]interface{}{
		"action":  "delete",
		"item_id": 2,
	})
	require.NoError(t, err)
	assert.Contains(t, outDel, "Deleted maintenance task 2")
}

// TestModule_VehicleVault tests vehicle asset lifecycle, mileage updates, and service logging.
func TestModule_VehicleVault(t *testing.T) {
	ctx := context.Background()
	repo := newFakeHouseRepo()
	eventBus := events.NewEventBus()

	mod := NewModule(repo, WithEventBus(eventBus))
	callCtx := withScope(orchestrator.CallContext{
		User: &models.User{
			ID:          "user-dave",
			FamilyGroup: "family-smith",
		},
		EventBus: eventBus,
	})

	testExpiry := time.Now().AddDate(0, 0, 25).Format("2006-01-02")
	insExpiry := time.Now().AddDate(0, 0, 10).Format("2006-01-02")

	// 1. Create vehicle asset
	createVehicle := map[string]interface{}{
		"action":                  "create",
		"name":                    "Family Toyota RAV4",
		"license_plate":           "789-12-345",
		"insurance_expiry":        insExpiry,
		"annual_test_expiry":      testExpiry,
		"current_mileage_km":      42000,
		"next_service_mileage_km": 45000,
		"notes":                   "Michelin CrossClimate 2 tires",
	}

	outCreate, err := mod.Execute(ctx, callCtx, "manage_vehicle_asset", createVehicle)
	require.NoError(t, err)
	assert.Contains(t, outCreate, "Family Toyota RAV4")
	assert.Contains(t, outCreate, "789-12-345")
	assert.Contains(t, outCreate, "42000 km")

	// 2. List vehicles
	outList, err := mod.Execute(ctx, callCtx, "manage_vehicle_asset", map[string]interface{}{"action": "list"})
	require.NoError(t, err)
	assert.Contains(t, outList, "Family Toyota RAV4")
	assert.Contains(t, outList, "Service OK")

	// 3. Update mileage below threshold
	outKm1, err := mod.Execute(ctx, callCtx, "manage_vehicle_asset", map[string]interface{}{
		"action":             "update_mileage",
		"asset_id":           1,
		"current_mileage_km": 44000,
	})
	require.NoError(t, err)
	assert.Contains(t, outKm1, "44000 km")
	assert.NotContains(t, outKm1, "Service Due Warning")

	// 4. Update mileage above threshold -> service warning
	outKm2, err := mod.Execute(ctx, callCtx, "manage_vehicle_asset", map[string]interface{}{
		"action":             "update_mileage",
		"asset_id":           1,
		"current_mileage_km": 45500,
	})
	require.NoError(t, err)
	assert.Contains(t, outKm2, "45500 km")
	assert.Contains(t, outKm2, "Service Due Warning")

	// 5. Log service
	outService, err := mod.Execute(ctx, callCtx, "manage_vehicle_asset", map[string]interface{}{
		"action":                  "log_service",
		"asset_id":                1,
		"current_mileage_km":      45600,
		"next_service_mileage_km": 60000,
		"notes":                   "Oil & filter changed, brake pads checked",
	})
	require.NoError(t, err)
	assert.Contains(t, outService, "Service Logged for Family Toyota RAV4")
	assert.Contains(t, outService, "60000 km")

	// 6. Delete vehicle
	outDel, err := mod.Execute(ctx, callCtx, "manage_vehicle_asset", map[string]interface{}{
		"action":   "delete",
		"asset_id": 1,
	})
	require.NoError(t, err)
	assert.Contains(t, outDel, "Deleted vehicle asset 1")
}

// TestModule_SystemPromptSection verifies dynamic injection into system prompt.
func TestModule_SystemPromptSection(t *testing.T) {
	ctx := context.Background()
	repo := newFakeHouseRepo()
	mod := NewModule(repo)

	callCtx := withScope(orchestrator.CallContext{
		User: &models.User{
			ID:          "user-alice",
			FamilyGroup: "family-jones",
		},
	})

	// 1. Empty repository
	promptEmpty := mod.SystemPromptSection(ctx, callCtx)
	assert.Contains(t, promptEmpty, "All house contracts, preventive home maintenance, and vehicle tests are currently up to date.")

	// 2. Add expiring contract
	expiry := time.Now().AddDate(0, 0, 20)
	_ = repo.CreateContract(callCtx.Scope, &models.HouseContractRecord{
		FamilyGroup:             "family-jones",
		RecordType:              "lease",
		Title:                   "Rental Lease",
		VendorOrLandlord:        "Jones Landlord",
		ExpirationOrRenewalDate: &expiry,
		NoticePeriodDays:        30,
	})

	// 3. Add overdue maintenance item
	pastDue := time.Now().AddDate(0, 0, -3)
	_ = repo.CreateMaintenanceItem(callCtx.Scope, &models.HomeMaintenanceItem{
		FamilyGroup: "family-jones",
		TaskName:    "Clean Gutter",
		Category:    "general",
		NextDueAt:   pastDue,
	})

	// 4. Add vehicle with expiring test
	testExp := time.Now().AddDate(0, 0, 15).Add(time.Hour)
	_ = repo.CreateVehicleAsset(callCtx.Scope, &models.VehicleAsset{
		FamilyGroup:      "family-jones",
		Name:             "Subaru Outback",
		LicensePlate:     "44-555-66",
		AnnualTestExpiry: &testExp,
	})

	promptFull := mod.SystemPromptSection(ctx, callCtx)
	assert.Contains(t, promptFull, "Upcoming Contract & Warranty Deadlines")
	assert.Contains(t, promptFull, "Rental Lease")
	assert.Contains(t, promptFull, "Notice deadline PASSED")
	assert.Contains(t, promptFull, "Pending Home Maintenance Items")
	assert.Contains(t, promptFull, "Clean Gutter")
	assert.Contains(t, promptFull, "Vehicle Alerts")
	assert.Contains(t, promptFull, "Subaru Outback")
	assert.Contains(t, promptFull, "Annual test due in 15 days")
}

// TestModule_ErrorHandling verifies input validation and error states.
func TestModule_ErrorHandling(t *testing.T) {
	ctx := context.Background()
	repo := newFakeHouseRepo()
	mod := NewModule(repo)

	callCtx := withScope(orchestrator.CallContext{
		User: &models.User{
			ID:          "user-alice",
			FamilyGroup: "family-jones",
		},
	})

	// Unknown tool
	_, err := mod.Execute(ctx, callCtx, "non_existent_tool", map[string]interface{}{})
	assert.Error(t, err)

	// Missing title in save contract
	_, err = mod.Execute(ctx, callCtx, "save_house_contract_or_receipt", map[string]interface{}{"record_type": "lease"})
	assert.Error(t, err)

	// Missing task_name in create maintenance
	_, err = mod.Execute(ctx, callCtx, "manage_home_maintenance", map[string]interface{}{"action": "create"})
	assert.Error(t, err)

	// Missing name in create vehicle
	_, err = mod.Execute(ctx, callCtx, "manage_vehicle_asset", map[string]interface{}{"action": "create"})
	assert.Error(t, err)

	// Unknown maintenance action
	_, err = mod.Execute(ctx, callCtx, "manage_home_maintenance", map[string]interface{}{"action": "invalid_action"})
	assert.Error(t, err)

	// Unknown vehicle action
	_, err = mod.Execute(ctx, callCtx, "manage_vehicle_asset", map[string]interface{}{"action": "invalid_action"})
	assert.Error(t, err)
}
