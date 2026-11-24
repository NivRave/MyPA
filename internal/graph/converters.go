// Code in this file is hand-written (not generated): conversions from DB models to
// GraphQL models and small resolver helpers. It lives outside schema.resolvers.go so
// `gqlgen generate` (follow-schema layout) never quarantines or deletes it.

package graph

import (
	"fmt"
	"github.com/nivik/mypa/internal/db"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nivik/mypa/internal/domains/durability"
	"github.com/nivik/mypa/internal/graph/model"
	dbmodels "github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/packs"
)

func uintToStr(id uint) string {
	return strconv.FormatUint(uint64(id), 10)
}

func tableNameForEntity(entityType string) (string, error) {
	return durability.ResolveRollbackTable(entityType)
}

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

// toModelHousehold converts a household for the API (V4-30).
func toModelHousehold(h dbmodels.Household) *model.Household {
	return &model.Household{
		ID:       h.ID,
		Name:     h.Name,
		Language: h.Language,
		TimeZone: h.TimeZone,
		Currency: h.Currency,
		Packs:    toModelPacks(packs.Parse(h.Packs)),
		Topics:   packs.Parse(h.Packs).Topics(),
	}
}

// toModelPacks lists every opt-in pack in display order with whether the household has it on.
func toModelPacks(on packs.Set) []*model.Pack {
	var out []*model.Pack
	for _, p := range packs.All() {
		out = append(out, &model.Pack{ID: string(p.ID), Name: p.Name, Description: p.Description, Enabled: on.Has(p.ID),
			Pages: append([]string{}, p.Pages...)})
	}
	return out
}

// validateHouseholdSettings checks the settings an admin may change (V4-30). A nil field is left
// unchanged; an empty language, time zone or currency resets it to the install default.
func validateHouseholdSettings(name, language, timeZone, currency *string) (db.HouseholdSettings, error) {
	var in db.HouseholdSettings
	if name != nil {
		n := strings.TrimSpace(*name)
		if n == "" || utf8.RuneCountInString(n) > 100 {
			return in, fmt.Errorf("invalid household name: it must be 1 to 100 characters")
		}
		in.Name = &n
	}
	if language != nil {
		l := strings.TrimSpace(*language)
		if len(l) > 35 || strings.IndexFunc(l, func(r rune) bool {
			return r != '-' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9')
		}) >= 0 {
			return in, fmt.Errorf("invalid language %q: use a language tag such as \"he\" or \"en-US\"", l)
		}
		in.Language = &l
	}
	if timeZone != nil {
		tz := strings.TrimSpace(*timeZone)
		if tz != "" {
			if _, err := time.LoadLocation(tz); err != nil || strings.EqualFold(tz, "local") {
				return in, fmt.Errorf("invalid time zone %q: use an IANA name such as \"Asia/Jerusalem\"", tz)
			}
		}
		in.TimeZone = &tz
	}
	if currency != nil {
		c := strings.ToUpper(strings.TrimSpace(*currency))
		if c != "" && (len(c) != 3 || strings.IndexFunc(c, func(r rune) bool { return r < 'A' || r > 'Z' }) >= 0) {
			return in, fmt.Errorf("invalid currency %q: use a 3-letter code such as \"ILS\"", c)
		}
		in.Currency = &c
	}
	return in, nil
}

// changedHouseholdSettings names the settings an update changed, for the INFO line that stands in
// for a mutation log entry (BL-47). Names only, not values.
func changedHouseholdSettings(before, after dbmodels.Household) []string {
	var out []string
	for _, f := range []struct {
		name      string
		was, want string
	}{
		{"name", before.Name, after.Name},
		{"language", before.Language, after.Language},
		{"time_zone", before.TimeZone, after.TimeZone},
		{"currency", before.Currency, after.Currency},
	} {
		if f.was != f.want {
			out = append(out, f.name)
		}
	}
	return out
}
