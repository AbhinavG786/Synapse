// Package workload models the service-time distributions for each mock endpoint
// class using lognormal random variables seeded deterministically.
//
// Endpoint calibration (see docs/MODEL.md and Section 7 of the spec):
//
//	Class          Median (ms)  mu         sigma  Pool
//	health         ~0.2         -1.609     0.20   none (no pool)
//	light_crud     ~5           1.609      0.40   crud
//	medium_query   ~40          3.689      0.50   query
//	heavy_compute  base ~80 +   4.382      0.60   compute (burns CPU)
//	               cpu_work(payload_bytes)
//	io_bound       ~100         4.605      0.50   io (sleep-based)
//
// The SpeedFactor from NodeConfig multiplies all sampled service times, so a
// node with SpeedFactor=0.6 is 40% slower than a baseline node.
//
// Reference for lognormal: if X ~ LogNormal(mu, sigma) then
//
//	median(X) = exp(mu)
//	mean(X)   = exp(mu + sigma²/2)
//	std(X)    = mean(X) * sqrt(exp(sigma²) - 1)
package workload

import (
	"math"
	"math/rand/v2"
	"time"
)

// Class identifies the endpoint workload category.
type Class int

const (
	ClassHealth       Class = iota // GET /health
	ClassLightCRUD                 // GET,POST /crud/*
	ClassMediumQuery               // POST /query
	ClassHeavyCompute              // POST /compute
	ClassIOBound                   // POST /io
)

// classParams holds lognormal parameters for each endpoint class.
// mu and sigma are in the log domain; the sampled value is in milliseconds.
type classParams struct {
	mu    float64 // log-space mean → median = exp(mu) ms
	sigma float64 // log-space std
}

var params = map[Class]classParams{
	ClassHealth:       {mu: -1.609, sigma: 0.20}, // median ≈ 0.2 ms
	ClassLightCRUD:    {mu: 1.609, sigma: 0.40},  // median ≈ 5 ms
	ClassMediumQuery:  {mu: 3.689, sigma: 0.50},  // median ≈ 40 ms
	ClassHeavyCompute: {mu: 4.382, sigma: 0.60},  // median ≈ 80 ms base
	ClassIOBound:      {mu: 4.605, sigma: 0.50},  // median ≈ 100 ms
}

// Model is a seeded service-time sampler.
// Create one per backend node; it is NOT goroutine-safe — protect with a mutex
// or use separate Models per goroutine.
type Model struct {
	rng         *rand.Rand
	speedFactor float64
	cpuItersPB  int // CPU iterations per payload byte for /compute
}

// New creates a Model seeded with the given value.
// speedFactor scales all sampled times (>1 = slower node, <1 = faster).
// cpuIterationsPerByte controls /compute CPU burn intensity.
func New(seed uint64, speedFactor float64, cpuIterationsPerByte int) *Model {
	return &Model{
		rng:         rand.New(rand.NewPCG(seed, seed^0xdeadbeef)),
		speedFactor: speedFactor,
		cpuItersPB:  cpuIterationsPerByte,
	}
}

// Sample returns a lognormal service-time duration for the given endpoint class.
// payloadBytes is used only for ClassHeavyCompute to add CPU work time.
// The returned duration reflects the SpeedFactor; it does NOT perform actual
// CPU work — call BurnCPU separately for the /compute endpoint.
func (m *Model) Sample(class Class, payloadBytes int64) time.Duration {
	p, ok := params[class]
	if !ok {
		p = params[ClassLightCRUD]
	}

	ms := m.logNormal(p.mu, p.sigma) * m.speedFactor

	// For compute: add payload-proportional work time on top of the base.
	if class == ClassHeavyCompute && payloadBytes > 0 {
		cpuMs := float64(payloadBytes) * float64(m.cpuItersPB) / 1e6
		ms += cpuMs * m.speedFactor
	}

	// Floor at 0.05 ms to avoid zero/negative durations.
	if ms < 0.05 {
		ms = 0.05
	}
	return time.Duration(ms * float64(time.Millisecond))
}

// logNormal samples X ~ LogNormal(mu, sigma) using Box-Muller transform.
// The rng is NOT goroutine-safe; callers must hold a mutex if shared.
func (m *Model) logNormal(mu, sigma float64) float64 {
	// Box-Muller: produce standard normal z
	u1 := m.rng.Float64()
	u2 := m.rng.Float64()
	// Guard against log(0)
	if u1 < 1e-300 {
		u1 = 1e-300
	}
	z := math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
	return math.Exp(mu + sigma*z)
}

// BurnCPU performs actual floating-point computation proportional to
// iterations, ensuring the compute endpoint genuinely uses CPU.
// Returns a sink value to prevent the compiler from eliding the work.
// Call this in the request handler goroutine while the pool token is held.
func BurnCPU(iterations int) float64 {
	var sink float64
	for i := 0; i < iterations; i++ {
		// Mix of sqrt and log to prevent vectorisation optimisation.
		sink += math.Sqrt(float64(i+1)*3.14159265) + math.Log1p(float64(i+1))
	}
	return sink
}

// CPUIterations converts a payload size to a BurnCPU iteration count
// using the model's calibration constant.
func (m *Model) CPUIterations(payloadBytes int64) int {
	if payloadBytes <= 0 {
		payloadBytes = 256 // minimum burn for /compute with no body
	}
	n := int(payloadBytes) * m.cpuItersPB
	// Clamp to a reasonable range.
	if n < 1000 {
		n = 1000
	}
	if n > 50_000_000 {
		n = 50_000_000
	}
	return n
}

// TheoreticalMedian returns the theoretical median service time for a class.
// Useful for test assertions.
func TheoreticalMedian(class Class) float64 {
	p, ok := params[class]
	if !ok {
		return 0
	}
	return math.Exp(p.mu) // ms
}

// TheoreticalMean returns the theoretical mean service time for a class.
func TheoreticalMean(class Class) float64 {
	p, ok := params[class]
	if !ok {
		return 0
	}
	return math.Exp(p.mu + p.sigma*p.sigma/2) // ms
}
