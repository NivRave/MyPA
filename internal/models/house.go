package models

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// RecordType constants for HouseContractRecord.
const (
	HouseRecordLease     = "lease"
	HouseRecordMortgage  = "mortgage"
	HouseRecordUtility   = "utility"
	HouseRecordWarranty  = "warranty"
	HouseRecordInsurance = "insurance"
	HouseRecordReceipt   = "receipt"
)

// Category constants for HomeMaintenanceItem.
const (
	MaintenanceHVAC        = "hvac"
	MaintenancePlumbing    = "plumbing"
	MaintenanceAppliance   = "appliance"
	MaintenancePestControl = "pest_control"
	MaintenanceGeneral     = "general"
)

// HouseContractRecord stores leases, mortgages, utility contracts, warranties,
// insurance policies, and purchase/repair receipts.
type HouseContractRecord struct {
	ID                      uint       `json:"id" gorm:"primaryKey"`
	HouseholdID             string     `json:"household_id" gorm:"type:uuid;not null;index"` // V4-30: the tenant key; FamilyGroup is still written until V4-44
	FamilyGroup             string     `json:"family_group" gorm:"index;index:idx_house_contracts_fam_exp,priority:1"`
	RecordType              string     `json:"record_type" gorm:"index"` // lease, mortgage, utility, warranty, insurance, receipt
	Title                   string     `json:"title"`
	VendorOrLandlord        string     `json:"vendor_or_landlord"`
	ContactDetails          string     `json:"contact_details"`
	Amount                  float64    `json:"amount"`
	Currency                string     `json:"currency"`
	StartDate               *time.Time `json:"start_date,omitempty"`
	ExpirationOrRenewalDate *time.Time `json:"expiration_or_renewal_date,omitempty" gorm:"index;index:idx_house_contracts_fam_exp,priority:2"`
	NoticePeriodDays        int        `json:"notice_period_days"`
	KeyTermsJSON            string     `json:"key_terms_json" gorm:"type:jsonb"`
	CreatedByUserID         string     `json:"created_by_user_id"`
	CreatedAt               time.Time  `json:"created_at"`
}

// KeyTerms unmarshals KeyTermsJSON into a key-value map.
func (c *HouseContractRecord) KeyTerms() map[string]interface{} {
	if strings.TrimSpace(c.KeyTermsJSON) == "" {
		return make(map[string]interface{})
	}
	var terms map[string]interface{}
	if err := json.Unmarshal([]byte(c.KeyTermsJSON), &terms); err != nil {
		return map[string]interface{}{"raw": c.KeyTermsJSON}
	}
	return terms
}

// SetKeyTerms serializes a key-value map into KeyTermsJSON.
func (c *HouseContractRecord) SetKeyTerms(terms map[string]interface{}) error {
	if len(terms) == 0 {
		c.KeyTermsJSON = "{}"
		return nil
	}
	b, err := json.Marshal(terms)
	if err != nil {
		return fmt.Errorf("failed to marshal key terms: %w", err)
	}
	c.KeyTermsJSON = string(b)
	return nil
}

// NoticeDeadline returns the cutoff date by which notice must be given before renewal or expiration.
func (c *HouseContractRecord) NoticeDeadline() *time.Time {
	if c.ExpirationOrRenewalDate == nil {
		return nil
	}
	d := c.ExpirationOrRenewalDate.AddDate(0, 0, -c.NoticePeriodDays)
	return &d
}

// IsExpiringSoon checks whether the contract expires within the given number of days.
func (c *HouseContractRecord) IsExpiringSoon(now time.Time, withinDays int) bool {
	if c.ExpirationOrRenewalDate == nil {
		return false
	}
	cutoff := now.AddDate(0, 0, withinDays)
	return c.ExpirationOrRenewalDate.After(now.Add(-24*time.Hour)) && c.ExpirationOrRenewalDate.Before(cutoff)
}

// HomeMaintenanceItem tracks recurring or seasonal home upkeep items with intervals,
// contractor info, and last-completed timestamps.
type HomeMaintenanceItem struct {
	ID              uint       `json:"id" gorm:"primaryKey"`
	HouseholdID     string     `json:"household_id" gorm:"type:uuid;not null;index"` // V4-30: the tenant key; FamilyGroup is still written until V4-44
	FamilyGroup     string     `json:"family_group" gorm:"index"`
	TaskName        string     `json:"task_name"`
	Category        string     `json:"category"` // hvac, plumbing, appliance, pest_control, general
	IntervalDays    int        `json:"interval_days"`
	LastCompletedAt *time.Time `json:"last_completed_at,omitempty"`
	NextDueAt       time.Time  `json:"next_due_at" gorm:"index"`
	ContractorInfo  string     `json:"contractor_info"`
	Notes           string     `json:"notes"`
	UpdatedByUserID string     `json:"updated_by_user_id"`
}

// IsOverdue checks if the maintenance task is past its next due date.
func (m *HomeMaintenanceItem) IsOverdue(now time.Time) bool {
	return now.After(m.NextDueAt)
}

// CalculateNextDue returns the next due date by adding IntervalDays to the completion time.
func (m *HomeMaintenanceItem) CalculateNextDue(completedAt time.Time) time.Time {
	if m.IntervalDays <= 0 {
		return completedAt
	}
	return completedAt.AddDate(0, 0, m.IntervalDays)
}

// MarkCompleted updates LastCompletedAt, rolls forward NextDueAt, and records the completing user ID.
func (m *HomeMaintenanceItem) MarkCompleted(completedAt time.Time, userID string) {
	m.LastCompletedAt = &completedAt
	m.UpdatedByUserID = userID
	m.NextDueAt = m.CalculateNextDue(completedAt)
}

// VehicleAsset tracks vehicle insurance, annual licensing inspection, mileage,
// and maintenance service intervals.
type VehicleAsset struct {
	ID                   uint       `json:"id" gorm:"primaryKey"`
	HouseholdID          string     `json:"household_id" gorm:"type:uuid;not null;index"` // V4-30: the tenant key; FamilyGroup is still written until V4-44
	FamilyGroup          string     `json:"family_group" gorm:"index"`
	Name                 string     `json:"name"`
	LicensePlate         string     `json:"license_plate"`
	InsuranceExpiry      *time.Time `json:"insurance_expiry,omitempty"`
	AnnualTestExpiry     *time.Time `json:"annual_test_expiry,omitempty"`
	LastServiceDate      *time.Time `json:"last_service_date,omitempty"`
	CurrentMileageKm     int        `json:"current_mileage_km"`
	NextServiceMileageKm int        `json:"next_service_mileage_km"`
	Notes                string     `json:"notes"`
}

// IsServiceDue checks if the vehicle's current mileage meets or exceeds the next service threshold.
func (v *VehicleAsset) IsServiceDue() bool {
	return v.NextServiceMileageKm > 0 && v.CurrentMileageKm >= v.NextServiceMileageKm
}

// IsInspectionDue checks if the annual licensing/test expires within the given number of days.
func (v *VehicleAsset) IsInspectionDue(now time.Time, withinDays int) bool {
	if v.AnnualTestExpiry == nil {
		return false
	}
	cutoff := now.AddDate(0, 0, withinDays)
	return v.AnnualTestExpiry.Before(cutoff)
}

// IsInsuranceExpiring checks if the vehicle insurance policy expires within the given number of days.
func (v *VehicleAsset) IsInsuranceExpiring(now time.Time, withinDays int) bool {
	if v.InsuranceExpiry == nil {
		return false
	}
	cutoff := now.AddDate(0, 0, withinDays)
	return v.InsuranceExpiry.Before(cutoff)
}
