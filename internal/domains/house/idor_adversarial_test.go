package house_test

import (
	"context"
	"fmt"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/identity/identitytest"
	"strings"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/domains/house"
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

// fakeRepoForIDOR provides an in-memory implementation for IDOR stress testing.
type fakeRepoForIDOR struct {
	contracts    map[uint]*models.HouseContractRecord
	maintenance  map[uint]*models.HomeMaintenanceItem
	vehicles     map[uint]*models.VehicleAsset
	nextContract uint
	nextMaint    uint
	nextVehicle  uint
}

func newFakeRepoForIDOR() *fakeRepoForIDOR {
	return &fakeRepoForIDOR{
		contracts:    make(map[uint]*models.HouseContractRecord),
		maintenance:  make(map[uint]*models.HomeMaintenanceItem),
		vehicles:     make(map[uint]*models.VehicleAsset),
		nextContract: 1,
		nextMaint:    1,
		nextVehicle:  1,
	}
}

func (r *fakeRepoForIDOR) CreateContract(s identity.Scope, c *models.HouseContractRecord) error {
	c.HouseholdID, c.FamilyGroup = s.HouseholdID(), s.LegacyLabel()
	c.ID = r.nextContract
	r.nextContract++
	cp := *c
	r.contracts[c.ID] = &cp
	return nil
}
func (r *fakeRepoForIDOR) GetContract(s identity.Scope, id uint) (*models.HouseContractRecord, error) {
	c, ok := r.contracts[id]
	if !ok {
		return nil, nil
	}
	if c.HouseholdID != s.HouseholdID() {
		return nil, nil
	}
	cp := *c
	return &cp, nil
}
func (r *fakeRepoForIDOR) UpdateContract(s identity.Scope, c *models.HouseContractRecord) error {
	c.HouseholdID, c.FamilyGroup = s.HouseholdID(), s.LegacyLabel()
	cp := *c
	r.contracts[c.ID] = &cp
	return nil
}
func (r *fakeRepoForIDOR) DeleteContract(_ identity.Scope, id uint) error {
	delete(r.contracts, id)
	return nil
}
func (r *fakeRepoForIDOR) ListContracts(s identity.Scope, recordType string, expiringBefore *time.Time) ([]models.HouseContractRecord, error) {
	var list []models.HouseContractRecord
	for _, c := range r.contracts {
		if c.HouseholdID != s.HouseholdID() {
			continue
		}
		list = append(list, *c)
	}
	return list, nil
}
func (r *fakeRepoForIDOR) ListExpiringContracts(s identity.Scope, withinDays int) ([]models.HouseContractRecord, error) {
	var list []models.HouseContractRecord
	for _, c := range r.contracts {
		if c.HouseholdID != s.HouseholdID() {
			continue
		}
		list = append(list, *c)
	}
	return list, nil
}
func (r *fakeRepoForIDOR) CreateMaintenanceItem(s identity.Scope, m *models.HomeMaintenanceItem) error {
	m.HouseholdID, m.FamilyGroup = s.HouseholdID(), s.LegacyLabel()
	m.ID = r.nextMaint
	r.nextMaint++
	cp := *m
	r.maintenance[m.ID] = &cp
	return nil
}
func (r *fakeRepoForIDOR) GetMaintenanceItem(s identity.Scope, id uint) (*models.HomeMaintenanceItem, error) {
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
func (r *fakeRepoForIDOR) UpdateMaintenanceItem(s identity.Scope, m *models.HomeMaintenanceItem) error {
	m.HouseholdID, m.FamilyGroup = s.HouseholdID(), s.LegacyLabel()
	cp := *m
	r.maintenance[m.ID] = &cp
	return nil
}
func (r *fakeRepoForIDOR) DeleteMaintenanceItem(_ identity.Scope, id uint) error {
	delete(r.maintenance, id)
	return nil
}
func (r *fakeRepoForIDOR) ListMaintenanceItems(s identity.Scope, category string, overdueOnly bool) ([]models.HomeMaintenanceItem, error) {
	var list []models.HomeMaintenanceItem
	for _, m := range r.maintenance {
		if m.HouseholdID != s.HouseholdID() {
			continue
		}
		list = append(list, *m)
	}
	return list, nil
}
func (r *fakeRepoForIDOR) CompleteMaintenanceItem(s identity.Scope, id uint, completedAt time.Time, userID string) (*models.HomeMaintenanceItem, error) {
	item, err := r.GetMaintenanceItem(s, id)
	if err != nil || item == nil {
		return nil, fmt.Errorf("maintenance item %d not found in family", id)
	}
	m := r.maintenance[id]
	m.MarkCompleted(completedAt, userID)
	cp := *m
	return &cp, nil
}
func (r *fakeRepoForIDOR) CreateVehicleAsset(s identity.Scope, v *models.VehicleAsset) error {
	v.HouseholdID, v.FamilyGroup = s.HouseholdID(), s.LegacyLabel()
	v.ID = r.nextVehicle
	r.nextVehicle++
	cp := *v
	r.vehicles[v.ID] = &cp
	return nil
}
func (r *fakeRepoForIDOR) GetVehicleAsset(_ identity.Scope, id uint) (*models.VehicleAsset, error) {
	v, ok := r.vehicles[id]
	if !ok {
		return nil, nil
	}
	cp := *v
	return &cp, nil
}
func (r *fakeRepoForIDOR) UpdateVehicleAsset(s identity.Scope, v *models.VehicleAsset) error {
	v.HouseholdID, v.FamilyGroup = s.HouseholdID(), s.LegacyLabel()
	cp := *v
	r.vehicles[v.ID] = &cp
	return nil
}
func (r *fakeRepoForIDOR) DeleteVehicleAsset(_ identity.Scope, id uint) error {
	delete(r.vehicles, id)
	return nil
}
func (r *fakeRepoForIDOR) ListVehicleAssets(s identity.Scope) ([]models.VehicleAsset, error) {
	var list []models.VehicleAsset
	for _, v := range r.vehicles {
		if v.HouseholdID != s.HouseholdID() {
			continue
		}
		list = append(list, *v)
	}
	return list, nil
}
func (r *fakeRepoForIDOR) UpdateVehicleMileage(_ identity.Scope, id uint, mileageKm int) (*models.VehicleAsset, error) {
	v, ok := r.vehicles[id]
	if !ok {
		return nil, assert.AnError
	}
	v.CurrentMileageKm = mileageKm
	cp := *v
	return &cp, nil
}

// TestSEC04_HouseDeclarations_NoTenantOverrides verifies tool declarations do not accept family_group overrides.
func TestSEC04_HouseDeclarations_NoTenantOverrides(t *testing.T) {
	mod := house.NewModule(newFakeRepoForIDOR())
	for _, decl := range mod.Declarations() {
		if decl.Parameters != nil && decl.Parameters.Properties != nil {
			assert.Nil(t, decl.Parameters.Properties["family_group"], "tool %s should not declare family_group parameter", decl.Name)
			assert.Nil(t, decl.Parameters.Properties["family_id"], "tool %s should not declare family_id parameter", decl.Name)
			assert.Nil(t, decl.Parameters.Properties["user_id"], "tool %s should not declare user_id parameter", decl.Name)
		}
	}
}

// TestSEC04_CrossTenant_MaintenanceComplete_IDOR_Vulnerability tests whether user in Family Beta
// can complete a maintenance task owned by Family Alpha.
func TestSEC04_CrossTenant_MaintenanceComplete_IDOR_Vulnerability(t *testing.T) {
	repo := newFakeRepoForIDOR()
	mod := house.NewModule(repo)
	ctx := context.Background()

	// 1. Family Alpha creates a maintenance item
	userAlpha := &models.User{ID: "u1", PlatformID: "user_alpha", FamilyGroup: "family_alpha"}
	callCtxAlpha := withScope(orchestrator.CallContext{User: userAlpha, Message: channel.Inbound{Sender: "user_alpha"}})
	createResp, err := mod.Execute(ctx, callCtxAlpha, "manage_home_maintenance", map[string]interface{}{
		"action":        "create",
		"task_name":     "Replace HVAC Filters",
		"category":      "hvac",
		"interval_days": 90,
	})
	require.NoError(t, err)
	assert.Contains(t, createResp, "Home Maintenance Task Created")

	// Verify item 1 exists in family_alpha
	item1, err := repo.GetMaintenanceItem(callCtxAlpha.Scope, 1)
	require.NoError(t, err)
	require.NotNil(t, item1)
	assert.Equal(t, "family_alpha", item1.FamilyGroup)
	assert.Nil(t, item1.LastCompletedAt)

	// 2. Family Beta attempts to complete Family Alpha's task (item_id: 1)
	userBeta := &models.User{ID: "u2", PlatformID: "user_beta", FamilyGroup: "family_beta"}
	callCtxBeta := withScope(orchestrator.CallContext{User: userBeta, Message: channel.Inbound{Sender: "user_beta"}})
	_, completeErr := mod.Execute(ctx, callCtxBeta, "manage_home_maintenance", map[string]interface{}{
		"action":  "complete",
		"item_id": 1,
	})

	// Check that complete action was strictly blocked across tenants
	assert.Error(t, completeErr, "Family Beta must NOT be able to complete Family Alpha's maintenance task")
	assert.Contains(t, completeErr.Error(), "not found in family")
}

// TestSEC04_CrossTenant_MaintenanceUpdateDelete_Blocked tests that update and delete are properly blocked.
func TestSEC04_CrossTenant_MaintenanceUpdateDelete_Blocked(t *testing.T) {
	repo := newFakeRepoForIDOR()
	mod := house.NewModule(repo)
	ctx := context.Background()

	// Family Alpha creates item
	userAlpha := &models.User{ID: "u1", PlatformID: "user_alpha", FamilyGroup: "family_alpha"}
	callCtxAlpha := withScope(orchestrator.CallContext{User: userAlpha, Message: channel.Inbound{Sender: "user_alpha"}})
	_, err := mod.Execute(ctx, callCtxAlpha, "manage_home_maintenance", map[string]interface{}{
		"action":    "create",
		"task_name": "Fix Roof",
	})
	require.NoError(t, err)

	// Family Beta attempts update
	userBeta := &models.User{ID: "u2", PlatformID: "user_beta", FamilyGroup: "family_beta"}
	callCtxBeta := withScope(orchestrator.CallContext{User: userBeta, Message: channel.Inbound{Sender: "user_beta"}})
	_, updateErr := mod.Execute(ctx, callCtxBeta, "manage_home_maintenance", map[string]interface{}{
		"action":    "update",
		"item_id":   1,
		"task_name": "Hacked Roof",
	})
	assert.Error(t, updateErr, "updating item across family groups must fail")
	assert.Contains(t, updateErr.Error(), "not found in family")

	// Family Beta attempts delete
	_, deleteErr := mod.Execute(ctx, callCtxBeta, "manage_home_maintenance", map[string]interface{}{
		"action":  "delete",
		"item_id": 1,
	})
	assert.Error(t, deleteErr, "deleting item across family groups must fail")
	assert.Contains(t, deleteErr.Error(), "not found in family")
}

// TestSEC04_EmptyFamilyGroup_StrictUserIsolation tests that users with empty family_group are isolated.
func TestSEC04_EmptyFamilyGroup_StrictUserIsolation(t *testing.T) {
	repo := newFakeRepoForIDOR()
	mod := house.NewModule(repo)
	ctx := context.Background()

	// User Solo1 (empty family group)
	userSolo1 := &models.User{ID: "u10", PlatformID: "solo_1", FamilyGroup: ""}
	callCtxSolo1 := withScope(orchestrator.CallContext{User: userSolo1, Message: channel.Inbound{Sender: "solo_1"}})

	// Solo1 creates vehicle
	resp1, err := mod.Execute(ctx, callCtxSolo1, "manage_vehicle_asset", map[string]interface{}{
		"action": "create",
		"name":   "Solo1 Electric Bike",
	})
	require.NoError(t, err)
	assert.Contains(t, resp1, "Vehicle Asset Registered")

	// User Solo2 (also empty family group)
	userSolo2 := &models.User{ID: "u20", PlatformID: "solo_2", FamilyGroup: ""}
	callCtxSolo2 := withScope(orchestrator.CallContext{User: userSolo2, Message: channel.Inbound{Sender: "solo_2"}})

	// Solo2 attempts to update Solo1's vehicle (asset_id: 1)
	_, solo2Err := mod.Execute(ctx, callCtxSolo2, "manage_vehicle_asset", map[string]interface{}{
		"action":   "update",
		"asset_id": 1,
		"name":     "Stolen Electric Bike",
	})
	assert.Error(t, solo2Err, "Solo2 must NOT be able to modify Solo1's vehicle even though both have empty family_group")

	// Solo2 lists vehicles — should NOT see Solo1's vehicle
	listResp, err := mod.Execute(ctx, callCtxSolo2, "manage_vehicle_asset", map[string]interface{}{
		"action": "list",
	})
	require.NoError(t, err)
	assert.NotContains(t, listResp, "Solo1 Electric Bike", "Solo2 must not see Solo1's vehicle in list")
}
