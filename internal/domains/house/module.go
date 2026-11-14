package house

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"google.golang.org/genai"
)

// Option defines a functional configuration option for Module.
type Option func(*Module)

// WithEventBus sets the event bus on the house Module.
func WithEventBus(bus *events.EventBus) Option {
	return func(m *Module) {
		m.bus = bus
	}
}

// Module implements orchestrator.ToolModule for House Maintenance, Contracts & Vehicle Vault.
type Module struct {
	repo db.HouseRepository
	bus  *events.EventBus
	mu   sync.RWMutex
}

// NewModule constructs a new House ToolModule.
func NewModule(repo db.HouseRepository, opts ...Option) *Module {
	m := &Module{
		repo: repo,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Name returns the unique identifier for the module.
func (m *Module) Name() string {
	return "house"
}

// Declarations provides Gemini FunctionDeclaration schemas for house and vehicle tools.
func (m *Module) Declarations() []*genai.FunctionDeclaration {
	return []*genai.FunctionDeclaration{
		{
			Name:        "save_house_contract_or_receipt",
			Description: "Store or update house leases, mortgages, utility contracts, appliance warranties, insurance policies, and repair receipts.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"contract_id": {
						Type:        genai.TypeInteger,
						Description: "Optional numeric ID of an existing contract or receipt to update.",
					},
					"record_type": {
						Type:        genai.TypeString,
						Description: "Type of record: 'lease', 'mortgage', 'utility', 'warranty', 'insurance', or 'receipt'.",
					},
					"title": {
						Type:        genai.TypeString,
						Description: "Descriptive title (e.g., 'Apartment Lease 2026', 'Refrigerator 5-Year Warranty', 'Homeowners Insurance Policy', 'Plumbing Invoice').",
					},
					"vendor_or_landlord": {
						Type:        genai.TypeString,
						Description: "Name of the landlord, HOA, utility provider, insurer, or contractor/merchant.",
					},
					"contact_details": {
						Type:        genai.TypeString,
						Description: "Phone, email, or contact info for landlord or vendor.",
					},
					"amount": {
						Type:        genai.TypeNumber,
						Description: "Recurring payment amount or invoice total (e.g. 5200.0).",
					},
					"currency": {
						Type:        genai.TypeString,
						Description: "Currency code (e.g. 'USD', 'EUR', 'ILS'). Default is 'USD'.",
					},
					"start_date": {
						Type:        genai.TypeString,
						Description: "Start date in ISO 8601 / YYYY-MM-DD format.",
					},
					"expiration_date": {
						Type:        genai.TypeString,
						Description: "Expiration or lease renewal date in ISO 8601 / YYYY-MM-DD format.",
					},
					"notice_period_days": {
						Type:        genai.TypeInteger,
						Description: "Required notice period in days prior to expiration/renewal (e.g., 60 days for lease renewal).",
					},
					"key_terms": {
						Type:        genai.TypeString,
						Description: "Key clauses, warranty conditions, or JSON object with terms (e.g., 'Security deposit: 2 months, No subletting').",
					},
				},
				Required: []string{"record_type", "title"},
			},
		},
		{
			Name:        "list_house_contracts",
			Description: "List and search house contracts, leases, warranties, insurance policies, or receipts with optional expiration and category filters.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"record_type": {
						Type:        genai.TypeString,
						Description: "Filter by record type: 'lease', 'mortgage', 'utility', 'warranty', 'insurance', 'receipt', or 'all'.",
					},
					"expiring_within_days": {
						Type:        genai.TypeInteger,
						Description: "Query contracts or warranties expiring within N days from now (e.g. 30, 60, 90).",
					},
					"search_query": {
						Type:        genai.TypeString,
						Description: "Optional keyword to search within title or vendor/landlord name.",
					},
				},
			},
		},
		{
			Name:        "manage_home_maintenance",
			Description: "Manage preventive home upkeep schedules (HVAC, plumbing, appliances, pest control), log completions, list overdue tasks, or get seasonal suggestions.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"action": {
						Type:        genai.TypeString,
						Description: "Action to execute: 'create', 'complete', 'list', 'update', 'delete', or 'suggest_seasonal'.",
					},
					"item_id": {
						Type:        genai.TypeInteger,
						Description: "Numeric ID of the maintenance item (required for 'complete', 'update', 'delete').",
					},
					"task_name": {
						Type:        genai.TypeString,
						Description: "Name of the task (e.g., 'Clean HVAC air filters', 'Flush water heater', 'Descale espresso machine', 'Exterminator inspection').",
					},
					"category": {
						Type:        genai.TypeString,
						Description: "Maintenance category: 'hvac', 'plumbing', 'appliance', 'pest_control', or 'general'.",
					},
					"interval_days": {
						Type:        genai.TypeInteger,
						Description: "Recurrence interval in days (e.g. 90 for quarterly, 180 for biannual, 365 for annual).",
					},
					"next_due_at": {
						Type:        genai.TypeString,
						Description: "Explicit next due date in YYYY-MM-DD or RFC3339 format.",
					},
					"contractor_info": {
						Type:        genai.TypeString,
						Description: "Preferred service provider, phone number, or technician name.",
					},
					"notes": {
						Type:        genai.TypeString,
						Description: "Instructions, filter part numbers, or upkeep notes.",
					},
					"overdue_only": {
						Type:        genai.TypeBoolean,
						Description: "If true, only return overdue maintenance items during 'list'.",
					},
				},
				Required: []string{"action"},
			},
		},
		{
			Name:        "manage_vehicle_asset",
			Description: "Log and track family vehicles, annual licensing/test dates, insurance policy expiry, odometer mileage, and service maintenance history.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"action": {
						Type:        genai.TypeString,
						Description: "Action to execute: 'create', 'update', 'list', 'delete', 'update_mileage', or 'log_service'.",
					},
					"asset_id": {
						Type:        genai.TypeInteger,
						Description: "Numeric ID of the vehicle asset (required for 'update', 'delete', 'update_mileage', 'log_service').",
					},
					"name": {
						Type:        genai.TypeString,
						Description: "Vehicle description (e.g. 'Toyota RAV4 2022', 'Family Mazda CX-5').",
					},
					"license_plate": {
						Type:        genai.TypeString,
						Description: "Vehicle license plate number.",
					},
					"insurance_expiry": {
						Type:        genai.TypeString,
						Description: "Car insurance policy expiration date in YYYY-MM-DD or RFC3339 format.",
					},
					"annual_test_expiry": {
						Type:        genai.TypeString,
						Description: "Annual vehicle inspection / licensing test expiration date in YYYY-MM-DD or RFC3339 format.",
					},
					"last_service_date": {
						Type:        genai.TypeString,
						Description: "Date of last mechanic service in YYYY-MM-DD or RFC3339 format.",
					},
					"current_mileage_km": {
						Type:        genai.TypeInteger,
						Description: "Current odometer reading in kilometers.",
					},
					"next_service_mileage_km": {
						Type:        genai.TypeInteger,
						Description: "Target odometer reading for next scheduled service in kilometers.",
					},
					"notes": {
						Type:        genai.TypeString,
						Description: "Vehicle details, tire specifications, or service notes.",
					},
				},
				Required: []string{"action"},
			},
		},
	}
}

