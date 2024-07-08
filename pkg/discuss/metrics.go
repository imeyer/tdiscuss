package discuss

import "github.com/prometheus/client_golang/prometheus"

var (
	getMemberIDQueryDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "tdiscuss_queries",
		Name:      "get_member_id_duration_seconds",
		Help:      "Histogram of the time it takes to execute GetMemberId.",
		Buckets:   prometheus.DefBuckets,
	},
		[]string{"function"},
	)

	listThreadsQueryDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "tdiscuss_queries",
		Name:      "list_threads_duration_seconds",
		Help:      "Histogram of the time it takes to execute ListThreads.",
		Buckets:   prometheus.DefBuckets,
	},
		[]string{"function"},
	)

	listThreadPostsQueryDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "tdiscuss_queries",
		Name:      "list_thread_posts_duration_seconds",
		Help:      "Histogram of the time it takes to execute ListThreadPosts.",
		Buckets:   prometheus.DefBuckets,
	},
		[]string{"function"},
	)
)

func init() {
	prometheus.MustRegister(getMemberIDQueryDuration)
	prometheus.MustRegister(listThreadsQueryDuration)
	prometheus.MustRegister(listThreadPostsQueryDuration)
}
