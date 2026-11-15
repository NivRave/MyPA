package baby

import (
	"context"
	"fmt"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"strconv"
	"strings"
	"time"
)

// Execute handles tool dispatches for the baby module.
func (m *Module) Execute(ctx context.Context, callCtx orchestrator.CallContext, toolName string, args map[string]interface{}) (string, error) {
	repo, err := m.resolveRepo(callCtx)
	if err != nil {
		return "", err
	}

	switch toolName {
	case "upsert_baby_profile":
		return m.executeUpsertProfile(ctx, callCtx, repo, args)
	case "log_baby_care":
		return m.executeLogCare(ctx, callCtx, repo, args)
	case "query_baby_logs_and_trends":
		return m.executeQueryLogsAndTrends(ctx, callCtx, repo, args)
	case "get_baby_milestone_suggestions":
		return m.executeGetMilestoneSuggestions(ctx, callCtx, repo, args)
	case "manage_baby_inventory":
		return m.executeManageInventory(ctx, callCtx, repo, args)
	default:
		return "", fmt.Errorf("unknown tool %s in module baby", toolName)
	}
}

// executeUpsertProfile creates or updates baby profile details.
func (m *Module) executeUpsertProfile(ctx context.Context, callCtx orchestrator.CallContext, repo db.BabyRepository, args map[string]interface{}) (string, error) {
	familyGroup, userID := m.resolveContext(callCtx)

	name := getStringArg(args, "name")
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("name is required")
	}

	dobStr := getStringArg(args, "date_of_birth")
	if strings.TrimSpace(dobStr) == "" {
		return "", fmt.Errorf("date_of_birth is required")
	}
	dob, err := parseDate(dobStr)
	if err != nil {
		return "", fmt.Errorf("invalid date_of_birth format: %w", err)
	}

	var dueDatePtr *time.Time
	dueStr := getStringArg(args, "due_date")
	if strings.TrimSpace(dueStr) != "" {
		dd, err := parseDate(dueStr)
		if err == nil {
			dueDatePtr = &dd
		}
	}

	profileID := uint(getIntArg(args, "id"))

	var existing *models.BabyProfile
	if profileID > 0 {
		existing, _ = repo.GetBabyProfile(profileID)
		if existing != nil && existing.FamilyGroup != familyGroup {
			return "", fmt.Errorf("baby profile with ID %d not found in family %q", profileID, familyGroup)
		}
	} else {
		existing, _ = repo.GetBabyProfileByName(familyGroup, name)
	}

	birthWeight := getIntArg(args, "birth_weight_grams")
	currentWeight := getIntArg(args, "current_weight_grams")
	heightCm := getFloatArg(args, "height_cm")
	gender := getStringArg(args, "gender")
	pediatrician := getStringArg(args, "pediatrician_info")
	allergies := getStringArg(args, "allergies_notes")

	if existing != nil {
		profileID = existing.ID
		if _, ok := args["birth_weight_grams"]; !ok {
			birthWeight = existing.BirthWeightGrams
		}
		if _, ok := args["current_weight_grams"]; !ok {
			currentWeight = existing.CurrentWeightGrams
		}
		if _, ok := args["height_cm"]; !ok {
			heightCm = existing.HeightCm
		}
		if _, ok := args["gender"]; !ok {
			gender = existing.Gender
		}
		if _, ok := args["due_date"]; !ok {
			dueDatePtr = existing.DueDate
		}
		if _, ok := args["pediatrician_info"]; !ok {
			pediatrician = existing.PediatricianInfo
		}
		if _, ok := args["allergies_notes"]; !ok {
			allergies = existing.AllergiesNotes
		}
	}

	profile := models.BabyProfile{
		ID:                 profileID,
		FamilyGroup:        familyGroup,
		Name:               strings.TrimSpace(name),
		DateOfBirth:        dob,
		DueDate:            dueDatePtr,
		Gender:             gender,
		BirthWeightGrams:   birthWeight,
		CurrentWeightGrams: currentWeight,
		HeightCm:           heightCm,
		PediatricianInfo:   pediatrician,
		AllergiesNotes:     allergies,
		UpdatedByUserID:    userID,
	}

	if err := repo.UpsertBabyProfile(&profile); err != nil {
		return "", fmt.Errorf("failed to save baby profile: %w", err)
	}

	age := CalculateAge(profile.DateOfBirth, profile.DueDate, time.Now())

	action := "create"
	if profileID > 0 {
		action = "update"
	}
	summary := fmt.Sprintf("Saved baby profile for %s (Age: %s)", profile.Name, age.FormattedAge)
	m.publishMutation(ctx, callCtx, action, "BabyProfile", strconv.FormatUint(uint64(profile.ID), 10), familyGroup, userID, summary)

	resp := map[string]interface{}{
		"status":       "success",
		"message":      summary,
		"profile":      profile,
		"age_summary":  age,
		"is_premature": age.IsPremature,
	}
	return toJSON(resp), nil
}

