package fault_test

import (
	"testing"
	"time"

	"github.com/synapse/mockbackend/internal/fault"
)

func TestDefaultNoFault(t *testing.T) {
	c := fault.NewController()
	defer c.Stop()

	cfg := c.Load()
	if cfg.IsHardFail() {
		t.Error("expected no hard fail by default")
	}
	if c.IsHardFail() {
		t.Error("expected IsHardFail()=false by default")
	}
	if extra := c.ExtraLatency(); extra != 0 {
		t.Errorf("expected 0 extra latency by default, got %.2f ms", extra)
	}
}

func TestF1LatencyAddition(t *testing.T) {
	c := fault.NewController()
	defer c.Stop()

	c.Set(fault.Config{
		F1LatencyAddMs: 200.0,
		F1LatencyMul:   1.0,
	})

	extra := c.ExtraLatency()
	if extra < 190 || extra > 210 {
		t.Errorf("expected ~200ms extra latency, got %.2f ms", extra)
	}
}

func TestF1Multiplier(t *testing.T) {
	c := fault.NewController()
	defer c.Stop()

	c.Set(fault.Config{
		F1LatencyMul: 3.0,
	})

	cfg := c.Load()
	if mul := cfg.EffectiveMultiplier(); mul < 2.9 || mul > 3.1 {
		t.Errorf("expected multiplier ≈3.0, got %.2f", mul)
	}
}

func TestF5HardFail(t *testing.T) {
	c := fault.NewController()
	defer c.Stop()

	c.Set(fault.Config{F5HardFail: true})
	if !c.IsHardFail() {
		t.Error("expected IsHardFail()=true when F5 active")
	}

	c.Clear()
	if c.IsHardFail() {
		t.Error("expected IsHardFail()=false after Clear()")
	}
}

func TestF6RampIncreasesOverTime(t *testing.T) {
	c := fault.NewController()
	defer c.Stop()

	c.Set(fault.Config{F6RampRateMs: 100.0}) // +100ms per second

	time.Sleep(300 * time.Millisecond) // wait for ~30ms ramp
	ramp := c.ExtraLatency()
	if ramp < 10 { // conservative: at least 10ms in 300ms
		t.Errorf("expected ramp > 10ms after 300ms, got %.2f ms", ramp)
	}

	c.Clear()
	// After clear, extra latency should reset
	time.Sleep(20 * time.Millisecond)
	after := c.ExtraLatency()
	if after > 5 {
		t.Errorf("expected ramp reset after Clear(), got %.2f ms", after)
	}
}

func TestF7Flapping(t *testing.T) {
	c := fault.NewController()
	defer c.Stop()

	// Flap every 100ms
	c.Set(fault.Config{F7FlappingIntervalMs: 100.0})

	// After 350ms we should have seen at least one state flip
	var sawFail, sawNormal bool
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if c.IsHardFail() {
			sawFail = true
		} else {
			sawNormal = true
		}
		if sawFail && sawNormal {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !sawFail || !sawNormal {
		t.Errorf("expected both hard-fail and normal states during F7 flapping (sawFail=%v sawNormal=%v)",
			sawFail, sawNormal)
	}
}

func TestClearResetsAll(t *testing.T) {
	c := fault.NewController()
	defer c.Stop()

	c.Set(fault.Config{
		F1LatencyAddMs:   100,
		F5HardFail:       true,
		F6RampRateMs:     50,
	})

	c.Clear()
	time.Sleep(20 * time.Millisecond)

	if c.IsHardFail() {
		t.Error("Clear() should reset F5")
	}
	if extra := c.ExtraLatency(); extra > 1.0 {
		t.Errorf("Clear() should reset F1/F6 extra latency, got %.2f ms", extra)
	}
}

func TestSetReplacesConcurrently(t *testing.T) {
	c := fault.NewController()
	defer c.Stop()

	// Stress: rapid Set/Clear while Load() is called concurrently.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			_ = c.Load()
			_ = c.IsHardFail()
			_ = c.ExtraLatency()
			time.Sleep(time.Millisecond)
		}
	}()

	for i := 0; i < 20; i++ {
		c.Set(fault.Config{F1LatencyAddMs: float64(i * 10)})
		time.Sleep(5 * time.Millisecond)
		c.Clear()
		time.Sleep(5 * time.Millisecond)
	}
	<-done
}
