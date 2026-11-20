package house

import (
	"context"
	"fmt"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/orchestrator"
	"strings"
	"time"
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
	// concurrency managed by repository
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

// SystemPromptSection injects active house notices, pending maintenance due,
// and upcoming vehicle inspection/insurance dates into the system prompt.
func (m *Module) SystemPromptSection(ctx context.Context, callCtx orchestrator.CallContext) string {
	scope := resolveScope(callCtx)
	if !scope.Valid() {
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
	expiring, err := repo.ListExpiringContracts(scope, 60)
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
	maintenance, err := repo.ListMaintenanceItems(scope, "all", false)
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
	vehicles, err := repo.ListVehicleAssets(scope)
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
