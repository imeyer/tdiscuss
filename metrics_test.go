package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestHistogramHttpHandler(t *testing.T) {
	// Create a dummy handler to pass to HistogramHttpHandler
	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond) // Simulate some processing time
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	handler := HistogramHttpHandler(dummyHandler)

	// Create a new HTTP request
	req, err := http.NewRequest("GET", "/test/123", nil)
	if err != nil {
		t.Fatalf("Could not create request: %v", err)
	}

	// Create a ResponseRecorder to record the response
	rr := httptest.NewRecorder()

	// Call the handler
	handler.ServeHTTP(rr, req)

	// Check the status code
	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	// Check the response body
	expected := "OK"
	if rr.Body.String() != expected {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}

	const metadata = `
		# HELP tdiscuss_http_request_duration_seconds Histogram of response latency (seconds) for HTTP requests.
		# TYPE tdiscuss_http_request_duration_seconds histogram
	`

	// Check if the metric was recorded
	_ = `
		tdiscuss_http_request_duration_seconds{code="200",method="GET",path="/test/:id",le="0.005"} 0
		tdiscuss_http_request_duration_seconds_bucket{code="200",method="GET",path="/test/:id",le="+Inf"} 0
		tdiscuss_http_request_duration_seconds_sum{code="200",method="GET",path="/test/:id"} 0
		tdiscuss_http_request_duration_seconds_count{code="200",method="GET",path="/test/:id"} 0
	`
	if count := testutil.CollectAndCount(httpRequestDuration); count == 0 {
		t.Errorf("Expected metric to be recorded, but it was not")
	}

	// Check if the metric value is within expected range
	// if err := testutil.CollectAndCompare(httpRequestDuration, strings.NewReader(metadata+metric), "tdiscuss_http_request_duration_seconds"); err != nil {
	// 	t.Errorf("Unexpected metric value: %v", err)
	// }
}
