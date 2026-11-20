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

// TestAdversarial_ZeroElapsedTime verifies that administering a dose at the exact same timestamp
// as the previous dose (zero elapsed time) is strictly blocked and never persisted to the database.
func TestAdversarial_ZeroElapsedTime(t *testing.T) {
	mod, repo, bus := setupTestModule(t)
	callCtx := createTestCallContext(bus)
	fg := callCtx.User.FamilyGroup

	now := time.Now()
	dob := now.AddDate(0, -6, 0)
	profile := models.BabyProfile{
		FamilyGroup: fg,
		Name:        "BabyZero",
		DateOfBirth: dob,
	}
	require.NoError(t, repo.UpsertBabyProfile(callCtx.Scope, &profile))

	t0 := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

	// Dose 1
	out1, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Paracetamol 2.5ml",
		"timestamp": t0.Format(time.RFC3339),
	})
	require.NoError(t, err)
	assert.Contains(t, out1, "success")

	// Dose 2 at the exact same instant (elapsed = 0)
	out2, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Paracetamol 2.5ml",
		"timestamp": t0.Format(time.RFC3339),
	})
	require.NoError(t, err)

	var resp2 map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(out2), &resp2))
	assert.Equal(t, "blocked", resp2["status"])
	assert.Contains(t, out2, "COOLDOWN ACTIVE")

	// Verify database contains ONLY Dose 1
	logs, err := repo.GetCareLogs(callCtx.Scope, profile.ID, "medication", time.Time{}, time.Time{}, 100)
	require.NoError(t, err)
	assert.Len(t, logs, 1, "Unsafe dose with zero elapsed time must NOT be persisted in database")
}

// TestAdversarial_NegativeElapsedTime verifies that backdated or future clock-skewed timestamps
// cannot bypass the cooldown guardrail.
func TestAdversarial_NegativeElapsedTime(t *testing.T) {
	mod, repo, bus := setupTestModule(t)
	callCtx := createTestCallContext(bus)
	fg := callCtx.User.FamilyGroup

	now := time.Now()
	dob := now.AddDate(0, -6, 0)
	profile := models.BabyProfile{
		FamilyGroup: fg,
		Name:        "BabyNeg",
		DateOfBirth: dob,
	}
	require.NoError(t, repo.UpsertBabyProfile(callCtx.Scope, &profile))

	// Dose 1 is recorded at 14:00 (future clock skew)
	tFuture := time.Date(2026, 11, 5, 14, 0, 0, 0, time.UTC)
	out1, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Paracetamol 2.5ml",
		"timestamp": tFuture.Format(time.RFC3339),
	})
	require.NoError(t, err)
	assert.Contains(t, out1, "success")

	// Dose 2 attempted at 13:00 (which is 1 hour BEFORE Dose 1)
	tEarlier := time.Date(2026, 11, 5, 13, 0, 0, 0, time.UTC)
	out2, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Paracetamol 2.5ml",
		"timestamp": tEarlier.Format(time.RFC3339),
	})
	require.NoError(t, err)

	var resp2 map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(out2), &resp2))
	assert.Equal(t, "blocked", resp2["status"])
	assert.Contains(t, out2, "COOLDOWN ACTIVE")

	// Ensure DB still has only 1 log
	logs, err := repo.GetCareLogs(callCtx.Scope, profile.ID, "medication", time.Time{}, time.Time{}, 100)
	require.NoError(t, err)
	assert.Len(t, logs, 1, "Backdated dose against a future log must be blocked from database")
}

