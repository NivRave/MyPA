package baby_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAdversarial_InterveningNonAntipyreticMedications tests that routine non-antipyretic medication logs
// (e.g. Vitamin D, Saline spray, Diaper cream) do not shadow or bypass the antipyretic cooldown
// when a subsequent dose of antipyretic (e.g. Tylenol, Calpol) is administered.
func TestAdversarial_InterveningNonAntipyreticMedications(t *testing.T) {
	mod, repo, bus := setupTestModule(t)
	callCtx := createTestCallContext(bus)
	fg := callCtx.User.FamilyGroup

	now := time.Now()
	dob := now.AddDate(0, -6, 0)
	profile := models.BabyProfile{
		FamilyGroup: fg,
		Name:        "BabyIntervening",
		DateOfBirth: dob,
	}
	require.NoError(t, repo.UpsertBabyProfile(callCtx.Scope, &profile))

	t0 := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

	// Dose 1 at 12:00: Paracetamol 2.5ml
	out1, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Paracetamol 2.5ml",
		"timestamp": t0.Format(time.RFC3339),
	})
	require.NoError(t, err)
	assert.Contains(t, out1, "success")

	// Intervening Med 1 at 12:10: Vitamin D3 drops
	tVitD := t0.Add(10 * time.Minute)
	outVitD, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Vitamin D3 400 IU drops",
		"timestamp": tVitD.Format(time.RFC3339),
	})
	require.NoError(t, err)
	assert.Contains(t, outVitD, "success")

	// Intervening Med 2 at 12:20: Saline Nasal Spray
	tSaline := t0.Add(20 * time.Minute)
	outSaline, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Saline Nasal Spray",
		"timestamp": tSaline.Format(time.RFC3339),
	})
	require.NoError(t, err)
	assert.Contains(t, outSaline, "success")

	// Intervening Med 3 at 12:30: Zinc Diaper Ointment
	tZinc := t0.Add(30 * time.Minute)
	outZinc, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Zinc Oxide Diaper Ointment",
		"timestamp": tZinc.Format(time.RFC3339),
	})
	require.NoError(t, err)
	assert.Contains(t, outZinc, "success")

	// Unsafe Dose at 12:45 (45 mins after Paracetamol Dose 1): Tylenol Infant Drops
	// MUST BE BLOCKED despite 3 intervening medication logs!
	tUnsafe := t0.Add(45 * time.Minute)
	outUnsafe, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Tylenol Infant Drops",
		"timestamp": tUnsafe.Format(time.RFC3339),
	})
	require.NoError(t, err)

	var respUnsafe map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(outUnsafe), &respUnsafe))
	assert.Equal(t, "blocked", respUnsafe["status"], "Tylenol given 45m after Paracetamol must be blocked despite intervening Vitamin D and Saline logs!")

	// Second Unsafe Dose at 14:00 (2 hours after Paracetamol Dose 1): Calpol Infant Suspension
	// MUST BE BLOCKED!
	tCalpol := t0.Add(2 * time.Hour)
	outCalpol, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Calpol Infant Suspension",
		"timestamp": tCalpol.Format(time.RFC3339),
	})
	require.NoError(t, err)

	var respCalpol map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(outCalpol), &respCalpol))
	assert.Equal(t, "blocked", respCalpol["status"], "Calpol given 2h after Paracetamol must be blocked!")

	// Verify database contains ONLY the 4 valid doses (Paracetamol, Vit D, Saline, Zinc)
	logs, err := repo.GetCareLogs(callCtx.Scope, profile.ID, "medication", time.Time{}, time.Time{}, 100)
	require.NoError(t, err)
	assert.Len(t, logs, 4, "Blocked antipyretic doses must NOT be persisted in database")

	// Safe Dose at 16:01 (4h 1m after Paracetamol Dose 1): Panadol Baby Drops
	// MUST SUCCEED!
	tSafe := t0.Add(4*time.Hour + 1*time.Minute)
	outSafe, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Panadol Baby Drops",
		"timestamp": tSafe.Format(time.RFC3339),
	})
	require.NoError(t, err)

	var respSafe map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(outSafe), &respSafe))
	assert.Equal(t, "success", respSafe["status"], "Panadol given >4h after Paracetamol must succeed!")

	// Dose at 16:20 (19 mins after Panadol at 16:01): Paracetamol 5ml
	// MUST BE BLOCKED (cooldown anchored to 16:01)!
	tBlockedPanadol := tSafe.Add(19 * time.Minute)
	outBlocked2, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Paracetamol 5ml",
		"timestamp": tBlockedPanadol.Format(time.RFC3339),
	})
	require.NoError(t, err)

	var respBlocked2 map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(outBlocked2), &respBlocked2))
	assert.Equal(t, "blocked", respBlocked2["status"], "Paracetamol given 19m after Panadol must be blocked!")
}

