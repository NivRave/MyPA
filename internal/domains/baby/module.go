package baby

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"google.golang.org/genai"
)

// Option defines a functional option for Module configuration.
type Option func(*Module)

// WithEventBus configures an EventBus for mutation event publishing.
func WithEventBus(bus *events.EventBus) Option {
	return func(m *Module) {
		m.bus = bus
	}
}

// Module implements orchestrator.ToolModule for the Baby & Family Care Tracker.
type Module struct {
	repo db.BabyRepository
	bus  *events.EventBus
	mu   sync.RWMutex
}

// NewModule constructs a new baby ToolModule.
func NewModule(repo db.BabyRepository, opts ...Option) *Module {
	m := &Module{
		repo: repo,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Name returns the unique identifier for this tool module.
func (m *Module) Name() string {
	return "baby"
}

// Declarations returns the Gemini FunctionDeclaration schemas for baby tracker tools.
func (m *Module) Declarations() []*genai.FunctionDeclaration {
	return []*genai.FunctionDeclaration{
		{
			Name: "upsert_baby_profile",
			Description: "Create or update a baby/child profile with name, date of birth, due date, birth weight, current height/weight, pediatrician details, and allergies.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"id": {
						Type:        genai.TypeInteger,
						Description: "Optional numeric ID of an existing baby profile to update.",
					},
					"name": {
						Type:        genai.TypeString,
						Description: "The baby's name (e.g. 'Leo', 'Maya').",
					},
					"date_of_birth": {
						Type:        genai.TypeString,
						Description: "Date of birth in YYYY-MM-DD or RFC3339 format (e.g. '2026-06-15').",
					},
					"due_date": {
						Type:        genai.TypeString,
						Description: "Optional original due date (crucial for calculating gestational adjusted age if baby was born premature).",
					},
					"gender": {
						Type:        genai.TypeString,
						Description: "Gender: 'male', 'female', or 'other'.",
					},
					"birth_weight_grams": {
						Type:        genai.TypeInteger,
						Description: "Birth weight in grams (e.g. 3400).",
					},
					"current_weight_grams": {
						Type:        genai.TypeInteger,
						Description: "Most recently measured weight in grams (e.g. 5800).",
					},
					"height_cm": {
						Type:        genai.TypeNumber,
						Description: "Height or length in centimeters (e.g. 62.5).",
					},
					"pediatrician_info": {
						Type:        genai.TypeString,
						Description: "Pediatrician name, clinic, or contact phone.",
					},
					"allergies_notes": {
						Type:        genai.TypeString,
						Description: "Known allergies, sensitivities, or special medical instructions.",
					},
					"family_group": {
						Type:        genai.TypeString,
						Description: "Optional family group ID. Defaults to the active user's family group.",
					},
				},
				Required: []string{"name", "date_of_birth"},
			},
		},
		{
			Name: "log_baby_care",
			Description: "Log a baby care event: feeding (ml/breast), diaper (wet/dirty/both), sleep duration, temperature, medication (with automatic safety cooldown check), milestone, or growth measurement.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"baby_id": {
						Type:        genai.TypeInteger,
						Description: "Numeric ID of the baby. If omitted and only one baby exists in the family group, it is automatically resolved.",
					},
					"baby_name": {
						Type:        genai.TypeString,
						Description: "Name of the baby (used to look up the baby if baby_id is unknown).",
					},
					"log_type": {
						Type:        genai.TypeString,
						Description: "Type of care event: 'feeding', 'sleep', 'diaper', 'temperature', 'medication', 'milestone', or 'measurement'.",
					},
					"value_numeric": {
						Type:        genai.TypeNumber,
						Description: "Numeric measurement value (e.g., 120 for feeding ml, 1.5 for sleep hours, 38.2 for temperature, 6200 for weight in grams).",
					},
					"unit": {
						Type:        genai.TypeString,
						Description: "Unit of measurement: 'ml', 'oz', 'hours', 'minutes', 'celsius', 'fahrenheit', 'kg', 'g', 'cm', 'doses'.",
					},
					"details": {
						Type:        genai.TypeString,
						Description: "Descriptive details (e.g., 'formula', 'breastmilk left breast', 'wet and dirty', 'Paracetamol 2.5ml', 'rolled tummy to back').",
					},
					"timestamp": {
						Type:        genai.TypeString,
						Description: "Time of the event in RFC3339 or ISO 8601 format (e.g. '2026-11-03T14:30:00Z'). Defaults to current time if omitted.",
					},
					"family_group": {
						Type:        genai.TypeString,
						Description: "Optional family group override.",
					},
					"force": {
						Type:        genai.TypeBoolean,
						Description: "Optional override flag to force logging medication even if pediatric safety cooldown is active.",
					},
				},
				Required: []string{"log_type"},
			},
		},
		{
			Name: "query_baby_logs_and_trends",
			Description: "Query baby care logs and aggregated trend statistics (total feeding volume, sleep totals, diaper count, last medication, fever checks) over a time range (e.g. today, last 24h, 3 days, 7 days).",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"baby_id": {
						Type:        genai.TypeInteger,
						Description: "Numeric ID of the baby.",
					},
					"baby_name": {
						Type:        genai.TypeString,
						Description: "Name of the baby to query.",
					},
					"log_type": {
						Type:        genai.TypeString,
						Description: "Filter by log type ('feeding', 'sleep', 'diaper', 'temperature', 'medication', 'milestone', 'measurement', or 'all'). Default 'all'.",
					},
					"time_range": {
						Type:        genai.TypeString,
						Description: "Convenience time period: 'today', '24h', '3d', '7d', '14d', '30d'. Default '24h'.",
					},
					"start_time": {
						Type:        genai.TypeString,
						Description: "Explicit start time filter in RFC3339 format.",
					},
					"end_time": {
						Type:        genai.TypeString,
						Description: "Explicit end time filter in RFC3339 format.",
					},
					"limit": {
						Type:        genai.TypeInteger,
						Description: "Maximum number of recent raw log entries to return (default 50).",
					},
					"family_group": {
						Type:        genai.TypeString,
						Description: "Optional family group filter.",
					},
				},
			},
		},
		{
			Name: "get_baby_milestone_suggestions",
			Description: "Get developmental milestone insights, current Wonder Weeks cognitive leap, upcoming vaccination schedule, recommended pediatric checkups, feeding transitions, and wake windows based on exact age.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"baby_id": {
						Type:        genai.TypeInteger,
						Description: "Numeric ID of the baby.",
					},
					"baby_name": {
						Type:        genai.TypeString,
						Description: "Name of the baby.",
					},
					"category": {
						Type:        genai.TypeString,
						Description: "Filter guidance category: 'all', 'development', 'vaccine', 'checkup', 'feeding', or 'sleep'. Default 'all'.",
					},
					"family_group": {
						Type:        genai.TypeString,
						Description: "Optional family group filter.",
					},
				},
			},
		},
		{
			Name: "manage_baby_inventory",
			Description: "Track and manage baby supplies (formula, diapers, wipes, Vitamin D drops). View stock levels, record consumption or restocks, and check low-threshold alerts.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"action": {
						Type:        genai.TypeString,
						Description: "Action to perform: 'list', 'get', 'set', 'adjust', or 'check_low'.",
					},
					"item_name": {
						Type:        genai.TypeString,
						Description: "Name of the supply item (e.g. 'Formula Stage 1', 'Diapers Size 2', 'Vitamin D Drops', 'Wipes').",
					},
					"quantity": {
						Type:        genai.TypeNumber,
						Description: "Exact quantity remaining when action is 'set'.",
					},
					"delta": {
						Type:        genai.TypeNumber,
						Description: "Amount to adjust quantity by when action is 'adjust' (e.g. -1 for using 1 can, +3 for restock).",
					},
					"unit": {
						Type:        genai.TypeString,
						Description: "Unit of item (e.g. 'cans', 'packs', 'boxes', 'bottles', 'drops').",
					},
					"low_threshold": {
						Type:        genai.TypeNumber,
						Description: "Alert threshold when remaining stock falls at or below this value.",
					},
					"family_group": {
						Type:        genai.TypeString,
						Description: "Optional family group filter.",
					},
				},
				Required: []string{"action"},
			},
		},
	}
}

