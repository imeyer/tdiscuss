package main

import (
	"net/http"
)

// ServeStatic serves static files
func (s *DiscussService) ServeStatic(w http.ResponseWriter, r *http.Request) {
	// TODO: Implement static file serving
	http.StripPrefix("/static/", http.FileServer(http.Dir("./static/"))).ServeHTTP(w, r)
}

// MetricsHandler serves Prometheus metrics
func (s *DiscussService) MetricsHandler(w http.ResponseWriter, r *http.Request) {
	// This would typically be handled by promhttp.Handler()
	// For now, return a placeholder
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("# Metrics endpoint\n# TODO: Implement Prometheus metrics\n"))
}
