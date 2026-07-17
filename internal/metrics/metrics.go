// Package metrics exposes tidal-syncer runtime and library statistics in the
// Prometheus text exposition format for scraping into Prometheus and Grafana.
//
// It has two parts: per-cycle runtime metrics recorded by the daemon (cycle
// count, failures by class, duration, last success), and a library collector
// (collector.go) that reports the current cache composition — track counts by
// status and quality, genres, favorites, distinct albums/artists — computed
// from the store at scrape time.
package metrics

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// namespace prefixes every metric this package exports.
const namespace = "tidal_syncer"

// Error classes recorded on the sync_errors_total counter. They mirror the
// daemon's per-cycle error classification.
const (
	// ClassLock is a cycle skipped because another sync held the data lock.
	ClassLock = "lock"
	// ClassReauth is a cycle that needs an operator re-login.
	ClassReauth = "reauth"
	// ClassDeadCredentials is a cycle blocked by invalid client credentials.
	ClassDeadCredentials = "dead_credentials"
	// ClassTransient is any other, retryable per-cycle failure.
	ClassTransient = "transient"
)

// durationBucketBase/Factor/Count define the cycle-duration histogram: 12
// exponential buckets from 1s to ~34m, which spans a fast no-op cycle to a large
// first sync.
const (
	durationBucketBase   = 1.0
	durationBucketFactor = 2.0
	durationBucketCount  = 12
)

// Metrics owns the Prometheus registry and the runtime (daemon-cycle) metrics.
// Its record methods are nil-safe, so a caller may hold a nil *Metrics when the
// metrics endpoint is disabled and record unconditionally.
type Metrics struct {
	reg         *prometheus.Registry
	cycles      prometheus.Counter
	errors      *prometheus.CounterVec
	duration    prometheus.Histogram
	lastSuccess prometheus.Gauge
}

// New builds a Metrics with a private registry and registers the runtime cycle
// metrics, a build-info gauge, the Go and process collectors, and the injected
// library collector. version and commit label the build_info metric.
func New(version, commit string, library prometheus.Collector) *Metrics {
	m := &Metrics{
		reg: prometheus.NewRegistry(),
		cycles: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "sync_cycles_total",
			Help:      "Total number of daemon sync cycles executed.",
		}),
		errors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "sync_errors_total",
			Help:      "Total number of failed daemon sync cycles, by recovery class.",
		}, []string{"class"}),
		duration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "sync_duration_seconds",
			Help:      "Wall-clock duration of a daemon sync cycle in seconds.",
			Buckets:   prometheus.ExponentialBuckets(durationBucketBase, durationBucketFactor, durationBucketCount),
		}),
		lastSuccess: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "last_cycle_success_timestamp_seconds",
			Help:      "Unix timestamp of the last successful daemon sync cycle.",
		}),
	}

	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "build_info",
		Help:      "Build metadata; constant 1 labeled with version and commit.",
	}, []string{"version", "commit"})
	buildInfo.WithLabelValues(version, commit).Set(1)

	m.reg.MustRegister(
		m.cycles,
		m.errors,
		m.duration,
		m.lastSuccess,
		buildInfo,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		library,
	)

	// Initialise the known error classes to 0 so they are present before the
	// first failure and rate() has a baseline.
	for _, class := range []string{ClassLock, ClassReauth, ClassDeadCredentials, ClassTransient} {
		m.errors.WithLabelValues(class)
	}

	return m
}

// RecordCycle records one daemon cycle: its duration and outcome. An empty class
// marks success and stamps the last-success gauge; a non-empty class increments
// the matching sync_errors_total series. Nil-safe.
func (m *Metrics) RecordCycle(dur time.Duration, class string) {
	if m == nil {
		return
	}
	m.cycles.Inc()
	m.duration.Observe(dur.Seconds())
	if class == "" {
		m.lastSuccess.SetToCurrentTime()

		return
	}
	m.errors.WithLabelValues(class).Inc()
}

// Handler returns the HTTP handler serving this registry in the Prometheus text
// exposition format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}
