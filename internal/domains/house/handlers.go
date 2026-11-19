package house

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"strings"
	"time"
)

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
		currency = callCtx.Scope.Currency("USD") // the household's currency (V4-30)
	}
	noticePeriod := parseInt(args["notice_period_days"])

	startDate, _ := parseDate(args["start_date"])
	expirationDate, _ := parseDate(args["expiration_date"])

	keyTerms := parseKeyTerms(args["key_terms"])

	scope := resolveScope(callCtx)
	userID := resolveUserID(callCtx)

	contractID := parseInt(args["contract_id"])
	var contract *models.HouseContractRecord
	var beforeJSON string
	action := "contract_saved"

	if contractID > 0 {
		existing, err := repo.GetContract(scope, uint(contractID))
		if err != nil {
			return "", fmt.Errorf("failed to load contract %d: %w", contractID, err)
		}
		isAuthorized := existing != nil && existing.HouseholdID == scope.HouseholdID()
		if !isAuthorized {
			return "", fmt.Errorf("contract %d not found in family %q", contractID, scope.LegacyLabel())
		}
		b, _ := json.Marshal(existing)
		beforeJSON = string(b)
		contract = existing
		action = "contract_updated"
	} else {
		contract = &models.HouseContractRecord{
			CreatedByUserID: userID,
			FamilyGroup:     scope.LegacyLabel(),
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
		err = repo.UpdateContract(scope, contract)
	} else {
		err = repo.CreateContract(scope, contract)
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
		FamilyGroup: scope.LegacyLabel(),
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
	scope := resolveScope(callCtx)
	recordType, _ := args["record_type"].(string)
	expiringDays := parseInt(args["expiring_within_days"])
	searchQuery, _ := args["search_query"].(string)
	searchQuery = strings.ToLower(strings.TrimSpace(searchQuery))

	var records []models.HouseContractRecord
	var err error

	if expiringDays > 0 {
		records, err = repo.ListExpiringContracts(scope, expiringDays)
	} else {
		records, err = repo.ListContracts(scope, recordType, nil)
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
	scope := resolveScope(callCtx)
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
			FamilyGroup:     scope.LegacyLabel(),
			TaskName:        taskName,
			Category:        category,
			IntervalDays:    intervalDays,
			NextDueAt:       nextDueAt,
			ContractorInfo:  strings.TrimSpace(contractor),
			Notes:           strings.TrimSpace(notes),
			UpdatedByUserID: userID,
		}

		if err := repo.CreateMaintenanceItem(scope, item); err != nil {
			return "", fmt.Errorf("failed to create home maintenance item: %w", err)
		}

		afterBytes, _ := json.Marshal(item)
		m.publishEvent(ctx, callCtx, events.MutationEvent{
			Topic:       "house",
			Action:      "maintenance_created",
			EntityType:  "HomeMaintenanceItem",
			EntityID:    fmt.Sprintf("%d", item.ID),
			ActorUserID: userID,
			FamilyGroup: scope.LegacyLabel(),
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

		existing, err := repo.GetMaintenanceItem(scope, uint(itemID))
		if err != nil {
			return "", fmt.Errorf("failed to load maintenance item: %w", err)
		}
		isAuthorized := existing != nil && existing.HouseholdID == scope.HouseholdID()
		if !isAuthorized {
			return "", fmt.Errorf("maintenance item %d not found in family %q", itemID, scope.LegacyLabel())
		}

		item, err := repo.CompleteMaintenanceItem(scope, uint(itemID), time.Now(), userID)
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
			FamilyGroup: scope.LegacyLabel(),
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

		existing, err := repo.GetMaintenanceItem(scope, uint(itemID))
		if err != nil {
			return "", fmt.Errorf("failed to load maintenance item: %w", err)
		}
		isAuthorized := existing != nil && existing.HouseholdID == scope.HouseholdID()
		if !isAuthorized {
			return "", fmt.Errorf("maintenance item %d not found in family %q", itemID, scope.LegacyLabel())
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

		if err := repo.UpdateMaintenanceItem(scope, existing); err != nil {
			return "", fmt.Errorf("failed to update maintenance item: %w", err)
		}

		afterBytes, _ := json.Marshal(existing)
		m.publishEvent(ctx, callCtx, events.MutationEvent{
			Topic:       "house",
			Action:      "maintenance_updated",
			EntityType:  "HomeMaintenanceItem",
			EntityID:    fmt.Sprintf("%d", existing.ID),
			ActorUserID: userID,
			FamilyGroup: scope.LegacyLabel(),
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

		existing, err := repo.GetMaintenanceItem(scope, uint(itemID))
		if err != nil {
			return "", fmt.Errorf("failed to load maintenance item: %w", err)
		}
		isAuthorized := existing != nil && existing.HouseholdID == scope.HouseholdID()
		if !isAuthorized {
			return "", fmt.Errorf("maintenance item %d not found in family %q", itemID, scope.LegacyLabel())
		}

		b, _ := json.Marshal(existing)
		if err := repo.DeleteMaintenanceItem(scope, uint(itemID)); err != nil {
			return "", fmt.Errorf("failed to delete maintenance item: %w", err)
		}

		m.publishEvent(ctx, callCtx, events.MutationEvent{
			Topic:       "house",
			Action:      "maintenance_deleted",
			EntityType:  "HomeMaintenanceItem",
			EntityID:    fmt.Sprintf("%d", itemID),
			ActorUserID: userID,
			FamilyGroup: scope.LegacyLabel(),
			BeforeJSON:  string(b),
			Summary:     fmt.Sprintf("Deleted home maintenance task: %s (ID %d)", existing.TaskName, itemID),
			Timestamp:   time.Now(),
		})

		return fmt.Sprintf("🗑️ Deleted maintenance task %d (%s).", itemID, existing.TaskName), nil

	case "list":
		category, _ := args["category"].(string)
		overdueOnly := parseBool(args["overdue_only"], false)

		items, err := repo.ListMaintenanceItems(scope, category, overdueOnly)
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
			var status string
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
		return m.getSeasonalMaintenanceSuggestions(repo, scope)

	default:
		return "", fmt.Errorf("unknown maintenance action: %s", action)
	}
}

// executeManageVehicle handles CRUD, mileage updates, and service logging for vehicles.
func (m *Module) executeManageVehicle(ctx context.Context, callCtx orchestrator.CallContext, repo db.HouseRepository, args map[string]interface{}) (string, error) {
	action, _ := args["action"].(string)
	action = strings.ToLower(strings.TrimSpace(action))
	scope := resolveScope(callCtx)
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
			FamilyGroup:          scope.LegacyLabel(),
			Name:                 name,
			LicensePlate:         strings.TrimSpace(plate),
			InsuranceExpiry:      insuranceExpiry,
			AnnualTestExpiry:     annualTestExpiry,
			LastServiceDate:      lastServiceDate,
			CurrentMileageKm:     currentMileage,
			NextServiceMileageKm: nextServiceMileage,
			Notes:                strings.TrimSpace(notes),
		}

		if err := repo.CreateVehicleAsset(scope, vehicle); err != nil {
			return "", fmt.Errorf("failed to create vehicle asset: %w", err)
		}

		afterBytes, _ := json.Marshal(vehicle)
		m.publishEvent(ctx, callCtx, events.MutationEvent{
			Topic:       "house",
			Action:      "vehicle_created",
			EntityType:  "VehicleAsset",
			EntityID:    fmt.Sprintf("%d", vehicle.ID),
			ActorUserID: userID,
			FamilyGroup: scope.LegacyLabel(),
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

		existing, err := repo.GetVehicleAsset(scope, uint(assetID))
		if err != nil {
			return "", fmt.Errorf("failed to load vehicle asset: %w", err)
		}
		isAuthorized := existing != nil && existing.HouseholdID == scope.HouseholdID()
		if !isAuthorized {
			return "", fmt.Errorf("vehicle asset %d not found in family %q", assetID, scope.LegacyLabel())
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

		if err := repo.UpdateVehicleAsset(scope, existing); err != nil {
			return "", fmt.Errorf("failed to update vehicle asset: %w", err)
		}

		afterBytes, _ := json.Marshal(existing)
		m.publishEvent(ctx, callCtx, events.MutationEvent{
			Topic:       "house",
			Action:      "vehicle_updated",
			EntityType:  "VehicleAsset",
			EntityID:    fmt.Sprintf("%d", existing.ID),
			ActorUserID: userID,
			FamilyGroup: scope.LegacyLabel(),
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

		existing, err := repo.GetVehicleAsset(scope, uint(assetID))
		if err != nil {
			return "", fmt.Errorf("failed to load vehicle asset: %w", err)
		}
		isAuthorized := existing != nil && existing.HouseholdID == scope.HouseholdID()
		if !isAuthorized {
			return "", fmt.Errorf("vehicle asset %d not found in family %q", assetID, scope.LegacyLabel())
		}

		vehicle, err := repo.UpdateVehicleMileage(scope, uint(assetID), mileage)
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
			FamilyGroup: scope.LegacyLabel(),
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

		existing, err := repo.GetVehicleAsset(scope, uint(assetID))
		if err != nil {
			return "", fmt.Errorf("failed to load vehicle asset: %w", err)
		}
		isAuthorized := existing != nil && existing.HouseholdID == scope.HouseholdID()
		if !isAuthorized {
			return "", fmt.Errorf("vehicle asset %d not found in family %q", assetID, scope.LegacyLabel())
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

		if err := repo.UpdateVehicleAsset(scope, existing); err != nil {
			return "", fmt.Errorf("failed to log vehicle service: %w", err)
		}

		afterBytes, _ := json.Marshal(existing)
		m.publishEvent(ctx, callCtx, events.MutationEvent{
			Topic:       "house",
			Action:      "vehicle_service_logged",
			EntityType:  "VehicleAsset",
			EntityID:    fmt.Sprintf("%d", existing.ID),
			ActorUserID: userID,
			FamilyGroup: scope.LegacyLabel(),
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

		existing, err := repo.GetVehicleAsset(scope, uint(assetID))
		if err != nil {
			return "", fmt.Errorf("failed to load vehicle asset: %w", err)
		}
		isAuthorized := existing != nil && existing.HouseholdID == scope.HouseholdID()
		if !isAuthorized {
			return "", fmt.Errorf("vehicle asset %d not found in family %q", assetID, scope.LegacyLabel())
		}

		b, _ := json.Marshal(existing)
		if err := repo.DeleteVehicleAsset(scope, uint(assetID)); err != nil {
			return "", fmt.Errorf("failed to delete vehicle asset: %w", err)
		}

		m.publishEvent(ctx, callCtx, events.MutationEvent{
			Topic:       "house",
			Action:      "vehicle_deleted",
			EntityType:  "VehicleAsset",
			EntityID:    fmt.Sprintf("%d", assetID),
			ActorUserID: userID,
			FamilyGroup: scope.LegacyLabel(),
			BeforeJSON:  string(b),
			Summary:     fmt.Sprintf("Deleted vehicle asset: %s", existing.Name),
			Timestamp:   time.Now(),
		})

		return fmt.Sprintf("🗑️ Deleted vehicle asset %d (%s).", assetID, existing.Name), nil

	case "list":
		vehicles, err := repo.ListVehicleAssets(scope)
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