// TestAdversarial_CapitalizationAndWhitespaceVariations tests that whitespace padding, tabs,
// newlines, mixed casing, and brand variations cannot bypass the cooldown guardrail.
func TestAdversarial_CapitalizationAndWhitespaceVariations(t *testing.T) {
	mod, repo, bus := setupTestModule(t)
	callCtx := createTestCallContext(bus)
	fg := callCtx.User.FamilyGroup

	now := time.Now()
	dob := now.AddDate(0, -6, 0)
	profile := models.BabyProfile{
		FamilyGroup: fg,
		Name:        "BabyCase",
		DateOfBirth: dob,
	}
	require.NoError(t, repo.UpsertBabyProfile(callCtx.Scope, &profile))

	t0 := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

	// Dose 1: "  pArAcEtAmOl   2.5ml \n"
	out1, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "  pArAcEtAmOl   2.5ml \n",
		"timestamp": t0.Format(time.RFC3339),
	})
	require.NoError(t, err)
	assert.Contains(t, out1, "success")

	// Variations that MUST be blocked within 4-hour window
	variations := []struct {
		name    string
		offset  time.Duration
		details string
	}{
		{"Tabs and uppercase Tylenol", 15 * time.Minute, "\t\tTYLENOL  drops\t"},
		{"Mixed case Calpol with spaces", 30 * time.Minute, "   cAlPoL    infant   suspension  "},
		{"Uppercase Acetaminophen", 60 * time.Minute, " ACETAMINOPHEN  160mg / 5ml "},
		{"Uppercase Panadol", 90 * time.Minute, "  PANADOL  baby  "},
		{"Newlines and mixed case Acamol", 120 * time.Minute, "\n\tACAMOL drops\n"},
		{"Uppercase Febricol with trailing spaces", 150 * time.Minute, "  FEBRICOL  syrup  "},
	}

	for _, v := range variations {
		tVar := t0.Add(v.offset)
		out, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
			"baby_id":   int(profile.ID),
			"log_type":  "medication",
			"details":   v.details,
			"timestamp": tVar.Format(time.RFC3339),
		})
		require.NoError(t, err)

		var resp map[string]interface{}
		require.NoError(t, json.Unmarshal([]byte(out), &resp))
		assert.Equal(t, "blocked", resp["status"], "Variation '%s' (%s) must be blocked within cooldown!", v.name, v.details)
	}

	// Verify only Dose 1 is in database
	logs, err := repo.GetCareLogs(callCtx.Scope, profile.ID, "medication", time.Time{}, time.Time{}, 100)
	require.NoError(t, err)
	assert.Len(t, logs, 1, "Database must only contain the initial valid dose; variations must not be saved")
}

