package graph

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/graph/model"
	dbmodels "github.com/nivik/mypa/internal/models"
)

// Helper ID conversions
func uintToStr(id uint) string {
	return strconv.FormatUint(uint64(id), 10)
}

func strToUint(s string) uint {
	val, _ := strconv.ParseUint(s, 10, 64)
	return uint(val)
}

func tableNameForEntity(entityType string) string {
	norm := strings.ToLower(entityType)
	switch norm {
	case "topicmemory", "topic_memory", "topic_memories":
		return "topic_memories"
	case "entityprofile", "entity_profile", "entity_profiles":
		return "entity_profiles"
	case "sharedspace", "shared_space", "shared_spaces":
		return "shared_spaces"
	case "sharedtaskreminder", "shared_task", "shared_task_reminders":
		return "shared_task_reminders"
	case "scheduleevent", "schedule_event", "schedule_events":
		return "schedule_events"
	case "topiccalendarconfig", "topic_calendar_config", "topic_calendar_configs":
		return "topic_calendar_configs"
	case "babyprofile", "baby_profile", "baby_profiles":
		return "baby_profiles"
	case "babycarelog", "baby_care_log", "baby_care_logs":
		return "baby_care_logs"
	case "babyinventoryitem", "baby_inventory_item", "baby_inventory_items":
		return "baby_inventory_items"
	case "housecontractrecord", "house_contract_record", "house_contracts":
		return "house_contract_records"
	case "homemaintenanceitem", "home_maintenance_item", "home_maintenance_items":
		return "home_maintenance_items"
	case "vehicleasset", "vehicle_asset", "vehicle_assets":
		return "vehicle_assets"
	case "financialtransaction", "financial_transaction", "financial_transactions":
		return "financial_transactions"
	case "categorybudget", "category_budget", "category_budgets":
		return "category_budgets"
	case "recurringbill", "recurring_bill", "recurring_bills":
		return "recurring_bills"
	case "externalapiconnector", "external_api_connector", "external_api_connectors":
		return "external_api_connectors"
	case "automationrule", "automation_rule", "automation_rules":
		return "automation_rules"
	case "userpreference", "user_preference", "user_preferences":
		return "user_preferences"
	case "personalcrmcontact", "personal_crm_contact", "personal_crm_contacts":
		return "personal_crm_contacts"
	default:
		return norm
	}
}

// ----------------- Mutation Resolvers -----------------

// GeneratePairingCode is the resolver for the generatePairingCode field.
func (r *mutationResolver) GeneratePairingCode(ctx context.Context, role string, familyGroup string) (*model.PairingCode, error) {
	n, _ := rand.Int(rand.Reader, big.NewInt(1000000))
	code := fmt.Sprintf("%06d", n.Int64())

	pc := dbmodels.PairingCode{
		Code:        code,
		Role:        role,
		FamilyGroup: familyGroup,
		ExpiresAt:   time.Now().Add(24 * time.Hour),
		IsUsed:      false,
	}

	if err := r.DB.CreatePairingCode(pc); err != nil {
		return nil, err
	}

	return &model.PairingCode{
		ID:          pc.ID,
		Code:        pc.Code,
		Role:        pc.Role,
		FamilyGroup: pc.FamilyGroup,
		ExpiresAt:   pc.ExpiresAt,
		IsUsed:      pc.IsUsed,
		CreatedAt:   pc.CreatedAt,
	}, nil
}

func (r *mutationResolver) SaveTopicMemory(ctx context.Context, topic string, subtopic string, fact string, tagsJSON *string, scope *string) (*model.TopicMemory, error) {
	tJSON := "[]"
	if tagsJSON != nil && *tagsJSON != "" {
		tJSON = *tagsJSON
	}
	sc := "personal"
	if scope != nil && *scope != "" {
		sc = *scope
	}

	mem := dbmodels.TopicMemory{
		Topic:           topic,
		Subtopic:        subtopic,
		Fact:            fact,
		TagsJSON:        tJSON,
		Scope:           sc,
		ValidFrom:       time.Now(),
		CreatedAt:       time.Now(),
		CreatedByUserID: "user",
	}

	if err := r.DB.DB.Create(&mem).Error; err != nil {
		return nil, err
	}

	return toModelTopicMemory(&mem), nil
}

func (r *mutationResolver) DeleteTopicMemory(ctx context.Context, id string) (bool, error) {
	uID := strToUint(id)
	if err := r.DB.DB.Delete(&dbmodels.TopicMemory{}, uID).Error; err != nil {
		return false, err
	}
	return true, nil
}

func (r *mutationResolver) PinTopicMemory(ctx context.Context, id string, isPinned bool) (*model.TopicMemory, error) {
	uID := strToUint(id)
	var mem dbmodels.TopicMemory
	if err := r.DB.DB.First(&mem, uID).Error; err != nil {
		return nil, err
	}
	mem.IsPinned = isPinned
	if err := r.DB.DB.Save(&mem).Error; err != nil {
		return nil, err
	}
	return toModelTopicMemory(&mem), nil
}

func (r *mutationResolver) UpsertEntityProfile(ctx context.Context, topic string, entityType string, entityName string, attributesJSON string) (*model.EntityProfile, error) {
	var profile dbmodels.EntityProfile
	err := r.DB.DB.Where("topic = ? AND entity_type = ? AND entity_name = ?", topic, entityType, entityName).First(&profile).Error
	if err != nil {
		profile = dbmodels.EntityProfile{
			Topic:          topic,
			EntityType:     entityType,
			EntityName:     entityName,
			AttributesJSON: attributesJSON,
			UpdatedAt:      time.Now(),
		}
		if err := r.DB.DB.Create(&profile).Error; err != nil {
			return nil, err
		}
	} else {
		profile.AttributesJSON = attributesJSON
		profile.UpdatedAt = time.Now()
		if err := r.DB.DB.Save(&profile).Error; err != nil {
			return nil, err
		}
	}
	return toModelEntityProfile(&profile), nil
}

func (r *mutationResolver) UpdateSharedSpace(ctx context.Context, familyGroup string, topic string, sharedWithUserID string, canView bool, canEdit bool, notifyMode string) (*model.SharedSpace, error) {
	var space dbmodels.SharedSpace
	err := r.DB.DB.Where("family_group = ? AND topic = ? AND shared_with_user_id = ?", familyGroup, topic, sharedWithUserID).First(&space).Error
	if err != nil {
		space = dbmodels.SharedSpace{
			FamilyGroup:      familyGroup,
			Topic:            topic,
			SharedWithUserID: sharedWithUserID,
			CanView:          canView,
			CanEdit:          canEdit,
			NotifyMode:       notifyMode,
			UpdatedAt:        time.Now(),
		}
		if err := r.DB.DB.Create(&space).Error; err != nil {
			return nil, err
		}
	} else {
		space.CanView = canView
		space.CanEdit = canEdit
		space.NotifyMode = notifyMode
		space.UpdatedAt = time.Now()
		if err := r.DB.DB.Save(&space).Error; err != nil {
			return nil, err
		}
	}
	return toModelSharedSpace(&space), nil
}