// TestAdversarial_Exact4HourBoundary tests 3 boundary points:
// 1 second before 4h (15:59:59) -> blocked
// Exactly 4h (16:00:00) -> allowed
// 1 second after 4h (16:00:01) -> allowed
func TestAdversarial_Exact4HourBoundary(t *testing.T) {
	mod, repo, bus := setupTestModule(t)
	callCtx := createTestCallContext(bus)
	fg := callCtx.User.FamilyGroup

	now := time.Now()
	dob := now.AddDate(0, -6, 0)
	profile := models.BabyProfile{
		FamilyGroup: fg,
		Name:        "BabyBoundary",
		DateOfBirth: dob,
	}
	require.NoError(t, repo.UpsertBabyProfile(callCtx.Scope, &profile))

	t0 := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

	// Dose 1 at 12:00:00
	_, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Paracetamol 2.5ml",
		"timestamp": t0.Format(time.RFC3339),
	})
	require.NoError(t, err)

	// Boundary 1: 3 hours 59 minutes 59 seconds (1 second BEFORE 4h cooldown)
	tBefore := t0.Add(4*time.Hour - 1*time.Second)
	outBefore, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Paracetamol 2.5ml",
		"timestamp": tBefore.Format(time.RFC3339),
	})
	require.NoError(t, err)
	var respBefore map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(outBefore), &respBefore))
	assert.Equal(t, "blocked", respBefore["status"], "1 second before 4-hour mark must be blocked")

	// Boundary 2: Exactly 4 hours (16:00:00)
	tExact := t0.Add(4 * time.Hour)
	outExact, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Paracetamol 2.5ml",
		"timestamp": tExact.Format(time.RFC3339),
	})
	require.NoError(t, err)
	var respExact map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(outExact), &respExact))
	assert.Equal(t, "success", respExact["status"], "Exactly at 4 hours must be permitted")

	// Boundary 3: 4 hours 1 second (16:00:01)
	tAfter := tExact.Add(1 * time.Second)
	outAfter, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Paracetamol 2.5ml",
		"timestamp": tAfter.Format(time.RFC3339),
	})
	require.NoError(t, err)
	var respAfter map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(outAfter), &respAfter))
	// Because dose 2 was recorded at 16:00:00, Dose 3 at 16:00:01 is only 1s after Dose 2, so it must be blocked!
	assert.Equal(t, "blocked", respAfter["status"], "1 second after Dose 2 must be blocked")

	// Verify DB has 2 logs (Dose 1 and Dose 2)
	logs, err := repo.GetCareLogs(callCtx.Scope, profile.ID, "medication", time.Time{}, time.Time{}, 100)
	require.NoError(t, err)
	assert.Len(t, logs, 2)
}

// TestAdversarial_ForceFlagHandling verifies force flag behavior:
// unforced unsafe dose is blocked; force: false is blocked; force: true writes to DB
// and establishes a new cooldown window.
func TestAdversarial_ForceFlagHandling(t *testing.T) {
	mod, repo, bus := setupTestModule(t)
	callCtx := createTestCallContext(bus)
	fg := callCtx.User.FamilyGroup

	now := time.Now()
	dob := now.AddDate(0, -6, 0)
	profile := models.BabyProfile{
		FamilyGroup: fg,
		Name:        "BabyForce",
		DateOfBirth: dob,
	}
	require.NoError(t, repo.UpsertBabyProfile(callCtx.Scope, &profile))

	t0 := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

	// Dose 1 at 12:00
	_, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Paracetamol 2.5ml",
		"timestamp": t0.Format(time.RFC3339),
	})
	require.NoError(t, err)

	// Dose 2 at 13:00 without force
	t1 := t0.Add(1 * time.Hour)
	outNoForce, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Paracetamol 2.5ml",
		"timestamp": t1.Format(time.RFC3339),
	})
	require.NoError(t, err)
	assert.Contains(t, outNoForce, "blocked")

	// Dose 2 at 13:00 with explicit force: false
	outForceFalse, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Paracetamol 2.5ml",
		"timestamp": t1.Format(time.RFC3339),
		"force":     false,
	})
	require.NoError(t, err)
	assert.Contains(t, outForceFalse, "blocked")

	// Dose 2 at 13:00 with force: true (doctor prescribed early dose)
	outForceTrue, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Paracetamol 2.5ml",
		"timestamp": t1.Format(time.RFC3339),
		"force":     true,
	})
	require.NoError(t, err)
	assert.Contains(t, outForceTrue, "success")

	// DB must now have 2 records
	logs, err := repo.GetCareLogs(callCtx.Scope, profile.ID, "medication", time.Time{}, time.Time{}, 100)
	require.NoError(t, err)
	assert.Len(t, logs, 2)

	// Dose 3 at 14:00 (1 hour after forced Dose 2): cooldown must now anchor from Dose 2 (13:00)
	t2 := t1.Add(1 * time.Hour)
	outDose3, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Paracetamol 2.5ml",
		"timestamp": t2.Format(time.RFC3339),
	})
	require.NoError(t, err)
	assert.Contains(t, outDose3, "blocked")
	assert.Contains(t, outDose3, "17:00", "Next safe dose should be 13:00 + 4h = 17:00")
}