// resolveRepo gets the active BabyRepository.
func (m *Module) resolveRepo(callCtx orchestrator.CallContext) (db.BabyRepository, error) {
	if m.repo != nil {
		return m.repo, nil
	}
	if repo, ok := callCtx.DB.(db.BabyRepository); ok {
		return repo, nil
	}
	return nil, fmt.Errorf("no baby repository configured")
}

// resolveContext extracts familyGroup and userID from callCtx and optional override.
func (m *Module) resolveContext(callCtx orchestrator.CallContext, fgOverride string) (string, string) {
	userID := ""
	familyGroup := ""
	if callCtx.User != nil {
		userID = callCtx.User.ID
		familyGroup = callCtx.User.FamilyGroup
	}
	if strings.TrimSpace(fgOverride) != "" {
		familyGroup = strings.TrimSpace(fgOverride)
	}
	if familyGroup == "" {
		familyGroup = userID
	}
	return familyGroup, userID
}

// resolveBabyProfile locates a baby profile by ID, name, or auto-selection if only one baby exists.
func (m *Module) resolveBabyProfile(repo db.BabyRepository, familyGroup string, babyID uint, babyName string) (*models.BabyProfile, error) {
	if babyID > 0 {
		p, err := repo.GetBabyProfile(babyID)
		if err != nil {
			return nil, err
		}
		if p == nil {
			return nil, fmt.Errorf("baby profile with ID %d not found", babyID)
		}
		return p, nil
	}

	if strings.TrimSpace(babyName) != "" {
		p, err := repo.GetBabyProfileByName(familyGroup, babyName)
		if err != nil {
			return nil, err
		}
		if p != nil {
			return p, nil
		}
	}

	// Auto-select if family only has 1 baby
	profiles, err := repo.GetBabyProfilesForFamily(familyGroup)
	if err != nil {
		return nil, err
	}
	if len(profiles) == 1 {
		return &profiles[0], nil
	}
	if len(profiles) == 0 {
		return nil, fmt.Errorf("no baby profiles registered for family '%s'. Please create a profile first", familyGroup)
	}

	var names []string
	for _, p := range profiles {
		names = append(names, fmt.Sprintf("%s (ID: %d)", p.Name, p.ID))
	}
	return nil, fmt.Errorf("multiple babies found (%s). Please specify baby_id or baby_name", strings.Join(names, ", "))
}