func (r *mutationResolver) CreateSharedTask(ctx context.Context, familyGroup string, topic string, title string, dueTime *time.Time, assignedToUserID *string) (*model.SharedTaskReminder, error) {
	task := dbmodels.SharedTaskReminder{
		FamilyGroup:     familyGroup,
		Topic:           topic,
		Title:           title,
		DueTime:         dueTime,
		CreatedByUserID: "user",
		CreatedAt:       time.Now(),
	}
	if assignedToUserID != nil {
		task.AssignedToUserID = *assignedToUserID
	}
	if err := r.DB.DB.Create(&task).Error; err != nil {
		return nil, err
	}
	return toModelSharedTask(&task), nil
}

func (r *mutationResolver) CompleteSharedTask(ctx context.Context, id string, completedByUserID string) (*model.SharedTaskReminder, error) {
	uID := strToUint(id)
	var task dbmodels.SharedTaskReminder
	if err := r.DB.DB.First(&task, uID).Error; err != nil {
		return nil, err
	}
	now := time.Now()
	task.IsCompleted = true
	task.CompletedByUserID = completedByUserID
	task.CompletedAt = &now
	if err := r.DB.DB.Save(&task).Error; err != nil {
		return nil, err
	}
	return toModelSharedTask(&task), nil
}

func (r *mutationResolver) CreateScheduleEvent(ctx context.Context, topic string, title string, description *string, startTime time.Time, endTime time.Time, tagsJSON *string, syncToGoogle *bool, isShared *bool) (*model.ScheduleEvent, error) {
	desc := ""
	if description != nil {
		desc = *description
	}
	tJSON := "[]"
	if tagsJSON != nil && *tagsJSON != "" {
		tJSON = *tagsJSON
	}
	syncG := true
	if syncToGoogle != nil {
		syncG = *syncToGoogle
	}
	shared := false
	if isShared != nil {
		shared = *isShared
	}

	event := dbmodels.ScheduleEvent{
		Topic:           topic,
		Title:           title,
		Description:     desc,
		StartTime:       startTime,
		EndTime:         endTime,
		TagsJSON:        tJSON,
		SyncToGoogle:    syncG,
		IsShared:        shared,
		CreatedByUserID: "user",
		CreatedAt:       time.Now(),
	}
	if err := r.DB.DB.Create(&event).Error; err != nil {
		return nil, err
	}
	return toModelScheduleEvent(&event), nil
}

func (r *mutationResolver) DeleteScheduleEvent(ctx context.Context, id string) (bool, error) {
	uID := strToUint(id)
	if err := r.DB.DB.Delete(&dbmodels.ScheduleEvent{}, uID).Error; err != nil {
		return false, err
	}
	return true, nil
}

func (r *mutationResolver) SaveTopicCalendarConfig(ctx context.Context, topic string, colorID *string, prefixTag *string, includeInMorningBriefing *bool, syncToGoogleDefault *bool) (*model.TopicCalendarConfig, error) {
	var cfg dbmodels.TopicCalendarConfig
	err := r.DB.DB.Where("topic = ?", topic).First(&cfg).Error
	if err != nil {
		cfg = dbmodels.TopicCalendarConfig{
			Topic:                    topic,
			UserID:                   "default",
			IncludeInMorningBriefing: true,
			UpdatedAt:                time.Now(),
		}
		if colorID != nil {
			cfg.ColorID = *colorID
		}
		if prefixTag != nil {
			cfg.PrefixTag = *prefixTag
		}
		if includeInMorningBriefing != nil {
			cfg.IncludeInMorningBriefing = *includeInMorningBriefing
		}
		cfg.SyncToGoogleDefault = syncToGoogleDefault
		if err := r.DB.DB.Create(&cfg).Error; err != nil {
			return nil, err
		}
	} else {
		if colorID != nil {
			cfg.ColorID = *colorID
		}
		if prefixTag != nil {
			cfg.PrefixTag = *prefixTag
		}
		if includeInMorningBriefing != nil {
			cfg.IncludeInMorningBriefing = *includeInMorningBriefing
		}
		if syncToGoogleDefault != nil {
			cfg.SyncToGoogleDefault = syncToGoogleDefault
		}
		cfg.UpdatedAt = time.Now()
		if err := r.DB.DB.Save(&cfg).Error; err != nil {
			return nil, err
		}
	}
	return toModelTopicCalendarConfig(&cfg), nil
}

func (r *mutationResolver) UpsertBabyProfile(ctx context.Context, familyGroup string, name string, dateOfBirth time.Time, gender string, currentWeightGrams *int, heightCm *float64, pediatricianInfo *string, allergiesNotes *string) (*model.BabyProfile, error) {
	var p dbmodels.BabyProfile
	err := r.DB.DB.Where("family_group = ? AND name = ?", familyGroup, name).First(&p).Error
	if err != nil {
		p = dbmodels.BabyProfile{
			FamilyGroup: familyGroup,
			Name:        name,
			DateOfBirth: dateOfBirth,
			Gender:      gender,
			UpdatedAt:   time.Now(),
		}
		if currentWeightGrams != nil {
			p.CurrentWeightGrams = *currentWeightGrams
		}
		if heightCm != nil {
			p.HeightCm = *heightCm
		}
		if pediatricianInfo != nil {
			p.PediatricianInfo = *pediatricianInfo
		}
		if allergiesNotes != nil {
			p.AllergiesNotes = *allergiesNotes
		}
		if err := r.DB.DB.Create(&p).Error; err != nil {
			return nil, err
		}
	} else {
		p.DateOfBirth = dateOfBirth
		p.Gender = gender
		if currentWeightGrams != nil {
			p.CurrentWeightGrams = *currentWeightGrams
		}
		if heightCm != nil {
			p.HeightCm = *heightCm
		}
		if pediatricianInfo != nil {
			p.PediatricianInfo = *pediatricianInfo
		}
		if allergiesNotes != nil {
			p.AllergiesNotes = *allergiesNotes
		}
		p.UpdatedAt = time.Now()
		if err := r.DB.DB.Save(&p).Error; err != nil {
			return nil, err
		}
	}
	return toModelBabyProfile(&p), nil
}

func (r *mutationResolver) LogBabyCare(ctx context.Context, babyID string, familyGroup string, logType string, valueNumeric *float64, unit *string, details *string, timestamp *time.Time) (*model.BabyCareLog, error) {
	ts := time.Now()
	if timestamp != nil {
		ts = *timestamp
	}
	var val float64
	if valueNumeric != nil {
		val = *valueNumeric
	}
	u := ""
	if unit != nil {
		u = *unit
	}
	det := ""
	if details != nil {
		det = *details
	}

	log := dbmodels.BabyCareLog{
		BabyID:         strToUint(babyID),
		FamilyGroup:    familyGroup,
		LogType:        logType,
		ValueNumeric:   val,
		Unit:           u,
		Details:        det,
		Timestamp:      ts,
		LoggedByUserID: "user",
	}
	if err := r.DB.DB.Create(&log).Error; err != nil {
		return nil, err
	}
	return toModelBabyCareLog(&log), nil
}