// TestAdversarial_NonMedicationBypass tests that non-medication care logs (feeding, diaper, sleep,
// measurement) are never blocked by the medication cooldown guardrail and do not corrupt the medication timeline.
func TestAdversarial_NonMedicationBypass(t *testing.T) {
	mod, repo, bus := setupTestModule(t)
	callCtx := createTestCallContext(bus)
	fg := callCtx.User.FamilyGroup

	now := time.Now()
	dob := now.AddDate(0, -6, 0)
	profile := models.BabyProfile{
		FamilyGroup: fg,
		Name:        "BabyNonMed",
		DateOfBirth: dob,
	}
	require.NoError(t, repo.UpsertBabyProfile(callCtx.Scope, &profile))

	t0 := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

	// Medication at 12:00
	_, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Paracetamol 2.5ml",
		"timestamp": t0.Format(time.RFC3339),
	})
	require.NoError(t, err)

	// Routine logs 5-15 mins later should all succeed
	nonMedTypes := []string{"feeding", "diaper", "sleep", "measurement", "temperature"}
	for i, lt := range nonMedTypes {
		tNonMed := t0.Add(time.Duration(i+1) * 3 * time.Minute)
		out, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
			"baby_id":       int(profile.ID),
			"log_type":      lt,
			"details":       "Routine check",
			"value_numeric": 150.0,
			"unit":          "ml",
			"timestamp":     tNonMed.Format(time.RFC3339),
		})
		require.NoError(t, err)
		assert.Contains(t, out, "success", "Non-medication log type %s must succeed", lt)
	}

	// Another medication dose at 12:30 (30m after Dose 1) MUST still be blocked!
	tUnsafe := t0.Add(30 * time.Minute)
	outUnsafe, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Paracetamol 2.5ml",
		"timestamp": tUnsafe.Format(time.RFC3339),
	})
	require.NoError(t, err)
	assert.Contains(t, outUnsafe, "blocked", "Medication cooldown must remain active despite intermediate non-med logs")
}

// TestAdversarial_VaryingMedicationDetails tests whether the cooldown check can be bypassed
// by varying the details string (e.g., "Paracetamol 5ml" vs "Paracetamol 2.5ml" or "Calpol" vs "Paracetamol").
func TestAdversarial_VaryingMedicationDetails(t *testing.T) {
	mod, repo, bus := setupTestModule(t)
	callCtx := createTestCallContext(bus)
	fg := callCtx.User.FamilyGroup

	now := time.Now()
	dob := now.AddDate(0, -6, 0)
	profile := models.BabyProfile{
		FamilyGroup: fg,
		Name:        "BabyVariation",
		DateOfBirth: dob,
	}
	require.NoError(t, repo.UpsertBabyProfile(callCtx.Scope, &profile))

	t0 := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

	// Dose 1: "Paracetamol 2.5ml"
	_, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Paracetamol 2.5ml",
		"timestamp": t0.Format(time.RFC3339),
	})
	require.NoError(t, err)

	// Dose 2 at 12:30 (30 minutes later!): logged as "Paracetamol 5ml"
	t1 := t0.Add(30 * time.Minute)
	outDose2, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Paracetamol 5ml",
		"timestamp": t1.Format(time.RFC3339),
	})
	require.NoError(t, err)

	var resp2 map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(outDose2), &resp2))

	// Assert that varying the dosage string does NOT bypass the cooldown guardrail
	assert.Equal(t, "blocked", resp2["status"], "Administering another dose of paracetamol with a different details string ('Paracetamol 5ml' vs 'Paracetamol 2.5ml') within 30 minutes MUST be blocked!")
}

// TestAdversarial_DifferentBrandNamesSameClass tests whether giving Calpol followed by Tylenol
// 30 minutes later (both Paracetamol) triggers the cooldown guardrail or allows lethal double dosing.
func TestAdversarial_DifferentBrandNamesSameClass(t *testing.T) {
	mod, repo, bus := setupTestModule(t)
	callCtx := createTestCallContext(bus)
	fg := callCtx.User.FamilyGroup

	now := time.Now()
	dob := now.AddDate(0, -6, 0)
	profile := models.BabyProfile{
		FamilyGroup: fg,
		Name:        "BabyBrands",
		DateOfBirth: dob,
	}
	require.NoError(t, repo.UpsertBabyProfile(callCtx.Scope, &profile))

	t0 := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

	// Dose 1: "Calpol Infant Suspension"
	_, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Calpol Infant Suspension",
		"timestamp": t0.Format(time.RFC3339),
	})
	require.NoError(t, err)

	// Dose 2: "Tylenol Drops" 30 minutes later
	t1 := t0.Add(30 * time.Minute)
	outDose2, err := mod.Execute(context.Background(), callCtx, "log_baby_care", map[string]interface{}{
		"baby_id":   int(profile.ID),
		"log_type":  "medication",
		"details":   "Tylenol Drops",
		"timestamp": t1.Format(time.RFC3339),
	})
	require.NoError(t, err)

	var resp2 map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(outDose2), &resp2))

	assert.Equal(t, "blocked", resp2["status"], "Calpol followed by Tylenol 30m later MUST be blocked because both are Paracetamol!")
}
