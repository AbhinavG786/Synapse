// Package features extracts a context vector from an incoming HTTP request
// and per-node system metrics.
//
// Extraction must be O(1) with no body parsing beyond headers and
// Content-Length (R-4). The route table is a precompiled prefix/path map,
// not a regex scan.
//
// Phase 0: type definitions and stub extractor only. Full implementation in Phase 2.3.
package features

import (
	"math"
	"net/http"
)

// EndpointClass classifies an HTTP route into one of the configured cost tiers.
// These are indices into the one-hot section of the context vector.
type EndpointClass int

const (
	ClassUnknown      EndpointClass = 0
	ClassLightCRUD    EndpointClass = 1
	ClassMediumQuery  EndpointClass = 2
	ClassHeavyCompute EndpointClass = 3
	ClassIOBound      EndpointClass = 4
	ClassHealth       EndpointClass = 5
	numClasses                      = 6
)

// MethodClass maps HTTP methods to a compact integer.
type MethodClass int

const (
	MethodOther  MethodClass = 0
	MethodGET    MethodClass = 1
	MethodPOST   MethodClass = 2
	MethodPUT    MethodClass = 3
	MethodDELETE MethodClass = 4
	numMethods               = 5
)

// NodeMetrics is a snapshot of per-node system state at the time of routing.
// Populated from the node's /metrics endpoint and proxy-side counters.
type NodeMetrics struct {
	CPUUtil           float64 // fraction [0, 1]
	InFlight          int64   // proxy-side atomic counter
	QueueDepth        int64   // reported by backend
	LatencyEWMAShort  float64 // ms, short window
	LatencyEWMALong   float64 // ms, long window
	ErrorRate         float64 // fraction [0, 1]
	MetricAgeSeconds  float64 // staleness
}

// RequestFeatures holds the request-side features extracted from the HTTP request.
type RequestFeatures struct {
	EndpointClass EndpointClass
	MethodClass   MethodClass
	Log1pPayload  float64 // log1p(Content-Length)
	AuthTier      int     // 0 = none, 1 = standard, 2 = priority
}

// ContextVector is the full context vector x_{t,a} for one (request, arm) pair.
// Dimension d is determined by the feature spec version; the bias term is appended
// before the dot product in the scoring step (not stored here).
//
// Layout (feature_spec_version = 1, d = 16):
//   [0..5]   endpoint class one-hot (numClasses = 6)
//   [6..10]  method class one-hot (numMethods = 5)
//   [11]     log1p(payload_bytes), normalised
//   [12]     auth_tier, normalised
//   [13]     cpu_util
//   [14]     in_flight, normalised
//   [15]     queue_depth, normalised
//   (latency EWMA ratio, error_rate, metric_age added in Phase 2.3 when d is confirmed)
type ContextVector [16]float64

// Extractor builds context vectors from requests and node metrics.
// Phase 0: stub that returns a zero vector with the endpoint class set.
type Extractor struct {
	// routeTable maps path prefixes to EndpointClass.
	// Populated from config at startup (Phase 2.3).
	routeTable map[string]EndpointClass
}

// NewExtractor creates an Extractor with the given route table.
func NewExtractor(routes map[string]EndpointClass) *Extractor {
	return &Extractor{routeTable: routes}
}

// ExtractRequest extracts request-side features in O(1).
// No body is read; Content-Length header is used as payload_bytes.
func (e *Extractor) ExtractRequest(r *http.Request) RequestFeatures {
	class := e.classifyRoute(r.URL.Path)
	method := classifyMethod(r.Method)

	var payloadBytes int64
	if r.ContentLength > 0 {
		payloadBytes = r.ContentLength
	}

	authTier := 0
	if t := r.Header.Get("X-Auth-Tier"); t == "priority" {
		authTier = 2
	} else if t != "" {
		authTier = 1
	}

	return RequestFeatures{
		EndpointClass: class,
		MethodClass:   method,
		Log1pPayload:  math.Log1p(float64(payloadBytes)),
		AuthTier:      authTier,
	}
}

// BuildVector builds the ContextVector for a (request, node) pair.
// Phase 0 stub: fills one-hot fields, leaves system fields at zero.
func (e *Extractor) BuildVector(req RequestFeatures, node NodeMetrics) ContextVector {
	var v ContextVector

	// One-hot: endpoint class
	if int(req.EndpointClass) < numClasses {
		v[int(req.EndpointClass)] = 1.0
	}

	// One-hot: method class (offset = numClasses = 6)
	if int(req.MethodClass) < numMethods {
		v[numClasses+int(req.MethodClass)] = 1.0
	}

	// Scalar request features
	v[11] = req.Log1pPayload
	v[12] = float64(req.AuthTier)

	// System features (Phase 2.3 will normalise these via Normaliser)
	v[13] = node.CPUUtil
	v[14] = float64(node.InFlight)
	v[15] = float64(node.QueueDepth)

	return v
}

// classifyRoute returns the EndpointClass for a URL path using the route table.
// Falls back to ClassUnknown if no prefix matches. O(len(routeTable)) worst-case
// but table is small (< 20 entries); a trie is a Phase 2.3 optimisation.
func (e *Extractor) classifyRoute(path string) EndpointClass {
	// Direct match first
	if c, ok := e.routeTable[path]; ok {
		return c
	}
	// Prefix match — longest wins
	best := ClassUnknown
	bestLen := 0
	for prefix, class := range e.routeTable {
		if len(prefix) > bestLen && len(path) >= len(prefix) && path[:len(prefix)] == prefix {
			best = class
			bestLen = len(prefix)
		}
	}
	return best
}

// classifyMethod maps an HTTP method string to a MethodClass.
func classifyMethod(m string) MethodClass {
	switch m {
	case http.MethodGet:
		return MethodGET
	case http.MethodPost:
		return MethodPOST
	case http.MethodPut:
		return MethodPUT
	case http.MethodDelete:
		return MethodDELETE
	default:
		return MethodOther
	}
}
