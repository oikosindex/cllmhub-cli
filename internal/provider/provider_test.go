package provider

import (
	"testing"
	"time"

	"github.com/cllmhub/cllmhub-cli/internal/hub"
)

// newTestProvider creates a minimal Provider for testing AIMD logic.
// updateHubSlots is left nil so hub calls are skipped.
func newTestProvider(maxSlots, ceiling int) *Provider {
	return &Provider{
		maxSlots:      maxSlots,
		slotCeiling:   ceiling,
		slots:         make(chan struct{}, maxSlots),
		modelServerUp: true,
		hubCfg:        hub.ConnectConfig{MaxConcurrent: maxSlots},
	}
}

// --- trackSuccessfulInflight (additive increase) ---

func TestRampUp_IncreasesAfterThreshold(t *testing.T) {
	p := newTestProvider(1, 5)

	// 3 consecutive successes at max should bump slots to 2.
	for i := 0; i < rampUpThreshold; i++ {
		p.trackSuccessfulInflight(1)
	}

	p.mu.Lock()
	got := p.maxSlots
	p.mu.Unlock()

	if got != 2 {
		t.Errorf("maxSlots = %d after %d successes at max, want 2", got, rampUpThreshold)
	}
}

func TestRampUp_DoesNotExceedCeiling(t *testing.T) {
	p := newTestProvider(4, 5)

	// Ramp from 4 → 5
	for i := 0; i < rampUpThreshold; i++ {
		p.trackSuccessfulInflight(4)
	}

	p.mu.Lock()
	if p.maxSlots != 5 {
		t.Fatalf("maxSlots = %d, want 5", p.maxSlots)
	}
	p.mu.Unlock()

	// Further successes at 5 should NOT increase beyond ceiling.
	for i := 0; i < rampUpThreshold*2; i++ {
		p.trackSuccessfulInflight(5)
	}

	p.mu.Lock()
	got := p.maxSlots
	p.mu.Unlock()

	if got != 5 {
		t.Errorf("maxSlots = %d after successes at ceiling, want 5", got)
	}
}

func TestRampUp_BelowMaxDoesNotCount(t *testing.T) {
	p := newTestProvider(3, 5)

	// Successes at inflight=2 (below maxSlots=3) should not count.
	for i := 0; i < rampUpThreshold*3; i++ {
		p.trackSuccessfulInflight(2)
	}

	p.mu.Lock()
	got := p.maxSlots
	p.mu.Unlock()

	if got != 3 {
		t.Errorf("maxSlots = %d after below-max successes, want 3 (unchanged)", got)
	}
}

func TestRampUp_CooldownPreventsIncrease(t *testing.T) {
	p := newTestProvider(2, 5)
	p.lastSlotReduction = time.Now() // just reduced

	for i := 0; i < rampUpThreshold*2; i++ {
		p.trackSuccessfulInflight(2)
	}

	p.mu.Lock()
	got := p.maxSlots
	p.mu.Unlock()

	if got != 2 {
		t.Errorf("maxSlots = %d during cooldown, want 2 (unchanged)", got)
	}
}

func TestRampUp_AllowsIncreaseAfterCooldown(t *testing.T) {
	p := newTestProvider(2, 5)
	p.lastSlotReduction = time.Now().Add(-slotCooldown - time.Second) // cooldown expired

	for i := 0; i < rampUpThreshold; i++ {
		p.trackSuccessfulInflight(2)
	}

	p.mu.Lock()
	got := p.maxSlots
	p.mu.Unlock()

	if got != 3 {
		t.Errorf("maxSlots = %d after cooldown expired, want 3", got)
	}
}

// --- reduceSlots (multiplicative decrease) ---

func TestReduceSlots_Halves(t *testing.T) {
	p := newTestProvider(4, 5)

	p.reduceSlots(4)

	p.mu.Lock()
	got := p.maxSlots
	p.mu.Unlock()

	if got != 2 {
		t.Errorf("maxSlots = %d after reduce from 4, want 2", got)
	}
}

func TestReduceSlots_FloorAtOne(t *testing.T) {
	p := newTestProvider(1, 5)

	p.reduceSlots(1)

	p.mu.Lock()
	got := p.maxSlots
	p.mu.Unlock()

	if got != 1 {
		t.Errorf("maxSlots = %d after reduce from 1, want 1 (floor)", got)
	}
}