// Execute routes incoming tool execution calls to the appropriate handler.
func (m *Module) Execute(ctx context.Context, callCtx orchestrator.CallContext, toolName string, args map[string]interface{}) (string, error) {
	repo, err := m.resolveRepo(callCtx)
	if err != nil {
		return "", err
	}

	switch toolName {
	case "save_house_contract_or_receipt":
		return m.executeSaveContract(ctx, callCtx, repo, args)
	case "list_house_contracts":
		return m.executeListContracts(ctx, callCtx, repo, args)
	case "manage_home_maintenance":
		return m.executeManageMaintenance(ctx, callCtx, repo, args)
	case "manage_vehicle_asset":
		return m.executeManageVehicle(ctx, callCtx, repo, args)
	default:
		return "", fmt.Errorf("unknown house tool: %s", toolName)
	}
}

// SystemPromptSection injects active house notices, pending maintenance due,
// and upcoming vehicle inspection/insurance dates into the system prompt.
func (m *Module) SystemPromptSection(ctx context.Context, callCtx orchestrator.CallContext) string {
	familyGroup := resolveFamilyGroup(callCtx)
	if familyGroup == "" {
		return ""
	}

	repo, err := m.resolveRepo(callCtx)
	if err != nil || repo == nil {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("### House Maintenance, Contracts & Vehicle Vault:\n")

	now := time.Now()
	hasItems := false

	// 1. Expiring contracts / warranties / leases within next 60 days
	expiring, err := repo.ListExpiringContracts(familyGroup, 60)
	if err == nil && len(expiring) > 0 {
		hasItems = true
		sb.WriteString("• Upcoming Contract & Warranty Deadlines:\n")
		for _, c := range expiring {
			expiryStr := ""
			if c.ExpirationOrRenewalDate != nil {
				daysLeft := int(c.ExpirationOrRenewalDate.Sub(now).Hours() / 24)
				expiryStr = fmt.Sprintf("%s (%d days remaining)", c.ExpirationOrRenewalDate.Format("2006-01-02"), daysLeft)
			}
			noticeStr := ""
			if c.NoticePeriodDays > 0 {
				noticeDeadline := c.NoticeDeadline()
				if noticeDeadline != nil {
					noticeDays := int(noticeDeadline.Sub(now).Hours() / 24)
					if noticeDays < 0 {
						noticeStr = fmt.Sprintf(" [⚠️ Notice deadline PASSED %d days ago!]", -noticeDays)
					} else {
						noticeStr = fmt.Sprintf(" [Notice due in %d days by %s]", noticeDays, noticeDeadline.Format("2006-01-02"))
					}
				}
			}
			sb.WriteString(fmt.Sprintf("  - [%s] %s (Vendor: %s, Exp: %s%s)\n", strings.ToUpper(c.RecordType), c.Title, c.VendorOrLandlord, expiryStr, noticeStr))
		}
	}

	// 2. Overdue or upcoming maintenance items (within next 14 days)
	maintenance, err := repo.ListMaintenanceItems(familyGroup, "all", false)
	if err == nil && len(maintenance) > 0 {
		var pendingItems []string
		for _, item := range maintenance {
			if now.After(item.NextDueAt) {
				daysOverdue := int(now.Sub(item.NextDueAt).Hours() / 24)
				pendingItems = append(pendingItems, fmt.Sprintf("  - [🔴 OVERDUE by %dd] %s (Category: %s, Due: %s)", daysOverdue, item.TaskName, item.Category, item.NextDueAt.Format("2006-01-02")))
			} else if item.NextDueAt.Before(now.AddDate(0, 0, 14)) {
				daysRemaining := int(item.NextDueAt.Sub(now).Hours() / 24)
				pendingItems = append(pendingItems, fmt.Sprintf("  - [🟡 DUE in %dd] %s (Category: %s, Due: %s)", daysRemaining, item.TaskName, item.Category, item.NextDueAt.Format("2006-01-02")))
			}
		}
		if len(pendingItems) > 0 {
			hasItems = true
			sb.WriteString("• Pending Home Maintenance Items:\n")
			for _, item := range pendingItems {
				sb.WriteString(item + "\n")
			}
		}
	}

	// 3. Vehicles with upcoming inspections, insurance renewals, or service overdue
	vehicles, err := repo.ListVehicleAssets(familyGroup)
	if err == nil && len(vehicles) > 0 {
		var vehicleAlerts []string
		for _, v := range vehicles {
			// Inspection
			if v.AnnualTestExpiry != nil && v.AnnualTestExpiry.Before(now.AddDate(0, 0, 60)) {
				days := int(v.AnnualTestExpiry.Sub(now).Hours() / 24)
				if days < 0 {
					vehicleAlerts = append(vehicleAlerts, fmt.Sprintf("  - %s (%s): Annual test OVERDUE by %d days (Expired: %s)!", v.Name, v.LicensePlate, -days, v.AnnualTestExpiry.Format("2006-01-02")))
				} else {
					vehicleAlerts = append(vehicleAlerts, fmt.Sprintf("  - %s (%s): Annual test due in %d days (%s)", v.Name, v.LicensePlate, days, v.AnnualTestExpiry.Format("2006-01-02")))
				}
			}
			// Insurance
			if v.InsuranceExpiry != nil && v.InsuranceExpiry.Before(now.AddDate(0, 0, 60)) {
				days := int(v.InsuranceExpiry.Sub(now).Hours() / 24)
				if days < 0 {
					vehicleAlerts = append(vehicleAlerts, fmt.Sprintf("  - %s (%s): Insurance EXPIRED %d days ago (%s)!", v.Name, v.LicensePlate, -days, v.InsuranceExpiry.Format("2006-01-02")))
				} else {
					vehicleAlerts = append(vehicleAlerts, fmt.Sprintf("  - %s (%s): Insurance expiring in %d days (%s)", v.Name, v.LicensePlate, days, v.InsuranceExpiry.Format("2006-01-02")))
				}
			}
			// Mileage service
			if v.NextServiceMileageKm > 0 && v.CurrentMileageKm >= v.NextServiceMileageKm {
				kmOver := v.CurrentMileageKm - v.NextServiceMileageKm
				vehicleAlerts = append(vehicleAlerts, fmt.Sprintf("  - %s (%s): Regular service OVERDUE by %d km (%d / %d km)!", v.Name, v.LicensePlate, kmOver, v.CurrentMileageKm, v.NextServiceMileageKm))
			}
		}
		if len(vehicleAlerts) > 0 {
			hasItems = true
			sb.WriteString("• Vehicle Alerts (Inspections, Insurance, Service):\n")
			for _, alert := range vehicleAlerts {
				sb.WriteString(alert + "\n")
			}
		}
	}

	if !hasItems {
		sb.WriteString("• All house contracts, preventive home maintenance, and vehicle tests are currently up to date.\n")
	}

	sb.WriteString("• Tools available: `save_house_contract_or_receipt`, `list_house_contracts`, `manage_home_maintenance`, `manage_vehicle_asset`.\n")

	return sb.String()
}

// executeSaveContract handles saving or updating a contract, warranty, or receipt.
func (m *Module) executeSaveContract(ctx context.Context, callCtx orchestrator.CallContext, repo db.HouseRepository, args map[string]interface{}) (string, error) {
	recordType, _ := args["record_type"].(string)
	recordType = strings.ToLower(strings.TrimSpace(recordType))
	if recordType == "" {
		return "", fmt.Errorf("record_type is required")
	}

	title, _ := args["title"].(string)
	title = strings.TrimSpace(title)
	if title == "" {
		return "", fmt.Errorf("title is required")
	}

	vendor, _ := args["vendor_or_landlord"].(string)
	contact, _ := args["contact_details"].(string)
	amount := parseFloat(args["amount"])
	currency, _ := args["currency"].(string)
	if strings.TrimSpace(currency) == "" {
		currency = "USD"
	}
	noticePeriod := parseInt(args["notice_period_days"])

	startDate, _ := parseDate(args["start_date"])
	expirationDate, _ := parseDate(args["expiration_date"])

	keyTerms := parseKeyTerms(args["key_terms"])

	familyGroup := resolveFamilyGroup(callCtx)
	userID := resolveUserID(callCtx)

	contractID := parseInt(args["contract_id"])
	var contract *models.HouseContractRecord
	var beforeJSON string
	action := "contract_saved"

	if contractID > 0 {
		existing, err := repo.GetContract(uint(contractID))
		if err != nil {
			return "", fmt.Errorf("failed to load contract %d: %w", contractID, err)
		}
		isAuthorized := existing != nil && ((familyGroup != "" && existing.FamilyGroup == familyGroup) || (existing.CreatedByUserID != "" && existing.CreatedByUserID == userID))
		if !isAuthorized {
			return "", fmt.Errorf("contract %d not found in family %q", contractID, familyGroup)
		}
		b, _ := json.Marshal(existing)
		beforeJSON = string(b)
		contract = existing
		action = "contract_updated"
	} else {
		contract = &models.HouseContractRecord{
			CreatedByUserID: userID,
			FamilyGroup:     familyGroup,
			CreatedAt:       time.Now(),
		}
	}

	contract.RecordType = recordType
	contract.Title = title
	contract.VendorOrLandlord = strings.TrimSpace(vendor)
	contract.ContactDetails = strings.TrimSpace(contact)
	contract.Amount = amount
	contract.Currency = strings.ToUpper(strings.TrimSpace(currency))
	contract.StartDate = startDate
	contract.ExpirationOrRenewalDate = expirationDate
	contract.NoticePeriodDays = noticePeriod
	contract.KeyTermsJSON = keyTerms

	var err error
	if contractID > 0 {
		err = repo.UpdateContract(contract)
	} else {
		err = repo.CreateContract(contract)
	}
	if err != nil {
		return "", fmt.Errorf("failed to save house contract: %w", err)
	}

	afterBytes, _ := json.Marshal(contract)
	summary := fmt.Sprintf("Saved %s '%s' with %s (Amount: %.2f %s)", recordType, title, vendor, amount, currency)
	if expirationDate != nil {
		summary += fmt.Sprintf(", Expiration: %s", expirationDate.Format("2006-01-02"))
	}
	if noticePeriod > 0 {
		summary += fmt.Sprintf(", Notice: %d days", noticePeriod)
	}

	m.publishEvent(ctx, callCtx, events.MutationEvent{
		Topic:       "house",
		Action:      action,
		EntityType:  "HouseContractRecord",
		EntityID:    fmt.Sprintf("%d", contract.ID),
		ActorUserID: userID,
		FamilyGroup: familyGroup,
		BeforeJSON:  beforeJSON,
		AfterJSON:   string(afterBytes),
		Summary:     summary,
		Timestamp:   time.Now(),
	})

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("✅ **House %s Saved Successfully** (ID: %d)\n", strings.ToUpper(recordType), contract.ID))
	sb.WriteString(fmt.Sprintf("- **Title**: %s\n", contract.Title))
	if contract.VendorOrLandlord != "" {
		sb.WriteString(fmt.Sprintf("- **Vendor / Landlord**: %s\n", contract.VendorOrLandlord))
	}
	if contract.ContactDetails != "" {
		sb.WriteString(fmt.Sprintf("- **Contact Details**: %s\n", contract.ContactDetails))
	}
	if contract.Amount > 0 {
		sb.WriteString(fmt.Sprintf("- **Amount**: %.2f %s\n", contract.Amount, contract.Currency))
	}
	if contract.StartDate != nil {
		sb.WriteString(fmt.Sprintf("- **Start Date**: %s\n", contract.StartDate.Format("2006-01-02")))
	}
	if contract.ExpirationOrRenewalDate != nil {
		sb.WriteString(fmt.Sprintf("- **Expiration / Renewal Date**: %s\n", contract.ExpirationOrRenewalDate.Format("2006-01-02")))
		if contract.NoticePeriodDays > 0 {
			noticeDeadline := contract.NoticeDeadline()
			if noticeDeadline != nil {
				sb.WriteString(fmt.Sprintf("- **⚠️ Notice Deadline**: %s (%d days before renewal)\n", noticeDeadline.Format("2006-01-02"), contract.NoticePeriodDays))
			}
		}
	}
	if contract.KeyTermsJSON != "" && contract.KeyTermsJSON != "{}" {
		sb.WriteString(fmt.Sprintf("- **Key Terms**: %s\n", contract.KeyTermsJSON))
	}

	return sb.String(), nil
}

