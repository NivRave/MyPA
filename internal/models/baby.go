package models

import (
	"time"
)

// BabyProfile represents a child's profile with growth, birth, and medical metadata.
type BabyProfile struct {
	ID                 uint       `json:"id" gorm:"primaryKey"`
	HouseholdID        string     `json:"household_id" gorm:"type:uuid;not null;index"` // V4-30: the tenant key; FamilyGroup is still written until V4-44
	FamilyGroup        string     `json:"family_group" gorm:"index"`
	Name               string     `json:"name"`
	DateOfBirth        time.Time  `json:"date_of_birth"`
	DueDate            *time.Time `json:"due_date,omitempty"`
	Gender             string     `json:"gender"` // "male", "female", "other"
	BirthWeightGrams   int        `json:"birth_weight_grams"`
	CurrentWeightGrams int        `json:"current_weight_grams"`
	HeightCm           float64    `json:"height_cm"`
	PediatricianInfo   string     `json:"pediatrician_info"`
	AllergiesNotes     string     `json:"allergies_notes"`
	UpdatedByUserID    string     `json:"updated_by_user_id"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// BabyCareLog records a single routine or medical care event for a child.
// Supported LogType values include: "feeding", "sleep", "diaper", "temperature",
// "medication", "milestone", "measurement".
type BabyCareLog struct {
	ID             uint      `json:"id" gorm:"primaryKey"`
	BabyID         uint      `json:"baby_id" gorm:"index;index:idx_baby_care_logs_lookup,priority:2;index:idx_baby_care_logs_baby_timestamp,priority:1"`
	HouseholdID    string    `json:"household_id" gorm:"type:uuid;not null;index"` // V4-30: the tenant key; FamilyGroup is still written until V4-44
	FamilyGroup    string    `json:"family_group" gorm:"index;index:idx_baby_care_logs_family_timestamp,priority:1;index:idx_baby_care_logs_lookup,priority:1"`
	LogType        string    `json:"log_type" gorm:"index;index:idx_baby_care_logs_lookup,priority:3"`
	ValueNumeric   float64   `json:"value_numeric"`
	Unit           string    `json:"unit"`
	Details        string    `json:"details"`
	Timestamp      time.Time `json:"timestamp" gorm:"index;index:idx_baby_care_logs_family_timestamp,priority:2,sort:desc;index:idx_baby_care_logs_lookup,priority:4,sort:desc;index:idx_baby_care_logs_baby_timestamp,priority:2,sort:desc"`
	LoggedByUserID string    `json:"logged_by_user_id"`
}

// BabyInventoryItem tracks supplies such as formula, diapers, or vitamin D drops.
type BabyInventoryItem struct {
	ID                uint      `json:"id" gorm:"primaryKey"`
	HouseholdID       string    `json:"household_id" gorm:"type:uuid;not null;index"` // V4-30: the tenant key; FamilyGroup is still written until V4-44
	FamilyGroup       string    `json:"family_group" gorm:"index"`
	ItemName          string    `json:"item_name"`
	QuantityRemaining float64   `json:"quantity_remaining"`
	Unit              string    `json:"unit"`
	LowThreshold      float64   `json:"low_threshold"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// BabyCareSummary aggregates statistics over a period for trend analysis.
type BabyCareSummary struct {
	BabyID             uint           `json:"baby_id"`
	FamilyGroup        string         `json:"family_group"`
	StartTime          time.Time      `json:"start_time"`
	EndTime            time.Time      `json:"end_time"`
	TotalFeedings      int            `json:"total_feedings"`
	TotalFeedingVolume float64        `json:"total_feeding_volume"`
	FeedingVolumeUnit  string         `json:"feeding_volume_unit"`
	TotalSleepMinutes  float64        `json:"total_sleep_minutes"`
	TotalDiaperChanges int            `json:"total_diaper_changes"`
	WetDiapers         int            `json:"wet_diapers"`
	DirtyDiapers       int            `json:"dirty_diapers"`
	BothDiapers        int            `json:"both_diapers"`
	LastTemperature    *float64       `json:"last_temperature,omitempty"`
	LastTempUnit       string         `json:"last_temp_unit,omitempty"`
	LastMedication     *BabyCareLog   `json:"last_medication,omitempty"`
	LogCountByType     map[string]int `json:"log_count_by_type"`
}