// executeLogCare records feeding, diaper, sleep, med, milestone, or measurement events.
func (m *Module) executeLogCare(ctx context.Context, callCtx orchestrator.CallContext, repo db.BabyRepository, args map[string]interface{}) (string, error) {
	familyGroup, userID := m.resolveContext(callCtx)

	logType := strings.ToLower(strings.TrimSpace(getStringArg(args, "log_type")))
	if logType == "" {
		return "", fmt.Errorf("log_type is required")
	}

	babyID := uint(getIntArg(args, "baby_id"))
	babyName := getStringArg(args, "baby_name")

	profile, err := m.resolveBabyProfile(repo, familyGroup, babyID, babyName)
	if err != nil {
		return "", err
	}

	timestamp := time.Now()
	timeStr := getStringArg(args, "timestamp")
	if strings.TrimSpace(timeStr) != "" {
		if t, err := time.Parse(time.RFC3339, timeStr); err == nil {
			timestamp = t
		} else if t, err := parseDate(timeStr); err == nil {
			timestamp = t
		}
	}

	valueNum := getFloatArg(args, "value_numeric")
	unit := getStringArg(args, "unit")
	details := getStringArg(args, "details")

	// Calculate current age for medical triage
	age := CalculateAge(profile.DateOfBirth, profile.DueDate, timestamp)

	var cooldownResult *MedicationCooldownResult

	// Medication Safety Cooldown Check
	if logType == "medication" {
		var lastMedLog *models.BabyCareLog
		recentMedLogs, err := repo.GetCareLogs(profile.ID, familyGroup, "medication", time.Time{}, time.Time{}, 50)
		if err == nil && len(recentMedLogs) > 0 {
			lastMedLog = FindLastMedicationLogForCooldown(recentMedLogs, details)
		} else {
			lastMedLog, _ = repo.GetLastMedicationLog(profile.ID, familyGroup, details)
		}

		cd := CheckMedicationCooldown(lastMedLog, details, timestamp, age.Months)
		cooldownResult = &cd

		// Pediatric Safety Guardrail: if cooldown is active, DO NOT record medication
		// as administered in database unless explicitly forced.
		force := getBoolArg(args, "force")
		if !cd.IsSafeToAdminister && !force {
			resp := map[string]interface{}{
				"status":              "blocked",
				"warning":             cd.WarningMessage,
				"message":             cd.WarningMessage,
				"baby_name":           profile.Name,
				"baby_age":            age.FormattedAge,
				"medication_cooldown": cd,
			}
			return toJSON(resp), nil
		}
	}

	careLog := models.BabyCareLog{
		BabyID:         profile.ID,
		FamilyGroup:    familyGroup,
		LogType:        logType,
		ValueNumeric:   valueNum,
		Unit:           unit,
		Details:        details,
		Timestamp:      timestamp,
		LoggedByUserID: userID,
	}

	if err := repo.CreateCareLog(&careLog); err != nil {
		return "", fmt.Errorf("failed to record care log: %w", err)
	}

	// Measurement update on profile
	if logType == "measurement" {
		updated := false
		if (strings.EqualFold(unit, "kg") || strings.EqualFold(unit, "g") || strings.EqualFold(unit, "grams")) && valueNum > 0 {
			weightGrams := int(valueNum)
			if strings.EqualFold(unit, "kg") {
				weightGrams = int(valueNum * 1000)
			}
			profile.CurrentWeightGrams = weightGrams
			updated = true
		}
		if (strings.EqualFold(unit, "cm") || strings.EqualFold(unit, "centimeters")) && valueNum > 0 {
			profile.HeightCm = valueNum
			updated = true
		}
		if updated {
			_ = repo.UpsertBabyProfile(profile)
		}
	}

	// Prepare user-friendly summary
	summary := formatLogSummary(profile.Name, careLog)
	m.publishMutation(ctx, callCtx, "create", "BabyCareLog", strconv.FormatUint(uint64(careLog.ID), 10), familyGroup, userID, summary)

	resp := map[string]interface{}{
		"status":    "success",
		"message":   summary,
		"baby_name": profile.Name,
		"care_log":  careLog,
		"baby_age":  age.FormattedAge,
	}

	if cooldownResult != nil {
		resp["medication_cooldown"] = cooldownResult
		if !cooldownResult.IsSafeToAdminister || cooldownResult.WarningMessage != "" {
			resp["warning"] = cooldownResult.WarningMessage
		}
	}

	return toJSON(resp), nil
}