// executeListContracts handles listing house contracts and receipts with filters.
func (m *Module) executeListContracts(ctx context.Context, callCtx orchestrator.CallContext, repo db.HouseRepository, args map[string]interface{}) (string, error) {
	familyGroup := resolveFamilyGroup(callCtx)
	recordType, _ := args["record_type"].(string)
	expiringDays := parseInt(args["expiring_within_days"])
	searchQuery, _ := args["search_query"].(string)
	searchQuery = strings.ToLower(strings.TrimSpace(searchQuery))

	var records []models.HouseContractRecord
	var err error

	if expiringDays > 0 {
		records, err = repo.ListExpiringContracts(familyGroup, expiringDays)
	} else {
		records, err = repo.ListContracts(familyGroup, recordType, nil)
	}
	if err != nil {
		return "", fmt.Errorf("failed to list house contracts: %w", err)
	}

	// Filter by search query if provided
	if searchQuery != "" {
		var filtered []models.HouseContractRecord
		for _, r := range records {
			if strings.Contains(strings.ToLower(r.Title), searchQuery) ||
				strings.Contains(strings.ToLower(r.VendorOrLandlord), searchQuery) ||
				strings.Contains(strings.ToLower(r.RecordType), searchQuery) {
				filtered = append(filtered, r)
			}
		}
		records = filtered
	}

	if len(records) == 0 {
		return "No house contracts or receipts found matching the criteria.", nil
	}

	now := time.Now()
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("### House Contracts & Receipts (%d found)\n", len(records)))
	for _, r := range records {
		status := "🟢 Active"
		if r.ExpirationOrRenewalDate != nil {
			if now.After(*r.ExpirationOrRenewalDate) {
				status = "🔴 Expired"
			} else if r.ExpirationOrRenewalDate.Before(now.AddDate(0, 0, 30)) {
				daysLeft := int(r.ExpirationOrRenewalDate.Sub(now).Hours() / 24)
				status = fmt.Sprintf("🟡 Expiring in %d days", daysLeft)
			}
			if r.NoticePeriodDays > 0 {
				noticeDeadline := r.NoticeDeadline()
				if noticeDeadline != nil && now.After(*noticeDeadline) && now.Before(*r.ExpirationOrRenewalDate) {
					status += " (⚠️ Notice cutoff passed)"
				}
			}
		}

		sb.WriteString(fmt.Sprintf("- **[%s] %s** (ID: %d) — %s\n", strings.ToUpper(r.RecordType), r.Title, r.ID, status))
		if r.VendorOrLandlord != "" {
			sb.WriteString(fmt.Sprintf("  • Vendor/Landlord: %s\n", r.VendorOrLandlord))
		}
		if r.Amount > 0 {
			sb.WriteString(fmt.Sprintf("  • Amount: %.2f %s\n", r.Amount, r.Currency))
		}
		if r.ExpirationOrRenewalDate != nil {
			sb.WriteString(fmt.Sprintf("  • Expiration: %s\n", r.ExpirationOrRenewalDate.Format("2006-01-02")))
		}
		if r.NoticePeriodDays > 0 {
			sb.WriteString(fmt.Sprintf("  • Notice Period: %d days\n", r.NoticePeriodDays))
		}
		if r.ContactDetails != "" {
			sb.WriteString(fmt.Sprintf("  • Contact: %s\n", r.ContactDetails))
		}
	}

	return sb.String(), nil
}