func (r *mutationResolver) UpdateBabyInventory(ctx context.Context, familyGroup string, itemName string, quantityRemaining float64, unit string, lowThreshold float64) (*model.BabyInventoryItem, error) {
	var item dbmodels.BabyInventoryItem
	err := r.DB.DB.Where("family_group = ? AND item_name = ?", familyGroup, itemName).First(&item).Error
	if err != nil {
		item = dbmodels.BabyInventoryItem{
			FamilyGroup:       familyGroup,
			ItemName:          itemName,
			QuantityRemaining: quantityRemaining,
			Unit:              unit,
			LowThreshold:      lowThreshold,
			UpdatedAt:         time.Now(),
		}
		if err := r.DB.DB.Create(&item).Error; err != nil {
			return nil, err
		}
	} else {
		item.QuantityRemaining = quantityRemaining
		item.Unit = unit
		item.LowThreshold = lowThreshold
		item.UpdatedAt = time.Now()
		if err := r.DB.DB.Save(&item).Error; err != nil {
			return nil, err
		}
	}
	return toModelBabyInventoryItem(&item), nil
}

func (r *mutationResolver) SaveHouseContract(ctx context.Context, familyGroup string, recordType string, title string, vendorOrLandlord *string, amount *float64, currency *string, expirationOrRenewalDate *time.Time, noticePeriodDays *int, keyTermsJSON *string) (*model.HouseContractRecord, error) {
	c := dbmodels.HouseContractRecord{
		FamilyGroup:             familyGroup,
		RecordType:              recordType,
		Title:                   title,
		ExpirationOrRenewalDate: expirationOrRenewalDate,
		CreatedAt:               time.Now(),
		CreatedByUserID:         "user",
	}
	if vendorOrLandlord != nil {
		c.VendorOrLandlord = *vendorOrLandlord
	}
	if amount != nil {
		c.Amount = *amount
	}
	if currency != nil {
		c.Currency = *currency
	}
	if noticePeriodDays != nil {
		c.NoticePeriodDays = *noticePeriodDays
	}
	if keyTermsJSON != nil {
		c.KeyTermsJSON = *keyTermsJSON
	} else {
		c.KeyTermsJSON = "{}"
	}
	if err := r.DB.DB.Create(&c).Error; err != nil {
		return nil, err
	}
	return toModelHouseContract(&c), nil
}

func (r *mutationResolver) SaveHomeMaintenanceItem(ctx context.Context, familyGroup string, taskName string, category string, intervalDays int, nextDueAt time.Time, contractorInfo *string, notes *string) (*model.HomeMaintenanceItem, error) {
	item := dbmodels.HomeMaintenanceItem{
		FamilyGroup:  familyGroup,
		TaskName:     taskName,
		Category:     category,
		IntervalDays: intervalDays,
		NextDueAt:    nextDueAt,
	}
	if contractorInfo != nil {
		item.ContractorInfo = *contractorInfo
	}
	if notes != nil {
		item.Notes = *notes
	}
	if err := r.DB.DB.Create(&item).Error; err != nil {
		return nil, err
	}
	return toModelHomeMaintenanceItem(&item), nil
}

func (r *mutationResolver) CompleteHomeMaintenance(ctx context.Context, id string) (*model.HomeMaintenanceItem, error) {
	uID := strToUint(id)
	var item dbmodels.HomeMaintenanceItem
	if err := r.DB.DB.First(&item, uID).Error; err != nil {
		return nil, err
	}
	now := time.Now()
	item.MarkCompleted(now, "user")
	if err := r.DB.DB.Save(&item).Error; err != nil {
		return nil, err
	}
	return toModelHomeMaintenanceItem(&item), nil
}

func (r *mutationResolver) SaveVehicleAsset(ctx context.Context, familyGroup string, name string, licensePlate string, insuranceExpiry *time.Time, annualTestExpiry *time.Time, currentMileageKm *int, nextServiceMileageKm *int, notes *string) (*model.VehicleAsset, error) {
	var v dbmodels.VehicleAsset
	err := r.DB.DB.Where("family_group = ? AND license_plate = ?", familyGroup, licensePlate).First(&v).Error
	if err != nil {
		v = dbmodels.VehicleAsset{
			FamilyGroup:      familyGroup,
			Name:             name,
			LicensePlate:     licensePlate,
			InsuranceExpiry:  insuranceExpiry,
			AnnualTestExpiry: annualTestExpiry,
		}
		if currentMileageKm != nil {
			v.CurrentMileageKm = *currentMileageKm
		}
		if nextServiceMileageKm != nil {
			v.NextServiceMileageKm = *nextServiceMileageKm
		}
		if notes != nil {
			v.Notes = *notes
		}
		if err := r.DB.DB.Create(&v).Error; err != nil {
			return nil, err
		}
	} else {
		v.Name = name
		if insuranceExpiry != nil {
			v.InsuranceExpiry = insuranceExpiry
		}
		if annualTestExpiry != nil {
			v.AnnualTestExpiry = annualTestExpiry
		}
		if currentMileageKm != nil {
			v.CurrentMileageKm = *currentMileageKm
		}
		if nextServiceMileageKm != nil {
			v.NextServiceMileageKm = *nextServiceMileageKm
		}
		if notes != nil {
			v.Notes = *notes
		}
		if err := r.DB.DB.Save(&v).Error; err != nil {
			return nil, err
		}
	}
	return toModelVehicleAsset(&v), nil
}

func (r *mutationResolver) LogFinancialTransaction(ctx context.Context, familyGroup string, typeArg string, category string, merchant string, amount float64, currency string, notes *string, isShared *bool) (*model.FinancialTransaction, error) {
	n := ""
	if notes != nil {
		n = *notes
	}
	sh := true
	if isShared != nil {
		sh = *isShared
	}

	tx := dbmodels.FinancialTransaction{
		FamilyGroup:     familyGroup,
		UserID:          "user",
		Type:            typeArg,
		Category:        category,
		Merchant:        merchant,
		Amount:          amount,
		Currency:        currency,
		TransactionDate: time.Now(),
		IsShared:        sh,
		Source:          "dashboard",
		Notes:           n,
		CreatedAt:       time.Now(),
	}
	if err := r.DB.DB.Create(&tx).Error; err != nil {
		return nil, err
	}
	return toModelFinancialTransaction(&tx), nil
}

func (r *mutationResolver) SetCategoryBudget(ctx context.Context, familyGroup string, category string, monthlyLimit float64, currency string, alertThresholdPct *float64) (*model.CategoryBudget, error) {
	var b dbmodels.CategoryBudget
	pct := 80.0
	if alertThresholdPct != nil {
		pct = *alertThresholdPct
	}
	err := r.DB.DB.Where("family_group = ? AND category = ?", familyGroup, category).First(&b).Error
	if err != nil {
		b = dbmodels.CategoryBudget{
			FamilyGroup:       familyGroup,
			Category:          category,
			MonthlyLimit:      monthlyLimit,
			Currency:          currency,
			AlertThresholdPct: pct,
			CreatedAt:         time.Now(),
			UpdatedAt:         time.Now(),
		}
		if err := r.DB.DB.Create(&b).Error; err != nil {
			return nil, err
		}
	} else {
		b.MonthlyLimit = monthlyLimit
		b.Currency = currency
		b.AlertThresholdPct = pct
		b.UpdatedAt = time.Now()
		if err := r.DB.DB.Save(&b).Error; err != nil {
			return nil, err
		}
	}
	return toModelCategoryBudget(&b), nil
}

