package main

import "testing"

// cpuBusyPct test cases — covers idle, fully busy, and partial-load deltas
// against the same total delta.

func TestCPUBusyPctFullyIdle(t *testing.T) {
	prev := cpuSnapshot{idle: 100, total: 200}
	cur := cpuSnapshot{idle: 200, total: 300}
	// delta: idle=100, total=100 → busy=0% (fully idle window)
	if got := cpuBusyPct(prev, cur); got != 0.0 {
		t.Errorf("idle window: expected 0%%, got %v", got)
	}
}

func TestCPUBusyPctFullySaturated(t *testing.T) {
	prev := cpuSnapshot{idle: 100, total: 200}
	cur := cpuSnapshot{idle: 100, total: 300}
	// delta: idle=0, total=100 → busy=100% (no idle accrued)
	if got := cpuBusyPct(prev, cur); got != 100.0 {
		t.Errorf("saturated: expected 100%%, got %v", got)
	}
}

func TestCPUBusyPctHalfLoad(t *testing.T) {
	prev := cpuSnapshot{idle: 100, total: 200}
	cur := cpuSnapshot{idle: 110, total: 220}
	// delta: idle=10, total=20 → busy=10/20 = 50%
	if got := cpuBusyPct(prev, cur); got != 50.0 {
		t.Errorf("half load: expected 50%%, got %v", got)
	}
}

func TestCPUBusyPctZeroDelta(t *testing.T) {
	// Two reads of the same snapshot (e.g., probe ran faster than jiffy resolution)
	prev := cpuSnapshot{idle: 100, total: 200}
	cur := cpuSnapshot{idle: 100, total: 200}
	if got := cpuBusyPct(prev, cur); got != 0.0 {
		t.Errorf("zero delta: expected 0 (degenerate), got %v", got)
	}
}

// readCPUSnapshot is best-effort; on Linux we expect /proc/stat. Test that
// the function either succeeds (giving us a non-zero total) or errors
// cleanly. We don't assert specific values since they're system-dependent.
func TestReadCPUSnapshot_LinuxOnly(t *testing.T) {
	snap, err := readCPUSnapshot()
	if err != nil {
		// Acceptable on macOS / non-Linux. Just confirm we returned a clean error.
		t.Logf("readCPUSnapshot unavailable on this platform: %v", err)
		return
	}
	if snap.total == 0 {
		t.Errorf("expected non-zero total on Linux, got %+v", snap)
	}
	if snap.idle > snap.total {
		t.Errorf("idle (%d) cannot exceed total (%d)", snap.idle, snap.total)
	}
}