// executeManageMaintenance handles CRUD and seasonal suggestions for home maintenance.
func (m *Module) executeManageMaintenance(ctx context.Context, callCtx orchestrator.CallContext, repo db.HouseRepository, args map[string]interface{}) (string, error) {
	action, _ := args["action"].(string)
	action = strings.ToLower(strings.TrimSpace(action))
	familyGroup := resolveFamilyGroup(callCtx)
	userID := resolveUserID(callCtx)

	switch action {
	case "create":
		taskName, _ := args["task_name"].(string)
		taskName = strings.TrimSpace(taskName)
		if taskName == "" {
			return "", fmt.Errorf("task_name is required for creating a maintenance item")
		}

		category, _ := args["category"].(string)
		category = strings.ToLower(strings.TrimSpace(category))
		if category == "" {
			category = models.MaintenanceGeneral
		}

		intervalDays := parseInt(args["interval_days"])
		if intervalDays <= 0 {
			intervalDays = 90
		}

		nextDue, _ := parseDate(args["next_due_at"])
		nextDueAt := time.Now().AddDate(0, 0, intervalDays)
		if nextDue != nil {
			nextDueAt = *nextDue
		}

		contractor, _ := args["contractor_info"].(string)
		notes, _ := args["notes"].(string)

		item := &models.HomeMaintenanceItem{
			FamilyGroup:     familyGroup,
			TaskName:        taskName,
			Category:        category,
			IntervalDays:    intervalDays,
			NextDueAt:       nextDueAt,
			ContractorInfo:  strings.TrimSpace(contractor),
			Notes:           strings.TrimSpace(notes),
			UpdatedByUserID: userID,
		}

		if err := repo.CreateMaintenanceItem(item); err != nil {
			return "", fmt.Errorf("failed to create home maintenance item: %w", err)
		}

		afterBytes, _ := json.Marshal(item)
		m.publishEvent(ctx, callCtx, events.MutationEvent{
			Topic:       "house",
			Action:      "maintenance_created",
			EntityType:  "HomeMaintenanceItem",
			EntityID:    fmt.Sprintf("%d", item.ID),
			ActorUserID: userID,
			FamilyGroup: familyGroup,
			AfterJSON:   string(afterBytes),
			Summary:     fmt.Sprintf("Created home maintenance task: %s (Category: %s, Next due: %s)", item.TaskName, item.Category, item.NextDueAt.Format("2006-01-02")),
			Timestamp:   time.Now(),
		})

		return fmt.Sprintf("✅ **Home Maintenance Task Created** (ID: %d)\n- **Task**: %s\n- **Category**: %s\n- **Interval**: Every %d days\n- **Next Due Date**: %s\n- **Contractor**: %s",
			item.ID, item.TaskName, item.Category, item.IntervalDays, item.NextDueAt.Format("2006-01-02"), item.ContractorInfo), nil

	case "complete":
		itemID := parseInt(args["item_id"])
		if itemID <= 0 {
			return "", fmt.Errorf("item_id is required for complete action")
		}

		existing, err := repo.GetMaintenanceItem(uint(itemID))
		if err != nil {
			return "", fmt.Errorf("failed to load maintenance item: %w", err)
		}
		isAuthorized := existing != nil && ((familyGroup != "" && existing.FamilyGroup == familyGroup) || (existing.UpdatedByUserID != "" && existing.UpdatedByUserID == userID))
		if !isAuthorized {
			return "", fmt.Errorf("maintenance item %d not found in family %q", itemID, familyGroup)
		}

		item, err := repo.CompleteMaintenanceItem(uint(itemID), time.Now(), userID, familyGroup)
		if err != nil {
			return "", fmt.Errorf("failed to mark maintenance item as completed: %w", err)
		}

		afterBytes, _ := json.Marshal(item)
		m.publishEvent(ctx, callCtx, events.MutationEvent{
			Topic:       "house",
			Action:      "maintenance_completed",
			EntityType:  "HomeMaintenanceItem",
			EntityID:    fmt.Sprintf("%d", item.ID),
			ActorUserID: userID,
			FamilyGroup: familyGroup,
			AfterJSON:   string(afterBytes),
			Summary:     fmt.Sprintf("Completed home maintenance task: %s (Next due rolled forward to %s)", item.TaskName, item.NextDueAt.Format("2006-01-02")),
			Timestamp:   time.Now(),
		})

		return fmt.Sprintf("✅ **Marked Maintenance Task as Completed!**\n- **Task**: %s (ID: %d)\n- **Last Completed**: %s\n- **Next Due Date Rolled Forward**: %s (Interval: %d days)",
			item.TaskName, item.ID, item.LastCompletedAt.Format("2006-01-02"), item.NextDueAt.Format("2006-01-02"), item.IntervalDays), nil

	case "update":
		itemID := parseInt(args["item_id"])
		if itemID <= 0 {
			return "", fmt.Errorf("item_id is required for update action")
		}

		existing, err := repo.GetMaintenanceItem(uint(itemID))
		if err != nil {
			return "", fmt.Errorf("failed to load maintenance item: %w", err)
		}
		isAuthorized := existing != nil && ((familyGroup != "" && existing.FamilyGroup == familyGroup) || (existing.UpdatedByUserID != "" && existing.UpdatedByUserID == userID))
		if !isAuthorized {
			return "", fmt.Errorf("maintenance item %d not found in family %q", itemID, familyGroup)
		}

		b, _ := json.Marshal(existing)
		beforeJSON := string(b)

		if taskName, ok := args["task_name"].(string); ok && strings.TrimSpace(taskName) != "" {
			existing.TaskName = strings.TrimSpace(taskName)
		}
		if cat, ok := args["category"].(string); ok && strings.TrimSpace(cat) != "" {
			existing.Category = strings.ToLower(strings.TrimSpace(cat))
		}
		if interval := parseInt(args["interval_days"]); interval > 0 {
			existing.IntervalDays = interval
		}
		if nextDue, _ := parseDate(args["next_due_at"]); nextDue != nil {
			existing.NextDueAt = *nextDue
		}
		if contractor, ok := args["contractor_info"].(string); ok {
			existing.ContractorInfo = strings.TrimSpace(contractor)
		}
		if notes, ok := args["notes"].(string); ok {
			existing.Notes = strings.TrimSpace(notes)
		}
		existing.UpdatedByUserID = userID

		if err := repo.UpdateMaintenanceItem(existing); err != nil {
			return "", fmt.Errorf("failed to update maintenance item: %w", err)
		}

		afterBytes, _ := json.Marshal(existing)
		m.publishEvent(ctx, callCtx, events.MutationEvent{
			Topic:       "house",
			Action:      "maintenance_updated",
			EntityType:  "HomeMaintenanceItem",
			EntityID:    fmt.Sprintf("%d", existing.ID),
			ActorUserID: userID,
			FamilyGroup: familyGroup,
			BeforeJSON:  beforeJSON,
			AfterJSON:   string(afterBytes),
			Summary:     fmt.Sprintf("Updated home maintenance task: %s", existing.TaskName),
			Timestamp:   time.Now(),
		})

		return fmt.Sprintf("✅ **Maintenance Task Updated** (ID: %d): %s (Next due: %s, Interval: %d days)",
			existing.ID, existing.TaskName, existing.NextDueAt.Format("2006-01-02"), existing.IntervalDays), nil

	case "delete":
		itemID := parseInt(args["item_id"])
		if itemID <= 0 {
			return "", fmt.Errorf("item_id is required for delete action")
		}

		existing, err := repo.GetMaintenanceItem(uint(itemID))
		if err != nil {
			return "", fmt.Errorf("failed to load maintenance item: %w", err)
		}
		isAuthorized := existing != nil && ((familyGroup != "" && existing.FamilyGroup == familyGroup) || (existing.UpdatedByUserID != "" && existing.UpdatedByUserID == userID))
		if !isAuthorized {
			return "", fmt.Errorf("maintenance item %d not found in family %q", itemID, familyGroup)
		}

		b, _ := json.Marshal(existing)
		if err := repo.DeleteMaintenanceItem(uint(itemID)); err != nil {
			return "", fmt.Errorf("failed to delete maintenance item: %w", err)
		}

		m.publishEvent(ctx, callCtx, events.MutationEvent{
			Topic:       "house",
			Action:      "maintenance_deleted",
			EntityType:  "HomeMaintenanceItem",
			EntityID:    fmt.Sprintf("%d", itemID),
			ActorUserID: userID,
			FamilyGroup: familyGroup,
			BeforeJSON:  string(b),
			Summary:     fmt.Sprintf("Deleted home maintenance task: %s (ID %d)", existing.TaskName, itemID),
			Timestamp:   time.Now(),
		})

		return fmt.Sprintf("🗑️ Deleted maintenance task %d (%s).", itemID, existing.TaskName), nil

	case "list":
		category, _ := args["category"].(string)
		overdueOnly := parseBool(args["overdue_only"], false)

		items, err := repo.ListMaintenanceItems(familyGroup, category, overdueOnly)
		if err != nil {
			return "", fmt.Errorf("failed to list maintenance items: %w", err)
		}

		if len(items) == 0 {
			return "No home maintenance items found matching the criteria.", nil
		}

		now := time.Now()
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("### Home Maintenance Tasks (%d items)\n", len(items)))
		for _, item := range items {
			status := "🟢 Scheduled"
			if now.After(item.NextDueAt) {
				daysOverdue := int(now.Sub(item.NextDueAt).Hours() / 24)
				status = fmt.Sprintf("🔴 OVERDUE by %d days (Due: %s)", daysOverdue, item.NextDueAt.Format("2006-01-02"))
			} else if item.NextDueAt.Before(now.AddDate(0, 0, 14)) {
				daysRemaining := int(item.NextDueAt.Sub(now).Hours() / 24)
				status = fmt.Sprintf("🟡 Due in %d days (%s)", daysRemaining, item.NextDueAt.Format("2006-01-02"))
			} else {
				status = fmt.Sprintf("🟢 Due %s", item.NextDueAt.Format("2006-01-02"))
			}

			sb.WriteString(fmt.Sprintf("- **%s** (ID: %d) [%s] — %s\n", item.TaskName, item.ID, strings.ToUpper(item.Category), status))
			sb.WriteString(fmt.Sprintf("  • Interval: Every %d days\n", item.IntervalDays))
			if item.LastCompletedAt != nil {
				sb.WriteString(fmt.Sprintf("  • Last Completed: %s\n", item.LastCompletedAt.Format("2006-01-02")))
			}
			if item.ContractorInfo != "" {
				sb.WriteString(fmt.Sprintf("  • Contractor/Service: %s\n", item.ContractorInfo))
			}
			if item.Notes != "" {
				sb.WriteString(fmt.Sprintf("  • Notes: %s\n", item.Notes))
			}
		}
		return sb.String(), nil

	case "suggest_seasonal":
		return m.getSeasonalMaintenanceSuggestions(repo, familyGroup)

	default:
		return "", fmt.Errorf("unknown maintenance action: %s", action)
	}
}