// executeQueryLogsAndTrends fetches logs and aggregated summaries.
func (m *Module) executeQueryLogsAndTrends(ctx context.Context, callCtx orchestrator.CallContext, repo db.BabyRepository, args map[string]interface{}) (string, error) {
	familyGroup, _ := m.resolveContext(callCtx)

	babyID := uint(getIntArg(args, "baby_id"))
	babyName := getStringArg(args, "baby_name")

	var profile *models.BabyProfile
	if babyID > 0 || babyName != "" {
		p, err := m.resolveBabyProfile(repo, familyGroup, babyID, babyName)
		if err == nil {
			profile = p
			babyID = p.ID
		}
	}

	// Parse date range
	now := time.Now()
	startTime := now.Add(-24 * time.Hour) // default 24h
	endTime := now

	rangePreset := strings.ToLower(getStringArg(args, "time_range"))
	switch rangePreset {
	case "today":
		startTime = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	case "3d":
		startTime = now.Add(-72 * time.Hour)
	case "7d", "week":
		startTime = now.Add(-7 * 24 * time.Hour)
	case "14d":
		startTime = now.Add(-14 * 24 * time.Hour)
	case "30d", "month":
		startTime = now.Add(-30 * 24 * time.Hour)
	}

	if s := getStringArg(args, "start_time"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			startTime = t
		}
	}
	if e := getStringArg(args, "end_time"); e != "" {
		if t, err := time.Parse(time.RFC3339, e); err == nil {
			endTime = t
		}
	}

	limit := getIntArg(args, "limit")
	if limit <= 0 {
		limit = 50
	}

	logType := getStringArg(args, "log_type")

	logs, err := repo.GetCareLogs(babyID, familyGroup, logType, startTime, endTime, limit)
	if err != nil {
		return "", fmt.Errorf("failed to retrieve logs: %w", err)
	}

	summary, err := repo.GetCareStatsSummary(babyID, familyGroup, startTime, endTime)
	if err != nil {
		return "", fmt.Errorf("failed to calculate stats summary: %w", err)
	}

	resp := map[string]interface{}{
		"status":        "success",
		"family_group":  familyGroup,
		"start_time":    startTime.Format(time.RFC3339),
		"end_time":      endTime.Format(time.RFC3339),
		"logs_returned": len(logs),
		"trends":        summary,
		"recent_logs":   logs,
	}
	if profile != nil {
		resp["baby_name"] = profile.Name
		age := CalculateAge(profile.DateOfBirth, profile.DueDate, now)
		resp["current_age"] = age.FormattedAge
	}

	return toJSON(resp), nil
}

// executeGetMilestoneSuggestions calculates age-based guidelines and leaps.
func (m *Module) executeGetMilestoneSuggestions(ctx context.Context, callCtx orchestrator.CallContext, repo db.BabyRepository, args map[string]interface{}) (string, error) {
	familyGroup, _ := m.resolveContext(callCtx)

	babyID := uint(getIntArg(args, "baby_id"))
	babyName := getStringArg(args, "baby_name")

	profile, err := m.resolveBabyProfile(repo, familyGroup, babyID, babyName)
	if err != nil {
		return "", err
	}

	now := time.Now()
	age := CalculateAge(profile.DateOfBirth, profile.DueDate, now)

	category := getStringArg(args, "category")
	suggestions := GenerateMilestoneSuggestions(age, category)

	leap, leapStatus := GetDevelopmentalLeapStatus(age.Weeks)

	lastMed, _ := repo.GetLastMedicationLog(profile.ID, familyGroup, "")
	var medStatus *MedicationCooldownResult
	if lastMed != nil {
		cd := CheckMedicationCooldown(lastMed, lastMed.Details, now, age.Months)
		medStatus = &cd
	}

	resp := map[string]interface{}{
		"status":            "success",
		"baby_name":         profile.Name,
		"date_of_birth":     profile.DateOfBirth.Format("2006-01-02"),
		"age_info":          age,
		"is_premature":      age.IsPremature,
		"current_leap":      leap,
		"leap_status":       leapStatus,
		"suggestions_count": len(suggestions),
		"suggestions":       suggestions,
	}
	if medStatus != nil {
		resp["active_medication_status"] = medStatus
	}

	return toJSON(resp), nil
}

