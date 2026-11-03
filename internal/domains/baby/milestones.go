package baby

import (
	"fmt"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/models"
)

// AgeInfo contains detailed duration breakdowns for a child's age.
type AgeInfo struct {
	TotalDays           int    `json:"total_days"`
	Weeks               int    `json:"weeks"`
	RemainingDays       int    `json:"remaining_days"`
	Months              int    `json:"months"`
	MonthsRemainingDays int    `json:"months_remaining_days"`
	Years               int    `json:"years"`
	FormattedAge        string `json:"formatted_age"`
	IsPremature         bool   `json:"is_premature"`
	AdjustedDays        int    `json:"adjusted_days,omitempty"`
	AdjustedWeeks       int    `json:"adjusted_weeks,omitempty"`
	AdjustedFormatted   string `json:"adjusted_formatted,omitempty"`
}

// CalculateAge computes exact calendar days, weeks, and months from DateOfBirth.
// If a DueDate is present and the baby was born > 10 days early, adjusted gestational age is also computed.
func CalculateAge(dob time.Time, dueDate *time.Time, asOf time.Time) AgeInfo {
	if asOf.IsZero() {
		asOf = time.Now()
	}

	// Normalize to start of day in UTC
	dUTC := time.Date(dob.Year(), dob.Month(), dob.Day(), 0, 0, 0, 0, time.UTC)
	nowUTC := time.Date(asOf.Year(), asOf.Month(), asOf.Day(), 0, 0, 0, 0, time.UTC)

	if nowUTC.Before(dUTC) {
		return AgeInfo{
			FormattedAge: "Not yet born (due soon)",
		}
	}

	totalDays := int(nowUTC.Sub(dUTC).Hours() / 24)
	weeks := totalDays / 7
	remainingDays := totalDays % 7

	// Calculate calendar years & months
	years := nowUTC.Year() - dUTC.Year()
	months := int(nowUTC.Month()) - int(dUTC.Month())
	days := nowUTC.Day() - dUTC.Day()

	if days < 0 {
		months--
		// Days in previous month
		prevMonth := nowUTC.AddDate(0, -1, 0)
		daysInPrev := daysInMonth(prevMonth.Year(), prevMonth.Month())
		days += daysInPrev
	}
	if months < 0 {
		years--
		months += 12
	}
	totalMonths := years*12 + months

	// Generate formatted string
	var formatted string
	if totalMonths == 0 {
		if weeks == 0 {
			formatted = fmt.Sprintf("%d days old", totalDays)
		} else {
			formatted = fmt.Sprintf("%d weeks, %d days (%d days old)", weeks, remainingDays, totalDays)
		}
	} else if totalMonths < 12 {
		formatted = fmt.Sprintf("%d months, %d days (%d weeks old)", totalMonths, days, weeks)
	} else {
		remMonths := totalMonths % 12
		yearStr := "years"
		if years == 1 {
			yearStr = "year"
		}
		if remMonths == 0 {
			formatted = fmt.Sprintf("%d %s old (%d months)", years, yearStr, totalMonths)
		} else {
			formatted = fmt.Sprintf("%d %s, %d months old (%d weeks)", years, yearStr, remMonths, weeks)
		}
	}

	info := AgeInfo{
		TotalDays:           totalDays,
		Weeks:               weeks,
		RemainingDays:       remainingDays,
		Months:              totalMonths,
		MonthsRemainingDays: days,
		Years:               years,
		FormattedAge:        formatted,
	}

	// Check prematurity
	if dueDate != nil && !dueDate.IsZero() {
		dueUTC := time.Date(dueDate.Year(), dueDate.Month(), dueDate.Day(), 0, 0, 0, 0, time.UTC)
		if dueUTC.After(dUTC) {
			earlyDays := int(dueUTC.Sub(dUTC).Hours() / 24)
			if earlyDays >= 14 { // born >= 2 weeks before due date
				info.IsPremature = true
				if nowUTC.After(dueUTC) {
					adjDays := int(nowUTC.Sub(dueUTC).Hours() / 24)
					info.AdjustedDays = adjDays
					info.AdjustedWeeks = adjDays / 7
					adjMonths := adjDays / 30
					info.AdjustedFormatted = fmt.Sprintf("%d months, %d weeks adjusted (%d weeks early)", adjMonths, info.AdjustedWeeks, earlyDays/7)
				} else {
					info.AdjustedDays = 0
					info.AdjustedWeeks = 0
					info.AdjustedFormatted = "Premature (not yet reached original due date)"
				}
			}
		}
	}

	return info
}