// executeManageVehicle handles CRUD, mileage updates, and service logging for vehicles.
func (m *Module) executeManageVehicle(ctx context.Context, callCtx orchestrator.CallContext, repo db.HouseRepository, args map[string]interface{}) (string, error) {
	action, _ := args["action"].(string)
	action = strings.ToLower(strings.TrimSpace(action))
	familyGroup := resolveFamilyGroup(callCtx)
	userID := resolveUserID(callCtx)

	switch action {
	case "create":
		name, _ := args["name"].(string)
		name = strings.TrimSpace(name)
		if name == "" {
			return "", fmt.Errorf("name is required for creating a vehicle asset")
		}

		plate, _ := args["license_plate"].(string)
		insuranceExpiry, _ := parseDate(args["insurance_expiry"])
		annualTestExpiry, _ := parseDate(args["annual_test_expiry"])
		lastServiceDate, _ := parseDate(args["last_service_date"])
		currentMileage := parseInt(args["current_mileage_km"])
		nextServiceMileage := parseInt(args["next_service_mileage_km"])
		notes, _ := args["notes"].(string)

		vehicle := &models.VehicleAsset{
			FamilyGroup:          familyGroup,
			Name:                 name,
			LicensePlate:         strings.TrimSpace(plate),
			InsuranceExpiry:      insuranceExpiry,
			AnnualTestExpiry:     annualTestExpiry,
			LastServiceDate:      lastServiceDate,
			CurrentMileageKm:     currentMileage,
			NextServiceMileageKm: nextServiceMileage,
			Notes:                strings.TrimSpace(notes),
		}

		if err := repo.CreateVehicleAsset(vehicle); err != nil {
			return "", fmt.Errorf("failed to create vehicle asset: %w", err)
		}

		afterBytes, _ := json.Marshal(vehicle)
		m.publishEvent(ctx, callCtx, events.MutationEvent{
			Topic:       "house",
			Action:      "vehicle_created",
			EntityType:  "VehicleAsset",
			EntityID:    fmt.Sprintf("%d", vehicle.ID),
			ActorUserID: userID,
			FamilyGroup: familyGroup,
			AfterJSON:   string(afterBytes),
			Summary:     fmt.Sprintf("Registered vehicle: %s (Plate: %s, Mileage: %d km)", vehicle.Name, vehicle.LicensePlate, vehicle.CurrentMileageKm),
			Timestamp:   time.Now(),
		})

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("🚗 **Vehicle Asset Registered** (ID: %d)\n", vehicle.ID))
		sb.WriteString(fmt.Sprintf("- **Name**: %s\n", vehicle.Name))
		if vehicle.LicensePlate != "" {
			sb.WriteString(fmt.Sprintf("- **License Plate**: %s\n", vehicle.LicensePlate))
		}
		if vehicle.CurrentMileageKm > 0 {
			sb.WriteString(fmt.Sprintf("- **Current Mileage**: %d km\n", vehicle.CurrentMileageKm))
		}
		if vehicle.NextServiceMileageKm > 0 {
			sb.WriteString(fmt.Sprintf("- **Next Service Due**: %d km\n", vehicle.NextServiceMileageKm))
		}
		if vehicle.AnnualTestExpiry != nil {
			sb.WriteString(fmt.Sprintf("- **Annual Test Expiry**: %s\n", vehicle.AnnualTestExpiry.Format("2006-01-02")))
		}
		if vehicle.InsuranceExpiry != nil {
			sb.WriteString(fmt.Sprintf("- **Insurance Expiry**: %s\n", vehicle.InsuranceExpiry.Format("2006-01-02")))
		}

		return sb.String(), nil

	case "update":
		assetID := parseInt(args["asset_id"])
		if assetID <= 0 {
			return "", fmt.Errorf("asset_id is required for update action")
		}

		existing, err := repo.GetVehicleAsset(uint(assetID))
		if err != nil {
			return "", fmt.Errorf("failed to load vehicle asset: %w", err)
		}
		isAuthorized := existing != nil && existing.FamilyGroup != "" && existing.FamilyGroup == familyGroup
		if !isAuthorized {
			return "", fmt.Errorf("vehicle asset %d not found in family %q", assetID, familyGroup)
		}

		b, _ := json.Marshal(existing)
		beforeJSON := string(b)

		if name, ok := args["name"].(string); ok && strings.TrimSpace(name) != "" {
			existing.Name = strings.TrimSpace(name)
		}
		if plate, ok := args["license_plate"].(string); ok {
			existing.LicensePlate = strings.TrimSpace(plate)
		}
		if ins, _ := parseDate(args["insurance_expiry"]); ins != nil {
			existing.InsuranceExpiry = ins
		}
		if test, _ := parseDate(args["annual_test_expiry"]); test != nil {
			existing.AnnualTestExpiry = test
		}
		if srv, _ := parseDate(args["last_service_date"]); srv != nil {
			existing.LastServiceDate = srv
		}
		if km := parseInt(args["current_mileage_km"]); km > 0 {
			existing.CurrentMileageKm = km
		}
		if nextKm := parseInt(args["next_service_mileage_km"]); nextKm > 0 {
			existing.NextServiceMileageKm = nextKm
		}
		if notes, ok := args["notes"].(string); ok {
			existing.Notes = strings.TrimSpace(notes)
		}

		if err := repo.UpdateVehicleAsset(existing); err != nil {
			return "", fmt.Errorf("failed to update vehicle asset: %w", err)
		}

		afterBytes, _ := json.Marshal(existing)
		m.publishEvent(ctx, callCtx, events.MutationEvent{
			Topic:       "house",
			Action:      "vehicle_updated",
			EntityType:  "VehicleAsset",
			EntityID:    fmt.Sprintf("%d", existing.ID),
			ActorUserID: userID,
			FamilyGroup: familyGroup,
			BeforeJSON:  beforeJSON,
			AfterJSON:   string(afterBytes),
			Summary:     fmt.Sprintf("Updated vehicle asset: %s", existing.Name),
			Timestamp:   time.Now(),
		})

		return fmt.Sprintf("✅ **Vehicle Asset Updated** (ID: %d): %s (Mileage: %d km)", existing.ID, existing.Name, existing.CurrentMileageKm), nil

	case "update_mileage":
		assetID := parseInt(args["asset_id"])
		if assetID <= 0 {
			return "", fmt.Errorf("asset_id is required for update_mileage action")
		}
		mileage := parseInt(args["current_mileage_km"])
		if mileage <= 0 {
			return "", fmt.Errorf("valid current_mileage_km is required")
		}

		existing, err := repo.GetVehicleAsset(uint(assetID))
		if err != nil {
			return "", fmt.Errorf("failed to load vehicle asset: %w", err)
		}
		isAuthorized := existing != nil && existing.FamilyGroup != "" && existing.FamilyGroup == familyGroup
		if !isAuthorized {
			return "", fmt.Errorf("vehicle asset %d not found in family %q", assetID, familyGroup)
		}

		vehicle, err := repo.UpdateVehicleMileage(uint(assetID), mileage)
		if err != nil {
			return "", fmt.Errorf("failed to update vehicle mileage: %w", err)
		}

		serviceWarning := ""
		if vehicle.IsServiceDue() {
			serviceWarning = fmt.Sprintf("\n⚠️ **Service Due Warning**: Mileage %d km has reached or exceeded scheduled service target of %d km!", vehicle.CurrentMileageKm, vehicle.NextServiceMileageKm)
		}

		afterBytes, _ := json.Marshal(vehicle)
		m.publishEvent(ctx, callCtx, events.MutationEvent{
			Topic:       "house",
			Action:      "vehicle_mileage_updated",
			EntityType:  "VehicleAsset",
			EntityID:    fmt.Sprintf("%d", vehicle.ID),
			ActorUserID: userID,
			FamilyGroup: familyGroup,
			AfterJSON:   string(afterBytes),
			Summary:     fmt.Sprintf("Updated mileage for %s to %d km", vehicle.Name, vehicle.CurrentMileageKm),
			Timestamp:   time.Now(),
		})

		return fmt.Sprintf("✅ **Updated Odometer for %s** (ID: %d): %d km%s", vehicle.Name, vehicle.ID, vehicle.CurrentMileageKm, serviceWarning), nil

	case "log_service":
		assetID := parseInt(args["asset_id"])
		if assetID <= 0 {
			return "", fmt.Errorf("asset_id is required for log_service action")
		}

		existing, err := repo.GetVehicleAsset(uint(assetID))
		if err != nil {
			return "", fmt.Errorf("failed to load vehicle asset: %w", err)
		}
		isAuthorized := existing != nil && existing.FamilyGroup != "" && existing.FamilyGroup == familyGroup
		if !isAuthorized {
			return "", fmt.Errorf("vehicle asset %d not found in family %q", assetID, familyGroup)
		}

		b, _ := json.Marshal(existing)
		beforeJSON := string(b)

		now := time.Now()
		existing.LastServiceDate = &now
		if srvDate, _ := parseDate(args["last_service_date"]); srvDate != nil {
			existing.LastServiceDate = srvDate
		}

		if km := parseInt(args["current_mileage_km"]); km > 0 {
			existing.CurrentMileageKm = km
		}

		nextServiceKm := parseInt(args["next_service_mileage_km"])
		if nextServiceKm > 0 {
			existing.NextServiceMileageKm = nextServiceKm
		} else if existing.CurrentMileageKm > 0 {
			existing.NextServiceMileageKm = existing.CurrentMileageKm + 15000 // default 15,000 km service interval
		}

		if notes, ok := args["notes"].(string); ok && strings.TrimSpace(notes) != "" {
			if existing.Notes != "" {
				existing.Notes += "; " + strings.TrimSpace(notes)
			} else {
				existing.Notes = strings.TrimSpace(notes)
			}
		}

		if err := repo.UpdateVehicleAsset(existing); err != nil {
			return "", fmt.Errorf("failed to log vehicle service: %w", err)
		}

		afterBytes, _ := json.Marshal(existing)
		m.publishEvent(ctx, callCtx, events.MutationEvent{
			Topic:       "house",
			Action:      "vehicle_service_logged",
			EntityType:  "VehicleAsset",
			EntityID:    fmt.Sprintf("%d", existing.ID),
			ActorUserID: userID,
			FamilyGroup: familyGroup,
			BeforeJSON:  beforeJSON,
			AfterJSON:   string(afterBytes),
			Summary:     fmt.Sprintf("Logged service for %s at %d km (Next service at %d km)", existing.Name, existing.CurrentMileageKm, existing.NextServiceMileageKm),
			Timestamp:   time.Now(),
		})

		return fmt.Sprintf("🔧 **Service Logged for %s** (ID: %d)\n- **Service Date**: %s\n- **Odometer**: %d km\n- **Next Service Due at**: %d km",
			existing.Name, existing.ID, existing.LastServiceDate.Format("2006-01-02"), existing.CurrentMileageKm, existing.NextServiceMileageKm), nil

	case "delete":
		assetID := parseInt(args["asset_id"])
		if assetID <= 0 {
			return "", fmt.Errorf("asset_id is required for delete action")
		}

		existing, err := repo.GetVehicleAsset(uint(assetID))
		if err != nil {
			return "", fmt.Errorf("failed to load vehicle asset: %w", err)
		}
		isAuthorized := existing != nil && existing.FamilyGroup != "" && existing.FamilyGroup == familyGroup
		if !isAuthorized {
			return "", fmt.Errorf("vehicle asset %d not found in family %q", assetID, familyGroup)
		}

		b, _ := json.Marshal(existing)
		if err := repo.DeleteVehicleAsset(uint(assetID)); err != nil {
			return "", fmt.Errorf("failed to delete vehicle asset: %w", err)
		}

		m.publishEvent(ctx, callCtx, events.MutationEvent{
			Topic:       "house",
			Action:      "vehicle_deleted",
			EntityType:  "VehicleAsset",
			EntityID:    fmt.Sprintf("%d", assetID),
			ActorUserID: userID,
			FamilyGroup: familyGroup,
			BeforeJSON:  string(b),
			Summary:     fmt.Sprintf("Deleted vehicle asset: %s", existing.Name),
			Timestamp:   time.Now(),
		})

		return fmt.Sprintf("🗑️ Deleted vehicle asset %d (%s).", assetID, existing.Name), nil

	case "list":
		vehicles, err := repo.ListVehicleAssets(familyGroup)
		if err != nil {
			return "", fmt.Errorf("failed to list vehicle assets: %w", err)
		}

		if len(vehicles) == 0 {
			return "No vehicle assets registered for your family group.", nil
		}

		now := time.Now()
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("### Family Vehicle Vault (%d vehicles)\n", len(vehicles)))
		for _, v := range vehicles {
			sb.WriteString(fmt.Sprintf("- **%s** (ID: %d)", v.Name, v.ID))
			if v.LicensePlate != "" {
				sb.WriteString(fmt.Sprintf(" — Plate: `%s`", v.LicensePlate))
			}
			sb.WriteString("\n")

			// Mileage & Service
			if v.CurrentMileageKm > 0 || v.NextServiceMileageKm > 0 {
				serviceStatus := "🟢 Service OK"
				if v.IsServiceDue() {
					serviceStatus = fmt.Sprintf("🔴 Service OVERDUE by %d km!", v.CurrentMileageKm-v.NextServiceMileageKm)
				}
				sb.WriteString(fmt.Sprintf("  • Odometer: %d km | Next Service: %d km (%s)\n", v.CurrentMileageKm, v.NextServiceMileageKm, serviceStatus))
			}
			if v.LastServiceDate != nil {
				sb.WriteString(fmt.Sprintf("  • Last Serviced: %s\n", v.LastServiceDate.Format("2006-01-02")))
			}

			// Annual Inspection / Test
			if v.AnnualTestExpiry != nil {
				testStatus := fmt.Sprintf("Exp: %s", v.AnnualTestExpiry.Format("2006-01-02"))
				days := int(v.AnnualTestExpiry.Sub(now).Hours() / 24)
				if days < 0 {
					testStatus = fmt.Sprintf("🔴 OVERDUE by %d days (%s)", -days, v.AnnualTestExpiry.Format("2006-01-02"))
				} else if days <= 30 {
					testStatus = fmt.Sprintf("🟡 Due in %d days (%s)", days, v.AnnualTestExpiry.Format("2006-01-02"))
				}
				sb.WriteString(fmt.Sprintf("  • Annual Test / Inspection: %s\n", testStatus))
			}

			// Insurance
			if v.InsuranceExpiry != nil {
				insStatus := fmt.Sprintf("Exp: %s", v.InsuranceExpiry.Format("2006-01-02"))
				days := int(v.InsuranceExpiry.Sub(now).Hours() / 24)
				if days < 0 {
					insStatus = fmt.Sprintf("🔴 EXPIRED %d days ago (%s)", -days, v.InsuranceExpiry.Format("2006-01-02"))
				} else if days <= 30 {
					insStatus = fmt.Sprintf("🟡 Expiring in %d days (%s)", days, v.InsuranceExpiry.Format("2006-01-02"))
				}
				sb.WriteString(fmt.Sprintf("  • Insurance Policy: %s\n", insStatus))
			}

			if v.Notes != "" {
				sb.WriteString(fmt.Sprintf("  • Notes: %s\n", v.Notes))
			}
		}

		return sb.String(), nil

	default:
		return "", fmt.Errorf("unknown vehicle action: %s", action)
	}
}

