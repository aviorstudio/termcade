package sdk

import "testing"

func TestKeyTrackerHoldAndExpiry(t *testing.T) {
	kt := NewKeyTracker(5)
	if kt.Held(KeyLeft) {
		t.Error("unpressed key reported held")
	}
	kt.Press(KeyLeft)
	if !kt.Held(KeyLeft) {
		t.Error("key not held immediately after press")
	}
	for i := 0; i < 4; i++ {
		kt.Tick()
	}
	if !kt.Held(KeyLeft) {
		t.Error("key expired before holdTicks frames")
	}
	kt.Tick() // 5th tick after press
	if kt.Held(KeyLeft) {
		t.Error("key still held after holdTicks frames")
	}
}

func TestKeyTrackerRefresh(t *testing.T) {
	kt := NewKeyTracker(3)
	kt.Press(KeyRight)
	for i := 0; i < 10; i++ {
		kt.Tick()
		kt.Press(KeyRight) // auto-repeat refreshes the hold
	}
	if !kt.Held(KeyRight) {
		t.Error("refreshed key not held")
	}
	if kt.Held(KeyLeft) {
		t.Error("other key held")
	}
}

func TestKeyTrackerReset(t *testing.T) {
	kt := NewKeyTracker(5)
	kt.Press(KeyA)
	kt.Reset()
	if kt.Held(KeyA) {
		t.Error("key held after Reset")
	}
}

// TestKeyTrackerExactModeSurvivesRepeatGap is the whole point of using key
// releases: between the initial press and the start of auto-repeat the
// terminal sends nothing for hundreds of milliseconds, and the decay fallback
// drops the key in that gap. With releases reported, the key stays held.
func TestKeyTrackerExactModeSurvivesRepeatGap(t *testing.T) {
	kt := NewKeyTracker(5)
	kt.Press(KeyLeft)
	kt.Release(KeyLeft) // proves the terminal reports releases
	if !kt.Exact() {
		t.Fatal("tracker did not switch to exact mode after a release")
	}

	kt.Press(KeyRight)
	for i := 0; i < 100; i++ { // far past holdTicks, with no repeats at all
		kt.Tick()
	}
	if !kt.Held(KeyRight) {
		t.Error("held key expired in exact mode")
	}
	kt.Release(KeyRight)
	if kt.Held(KeyRight) {
		t.Error("key still held after release")
	}
}

func TestKeyTrackerResetKeepsMode(t *testing.T) {
	kt := NewKeyTracker(5)
	kt.Press(KeyLeft)
	kt.Release(KeyLeft)
	kt.Reset()
	if !kt.Exact() {
		t.Error("Reset forgot the terminal reports releases")
	}
	if kt.Held(KeyLeft) {
		t.Error("key held after Reset")
	}
}
