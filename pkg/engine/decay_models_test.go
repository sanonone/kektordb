package engine

import (
	"math"
	"testing"
	"time"
)

func TestDecayModels(t *testing.T) {
	now := float64(time.Now().Unix())
	halfLife := 86400.0 // 1 day in seconds

	t.Run("ExponentialDecay", func(t *testing.T) {
		// At half-life, should be 0.5
		age := 86400.0 // 1 day old
		factor := calculateExponentialDecay(age, halfLife)
		if math.Abs(factor-0.5) > 0.01 {
			t.Errorf("Exponential decay at half-life: expected ~0.5, got %f", factor)
		}

		// At 2x half-life, should be 0.25
		age = 172800.0 // 2 days old
		factor = calculateExponentialDecay(age, halfLife)
		if math.Abs(factor-0.25) > 0.01 {
			t.Errorf("Exponential decay at 2x half-life: expected ~0.25, got %f", factor)
		}

		// At age 0, should be 1.0
		factor = calculateExponentialDecay(0, halfLife)
		if factor != 1.0 {
			t.Errorf("Exponential decay at age 0: expected 1.0, got %f", factor)
		}
	})

	t.Run("LinearDecay", func(t *testing.T) {
		// At half half-life, should be 0.5
		age := 43200.0 // 12 hours old
		factor := calculateLinearDecay(age, halfLife)
		if math.Abs(factor-0.5) > 0.01 {
			t.Errorf("Linear decay at half half-life: expected ~0.5, got %f", factor)
		}

		// At half-life, should be 0.0 (clamped)
		age = 86400.0 // 1 day old
		factor = calculateLinearDecay(age, halfLife)
		if factor != 0.0 {
			t.Errorf("Linear decay at half-life: expected 0.0 (clamped), got %f", factor)
		}

		// Beyond half-life, should stay at 0.0
		age = 172800.0 // 2 days old
		factor = calculateLinearDecay(age, halfLife)
		if factor != 0.0 {
			t.Errorf("Linear decay beyond half-life: expected 0.0, got %f", factor)
		}
	})

	t.Run("StepDecay", func(t *testing.T) {
		// Before half-life, should be 1.0
		age := 43200.0 // 12 hours old
		factor := calculateStepDecay(age, halfLife)
		if factor != 1.0 {
			t.Errorf("Step decay before half-life: expected 1.0, got %f", factor)
		}

		// At half-life - 1 second, should be 1.0
		age = 86399.0
		factor = calculateStepDecay(age, halfLife)
		if factor != 1.0 {
			t.Errorf("Step decay just before half-life: expected 1.0, got %f", factor)
		}

		// At half-life, should be 0.0
		age = 86400.0
		factor = calculateStepDecay(age, halfLife)
		if factor != 0.0 {
			t.Errorf("Step decay at half-life: expected 0.0, got %f", factor)
		}

		// Beyond half-life, should be 0.0
		age = 172800.0
		factor = calculateStepDecay(age, halfLife)
		if factor != 0.0 {
			t.Errorf("Step decay beyond half-life: expected 0.0, got %f", factor)
		}
	})

	t.Run("EbbinghausDecay", func(t *testing.T) {
		// With 0 reinforcements, should behave similarly to exponential
		age := 86400.0 // 1 day old
		factor0 := calculateEbbinghausDecay(age, halfLife, 0)

		// With 10 reinforcements, should decay slower
		factor10 := calculateEbbinghausDecay(age, halfLife, 10)

		// With 100 reinforcements, should decay even slower
		factor100 := calculateEbbinghausDecay(age, halfLife, 100)

		// More reinforcements = slower decay = higher factor
		if factor10 <= factor0 {
			t.Errorf("Ebbinghaus: 10 reinforcements should decay slower than 0 (higher factor): factor0=%f, factor10=%f", factor0, factor10)
		}
		if factor100 <= factor10 {
			t.Errorf("Ebbinghaus: 100 reinforcements should decay slower than 10 (higher factor): factor10=%f, factor100=%f", factor10, factor100)
		}

		// All factors should be between 0 and 1
		if factor0 <= 0 || factor0 > 1 {
			t.Errorf("Ebbinghaus factor0 out of range: %f", factor0)
		}
		if factor10 <= 0 || factor10 > 1 {
			t.Errorf("Ebbinghaus factor10 out of range: %f", factor10)
		}
		if factor100 <= 0 || factor100 > 1 {
			t.Errorf("Ebbinghaus factor100 out of range: %f", factor100)
		}
	})

	t.Run("DecayModelRouter", func(t *testing.T) {
		created := now - 43200.0 // 12 hours ago

		// Test exponential
		factor := calculateTimeDecayModel(created, halfLife, "exponential", 0)
		expected := calculateExponentialDecay(43200.0, halfLife)
		if math.Abs(factor-expected) > 0.0001 {
			t.Errorf("Router exponential: expected %f, got %f", expected, factor)
		}

		// Test linear
		factor = calculateTimeDecayModel(created, halfLife, "linear", 0)
		expected = calculateLinearDecay(43200.0, halfLife)
		if math.Abs(factor-expected) > 0.0001 {
			t.Errorf("Router linear: expected %f, got %f", expected, factor)
		}

		// Test step
		factor = calculateTimeDecayModel(created, halfLife, "step", 0)
		expected = calculateStepDecay(43200.0, halfLife)
		if factor != expected {
			t.Errorf("Router step: expected %f, got %f", expected, factor)
		}

		// Test ebbinghaus
		factor = calculateTimeDecayModel(created, halfLife, "ebbinghaus", 5)
		expected = calculateEbbinghausDecay(43200.0, halfLife, 5)
		if math.Abs(factor-expected) > 0.0001 {
			t.Errorf("Router ebbinghaus: expected %f, got %f", expected, factor)
		}

		// Test default (unknown model falls back to exponential)
		factor = calculateTimeDecayModel(created, halfLife, "unknown", 0)
		expected = calculateExponentialDecay(43200.0, halfLife)
		if math.Abs(factor-expected) > 0.0001 {
			t.Errorf("Router default: expected %f, got %f", expected, factor)
		}
	})

	t.Run("EdgeCases", func(t *testing.T) {
		// Half-life <= 0 (decay disabled)
		factor := calculateTimeDecayModel(now-86400, 0, "exponential", 0)
		if factor != 1.0 {
			t.Errorf("Disabled decay should return 1.0, got %f", factor)
		}

		// Negative age (future created)
		factor = calculateTimeDecayModel(now+86400, halfLife, "exponential", 0)
		if factor != 1.0 {
			t.Errorf("Future created should return 1.0, got %f", factor)
		}

		// Age 0
		factor = calculateTimeDecayModel(now, halfLife, "linear", 0)
		if factor != 1.0 {
			t.Errorf("Age 0 should return 1.0, got %f", factor)
		}
	})
}