// getSeasonalMaintenanceSuggestions provides season-specific home maintenance upkeep templates.
func (m *Module) getSeasonalMaintenanceSuggestions(repo db.HouseRepository, familyGroup string) (string, error) {
	currentMonth := time.Now().Month()
	season := "General"
	switch currentMonth {
	case time.March, time.April, time.May:
		season = "Spring"
	case time.June, time.July, time.August:
		season = "Summer"
	case time.September, time.October, time.November:
		season = "Fall"
	case time.December, time.January, time.February:
		season = "Winter"
	}

	type suggestion struct {
		name     string
		category string
		interval int
		notes    string
		season   string
	}

	suggestions := []suggestion{
		// Spring
		{name: "AC Filter Replacement & Condenser Check", category: models.MaintenanceHVAC, interval: 90, notes: "Replace indoor air filters and clear debris around outdoor compressor", season: "Spring"},
		{name: "Roof & Gutter Inspection", category: models.MaintenanceGeneral, interval: 180, notes: "Clear winter debris, check for loose shingles and clean downspouts", season: "Spring"},
		{name: "Smoke & CO Detector Battery Test", category: models.MaintenanceGeneral, interval: 180, notes: "Press test buttons on all smoke and carbon monoxide alarms", season: "Spring"},
		// Summer
		{name: "Irrigation & Sprinkler System Check", category: models.MaintenancePlumbing, interval: 90, notes: "Inspect sprinkler heads for clogs and verify timer settings", season: "Summer"},
		{name: "Refrigerator Coil Cleaning", category: models.MaintenanceAppliance, interval: 180, notes: "Vacuum dust from behind/under refrigerator condenser coils", season: "Summer"},
		{name: "Pest Control Perimeter Spray", category: models.MaintenancePestControl, interval: 180, notes: "Inspect foundations and apply barrier pest prevention", season: "Summer"},
		// Fall
		{name: "Boiler & Heating System Tune-Up", category: models.MaintenanceHVAC, interval: 365, notes: "Schedule certified technician inspection before cold weather", season: "Fall"},
		{name: "Chimney & Fireplace Inspection", category: models.MaintenanceGeneral, interval: 365, notes: "Check flue damper and have chimney swept if wood-burning", season: "Fall"},
		{name: "Exterior Pipe & Hose Bib Winterization", category: models.MaintenancePlumbing, interval: 365, notes: "Drain outdoor spigots and insulate exposed pipes to prevent freezing", season: "Fall"},
		// Winter
		{name: "Water Heater Flush & Descaling", category: models.MaintenancePlumbing, interval: 365, notes: "Drain sediment from tank or descale tankless heat exchanger", season: "Winter"},
		{name: "Dryer Vent Exhaust Cleaning", category: models.MaintenanceAppliance, interval: 180, notes: "Disconnect vent hose and vacuum lint buildup to prevent fire hazard", season: "Winter"},
		{name: "Door & Window Weatherstripping Check", category: models.MaintenanceGeneral, interval: 365, notes: "Inspect draft seals and replace worn weatherstripping", season: "Winter"},
	}

	// Fetch existing to indicate which tasks are already added
	existingItems, _ := repo.ListMaintenanceItems(familyGroup, "all", false)
	existingMap := make(map[string]bool)
	for _, it := range existingItems {
		existingMap[strings.ToLower(strings.TrimSpace(it.TaskName))] = true
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("### Seasonal Home Maintenance Suggestions (Current Season: %s)\n", season))
	sb.WriteString("Regular preventive upkeep keeps home systems operating efficiently and prevents costly repairs.\n\n")

	for _, s := range suggestions {
		isCurrentSeason := s.season == season
		badge := fmt.Sprintf("[%s]", s.season)
		if isCurrentSeason {
			badge = fmt.Sprintf("🌟 **[%s RECOMMENDED NOW]**", s.season)
		}

		alreadyAdded := existingMap[strings.ToLower(s.name)]
		statusNote := "➕ Recommended"
		if alreadyAdded {
			statusNote = "✅ Already in your maintenance list"
		}

		sb.WriteString(fmt.Sprintf("- %s **%s** (%s, every %d days) — *%s*\n", badge, s.name, strings.ToUpper(s.category), s.interval, statusNote))
		sb.WriteString(fmt.Sprintf("  • Upkeep Tip: %s\n", s.notes))
	}

	sb.WriteString("\n*To add any of these tasks, tell me: 'Add [Task Name] every [Interval] days under [Category]'*")

	return sb.String(), nil
}