// TestAdversarial_GenuineAlternateClasses tests the pediatric medical practice of alternating antipyretics
// (Paracetamol and Ibuprofen) for refractory fever under medical guidance:
// 1. Paracetamol at 12:00 (cooldown: 4h, until 16:00).
// 2. Ibuprofen at 13:00 (1h later) -> ALLOWED because different pharmacological class (cooldown: 6h, until 19:00).
// 3. Paracetamol (Calpol) at 14:00 (2h after Paracetamol at 12:00) -> BLOCKED because < 4h since Paracetamol!
// 4. Ibuprofen (Nurofen) at 15:00 (2h after Ibuprofen at 13:00) -> BLOCKED because < 6h since Ibuprofen!
// 5. Paracetamol (Tylenol) at 16:15 (4h 15m after Paracetamol at 12:00) -> ALLOWED (anchors Paracetamol cooldown to 16:15)!
// 6. Paracetamol at 17:00 (45m after Tylenol at 16:15) -> BLOCKED!
// 7. Ibuprofen (Advil) at 18:00 (5h after Ibuprofen at 13:00) -> BLOCKED (< 6h)!
// 8. Ibuprofen (Motrin) at 19:05 (6h 5m after Ibuprofen at 13:00) -> ALLOWED (> 6h)!
func TestAdversarial_GenuineAlternateClasses(t *testing.T) {
	mod, repo, bus := setupTestModule(t)
	callCtx := createTestCallContext(bus)
	fg := callCtx.User.FamilyGroup

	now := time.Now()
	// Baby is 9 months old (safe for both Paracetamol and Ibuprofen)
	dob := now.AddDate(0, -9, 0)
	profile := models.BabyProfile{
		FamilyGroup: fg,
		Name:        "BabyAlternate",
		DateOfBirth: dob,
	}
	require.NoError(t, repo.UpsertBabyProfile(callCtx.Scope, &profile))

	t0 := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

	// Step 1: Paracetamol at 12:00
	out1, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Paracetamol 120mg/5ml",
		"timestamp": t0.Format(time.RFC3339),
	})
	require.NoError(t, err)
	var resp1 map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(out1), &resp1))
	assert.Equal(t, "success", resp1["status"], "Initial Paracetamol dose must succeed")

	// Step 2: Ibuprofen at 13:00 (1 hour later) -> Alternate class MUST SUCCEED
	tIbu1 := t0.Add(1 * time.Hour)
	outIbu1, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Ibuprofen 100mg/5ml",
		"timestamp": tIbu1.Format(time.RFC3339),
	})
	require.NoError(t, err)
	var respIbu1 map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(outIbu1), &respIbu1))
	assert.Equal(t, "success", respIbu1["status"], "Alternate class (Ibuprofen after Paracetamol) must succeed under pediatric guidance")

	// Step 3: Paracetamol (Calpol) at 14:00 (2 hours after Paracetamol at 12:00) -> MUST BE BLOCKED
	tParaUnsafe := t0.Add(2 * time.Hour)
	outParaUnsafe, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Calpol Infant Suspension",
		"timestamp": tParaUnsafe.Format(time.RFC3339),
	})
	require.NoError(t, err)
	var respParaUnsafe map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(outParaUnsafe), &respParaUnsafe))
	assert.Equal(t, "blocked", respParaUnsafe["status"], "Calpol 2h after Paracetamol must be blocked despite intermediate Ibuprofen dose!")

	// Step 4: Ibuprofen (Nurofen) at 15:00 (2 hours after Ibuprofen at 13:00) -> MUST BE BLOCKED
	tIbuUnsafe := t0.Add(3 * time.Hour)
	outIbuUnsafe, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Nurofen for Children",
		"timestamp": tIbuUnsafe.Format(time.RFC3339),
	})
	require.NoError(t, err)
	var respIbuUnsafe map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(outIbuUnsafe), &respIbuUnsafe))
	assert.Equal(t, "blocked", respIbuUnsafe["status"], "Nurofen 2h after Ibuprofen must be blocked!")

	// Step 5: Paracetamol (Tylenol) at 16:15 (4h 15m after Paracetamol at 12:00) -> MUST SUCCEED
	tParaSafe := t0.Add(4*time.Hour + 15*time.Minute)
	outParaSafe, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Tylenol Drops",
		"timestamp": tParaSafe.Format(time.RFC3339),
	})
	require.NoError(t, err)
	var respParaSafe map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(outParaSafe), &respParaSafe))
	assert.Equal(t, "success", respParaSafe["status"], "Tylenol >4h after Paracetamol must succeed")

	// Step 6: Paracetamol (Acamol) at 17:00 (45m after Tylenol at 16:15) -> MUST BE BLOCKED
	tParaUnsafe2 := tParaSafe.Add(45 * time.Minute)
	outParaUnsafe2, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Acamol Drops",
		"timestamp": tParaUnsafe2.Format(time.RFC3339),
	})
	require.NoError(t, err)
	var respParaUnsafe2 map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(outParaUnsafe2), &respParaUnsafe2))
	assert.Equal(t, "blocked", respParaUnsafe2["status"], "Acamol 45m after Tylenol must be blocked (cooldown re-anchored to 16:15)!")

	// Step 7: Ibuprofen (Advil) at 18:00 (5 hours after Ibuprofen at 13:00) -> MUST BE BLOCKED (Ibuprofen needs 6h)
	tIbuUnsafe2 := t0.Add(6 * time.Hour) // 18:00
	outIbuUnsafe2, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Advil Children's Liquid",
		"timestamp": tIbuUnsafe2.Format(time.RFC3339),
	})
	require.NoError(t, err)
	var respIbuUnsafe2 map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(outIbuUnsafe2), &respIbuUnsafe2))
	assert.Equal(t, "blocked", respIbuUnsafe2["status"], "Advil 5h after Ibuprofen must be blocked because Ibuprofen requires 6 hours!")

	// Step 8: Ibuprofen (Motrin) at 19:05 (6 hours 5 mins after Ibuprofen at 13:00) -> MUST SUCCEED
	tIbuSafe := t0.Add(7*time.Hour + 5*time.Minute) // 19:05
	outIbuSafe, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Motrin Infant Drops",
		"timestamp": tIbuSafe.Format(time.RFC3339),
	})
	require.NoError(t, err)
	var respIbuSafe map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(outIbuSafe), &respIbuSafe))
	assert.Equal(t, "success", respIbuSafe["status"], "Motrin >6h after Ibuprofen must succeed")

	// Final DB verification: Exactly 4 doses persisted
	// 1. Paracetamol (12:00)
	// 2. Ibuprofen (13:00)
	// 3. Tylenol (16:15)
	// 4. Motrin (19:05)
	logs, err := repo.GetCareLogs(callCtx.Scope, profile.ID, "medication", time.Time{}, time.Time{}, 100)
	require.NoError(t, err)
	assert.Len(t, logs, 4, "Only the 4 valid doses must be persisted in database")
}

// TestAdversarial_IbuprofenAgeGuardrailUnder6Months verifies that pediatric age guardrails
// warn about Ibuprofen usage in infants under 6 months.
func TestAdversarial_IbuprofenAgeGuardrailUnder6Months(t *testing.T) {
	mod, repo, bus := setupTestModule(t)
	callCtx := createTestCallContext(bus)
	fg := callCtx.User.FamilyGroup

	now := time.Now()
	// Infant is 4 months old
	dob := now.AddDate(0, -4, 0)
	profile := models.BabyProfile{
		FamilyGroup: fg,
		Name:        "BabyUnder6m",
		DateOfBirth: dob,
	}
	require.NoError(t, repo.UpsertBabyProfile(callCtx.Scope, &profile))

	t0 := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	out, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Ibuprofen 50mg",
		"timestamp": t0.Format(time.RFC3339),
	})
	require.NoError(t, err)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(out), &resp))
	assert.Equal(t, "success", resp["status"])
	cdRaw, ok := resp["medication_cooldown"].(map[string]interface{})
	require.True(t, ok)
	warn, _ := cdRaw["warning_message"].(string)
	assert.Contains(t, warn, "NOT recommended for infants under 6 months", "Should advise caution for infant under 6 months")
}