func (r *mutationResolver) SaveRecurringBill(ctx context.Context, familyGroup string, name string, category string, expectedAmount float64, currency string, billingCycle string, nextDueDay int, autoRenew bool) (*model.RecurringBill, error) {
	bill := dbmodels.RecurringBill{
		FamilyGroup:    familyGroup,
		Name:           name,
		Category:       category,
		ExpectedAmount: expectedAmount,
		Currency:       currency,
		BillingCycle:   billingCycle,
		NextDueDay:     nextDueDay,
		AutoRenew:      autoRenew,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	if err := r.DB.DB.Create(&bill).Error; err != nil {
		return nil, err
	}
	return toModelRecurringBill(&bill), nil
}

func (r *mutationResolver) RegisterExternalAPI(ctx context.Context, name string, baseURL string, defaultMethod *string, description *string) (*model.ExternalAPIConnector, error) {
	m := "GET"
	if defaultMethod != nil && *defaultMethod != "" {
		m = *defaultMethod
	}
	d := ""
	if description != nil {
		d = *description
	}
	api := dbmodels.ExternalAPIConnector{
		Name:          name,
		BaseURL:       baseURL,
		DefaultMethod: m,
		Description:   d,
		UserID:        "user",
		CreatedAt:     time.Now(),
	}
	if err := r.DB.DB.Create(&api).Error; err != nil {
		return nil, err
	}
	return toModelExternalAPIConnector(&api), nil
}

func (r *mutationResolver) SaveAutomationRule(ctx context.Context, name string, triggerType string, triggerTopic string, conditionJSON string, actionInstruction string, isActive *bool) (*model.AutomationRule, error) {
	act := true
	if isActive != nil {
		act = *isActive
	}
	rule := dbmodels.AutomationRule{
		Name:              name,
		TriggerType:       triggerType,
		TriggerTopic:      triggerTopic,
		ConditionJSON:     conditionJSON,
		ActionInstruction: actionInstruction,
		IsActive:          act,
		UserID:            "user",
		CreatedAt:         time.Now(),
	}
	if err := r.DB.DB.Create(&rule).Error; err != nil {
		return nil, err
	}
	return toModelAutomationRule(&rule), nil
}

func (r *mutationResolver) ToggleAutomationRule(ctx context.Context, id string, isActive bool) (*model.AutomationRule, error) {
	uID := strToUint(id)
	var rule dbmodels.AutomationRule
	if err := r.DB.DB.First(&rule, uID).Error; err != nil {
		return nil, err
	}
	rule.IsActive = isActive
	if err := r.DB.DB.Save(&rule).Error; err != nil {
		return nil, err
	}
	return toModelAutomationRule(&rule), nil
}

func (r *mutationResolver) DeleteAutomationRule(ctx context.Context, id string) (bool, error) {
	uID := strToUint(id)
	if err := r.DB.DB.Delete(&dbmodels.AutomationRule{}, uID).Error; err != nil {
		return false, err
	}
	return true, nil
}

func (r *mutationResolver) SaveUserPreference(ctx context.Context, userID string, preferredLanguage *string, tonePersona *string, defaultCurrency *string, quietHoursStart *string, quietHoursEnd *string, standingInstructions *string) (*model.UserPreference, error) {
	var pref dbmodels.UserPreference
	err := r.DB.DB.Where("user_id = ?", userID).First(&pref).Error
	if err != nil {
		pref = dbmodels.UserPreference{
			UserID:             userID,
			PreferredLanguage:  "en",
			TonePersona:        "helpful",
			DefaultCurrency:    "USD",
			BriefingTopicsJSON: "[]",
			UpdatedAt:          time.Now(),
		}
		if preferredLanguage != nil {
			pref.PreferredLanguage = *preferredLanguage
		}
		if tonePersona != nil {
			pref.TonePersona = *tonePersona
		}
		if defaultCurrency != nil {
			pref.DefaultCurrency = *defaultCurrency
		}
		if quietHoursStart != nil {
			pref.QuietHoursStart = *quietHoursStart
		}
		if quietHoursEnd != nil {
			pref.QuietHoursEnd = *quietHoursEnd
		}
		if standingInstructions != nil {
			pref.StandingInstructions = *standingInstructions
		}
		if err := r.DB.DB.Create(&pref).Error; err != nil {
			return nil, err
		}
	} else {
		if preferredLanguage != nil {
			pref.PreferredLanguage = *preferredLanguage
		}
		if tonePersona != nil {
			pref.TonePersona = *tonePersona
		}
		if defaultCurrency != nil {
			pref.DefaultCurrency = *defaultCurrency
		}
		if quietHoursStart != nil {
			pref.QuietHoursStart = *quietHoursStart
		}
		if quietHoursEnd != nil {
			pref.QuietHoursEnd = *quietHoursEnd
		}
		if standingInstructions != nil {
			pref.StandingInstructions = *standingInstructions
		}
		pref.UpdatedAt = time.Now()
		if err := r.DB.DB.Save(&pref).Error; err != nil {
			return nil, err
		}
	}
	return toModelUserPreference(&pref), nil
}

func (r *mutationResolver) SavePersonalCRMContact(ctx context.Context, familyGroup string, personName string, relationship *string, importantDatesJSON *string, giftIdeasAndSizes *string, medicalNotes *string) (*model.PersonalCRMContact, error) {
	rel := ""
	if relationship != nil {
		rel = *relationship
	}
	dates := "{}"
	if importantDatesJSON != nil {
		dates = *importantDatesJSON
	}
	gifts := ""
	if giftIdeasAndSizes != nil {
		gifts = *giftIdeasAndSizes
	}
	med := ""
	if medicalNotes != nil {
		med = *medicalNotes
	}

	contact := dbmodels.PersonalCRMContact{
		FamilyGroup:        familyGroup,
		PersonName:         personName,
		Relationship:       rel,
		ImportantDatesJSON: dates,
		GiftIdeasAndSizes:  gifts,
		MedicalNotes:       med,
		UserID:             "user",
		UpdatedAt:          time.Now(),
	}
	if err := r.DB.DB.Create(&contact).Error; err != nil {
		return nil, err
	}
	return toModelPersonalCRMContact(&contact), nil
}

func (r *mutationResolver) RollbackMutation(ctx context.Context, mutationID string) (bool, error) {
	var mLog dbmodels.MutationLog
	if err := r.DB.DB.Where("id = ?", mutationID).First(&mLog).Error; err != nil {
		return false, err
	}
	if mLog.IsUndone {
		return false, nil
	}

	// Apply genuine rollback logic based on action and entity snapshot
	tbl := tableNameForEntity(mLog.EntityType)
	switch strings.ToLower(mLog.Action) {
	case "create":
		if tbl != "" && mLog.EntityID != "" {
			_ = r.DB.DB.Table(tbl).Where("id = ?", mLog.EntityID).Delete(nil)
		}
	case "update":
		if tbl != "" && mLog.EntityID != "" && mLog.BeforeJSON != "" {
			var beforeMap map[string]interface{}
			if err := json.Unmarshal([]byte(mLog.BeforeJSON), &beforeMap); err == nil {
				_ = r.DB.DB.Table(tbl).Where("id = ?", mLog.EntityID).Updates(beforeMap)
			}
		}
	case "delete":
		if tbl != "" && mLog.BeforeJSON != "" {
			var beforeMap map[string]interface{}
			if err := json.Unmarshal([]byte(mLog.BeforeJSON), &beforeMap); err == nil {
				_ = r.DB.DB.Table(tbl).Create(beforeMap)
			}
		}
	}

	mLog.IsUndone = true
	if err := r.DB.DB.Save(&mLog).Error; err != nil {
		return false, err
	}
	return true, nil
}

// ----------------- Query Resolvers -----------------

// Users is the resolver for the users field.
func (r *queryResolver) Users(ctx context.Context) ([]*model.User, error) {
	var dbUsers []dbmodels.User
	if err := r.DB.DB.Find(&dbUsers).Error; err != nil {
		return nil, err
	}

	var users []*model.User
	for _, u := range dbUsers {
		users = append(users, &model.User{
			ID:          u.ID,
			PlatformID:  u.PlatformID,
			Name:        u.Name,
			Role:        u.Role,
			FamilyGroup: u.FamilyGroup,
			CreatedAt:   u.CreatedAt,
		})
	}
	return users, nil
}

// ActivePairingCodes is the resolver for the activePairingCodes field.
func (r *queryResolver) ActivePairingCodes(ctx context.Context) ([]*model.PairingCode, error) {
	var dbCodes []dbmodels.PairingCode
	if err := r.DB.DB.Where("is_used = ? AND expires_at > now()", false).Find(&dbCodes).Error; err != nil {
		return nil, err
	}

	var codes []*model.PairingCode
	for _, pc := range dbCodes {
		codes = append(codes, &model.PairingCode{
			ID:          pc.ID,
			Code:        pc.Code,
			Role:        pc.Role,
			FamilyGroup: pc.FamilyGroup,
			ExpiresAt:   pc.ExpiresAt,
			IsUsed:      pc.IsUsed,
			CreatedAt:   pc.CreatedAt,
		})
	}
	return codes, nil
}

func (r *queryResolver) TopicMemories(ctx context.Context, topic *string, familyGroup *string, includeSuperseded *bool) ([]*model.TopicMemory, error) {
	var dbMems []dbmodels.TopicMemory
	query := r.DB.DB.Model(&dbmodels.TopicMemory{})

	if topic != nil && *topic != "" {
		query = query.Where("topic = ?", *topic)
	}
	if familyGroup != nil && *familyGroup != "" {
		query = query.Where("family_group = ?", *familyGroup)
	}
	if includeSuperseded == nil || !*includeSuperseded {
		query = query.Where("superseded_by_id IS NULL")
	}

	if err := query.Order("is_pinned DESC, created_at DESC").Find(&dbMems).Error; err != nil {
		return nil, err
	}

	var res []*model.TopicMemory
	for i := range dbMems {
		res = append(res, toModelTopicMemory(&dbMems[i]))
	}
	return res, nil
}

func (r *queryResolver) EntityProfiles(ctx context.Context, topic *string, entityType *string, familyGroup *string) ([]*model.EntityProfile, error) {
	var dbProfiles []dbmodels.EntityProfile
	query := r.DB.DB.Model(&dbmodels.EntityProfile{})

	if topic != nil && *topic != "" {
		query = query.Where("topic = ?", *topic)
	}
	if entityType != nil && *entityType != "" {
		query = query.Where("entity_type = ?", *entityType)
	}
	if familyGroup != nil && *familyGroup != "" {
		query = query.Where("family_group = ?", *familyGroup)
	}

	if err := query.Order("updated_at DESC").Find(&dbProfiles).Error; err != nil {
		return nil, err
	}

	var res []*model.EntityProfile
	for i := range dbProfiles {
		res = append(res, toModelEntityProfile(&dbProfiles[i]))
	}
	return res, nil
}

func (r *queryResolver) SharedSpaces(ctx context.Context, familyGroup string) ([]*model.SharedSpace, error) {
	var dbSpaces []dbmodels.SharedSpace
	if err := r.DB.DB.Where("family_group = ?", familyGroup).Find(&dbSpaces).Error; err != nil {
		return nil, err
	}

	var res []*model.SharedSpace
	for i := range dbSpaces {
		res = append(res, toModelSharedSpace(&dbSpaces[i]))
	}
	return res, nil
}

func (r *queryResolver) SharedTasks(ctx context.Context, familyGroup string, topic *string, isCompleted *bool) ([]*model.SharedTaskReminder, error) {
	var dbTasks []dbmodels.SharedTaskReminder
	query := r.DB.DB.Where("family_group = ?", familyGroup)

	if topic != nil && *topic != "" {
		query = query.Where("topic = ?", *topic)
	}
	if isCompleted != nil {
		query = query.Where("is_completed = ?", *isCompleted)
	}

	if err := query.Order("due_time ASC, created_at DESC").Find(&dbTasks).Error; err != nil {
		return nil, err
	}

	var res []*model.SharedTaskReminder
	for i := range dbTasks {
		res = append(res, toModelSharedTask(&dbTasks[i]))
	}
	return res, nil
}

func (r *queryResolver) ScheduleEvents(ctx context.Context, topic *string, familyGroup *string, startTime *time.Time, endTime *time.Time) ([]*model.ScheduleEvent, error) {
	var dbEvents []dbmodels.ScheduleEvent
	query := r.DB.DB.Model(&dbmodels.ScheduleEvent{})

	if topic != nil && *topic != "" {
		query = query.Where("topic = ?", *topic)
	}
	if familyGroup != nil && *familyGroup != "" {
		query = query.Where("family_group = ?", *familyGroup)
	}
	if startTime != nil {
		query = query.Where("end_time >= ?", *startTime)
	}
	if endTime != nil {
		query = query.Where("start_time <= ?", *endTime)
	}

	if err := query.Order("start_time ASC").Find(&dbEvents).Error; err != nil {
		return nil, err
	}

	var res []*model.ScheduleEvent
	for i := range dbEvents {
		res = append(res, toModelScheduleEvent(&dbEvents[i]))
	}
	return res, nil
}

func (r *queryResolver) TopicCalendarConfigs(ctx context.Context, userID *string) ([]*model.TopicCalendarConfig, error) {
	var dbConfigs []dbmodels.TopicCalendarConfig
	query := r.DB.DB.Model(&dbmodels.TopicCalendarConfig{})
	if userID != nil && *userID != "" {
		query = query.Where("user_id = ?", *userID)
	}

	if err := query.Find(&dbConfigs).Error; err != nil {
		return nil, err
	}

	var res []*model.TopicCalendarConfig
	for i := range dbConfigs {
		res = append(res, toModelTopicCalendarConfig(&dbConfigs[i]))
	}
	return res, nil
}

func (r *queryResolver) BabyProfiles(ctx context.Context, familyGroup string) ([]*model.BabyProfile, error) {
	var dbProfiles []dbmodels.BabyProfile
	if err := r.DB.DB.Where("family_group = ?", familyGroup).Find(&dbProfiles).Error; err != nil {
		return nil, err
	}

	var res []*model.BabyProfile
	for i := range dbProfiles {
		res = append(res, toModelBabyProfile(&dbProfiles[i]))
	}
	return res, nil
}

func (r *queryResolver) BabyCareLogs(ctx context.Context, babyID string, logType *string, limit *int) ([]*model.BabyCareLog, error) {
	var dbLogs []dbmodels.BabyCareLog
	query := r.DB.DB.Where("baby_id = ?", strToUint(babyID))

	if logType != nil && *logType != "" {
		query = query.Where("log_type = ?", *logType)
	}
	lim := 50
	if limit != nil && *limit > 0 {
		lim = *limit
	}

	if err := query.Order("timestamp DESC").Limit(lim).Find(&dbLogs).Error; err != nil {
		return nil, err
	}

	var res []*model.BabyCareLog
	for i := range dbLogs {
		res = append(res, toModelBabyCareLog(&dbLogs[i]))
	}
	return res, nil
}

func (r *queryResolver) BabyInventory(ctx context.Context, familyGroup string) ([]*model.BabyInventoryItem, error) {
	var dbItems []dbmodels.BabyInventoryItem
	if err := r.DB.DB.Where("family_group = ?", familyGroup).Order("item_name ASC").Find(&dbItems).Error; err != nil {
		return nil, err
	}

	var res []*model.BabyInventoryItem
	for i := range dbItems {
		res = append(res, toModelBabyInventoryItem(&dbItems[i]))
	}
	return res, nil
}

func (r *queryResolver) HouseContracts(ctx context.Context, familyGroup string, recordType *string) ([]*model.HouseContractRecord, error) {
	var dbRecords []dbmodels.HouseContractRecord
	query := r.DB.DB.Where("family_group = ?", familyGroup)

	if recordType != nil && *recordType != "" {
		query = query.Where("record_type = ?", *recordType)
	}

	if err := query.Order("expiration_or_renewal_date ASC, created_at DESC").Find(&dbRecords).Error; err != nil {
		return nil, err
	}

	var res []*model.HouseContractRecord
	for i := range dbRecords {
		res = append(res, toModelHouseContract(&dbRecords[i]))
	}
	return res, nil
}

func (r *queryResolver) HomeMaintenanceItems(ctx context.Context, familyGroup string, category *string) ([]*model.HomeMaintenanceItem, error) {
	var dbItems []dbmodels.HomeMaintenanceItem
	query := r.DB.DB.Where("family_group = ?", familyGroup)

	if category != nil && *category != "" {
		query = query.Where("category = ?", *category)
	}

	if err := query.Order("next_due_at ASC").Find(&dbItems).Error; err != nil {
		return nil, err
	}

	var res []*model.HomeMaintenanceItem
	for i := range dbItems {
		res = append(res, toModelHomeMaintenanceItem(&dbItems[i]))
	}
	return res, nil
}

func (r *queryResolver) VehicleAssets(ctx context.Context, familyGroup string) ([]*model.VehicleAsset, error) {
	var dbAssets []dbmodels.VehicleAsset
	if err := r.DB.DB.Where("family_group = ?", familyGroup).Find(&dbAssets).Error; err != nil {
		return nil, err
	}

	var res []*model.VehicleAsset
	for i := range dbAssets {
		res = append(res, toModelVehicleAsset(&dbAssets[i]))
	}
	return res, nil
}

func (r *queryResolver) FinancialTransactions(ctx context.Context, familyGroup string, category *string, limit *int) ([]*model.FinancialTransaction, error) {
	var dbTxs []dbmodels.FinancialTransaction
	query := r.DB.DB.Where("family_group = ?", familyGroup)

	if category != nil && *category != "" {
		query = query.Where("category = ?", *category)
	}
	lim := 50
	if limit != nil && *limit > 0 {
		lim = *limit
	}

	if err := query.Order("transaction_date DESC").Limit(lim).Find(&dbTxs).Error; err != nil {
		return nil, err
	}

	var res []*model.FinancialTransaction
	for i := range dbTxs {
		res = append(res, toModelFinancialTransaction(&dbTxs[i]))
	}
	return res, nil
}

func (r *queryResolver) CategoryBudgets(ctx context.Context, familyGroup string) ([]*model.CategoryBudget, error) {
	var dbBudgets []dbmodels.CategoryBudget
	if err := r.DB.DB.Where("family_group = ?", familyGroup).Order("category ASC").Find(&dbBudgets).Error; err != nil {
		return nil, err
	}

	var res []*model.CategoryBudget
	for i := range dbBudgets {
		res = append(res, toModelCategoryBudget(&dbBudgets[i]))
	}
	return res, nil
}

func (r *queryResolver) RecurringBills(ctx context.Context, familyGroup string) ([]*model.RecurringBill, error) {
	var dbBills []dbmodels.RecurringBill
	if err := r.DB.DB.Where("family_group = ?", familyGroup).Order("next_due_day ASC").Find(&dbBills).Error; err != nil {
		return nil, err
	}

	var res []*model.RecurringBill
	for i := range dbBills {
		res = append(res, toModelRecurringBill(&dbBills[i]))
	}
	return res, nil
}

func (r *queryResolver) ExternalAPIConnectors(ctx context.Context, familyGroup *string) ([]*model.ExternalAPIConnector, error) {
	var dbAPIs []dbmodels.ExternalAPIConnector
	query := r.DB.DB.Model(&dbmodels.ExternalAPIConnector{})
	if familyGroup != nil && *familyGroup != "" {
		query = query.Where("family_group = ?", *familyGroup)
	}

	if err := query.Order("created_at DESC").Find(&dbAPIs).Error; err != nil {
		return nil, err
	}

	var res []*model.ExternalAPIConnector
	for i := range dbAPIs {
		res = append(res, toModelExternalAPIConnector(&dbAPIs[i]))
	}
	return res, nil
}

func (r *queryResolver) AutomationRules(ctx context.Context, familyGroup *string) ([]*model.AutomationRule, error) {
	var dbRules []dbmodels.AutomationRule
	query := r.DB.DB.Model(&dbmodels.AutomationRule{})
	if familyGroup != nil && *familyGroup != "" {
		query = query.Where("family_group = ?", *familyGroup)
	}

	if err := query.Order("created_at DESC").Find(&dbRules).Error; err != nil {
		return nil, err
	}

	var res []*model.AutomationRule
	for i := range dbRules {
		res = append(res, toModelAutomationRule(&dbRules[i]))
	}
	return res, nil
}

func (r *queryResolver) UserPreference(ctx context.Context, userID string) (*model.UserPreference, error) {
	var dbPref dbmodels.UserPreference
	if err := r.DB.DB.Where("user_id = ?", userID).First(&dbPref).Error; err != nil {
		return nil, nil
	}
	return toModelUserPreference(&dbPref), nil
}

func (r *queryResolver) PersonalCRMContacts(ctx context.Context, familyGroup string) ([]*model.PersonalCRMContact, error) {
	var dbContacts []dbmodels.PersonalCRMContact
	if err := r.DB.DB.Where("family_group = ?", familyGroup).Order("person_name ASC").Find(&dbContacts).Error; err != nil {
		return nil, err
	}

	var res []*model.PersonalCRMContact
	for i := range dbContacts {
		res = append(res, toModelPersonalCRMContact(&dbContacts[i]))
	}
	return res, nil
}

func (r *queryResolver) MutationLogs(ctx context.Context, familyGroup *string, topic *string, limit *int) ([]*model.MutationLog, error) {
	var dbLogs []dbmodels.MutationLog
	query := r.DB.DB.Model(&dbmodels.MutationLog{})

	if familyGroup != nil && *familyGroup != "" {
		query = query.Where("family_group = ?", *familyGroup)
	}
	if topic != nil && *topic != "" {
		query = query.Where("topic = ?", *topic)
	}
	lim := 50
	if limit != nil && *limit > 0 {
		lim = *limit
	}

	if err := query.Order("created_at DESC").Limit(lim).Find(&dbLogs).Error; err != nil {
		return nil, err
	}

	var res []*model.MutationLog
	for i := range dbLogs {
		res = append(res, toModelMutationLog(&dbLogs[i]))
	}
	return res, nil
}

// ----------------- Model Mappers -----------------

func toModelTopicMemory(m *dbmodels.TopicMemory) *model.TopicMemory {
	var sID *int
	if m.SupersededByID != nil {
		v := int(*m.SupersededByID)
		sID = &v
	}
	return &model.TopicMemory{
		ID:              uintToStr(m.ID),
		UserID:          m.UserID,
		FamilyGroup:     m.FamilyGroup,
		Topic:           m.Topic,
		Subtopic:        m.Subtopic,
		TagsJSON:        m.TagsJSON,
		Fact:            m.Fact,
		Scope:           m.Scope,
		CreatedByUserID: m.CreatedByUserID,
		SupersededByID:  sID,
		ValidFrom:       m.ValidFrom,
		ValidUntil:      m.ValidUntil,
		IsPinned:        m.IsPinned,
		CreatedAt:       m.CreatedAt,
	}
}

func toModelEntityProfile(p *dbmodels.EntityProfile) *model.EntityProfile {
	return &model.EntityProfile{
		ID:              uintToStr(p.ID),
		UserID:          p.UserID,
		FamilyGroup:     p.FamilyGroup,
		Topic:           p.Topic,
		EntityType:      p.EntityType,
		EntityName:      p.EntityName,
		AttributesJSON:  p.AttributesJSON,
		UpdatedByUserID: p.UpdatedByUserID,
		UpdatedAt:       p.UpdatedAt,
	}
}

func toModelSharedSpace(s *dbmodels.SharedSpace) *model.SharedSpace {
	return &model.SharedSpace{
		ID:               uintToStr(s.ID),
		FamilyGroup:      s.FamilyGroup,
		Topic:            s.Topic,
		OwnerUserID:      s.OwnerUserID,
		SharedWithUserID: s.SharedWithUserID,
		CanView:          s.CanView,
		CanEdit:          s.CanEdit,
		NotifyMode:       s.NotifyMode,
		UpdatedAt:        s.UpdatedAt,
	}
}

func toModelSharedTask(t *dbmodels.SharedTaskReminder) *model.SharedTaskReminder {
	var assigned *string
	if t.AssignedToUserID != "" {
		assigned = &t.AssignedToUserID
	}
	var completedBy *string
	if t.CompletedByUserID != "" {
		completedBy = &t.CompletedByUserID
	}
	return &model.SharedTaskReminder{
		ID:                uintToStr(t.ID),
		FamilyGroup:       t.FamilyGroup,
		Topic:             t.Topic,
		Title:             t.Title,
		DueTime:           t.DueTime,
		CreatedByUserID:   t.CreatedByUserID,
		AssignedToUserID:  assigned,
		IsCompleted:       t.IsCompleted,
		CompletedByUserID: completedBy,
		CompletedAt:       t.CompletedAt,
		CreatedAt:         t.CreatedAt,
	}
}

func toModelScheduleEvent(e *dbmodels.ScheduleEvent) *model.ScheduleEvent {
	return &model.ScheduleEvent{
		ID:                    uintToStr(e.ID),
		ExternalGoogleEventID: e.ExternalGoogleEventID,
		PartnerGoogleEventID:  e.PartnerGoogleEventID,
		UserID:                e.UserID,
		FamilyGroup:           e.FamilyGroup,
		Topic:                 e.Topic,
		TagsJSON:              e.TagsJSON,
		Title:                 e.Title,
		Description:           e.Description,
		StartTime:             e.StartTime,
		EndTime:               e.EndTime,
		RecurrenceRule:        e.RecurrenceRule,
		SyncToGoogle:          e.SyncToGoogle,
		IsShared:              e.IsShared,
		CreatedByUserID:       e.CreatedByUserID,
		CreatedAt:             e.CreatedAt,
	}
}

func toModelTopicCalendarConfig(c *dbmodels.TopicCalendarConfig) *model.TopicCalendarConfig {
	return &model.TopicCalendarConfig{
		ID:                       uintToStr(c.ID),
		UserID:                   c.UserID,
		Topic:                    c.Topic,
		ColorID:                  c.ColorID,
		PrefixTag:                c.PrefixTag,
		TargetCalendarID:         c.TargetCalendarID,
		IncludeInMorningBriefing: c.IncludeInMorningBriefing,
		SyncToGoogleDefault:      c.SyncToGoogleDefault,
		UpdatedAt:                c.UpdatedAt,
	}
}

func toModelBabyProfile(b *dbmodels.BabyProfile) *model.BabyProfile {
	return &model.BabyProfile{
		ID:                 uintToStr(b.ID),
		FamilyGroup:        b.FamilyGroup,
		Name:               b.Name,
		DateOfBirth:        b.DateOfBirth,
		DueDate:            b.DueDate,
		Gender:             b.Gender,
		BirthWeightGrams:   b.BirthWeightGrams,
		CurrentWeightGrams: b.CurrentWeightGrams,
		HeightCm:           b.HeightCm,
		PediatricianInfo:   b.PediatricianInfo,
		AllergiesNotes:     b.AllergiesNotes,
		UpdatedByUserID:    b.UpdatedByUserID,
		UpdatedAt:          b.UpdatedAt,
	}
}

func toModelBabyCareLog(l *dbmodels.BabyCareLog) *model.BabyCareLog {
	return &model.BabyCareLog{
		ID:             uintToStr(l.ID),
		BabyID:         uintToStr(l.BabyID),
		FamilyGroup:    l.FamilyGroup,
		LogType:        l.LogType,
		ValueNumeric:   l.ValueNumeric,
		Unit:           l.Unit,
		Details:        l.Details,
		Timestamp:      l.Timestamp,
		LoggedByUserID: l.LoggedByUserID,
	}
}

func toModelBabyInventoryItem(i *dbmodels.BabyInventoryItem) *model.BabyInventoryItem {
	return &model.BabyInventoryItem{
		ID:                uintToStr(i.ID),
		FamilyGroup:       i.FamilyGroup,
		ItemName:          i.ItemName,
		QuantityRemaining: i.QuantityRemaining,
		Unit:              i.Unit,
		LowThreshold:      i.LowThreshold,
		UpdatedAt:         i.UpdatedAt,
	}
}

func toModelHouseContract(c *dbmodels.HouseContractRecord) *model.HouseContractRecord {
	return &model.HouseContractRecord{
		ID:                      uintToStr(c.ID),
		FamilyGroup:             c.FamilyGroup,
		RecordType:              c.RecordType,
		Title:                   c.Title,
		VendorOrLandlord:        c.VendorOrLandlord,
		ContactDetails:          c.ContactDetails,
		Amount:                  c.Amount,
		Currency:                c.Currency,
		StartDate:               c.StartDate,
		ExpirationOrRenewalDate: c.ExpirationOrRenewalDate,
		NoticePeriodDays:        c.NoticePeriodDays,
		KeyTermsJSON:            c.KeyTermsJSON,
		CreatedByUserID:         c.CreatedByUserID,
		CreatedAt:               c.CreatedAt,
	}
}

func toModelHomeMaintenanceItem(m *dbmodels.HomeMaintenanceItem) *model.HomeMaintenanceItem {
	return &model.HomeMaintenanceItem{
		ID:              uintToStr(m.ID),
		FamilyGroup:     m.FamilyGroup,
		TaskName:        m.TaskName,
		Category:        m.Category,
		IntervalDays:    m.IntervalDays,
		LastCompletedAt: m.LastCompletedAt,
		NextDueAt:       m.NextDueAt,
		ContractorInfo:  m.ContractorInfo,
		Notes:           m.Notes,
		UpdatedByUserID: m.UpdatedByUserID,
	}
}

func toModelVehicleAsset(v *dbmodels.VehicleAsset) *model.VehicleAsset {
	return &model.VehicleAsset{
		ID:                   uintToStr(v.ID),
		FamilyGroup:          v.FamilyGroup,
		Name:                 v.Name,
		LicensePlate:         v.LicensePlate,
		InsuranceExpiry:      v.InsuranceExpiry,
		AnnualTestExpiry:     v.AnnualTestExpiry,
		LastServiceDate:      v.LastServiceDate,
		CurrentMileageKm:     v.CurrentMileageKm,
		NextServiceMileageKm: v.NextServiceMileageKm,
		Notes:                v.Notes,
	}
}

func toModelFinancialTransaction(t *dbmodels.FinancialTransaction) *model.FinancialTransaction {
	return &model.FinancialTransaction{
		ID:              uintToStr(t.ID),
		UserID:          t.UserID,
		FamilyGroup:     t.FamilyGroup,
		Type:            t.Type,
		Category:        t.Category,
		Merchant:        t.Merchant,
		Amount:          t.Amount,
		Currency:        t.Currency,
		TransactionDate: t.TransactionDate,
		IsShared:        t.IsShared,
		Source:          t.Source,
		Notes:           t.Notes,
		CreatedAt:       t.CreatedAt,
	}
}

func toModelCategoryBudget(b *dbmodels.CategoryBudget) *model.CategoryBudget {
	return &model.CategoryBudget{
		ID:                uintToStr(b.ID),
		FamilyGroup:       b.FamilyGroup,
		Category:          b.Category,
		MonthlyLimit:      b.MonthlyLimit,
		Currency:          b.Currency,
		AlertThresholdPct: b.AlertThresholdPct,
		CreatedAt:         b.CreatedAt,
		UpdatedAt:         b.UpdatedAt,
	}
}

func toModelRecurringBill(r *dbmodels.RecurringBill) *model.RecurringBill {
	return &model.RecurringBill{
		ID:             uintToStr(r.ID),
		FamilyGroup:    r.FamilyGroup,
		Name:           r.Name,
		Category:       r.Category,
		ExpectedAmount: r.ExpectedAmount,
		Currency:       r.Currency,
		BillingCycle:   r.BillingCycle,
		NextDueDay:     r.NextDueDay,
		AutoRenew:      r.AutoRenew,
		CreatedAt:      r.CreatedAt,
		UpdatedAt:      r.UpdatedAt,
	}
}

func toModelExternalAPIConnector(a *dbmodels.ExternalAPIConnector) *model.ExternalAPIConnector {
	return &model.ExternalAPIConnector{
		ID:            uintToStr(a.ID),
		UserID:        a.UserID,
		FamilyGroup:   a.FamilyGroup,
		Name:          a.Name,
		BaseURL:       a.BaseURL,
		DefaultMethod: a.DefaultMethod,
		Description:   a.Description,
		CreatedAt:     a.CreatedAt,
	}
}

func toModelAutomationRule(r *dbmodels.AutomationRule) *model.AutomationRule {
	return &model.AutomationRule{
		ID:                uintToStr(r.ID),
		UserID:            r.UserID,
		FamilyGroup:       r.FamilyGroup,
		Name:              r.Name,
		TriggerType:       r.TriggerType,
		TriggerTopic:      r.TriggerTopic,
		ConditionJSON:     r.ConditionJSON,
		ActionInstruction: r.ActionInstruction,
		IsActive:          r.IsActive,
		CreatedAt:         r.CreatedAt,
	}
}

func toModelUserPreference(u *dbmodels.UserPreference) *model.UserPreference {
	return &model.UserPreference{
		UserID:               u.UserID,
		PreferredLanguage:    u.PreferredLanguage,
		TonePersona:          u.TonePersona,
		DefaultCurrency:      u.DefaultCurrency,
		QuietHoursStart:      u.QuietHoursStart,
		QuietHoursEnd:        u.QuietHoursEnd,
		BriefingTopicsJSON:   u.BriefingTopicsJSON,
		StandingInstructions: u.StandingInstructions,
		UpdatedAt:            u.UpdatedAt,
	}
}

func toModelPersonalCRMContact(c *dbmodels.PersonalCRMContact) *model.PersonalCRMContact {
	return &model.PersonalCRMContact{
		ID:                 uintToStr(c.ID),
		UserID:             c.UserID,
		FamilyGroup:        c.FamilyGroup,
		PersonName:         c.PersonName,
		Relationship:       c.Relationship,
		ImportantDatesJSON: c.ImportantDatesJSON,
		GiftIdeasAndSizes:  c.GiftIdeasAndSizes,
		MedicalNotes:       c.MedicalNotes,
		UpdatedAt:          c.UpdatedAt,
	}
}

func toModelMutationLog(m *dbmodels.MutationLog) *model.MutationLog {
	var before *string
	if m.BeforeJSON != "" {
		before = &m.BeforeJSON
	}
	var after *string
	if m.AfterJSON != "" {
		after = &m.AfterJSON
	}
	return &model.MutationLog{
		ID:          m.ID,
		UserID:      m.UserID,
		FamilyGroup: m.FamilyGroup,
		Topic:       m.Topic,
		Action:      m.Action,
		EntityType:  m.EntityType,
		EntityID:    m.EntityID,
		BeforeJSON:  before,
		AfterJSON:   after,
		Summary:     m.Summary,
		IsUndone:    m.IsUndone,
		CreatedAt:   m.CreatedAt,
	}
}

// Mutation returns MutationResolver implementation.
func (r *Resolver) Mutation() MutationResolver { return &mutationResolver{r} }

// Query returns QueryResolver implementation.
func (r *Resolver) Query() QueryResolver { return &queryResolver{r} }

type mutationResolver struct{ *Resolver }
type queryResolver struct{ *Resolver }