// executeManageInventory handles supply inventory CRUD and stock checks.
func (m *Module) executeManageInventory(ctx context.Context, callCtx orchestrator.CallContext, repo db.BabyRepository, args map[string]interface{}) (string, error) {
	familyGroup, userID := m.resolveContext(callCtx)

	action := strings.ToLower(strings.TrimSpace(getStringArg(args, "action")))
	itemName := strings.TrimSpace(getStringArg(args, "item_name"))

	switch action {
	case "list":
		items, err := repo.ListBabyInventoryItems(familyGroup)
		if err != nil {
			return "", err
		}
		lowItems, _ := repo.GetLowBabyInventoryItems(familyGroup)
		resp := map[string]interface{}{
			"status":          "success",
			"family_group":    familyGroup,
			"items":           items,
			"low_stock_count": len(lowItems),
			"low_stock_items": lowItems,
		}
		return toJSON(resp), nil

	case "check_low":
		lowItems, err := repo.GetLowBabyInventoryItems(familyGroup)
		if err != nil {
			return "", err
		}
		resp := map[string]interface{}{
			"status":          "success",
			"family_group":    familyGroup,
			"low_stock_count": len(lowItems),
			"low_stock_items": lowItems,
		}
		return toJSON(resp), nil

	case "get":
		if itemName == "" {
			return "", fmt.Errorf("item_name is required for action 'get'")
		}
		item, err := repo.GetBabyInventoryItemByName(familyGroup, itemName)
		if err != nil {
			return "", err
		}
		if item == nil {
			return "", fmt.Errorf("inventory item '%s' not found", itemName)
		}
		resp := map[string]interface{}{
			"status": "success",
			"item":   item,
			"is_low": item.QuantityRemaining <= item.LowThreshold,
		}
		return toJSON(resp), nil

	case "set":
		if itemName == "" {
			return "", fmt.Errorf("item_name is required for action 'set'")
		}
		qty := getFloatArg(args, "quantity")
		unit := getStringArg(args, "unit")
		threshold := getFloatArg(args, "low_threshold")

		item := models.BabyInventoryItem{
			FamilyGroup:       familyGroup,
			ItemName:          itemName,
			QuantityRemaining: qty,
			Unit:              unit,
			LowThreshold:      threshold,
		}
		if err := repo.UpsertBabyInventoryItem(&item); err != nil {
			return "", fmt.Errorf("failed to update inventory: %w", err)
		}

		summary := fmt.Sprintf("Updated inventory: %s quantity set to %.1f %s", itemName, qty, unit)
		m.publishMutation(ctx, callCtx, "update", "BabyInventoryItem", strconv.FormatUint(uint64(item.ID), 10), familyGroup, userID, summary)

		resp := map[string]interface{}{
			"status":  "success",
			"message": summary,
			"item":    item,
			"is_low":  item.QuantityRemaining <= item.LowThreshold,
		}
		return toJSON(resp), nil

	case "adjust":
		if itemName == "" {
			return "", fmt.Errorf("item_name is required for action 'adjust'")
		}
		delta := getFloatArg(args, "delta")
		if delta == 0 {
			return "", fmt.Errorf("delta cannot be 0 for action 'adjust'")
		}

		item, err := repo.GetBabyInventoryItemByName(familyGroup, itemName)
		if err != nil {
			return "", err
		}
		if item == nil {
			// Create new with delta
			unit := getStringArg(args, "unit")
			threshold := getFloatArg(args, "low_threshold")
			item = &models.BabyInventoryItem{
				FamilyGroup:       familyGroup,
				ItemName:          itemName,
				QuantityRemaining: delta,
				Unit:              unit,
				LowThreshold:      threshold,
			}
		} else {
			item.QuantityRemaining += delta
			if item.QuantityRemaining < 0 {
				item.QuantityRemaining = 0
			}
			if u := getStringArg(args, "unit"); u != "" {
				item.Unit = u
			}
			if th := getFloatArg(args, "low_threshold"); th > 0 {
				item.LowThreshold = th
			}
		}

		if err := repo.UpsertBabyInventoryItem(item); err != nil {
			return "", fmt.Errorf("failed to adjust inventory: %w", err)
		}

		summary := fmt.Sprintf("Adjusted inventory: %s changed by %+.1f (Now: %.1f %s)", itemName, delta, item.QuantityRemaining, item.Unit)
		m.publishMutation(ctx, callCtx, "update", "BabyInventoryItem", strconv.FormatUint(uint64(item.ID), 10), familyGroup, userID, summary)

		resp := map[string]interface{}{
			"status":  "success",
			"message": summary,
			"item":    item,
			"is_low":  item.QuantityRemaining <= item.LowThreshold,
		}
		if item.QuantityRemaining <= item.LowThreshold {
			resp["low_stock_warning"] = fmt.Sprintf("LOW STOCK ALERT: %s has only %.1f %s left (Threshold: %.1f)", item.ItemName, item.QuantityRemaining, item.Unit, item.LowThreshold)
		}
		return toJSON(resp), nil

	default:
		return "", fmt.Errorf("unknown inventory action: '%s'. Supported: list, get, set, adjust, check_low", action)
	}
}