func TestReduceSlots_ResetsConsecutiveCount(t *testing.T) {
	p := newTestProvider(4, 5)
	p.consecutiveAtMax = 2

	p.reduceSlots(4)

	p.mu.Lock()
	got := p.consecutiveAtMax
	p.mu.Unlock()

	if got != 0 {
		t.Errorf("consecutiveAtMax = %d after reduce, want 0", got)
	}
}

func TestReduceSlots_SetsCooldown(t *testing.T) {
	p := newTestProvider(4, 5)

	before := time.Now()
	p.reduceSlots(4)

	p.mu.Lock()
	cooldown := p.lastSlotReduction
	p.mu.Unlock()

	if cooldown.Before(before) {
		t.Error("lastSlotReduction was not updated after reduceSlots")
	}
}

func TestReduceSlots_OddNumber(t *testing.T) {
	p := newTestProvider(5, 5)

	p.reduceSlots(5)

	p.mu.Lock()
	got := p.maxSlots
	p.mu.Unlock()

	if got != 2 {
		t.Errorf("maxSlots = %d after reduce from 5, want 2 (5/2 truncated)", got)
	}
}

// --- peakInflight tracking ---

func TestPeakInflight_Tracked(t *testing.T) {
	p := newTestProvider(5, 5)

	p.trackSuccessfulInflight(3)
	p.trackSuccessfulInflight(1)

	p.mu.Lock()
	got := p.peakInflight
	p.mu.Unlock()

	if got != 3 {
		t.Errorf("peakInflight = %d, want 3", got)
	}
}

// --- semaphore ---

func TestSemaphore_EnforcesLimit(t *testing.T) {
	p := newTestProvider(2, 5)

	// Fill both slots.
	p.slots <- struct{}{}
	p.slots <- struct{}{}

	// Third should not fit (non-blocking check).
	select {
	case p.slots <- struct{}{}:
		t.Error("semaphore allowed 3rd slot with capacity 2")
	default:
		// expected
	}
}

func TestResizeSlots_ChangesCapacity(t *testing.T) {
	p := newTestProvider(2, 5)

	p.mu.Lock()
	p.resizeSlots(4)
	sem := p.slots
	p.mu.Unlock()

	if cap(sem) != 4 {
		t.Errorf("semaphore capacity = %d after resize, want 4", cap(sem))
	}
}

// --- AIMD full cycle ---

func TestAIMD_FullCycle(t *testing.T) {
	p := newTestProvider(1, 3)

	// Ramp 1 → 2
	for i := 0; i < rampUpThreshold; i++ {
		p.trackSuccessfulInflight(1)
	}
	p.mu.Lock()
	if p.maxSlots != 2 {
		t.Fatalf("step 1: maxSlots = %d, want 2", p.maxSlots)
	}
	p.mu.Unlock()

	// Ramp 2 → 3 (ceiling)
	for i := 0; i < rampUpThreshold; i++ {
		p.trackSuccessfulInflight(2)
	}
	p.mu.Lock()
	if p.maxSlots != 3 {
		t.Fatalf("step 2: maxSlots = %d, want 3", p.maxSlots)
	}
	p.mu.Unlock()

	// Error → halve: 3 → 1
	p.reduceSlots(3)
	p.mu.Lock()
	if p.maxSlots != 1 {
		t.Fatalf("step 3: maxSlots = %d after reduce, want 1", p.maxSlots)
	}
	p.mu.Unlock()

	// During cooldown, successes don't ramp up.
	for i := 0; i < rampUpThreshold*2; i++ {
		p.trackSuccessfulInflight(1)
	}
	p.mu.Lock()
	if p.maxSlots != 1 {
		t.Fatalf("step 4: maxSlots = %d during cooldown, want 1", p.maxSlots)
	}
	p.mu.Unlock()

	// After cooldown, ramp up again: 1 → 2
	p.mu.Lock()
	p.lastSlotReduction = time.Now().Add(-slotCooldown - time.Second)
	p.mu.Unlock()

	for i := 0; i < rampUpThreshold; i++ {
		p.trackSuccessfulInflight(1)
	}
	p.mu.Lock()
	if p.maxSlots != 2 {
		t.Fatalf("step 5: maxSlots = %d after cooldown, want 2", p.maxSlots)
	}
	p.mu.Unlock()
}
