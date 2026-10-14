// Package features_test — unit tests for the feature extractor.
// Phase 0: route classification, method classification, context vector shape.
package features_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/synapse/dataplane/internal/features"
)

func TestRouteClassification(t *testing.T) {
	routes := map[string]features.EndpointClass{
		"/health":  features.ClassHealth,
		"/crud/":   features.ClassLightCRUD,
		"/query":   features.ClassMediumQuery,
		"/compute": features.ClassHeavyCompute,
		"/io":      features.ClassIOBound,
	}
	ex := features.NewExtractor(routes)

	cases := []struct {
		path  string
		want  features.EndpointClass
	}{
		{"/health", features.ClassHealth},
		{"/crud/users", features.ClassLightCRUD},
		{"/query", features.ClassMediumQuery},
		{"/compute", features.ClassHeavyCompute},
		{"/io", features.ClassIOBound},
		{"/unknown", features.ClassUnknown},
	}

	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		got := ex.ExtractRequest(req)
		if got.EndpointClass != tc.want {
			t.Errorf("path=%q: got class %d, want %d", tc.path, got.EndpointClass, tc.want)
		}
	}
}

func TestContextVectorDimension(t *testing.T) {
	ex := features.NewExtractor(nil)
	req := httptest.NewRequest(http.MethodPost, "/compute", nil)
	req.ContentLength = 1024
	rf := ex.ExtractRequest(req)
	var nm features.NodeMetrics
	v := ex.BuildVector(rf, nm)
	if len(v) != 16 {
		t.Errorf("expected d=16, got %d", len(v))
	}
}

func TestLog1pPayload(t *testing.T) {
	ex := features.NewExtractor(nil)
	req := httptest.NewRequest(http.MethodPost, "/query", nil)
	req.ContentLength = 0
	rf := ex.ExtractRequest(req)
	if rf.Log1pPayload != 0.0 {
		t.Errorf("expected log1p(0)=0, got %f", rf.Log1pPayload)
	}
}