func daysInMonth(year int, month time.Month) int {
	// First day of next month minus one day
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// DevelopmentalLeap represents a Wonder Weeks cognitive/developmental leap.
type DevelopmentalLeap struct {
	LeapNumber  int    `json:"leap_number"`
	Title       string `json:"title"`
	Description string `json:"description"`
	StartWeek   int    `json:"start_week"`
	PeakWeek    int    `json:"peak_week"`
	EndWeek     int    `json:"end_week"`
}

var standardLeaps = []DevelopmentalLeap{
	{LeapNumber: 1, Title: "Sensations (The Changing World)", Description: "Baby takes in sights, sounds, and scents intensely; starts fixing gaze and social smiling.", StartWeek: 4, PeakWeek: 5, EndWeek: 6},
	{LeapNumber: 2, Title: "Patterns", Description: "Discovers hands and feet; recognizes familiar human faces and visual repeating patterns.", StartWeek: 7, PeakWeek: 8, EndWeek: 9},
	{LeapNumber: 3, Title: "Smooth Transitions", Description: "Movements become less jerky and more fluid; turns head toward voices and explores vocal sounds.", StartWeek: 11, PeakWeek: 12, EndWeek: 13},
	{LeapNumber: 4, Title: "Events", Description: "Understands cause and effect (dropping toy, grabbing objects); rolling over begins; 4-month sleep regression often occurs.", StartWeek: 17, PeakWeek: 19, EndWeek: 20},
	{LeapNumber: 5, Title: "Relationships", Description: "Grasps distance and spatial awareness; understands mother can walk away, leading to early separation anxiety.", StartWeek: 24, PeakWeek: 26, EndWeek: 27},
	{LeapNumber: 6, Title: "Categories", Description: "Classifies objects (dog is animal, ball rolls, soft vs hard); tests textures and finger foods.", StartWeek: 34, PeakWeek: 37, EndWeek: 38},
	{LeapNumber: 7, Title: "Sequences", Description: "Learns that tasks have orders (stacking blocks, putting spoon in mouth, taking things apart).", StartWeek: 44, PeakWeek: 46, EndWeek: 48},
	{LeapNumber: 8, Title: "Programs", Description: "Understands complete routines (getting dressed, washing hands, mealtime preparation).", StartWeek: 52, PeakWeek: 55, EndWeek: 56},
	{LeapNumber: 9, Title: "Principles", Description: "Experiments with rules, negotiation, sharing, and testing boundaries.", StartWeek: 61, PeakWeek: 64, EndWeek: 66},
	{LeapNumber: 10, Title: "Systems", Description: "Develops conscious self-identity, empathy, and begins adapting behavior to family expectations.", StartWeek: 72, PeakWeek: 75, EndWeek: 77},
}

// GetDevelopmentalLeapStatus checks if the child is currently experiencing or approaching a developmental leap.
func GetDevelopmentalLeapStatus(weeks int) (*DevelopmentalLeap, string) {
	for i := range standardLeaps {
		leap := standardLeaps[i]
		if weeks >= leap.StartWeek && weeks <= leap.EndWeek {
			return &leap, "active"
		}
		if weeks < leap.StartWeek && weeks >= leap.StartWeek-3 {
			return &leap, "upcoming"
		}
	}
	// If between leaps, find next leap
	for i := range standardLeaps {
		if standardLeaps[i].StartWeek > weeks {
			return &standardLeaps[i], "future"
		}
	}
	return nil, "completed"
}

// MilestoneSuggestion provides age-targeted guidance across care domains.
type MilestoneSuggestion struct {
	Category    string `json:"category"` // "development", "vaccine", "checkup", "feeding", "sleep"
	Title       string `json:"title"`
	Description string `json:"description"`
	Urgency     string `json:"urgency"` // "due_now", "upcoming", "routine"
	AgeTarget   string `json:"age_target"`
}

// GenerateMilestoneSuggestions returns age-specific recommendations across all categories.
func GenerateMilestoneSuggestions(age AgeInfo, category string) []MilestoneSuggestion {
	var suggestions []MilestoneSuggestion
	catLower := strings.ToLower(strings.TrimSpace(category))
	if catLower == "" {
		catLower = "all"
	}

	weeks := age.Weeks
	months := age.Months

	// 1. Developmental & Motor Milestones
	if catLower == "all" || catLower == "development" {
		leap, status := GetDevelopmentalLeapStatus(weeks)
		if leap != nil {
			urgency := "routine"
			if status == "active" {
				urgency = "due_now"
			}
			suggestions = append(suggestions, MilestoneSuggestion{
				Category:    "development",
				Title:       fmt.Sprintf("Leap %d: %s (%s)", leap.LeapNumber, leap.Title, strings.ToUpper(status)),
				Description: fmt.Sprintf("%s (Peaks around week %d). May experience temporary sleep disruption or clinginess.", leap.Description, leap.PeakWeek),
				Urgency:     urgency,
				AgeTarget:   fmt.Sprintf("Weeks %d-%d", leap.StartWeek, leap.EndWeek),
			})
		}

		if months <= 2 {
			suggestions = append(suggestions, MilestoneSuggestion{
				Category:    "development",
				Title:       "Early Head Control & Social Smile",
				Description: "Practice daily tummy time for 2-5 minutes per session. Baby should start tracking faces and smiling back.",
				Urgency:     "routine",
				AgeTarget:   "0-2 months",
			})
		} else if months <= 4 {
			suggestions = append(suggestions, MilestoneSuggestion{
				Category:    "development",
				Title:       "Rolling & Reaching for Toys",
				Description: "Pushing up on elbows during tummy time, grasping rattles, bringing hands to mouth, cooing and babbling.",
				Urgency:     "routine",
				AgeTarget:   "2-4 months",
			})
		} else if months <= 6 {
			suggestions = append(suggestions, MilestoneSuggestion{
				Category:    "development",
				Title:       "Sitting with Support & Rolling Both Ways",
				Description: "Rolling tummy to back and back to tummy. Sitting with slight hip support, transferring toys between hands.",
				Urgency:     "routine",
				AgeTarget:   "4-6 months",
			})
		} else if months <= 9 {
			suggestions = append(suggestions, MilestoneSuggestion{
				Category:    "development",
				Title:       "Independent Sitting & Pincer Grasp",
				Description: "Sitting unassisted without hand support, crawling or army scooting, developing 2-finger pincer grasp, responding to own name.",
				Urgency:     "routine",
				AgeTarget:   "6-9 months",
			})
		} else if months <= 12 {
			suggestions = append(suggestions, MilestoneSuggestion{
				Category:    "development",
				Title:       "Pulling to Stand & First Words",
				Description: "Cruising along furniture, waving 'bye-bye', saying first intentional words like 'mama' or 'dada', pointing to objects.",
				Urgency:     "routine",
				AgeTarget:   "9-12 months",
			})
		} else if months <= 18 {
			suggestions = append(suggestions, MilestoneSuggestion{
				Category:    "development",
				Title:       "Independent Walking & Utensil Practice",
				Description: "Walking unassisted, building a 2-3 block tower, using spoon, 10+ words vocabulary, identifying body parts.",
				Urgency:     "routine",
				AgeTarget:   "12-18 months",
			})
		} else {
			suggestions = append(suggestions, MilestoneSuggestion{
				Category:    "development",
				Title:       "Running, 2-Word Phrases & Jumping",
				Description: "Running confidently, kicking a ball, combining 2 words ('more milk', 'big truck'), showing pretend play.",
				Urgency:     "routine",
				AgeTarget:   "18-24 months",
			})
		}
	}

	// 2. Pediatric Checkups
	if catLower == "all" || catLower == "checkup" {
		checkups := []struct {
			monthTarget int
			title       string
			desc        string
		}{
			{0, "Newborn 1-Week Checkup", "Assess birth weight recovery, jaundice, umbilical cord healing, and feeding latch."},
			{1, "1-Month Well-Baby Visit", "Growth curve evaluation (weight, length, head circumference) and vision tracking."},
			{2, "2-Month Well-Baby Visit", "Developmental milestones check, head control, first routine immunizations."},
			{4, "4-Month Well-Baby Visit", "Growth evaluation, rolling readiness, sleep patterns, second vaccination dose."},
			{6, "6-Month Well-Baby Visit", "Solids introduction readiness, sitting stability, third routine vaccination dose."},
			{9, "9-Month Well-Baby Visit", "Motor development, pincer grasp, anemia / iron deficiency screening."},
			{12, "12-Month (1 Year) Comprehensive Checkup", "1-year immunizations (MMR, Varicella), hemoglobin test, standing/walking review, diet transition."},
			{18, "18-Month Pediatric Visit", "Autism / M-CHAT speech screening, booster vaccines, gross and fine motor skills review."},
			{24, "24-Month (2 Year) Milestone Evaluation", "Language development (50+ words), dental health check, behavioral review."},
		}

		for _, ch := range checkups {
			urgency := "routine"
			if months == ch.monthTarget {
				urgency = "due_now"
			} else if months == ch.monthTarget-1 || (ch.monthTarget > 0 && months == ch.monthTarget+1) {
				urgency = "upcoming"
			}

			if urgency == "due_now" || (urgency == "upcoming" && months <= ch.monthTarget) {
				suggestions = append(suggestions, MilestoneSuggestion{
					Category:    "checkup",
					Title:       ch.title,
					Description: ch.desc,
					Urgency:     urgency,
					AgeTarget:   fmt.Sprintf("%d months", ch.monthTarget),
				})
			}
		}
	}

	// 3. Vaccination Schedule (Standard CDC / WHO guidelines)
	if catLower == "all" || catLower == "vaccine" {
		vaccines := []struct {
			monthTarget int
			title       string
			desc        string
		}{
			{0, "Hepatitis B (Dose 1)", "Administered at birth to protect against hepatitis B virus."},
			{2, "Hexavalent / DTaP-IPV-Hib-HepB #1 + PCV13 #1 + Rotavirus #1", "First combination dose against Diphtheria, Tetanus, Pertussis, Polio, Hib, Hep B, Pneumococcal, and Rotavirus."},
			{4, "Hexavalent / DTaP-IPV-Hib-HepB #2 + PCV13 #2 + Rotavirus #2", "Second primary immunization series dose."},
			{6, "Hexavalent #3 + PCV13 #3 + Rotavirus #3 + Influenza (Seasonal)", "Third immunization series dose and seasonal flu vaccine eligibility (6+ months)."},
			{12, "MMR #1 + Varicella #1 + PCV13 Booster + Hepatitis A #1", "Measles, Mumps, Rubella, Chickenpox, Pneumococcal booster, and Hep A."},
			{18, "DTaP-IPV-Hib Booster + Hepatitis A #2", "Toddler booster series dose for long-term immunity."},
		}

		for _, v := range vaccines {
			urgency := "routine"
			if months == v.monthTarget {
				urgency = "due_now"
			} else if months == v.monthTarget-1 {
				urgency = "upcoming"
			}

			if urgency == "due_now" || urgency == "upcoming" {
				suggestions = append(suggestions, MilestoneSuggestion{
					Category:    "vaccine",
					Title:       v.title,
					Description: v.desc,
					Urgency:     urgency,
					AgeTarget:   fmt.Sprintf("%d months", v.monthTarget),
				})
			}
		}
	}

	// 4. Feeding Transitions
	if catLower == "all" || catLower == "feeding" {
		if months < 4 {
			suggestions = append(suggestions, MilestoneSuggestion{
				Category:    "feeding",
				Title:       "Exclusive Milk (Breastmilk or Infant Formula)",
				Description: "Infants under 4 months require only breastmilk or formula (~120-180ml every 2-4 hours). No water, juices, or solids needed yet.",
				Urgency:     "routine",
				AgeTarget:   "0-4 months",
			})
		} else if months < 6 {
			suggestions = append(suggestions, MilestoneSuggestion{
				Category:    "feeding",
				Title:       "Solids Readiness & Puree Exploration",
				Description: "Look for signs of readiness: good head control, sitting with support, losing tongue-thrust reflex, showing keen interest in family food. Start with single-ingredient vegetable or iron-fortified cereal purees.",
				Urgency:     "upcoming",
				AgeTarget:   "4-6 months",
			})
		} else if months < 9 {
			suggestions = append(suggestions, MilestoneSuggestion{
				Category:    "feeding",
				Title:       "Texture Progression & Allergen Introduction",
				Description: "Offer 2-3 small meals daily alongside milk. Introduce thick purees, mashed fruits/vegetables, soft finger foods, and top allergens (peanut butter thinned with water/puree, well-cooked egg). Offer small sips of water from an open cup.",
				Urgency:     "due_now",
				AgeTarget:   "6-9 months",
			})
		} else if months < 12 {
			suggestions = append(suggestions, MilestoneSuggestion{
				Category:    "feeding",
				Title:       "Table Foods & Self-Feeding",
				Description: "Encourage 3 meals + 1-2 healthy snacks. Offer soft bite-sized family table foods (steamed veggies, pasta, soft meat/tofu). Practice using hands and baby spoons.",
				Urgency:     "due_now",
				AgeTarget:   "9-12 months",
			})
		} else {
			suggestions = append(suggestions, MilestoneSuggestion{
				Category:    "feeding",
				Title:       "Whole Cow's Milk & Family Diet Transition",
				Description: "Pasteurized whole cow's milk can now replace formula (up to 400-500ml daily). Transition away from baby bottles to cups. Ensure iron-rich solid foods remain central.",
				Urgency:     "routine",
				AgeTarget:   "12+ months",
			})
		}
	}

	// 5. Sleep & Wake Windows
	if catLower == "all" || catLower == "sleep" {
		if months < 3 {
			suggestions = append(suggestions, MilestoneSuggestion{
				Category:    "sleep",
				Title:       "Newborn Sleep & Short Wake Windows (45-60 min)",
				Description: "Newborns can only stay awake 45-90 minutes before becoming overtired. Expect 4-5 daytime naps and 14-17 hours total sleep in 24 hours.",
				Urgency:     "routine",
				AgeTarget:   "0-3 months",
			})
		} else if months < 5 {
			suggestions = append(suggestions, MilestoneSuggestion{
				Category:    "sleep",
				Title:       "4-Month Sleep Progression & Wake Windows (75-120 min)",
				Description: "Circadian rhythms mature into adult-like sleep cycles. Watch for sleep regressions. Wake windows are typically 1.5 - 2 hours across 3-4 naps.",
				Urgency:     "routine",
				AgeTarget:   "3-5 months",
			})
		} else if months < 8 {
			suggestions = append(suggestions, MilestoneSuggestion{
				Category:    "sleep",
				Title:       "3-to-2 Nap Transition & Wake Windows (2-3 hours)",
				Description: "Wake windows expand to 2 to 3 hours. Babies typically transition from 3 naps to 2 solid naps (morning & afternoon). Bedtime between 19:00 - 20:00 recommended.",
				Urgency:     "routine",
				AgeTarget:   "5-8 months",
			})
		} else if months < 15 {
			suggestions = append(suggestions, MilestoneSuggestion{
				Category:    "sleep",
				Title:       "2-Nap Routine & Wake Windows (3-4 hours)",
				Description: "Stable 2-nap schedule with 3 to 4 hours wakefulness between sleeps. Average total sleep 12-14 hours per day.",
				Urgency:     "routine",
				AgeTarget:   "8-15 months",
			})
		} else {
			suggestions = append(suggestions, MilestoneSuggestion{
				Category:    "sleep",
				Title:       "1 Afternoon Nap & Wake Windows (4.5-5.5 hours)",
				Description: "Transitioning to a single restorative midday nap (typically 1.5 - 2.5 hours after lunch). Bedtime consistency remains key.",
				Urgency:     "routine",
				AgeTarget:   "15-24 months",
			})
		}
	}

	return suggestions
}

// MedicationCooldownResult holds the safety assessment for administering pediatric medication.
type MedicationCooldownResult struct {
	MedicationName     string        `json:"medication_name"`
	NormalizedType     string        `json:"normalized_type"` // "paracetamol", "ibuprofen", "other"
	LastGivenAt        *time.Time    `json:"last_given_at,omitempty"`
	Elapsed            time.Duration `json:"elapsed"`
	ElapsedFormatted   string        `json:"elapsed_formatted,omitempty"`
	MinCooldownHours   float64       `json:"min_cooldown_hours"`
	NextSafeDoseAt     time.Time     `json:"next_safe_dose_at"`
	IsSafeToAdminister bool          `json:"is_safe_to_administer"`
	TimeUntilSafe      time.Duration `json:"time_until_safe,omitempty"`
	TimeUntilFormatted string        `json:"time_until_formatted,omitempty"`
	WarningMessage     string        `json:"warning_message,omitempty"`
	PediatricGuidance  string        `json:"pediatric_guidance"`
}

// CheckMedicationCooldown evaluates whether enough time has elapsed since the last dose
// of fever reducer or pain reliever, enforcing minimum medical intervals.
func CheckMedicationCooldown(lastLog *models.BabyCareLog, medName string, now time.Time, babyAgeMonths int) MedicationCooldownResult {
	if now.IsZero() {
		now = time.Now()
	}

	normMed := strings.ToLower(strings.TrimSpace(medName))
	medType := "other"
	minCooldownHours := 4.0

	// Identify medication class
	if strings.Contains(normMed, "paracetamol") || strings.Contains(normMed, "acetaminophen") ||
		strings.Contains(normMed, "tylenol") || strings.Contains(normMed, "calpol") ||
		strings.Contains(normMed, "acamol") || strings.Contains(normMed, "panadol") ||
		strings.Contains(normMed, "febricol") {
		medType = "paracetamol"
		minCooldownHours = 4.0 // Minimum 4 hours between paracetamol doses (max 4 doses/24h)
	} else if strings.Contains(normMed, "ibuprofen") || strings.Contains(normMed, "advil") ||
		strings.Contains(normMed, "nurofen") || strings.Contains(normMed, "motrin") ||
		strings.Contains(normMed, "pediaprofen") {
		medType = "ibuprofen"
		minCooldownHours = 6.0 // Minimum 6 hours between ibuprofen doses (max 3-4 doses/24h)
	}

	res := MedicationCooldownResult{
		MedicationName:   medName,
		NormalizedType:   medType,
		MinCooldownHours: minCooldownHours,
	}

	// Pediatric age guardrails
	if medType == "ibuprofen" && babyAgeMonths < 6 {
		res.WarningMessage = "CAUTION: Ibuprofen is generally NOT recommended for infants under 6 months of age (or under 5kg) unless specifically directed by a pediatrician. Consider Paracetamol instead or contact your physician."
	}

	// Age-based fever safety guidance
	if babyAgeMonths < 3 {
		res.PediatricGuidance = "CRITICAL ALERT: For infants under 3 months, any rectal fever >= 38.0°C (100.4°F) requires IMMEDIATE medical evaluation at an emergency clinic or pediatrician, before giving antipyretics."
	} else if medType == "paracetamol" {
		res.PediatricGuidance = "Standard dosage: 10-15 mg/kg every 4-6 hours. Never exceed 4 doses in 24 hours. Always use an accurate measuring syringe."
	} else if medType == "ibuprofen" {
		res.PediatricGuidance = "Standard dosage: 5-10 mg/kg every 6-8 hours with food or milk. Never exceed 3-4 doses in 24 hours."
	} else {
		res.PediatricGuidance = "Consult pediatrician or pharmacist for exact weight-based dosing instructions."
	}

	if lastLog == nil {
		res.IsSafeToAdminister = true
		res.NextSafeDoseAt = now
		return res
	}

	res.LastGivenAt = &lastLog.Timestamp
	elapsed := now.Sub(lastLog.Timestamp)
	if elapsed < 0 {
		elapsed = 0
	}
	res.Elapsed = elapsed
	res.ElapsedFormatted = formatDuration(elapsed)

	cooldownDuration := time.Duration(minCooldownHours * float64(time.Hour))
	safeAt := lastLog.Timestamp.Add(cooldownDuration)
	res.NextSafeDoseAt = safeAt

	if elapsed >= cooldownDuration {
		res.IsSafeToAdminister = true
	} else {
		res.IsSafeToAdminister = false
		timeUntil := safeAt.Sub(now)
		res.TimeUntilSafe = timeUntil
		res.TimeUntilFormatted = formatDuration(timeUntil)

		cooldownWarn := fmt.Sprintf("COOLDOWN ACTIVE: Only %s has passed since last dose. Do NOT administer yet. Safe next dose time: %s (in %s). Minimum interval is %.0f hours.",
			res.ElapsedFormatted, safeAt.Format("15:04"), res.TimeUntilFormatted, minCooldownHours)

		if res.WarningMessage != "" {
			res.WarningMessage = cooldownWarn + " | " + res.WarningMessage
		} else {
			res.WarningMessage = cooldownWarn
		}
	}

	return res
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Minute)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	if h > 0 {
		return fmt.Sprintf("%dh %02dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}