// publishMutation emits a standardized MutationEvent for audit, undo, and partner notifications.
func (m *Module) publishMutation(ctx context.Context, callCtx orchestrator.CallContext, action, entityType, entityID, familyGroup, userID, summary string) {
	bus := m.bus
	if bus == nil && callCtx.EventBus != nil {
		bus = callCtx.EventBus
	}
	if bus == nil {
		return
	}

	ev := events.MutationEvent{
		Topic:       "baby",
		Action:      action,
		EntityType:  entityType,
		EntityID:    entityID,
		ActorUserID: userID,
		FamilyGroup: familyGroup,
		Summary:     summary,
		Timestamp:   time.Now(),
	}
	_ = bus.Publish(ctx, ev)
}

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
	fgOverride := getStringArg(args, "family_group")
	familyGroup, userID := m.resolveContext(callCtx, fgOverride)

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
		"status":        "success",
		"message":       summary,
		"profile":       profile,
		"age_summary":   age,
		"is_premature":  age.IsPremature,
	}
	return toJSON(resp), nil
}

// executeLogCare records feeding, diaper, sleep, med, milestone, or measurement events.
func (m *Module) executeLogCare(ctx context.Context, callCtx orchestrator.CallContext, repo db.BabyRepository, args map[string]interface{}) (string, error) {
	fgOverride := getStringArg(args, "family_group")
	familyGroup, userID := m.resolveContext(callCtx, fgOverride)

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
		"status":      "success",
		"message":     summary,
		"baby_name":   profile.Name,
		"care_log":    careLog,
		"baby_age":    age.FormattedAge,
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
	fgOverride := getStringArg(args, "family_group")
	familyGroup, _ := m.resolveContext(callCtx, fgOverride)

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
	fgOverride := getStringArg(args, "family_group")
	familyGroup, _ := m.resolveContext(callCtx, fgOverride)

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
	fgOverride := getStringArg(args, "family_group")
	familyGroup, userID := m.resolveContext(callCtx, fgOverride)

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

// SystemPromptSection injects current baby context, age, milestones, and medication status.
func (m *Module) SystemPromptSection(ctx context.Context, callCtx orchestrator.CallContext) string {
	repo, err := m.resolveRepo(callCtx)
	if err != nil {
		return ""
	}

	familyGroup, _ := m.resolveContext(callCtx, "")
	profiles, err := repo.GetBabyProfilesForFamily(familyGroup)
	if err != nil || len(profiles) == 0 {
		return ""
	}

	now := time.Now()
	var b strings.Builder
	b.WriteString("\n### Baby & Family Care Tracker (Active Lifecycle):\n")

	for _, p := range profiles {
		age := CalculateAge(p.DateOfBirth, p.DueDate, now)
		b.WriteString(fmt.Sprintf("- **%s**: DOB %s | Current Age: %s", p.Name, p.DateOfBirth.Format("2006-01-02"), age.FormattedAge))
		if age.IsPremature && age.AdjustedFormatted != "" {
			b.WriteString(fmt.Sprintf(" (%s)", age.AdjustedFormatted))
		}
		b.WriteString("\n")

		// Developmental Leap
		leap, status := GetDevelopmentalLeapStatus(age.Weeks)
		if leap != nil && (status == "active" || status == "upcoming") {
			b.WriteString(fmt.Sprintf("  - Milestone Leap: Leap %d (%s) - %s (Peaks week %d)\n", leap.LeapNumber, leap.Title, strings.ToUpper(status), leap.PeakWeek))
		}

		// Medication Cooldown
		lastMed, _ := repo.GetLastMedicationLog(p.ID, familyGroup, "")
		if lastMed != nil && now.Sub(lastMed.Timestamp) < 24*time.Hour {
			cd := CheckMedicationCooldown(lastMed, lastMed.Details, now, age.Months)
			if !cd.IsSafeToAdminister {
				b.WriteString(fmt.Sprintf("  - ⚠️ Medication Cooldown: %s given at %s (Only %s ago). Next safe dose after %s (in %s).\n",
					cd.MedicationName, lastMed.Timestamp.Format("15:04"), cd.ElapsedFormatted, cd.NextSafeDoseAt.Format("15:04"), cd.TimeUntilFormatted))
			} else {
				b.WriteString(fmt.Sprintf("  - Medication Status: %s last given %s ago at %s (Safe to administer if needed).\n",
					cd.MedicationName, cd.ElapsedFormatted, lastMed.Timestamp.Format("15:04")))
			}
		}

		// Pediatric Checkup or Vaccine Suggestion
		suggestions := GenerateMilestoneSuggestions(age, "checkup")
		for _, s := range suggestions {
			if s.Urgency == "due_now" {
				b.WriteString(fmt.Sprintf("  - 🏥 Pediatric Checkup Due: %s (%s)\n", s.Title, s.Description))
				break
			}
		}
	}

	// Inventory alerts
	lowItems, _ := repo.GetLowBabyInventoryItems(familyGroup)
	if len(lowItems) > 0 {
		var itemAlerts []string
		for _, it := range lowItems {
			itemAlerts = append(itemAlerts, fmt.Sprintf("%s (%.1f %s left, threshold: %.1f)", it.ItemName, it.QuantityRemaining, it.Unit, it.LowThreshold))
		}
		b.WriteString(fmt.Sprintf("- ⚠️ Low Baby Supplies: %s\n", strings.Join(itemAlerts, "; ")))
	}

	return b.String()
}

// Helpers

func formatLogSummary(babyName string, log models.BabyCareLog) string {
	switch log.LogType {
	case "feeding":
		vol := ""
		if log.ValueNumeric > 0 {
			vol = fmt.Sprintf(" %.0f %s", log.ValueNumeric, log.Unit)
		}
		det := ""
		if log.Details != "" {
			det = fmt.Sprintf(" (%s)", log.Details)
		}
		return fmt.Sprintf("Logged feeding for %s:%s%s", babyName, vol, det)
	case "sleep":
		duration := ""
		if log.ValueNumeric > 0 {
			duration = fmt.Sprintf(" %.1f %s", log.ValueNumeric, log.Unit)
		}
		return fmt.Sprintf("Logged sleep for %s:%s %s", babyName, duration, log.Details)
	case "diaper":
		return fmt.Sprintf("Logged diaper change for %s: %s", babyName, log.Details)
	case "temperature":
		return fmt.Sprintf("Logged temperature check for %s: %.1f %s %s", babyName, log.ValueNumeric, log.Unit, log.Details)
	case "medication":
		return fmt.Sprintf("Logged medication for %s: %s", babyName, log.Details)
	case "milestone":
		return fmt.Sprintf("Logged milestone for %s: %s", babyName, log.Details)
	case "measurement":
		return fmt.Sprintf("Logged growth measurement for %s: %.1f %s (%s)", babyName, log.ValueNumeric, log.Unit, log.Details)
	default:
		return fmt.Sprintf("Logged %s for %s", log.LogType, babyName)
	}
}

func parseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	formats := []string{
		"2006-01-02",
		time.RFC3339,
		"2006-01-02T15:04:05",
		"02/01/2006",
		"01/02/2006",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unable to parse date '%s'", s)
}

func getStringArg(args map[string]interface{}, key string) string {
	if val, ok := args[key]; ok && val != nil {
		if s, ok := val.(string); ok {
			return strings.TrimSpace(s)
		}
		return fmt.Sprintf("%v", val)
	}
	return ""
}

func getIntArg(args map[string]interface{}, key string) int {
	if val, ok := args[key]; ok && val != nil {
		switch v := val.(type) {
		case int:
			return v
		case int64:
			return int(v)
		case float64:
			return int(v)
		case string:
			i, _ := strconv.Atoi(v)
			return i
		}
	}
	return 0
}

func getFloatArg(args map[string]interface{}, key string) float64 {
	if val, ok := args[key]; ok && val != nil {
		switch v := val.(type) {
		case float64:
			return v
		case float32:
			return float64(v)
		case int:
			return float64(v)
		case int64:
			return float64(v)
		case string:
			f, _ := strconv.ParseFloat(v, 64)
			return f
		}
	}
	return 0.0
}

func getBoolArg(args map[string]interface{}, key string) bool {
	if val, ok := args[key]; ok && val != nil {
		switch v := val.(type) {
		case bool:
			return v
		case string:
			b, _ := strconv.ParseBool(v)
			return b
		case int:
			return v != 0
		case float64:
			return v != 0
		}
	}
	return false
}

func toJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf(`{"error": "%s"}`, err.Error())
	}
	return string(b)
}
