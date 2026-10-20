package workload_test

import (
	"math"
	"testing"
	"time"

	"github.com/synapse/mockbackend/internal/workload"
)

const (
	nSamples   = 20_000
	tolerancePct = 25.0 // allow ±25% from theoretical values
)

func TestLognormalMedian(t *testing.T) {
	classes := []workload.Class{
		workload.ClassHealth,
		workload.ClassLightCRUD,
		workload.ClassMediumQuery,
		workload.ClassHeavyCompute,
		workload.ClassIOBound,
	}

	m := workload.New(42, 1.0, 300)

	for _, class := range classes {
		samples := make([]float64, nSamples)
		for i := range samples {
			d := m.Sample(class, 0)
			samples[i] = float64(d) / float64(time.Millisecond)
		}

		got := median(samples)
		want := workload.TheoreticalMedian(class)

		pctErr := math.Abs(got-want) / want * 100
		if pctErr > tolerancePct {
			t.Errorf("class %d: median=%.3f ms, want ≈%.3f ms (err=%.1f%%)",
				class, got, want, pctErr)
		} else {
			t.Logf("class %d: median=%.3f ms, want ≈%.3f ms (err=%.1f%%) ✓",
				class, got, want, pctErr)
		}
	}
}

func TestLognormalMean(t *testing.T) {
	m := workload.New(123, 1.0, 300)
	samples := make([]float64, nSamples)
	for i := range samples {
		d := m.Sample(workload.ClassLightCRUD, 0)
		samples[i] = float64(d) / float64(time.Millisecond)
	}

	got := mean(samples)
	want := workload.TheoreticalMean(workload.ClassLightCRUD)
	pctErr := math.Abs(got-want) / want * 100
	if pctErr > tolerancePct {
		t.Errorf("mean=%.3f ms, want ≈%.3f ms (err=%.1f%%)", got, want, pctErr)
	}
}

func TestSpeedFactor(t *testing.T) {
	m1 := workload.New(99, 1.0, 300) // baseline
	m2 := workload.New(99, 2.0, 300) // 2× slower

	var sum1, sum2 float64
	for i := 0; i < nSamples; i++ {
		d1 := m1.Sample(workload.ClassMediumQuery, 0)
		d2 := m2.Sample(workload.ClassMediumQuery, 0)
		sum1 += float64(d1)
		sum2 += float64(d2)
	}
	ratio := sum2 / sum1
	// Expect ratio ≈ 2.0 ± 10%
	if ratio < 1.8 || ratio > 2.2 {
		t.Errorf("speed factor 2.0 expected ratio ≈2.0, got %.3f", ratio)
	}
}

func TestDeterminism(t *testing.T) {
	// Same seed → same sequence
	m1 := workload.New(777, 1.0, 300)
	m2 := workload.New(777, 1.0, 300)
	for i := 0; i < 100; i++ {
		d1 := m1.Sample(workload.ClassLightCRUD, 512)
		d2 := m2.Sample(workload.ClassLightCRUD, 512)
		if d1 != d2 {
			t.Fatalf("non-deterministic at i=%d: %v != %v", i, d1, d2)
		}
	}
}

func TestNonNegative(t *testing.T) {
	m := workload.New(1, 1.0, 300)
	for i := 0; i < nSamples; i++ {
		d := m.Sample(workload.ClassHealth, 0)
		if d < 0 {
			t.Fatalf("negative duration: %v", d)
		}
	}
}

func TestBurnCPUDoesSomeWork(t *testing.T) {
	// Just ensure it doesn't panic and returns a non-NaN value.
	sink := workload.BurnCPU(10_000)
	if math.IsNaN(sink) || math.IsInf(sink, 0) {
		t.Errorf("BurnCPU returned bad value: %v", sink)
	}
}

// median sorts and picks the middle value.
func median(xs []float64) float64 {
	sorted := make([]float64, len(xs))
	copy(sorted, xs)
	// simple insertion sort — fine for test-only use
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	mid := len(sorted) / 2
	if len(sorted)%2 == 0 {
		return (sorted[mid-1] + sorted[mid]) / 2
	}
	return sorted[mid]
}

func mean(xs []float64) float64 {
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}