// resolveRepo fetches the active HouseRepository from module or call context.
func (m *Module) resolveRepo(callCtx orchestrator.CallContext) (db.HouseRepository, error) {
	if m.repo != nil {
		return m.repo, nil
	}
	if repo, ok := callCtx.DB.(db.HouseRepository); ok {
		return repo, nil
	}
	if extraRepo, ok := callCtx.Extra["house_repo"].(db.HouseRepository); ok {
		return extraRepo, nil
	}
	return nil, fmt.Errorf("house: no house repository available in module or call context")
}

// publishEvent sends a MutationEvent to the configured event bus.
func (m *Module) publishEvent(ctx context.Context, callCtx orchestrator.CallContext, event events.MutationEvent) {
	bus := m.bus
	if bus == nil && callCtx.EventBus != nil {
		bus = callCtx.EventBus
	}
	if bus == nil {
		return
	}

	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	if event.Topic == "" {
		event.Topic = "house"
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	_ = bus.Publish(ctx, event)
}

func resolveFamilyGroup(callCtx orchestrator.CallContext) string {
	return callCtx.TenantFamilyGroup()
}

func resolveUserID(callCtx orchestrator.CallContext) string {
	return callCtx.ActorID()
}

func parseDate(v interface{}) (*time.Time, error) {
	if v == nil {
		return nil, nil
	}
	s, ok := v.(string)
	if !ok {
		return nil, nil
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
		"02/01/2006",
		"01/02/2006",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return &t, nil
		}
	}
	return nil, fmt.Errorf("cannot parse date: %s", s)
}

