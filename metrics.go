package main

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	versionGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tdiscuss_build_info",
		Help: "A gauge with version and git commit information",
	}, []string{"version", "git_commit", "hostname"})

	getMemberIDQueryDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "tdiscuss_queries",
			Name:      "get_member_id_duration_seconds",
			Help:      "Histogram of the time it takes to execute GetMemberId.",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"function"},
	)

	listThreadsQueryDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "tdiscuss_queries",
			Name:      "list_threads_duration_seconds",
			Help:      "Histogram of the time it takes to execute ListThreads.",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"function"},
	)

	listThreadPostsQueryDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "tdiscuss_queries",
			Name:      "list_thread_posts_duration_seconds",
			Help:      "Histogram of the time it takes to execute ListThreadPosts.",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"function"},
	)

	httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "tdiscuss",
			Name:      "http_request_duration_seconds",
			Help:      "Histogram of response latency (seconds) for HTTP requests.",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"path", "method", "code"},
	)
)

func init() {
	prometheus.MustRegister(getMemberIDQueryDuration)
	prometheus.MustRegister(listThreadsQueryDuration)
	prometheus.MustRegister(listThreadPostsQueryDuration)
	prometheus.MustRegister(httpRequestDuration)
	prometheus.MustRegister(versionGauge)
}

func HistogramHttpHandler(next http.Handler) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Create a ResponseWriter that captures the status code
		rw := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}

		re := regexp.MustCompile(`/(\d+)`)

		sanitizedPath := re.ReplaceAllString(r.URL.Path, "/:id")

		duration := time.Since(start).Seconds()

		httpRequestDuration.WithLabelValues(sanitizedPath, r.Method, strconv.Itoa(rw.statusCode)).Observe(duration)

		next.ServeHTTP(rw, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	statusCode int
	written    int64
}

func (rec *statusRecorder) WriteHeader(code int) {
	if rec.statusCode == 0 {
		rec.statusCode = code
		rec.ResponseWriter.WriteHeader(code)
	}
}

func (rec *statusRecorder) Write(b []byte) (int, error) {
	if rec.statusCode == 0 {
		rec.statusCode = http.StatusOK
	}
	n, err := rec.ResponseWriter.Write(b)
	rec.written += int64(n)
	return n, err
}

func (rec *statusRecorder) Flush() {
	if f, ok := rec.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (rec *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := rec.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not implement http.Hijacker")
}
