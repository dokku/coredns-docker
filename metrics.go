package docker

import (
	"github.com/coredns/coredns/plugin"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// requestSuccessCount is the number of DNS requests handled succesfully.
	requestSuccessCount = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: plugin.Namespace,
		Subsystem: pluginName,
		Name:      "success_requests_total",
		Help:      "Counter of DNS requests handled successfully.",
	}, []string{"server"})
	// requestFailedCount is the number of DNS requests that failed.
	requestFailedCount = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: plugin.Namespace,
		Subsystem: pluginName,
		Name:      "failed_requests_total",
		Help:      "Counter of DNS requests failed.",
	}, []string{"server"})
	// lastSyncTimestamp is the Unix timestamp of the last successful record sync from Docker.
	lastSyncTimestamp = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: plugin.Namespace,
		Subsystem: pluginName,
		Name:      "last_sync_timestamp_seconds",
		Help:      "Unix timestamp of the last successful record sync from Docker.",
	})
	// requestDuration is the histogram of DNS request durations.
	requestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: plugin.Namespace,
		Subsystem: pluginName,
		Name:      "request_duration_seconds",
		Help:      "Histogram of DNS request durations in seconds.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"server", "type"})
	// requestStaleCount is the number of DNS requests served from stale data.
	requestStaleCount = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: plugin.Namespace,
		Subsystem: pluginName,
		Name:      "stale_requests_total",
		Help:      "Counter of DNS requests served from stale data during Docker daemon disconnect.",
	}, []string{"server"})
	// requestFallthroughCount is the number of DNS requests passed to the next plugin via fallthrough.
	requestFallthroughCount = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: plugin.Namespace,
		Subsystem: pluginName,
		Name:      "fallthrough_requests_total",
		Help:      "Counter of DNS requests passed to the next plugin via fallthrough.",
	}, []string{"server"})
	// recordsCount is the number of A/AAAA DNS record names currently tracked.
	recordsCount = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: plugin.Namespace,
		Subsystem: pluginName,
		Name:      "records_total",
		Help:      "Number of A/AAAA DNS record names currently tracked.",
	})
	// srvRecordsCount is the number of SRV DNS record names currently tracked.
	srvRecordsCount = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: plugin.Namespace,
		Subsystem: pluginName,
		Name:      "srv_records_total",
		Help:      "Number of SRV DNS record names currently tracked.",
	})
	// ptrRecordsCount is the number of PTR DNS record names currently tracked.
	ptrRecordsCount = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: plugin.Namespace,
		Subsystem: pluginName,
		Name:      "ptr_records_total",
		Help:      "Number of PTR DNS record names currently tracked.",
	})
	// cnameRecordsCount is the number of CNAME DNS record names currently tracked.
	cnameRecordsCount = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: plugin.Namespace,
		Subsystem: pluginName,
		Name:      "cname_records_total",
		Help:      "Number of CNAME DNS record names currently tracked.",
	})
	// connectedGauge indicates whether the plugin is connected to the Docker daemon.
	connectedGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: plugin.Namespace,
		Subsystem: pluginName,
		Name:      "connected",
		Help:      "Whether the plugin is connected to the Docker daemon (1 = connected, 0 = disconnected).",
	})
	// containersCount is the number of Docker containers currently tracked.
	containersCount = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: plugin.Namespace,
		Subsystem: pluginName,
		Name:      "containers_total",
		Help:      "Number of Docker containers currently tracked.",
	})
	// syncDuration is the histogram of record sync durations.
	syncDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: plugin.Namespace,
		Subsystem: pluginName,
		Name:      "sync_duration_seconds",
		Help:      "Histogram of record sync durations in seconds.",
		Buckets:   prometheus.DefBuckets,
	})
	// syncErrorCount is the number of failed record sync attempts.
	syncErrorCount = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: plugin.Namespace,
		Subsystem: pluginName,
		Name:      "sync_errors_total",
		Help:      "Counter of failed record sync attempts.",
	})
)