func parseFloat(v interface{}) float64 {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return val
	case float32:
		return float64(val)
	case int:
		return float64(val)
	case int64:
		return float64(val)
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(val), 64)
		return f
	case json.Number:
		f, _ := val.Float64()
		return f
	default:
		return 0
	}
}

func parseInt(v interface{}) int {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case int:
		return val
	case int64:
		return int(val)
	case float64:
		return int(val)
	case float32:
		return int(val)
	case string:
		i, _ := strconv.Atoi(strings.TrimSpace(val))
		return i
	case json.Number:
		i, _ := val.Int64()
		return int(i)
	default:
		return 0
	}
}

func parseBool(v interface{}, defaultVal bool) bool {
	if v == nil {
		return defaultVal
	}
	switch val := v.(type) {
	case bool:
		return val
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(val))
		if err != nil {
			return defaultVal
		}
		return b
	default:
		return defaultVal
	}
}

func parseKeyTerms(v interface{}) string {
	if v == nil {
		return "{}"
	}
	switch val := v.(type) {
	case string:
		val = strings.TrimSpace(val)
		if val == "" {
			return "{}"
		}
		// If valid JSON, keep as is
		var js map[string]interface{}
		if err := json.Unmarshal([]byte(val), &js); err == nil {
			return val
		}
		// Wrap string in JSON terms
		b, _ := json.Marshal(map[string]string{"summary": val})
		return string(b)
	case map[string]interface{}:
		b, err := json.Marshal(val)
		if err != nil {
			return "{}"
		}
		return string(b)
	default:
		return "{}"
	}
}
