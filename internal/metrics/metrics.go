// Package metrics exposes the dockerapi's own state to Prometheus.
//
// The Python implementation published nothing: an operator could see individual
// log lines but not how often an action ran, how long it took, or whether the
// mailcow frontend was asking for calls this service does not implement. The log
// output is unchanged; these metrics are in addition to it.
//
// What is deliberately not measured here is the outcome of an action as the
// frontend sees it. That verdict lives in the "type" field of the response body,
// which every action builds itself, so counting it would mean parsing responses
// back. Failures that keep an action from running at all — an unknown call, an
// invalid target, a malformed request — are counted, because those are the ones
// that indicate a broken deployment rather than a broken mailbox.
package metrics

import "github.com/prometheus/client_golang/prometheus"

// namespace prefixes every metric.
const namespace = "mailcow_dockerapi"

// Sources an action can arrive through.
const (
	SourceHTTP   = "http"
	SourcePubSub = "pubsub"
)

// Reasons an action never ran.
const (
	ReasonUnknownCall = "unknown_call"
	ReasonNoTarget    = "no_target"
	ReasonMalformed   = "malformed"
)

// Outcomes of a PubSub message.
const (
	PubSubHandled   = "handled"
	PubSubMalformed = "malformed"
	PubSubUnknown   = "unknown"
)

// Metrics holds the collectors. Construct it with New and register it once.
type Metrics struct {
	httpRequests *prometheus.CounterVec
	httpDuration *prometheus.HistogramVec

	actions        *prometheus.CounterVec
	actionDuration *prometheus.HistogramVec
	actionRejected *prometheus.CounterVec

	pubsubMessages   *prometheus.CounterVec
	pubsubReconnects prometheus.Counter

	statsRequests *prometheus.CounterVec

	info *prometheus.GaugeVec
}

// New builds the collectors and registers them with reg.
func New(reg prometheus.Registerer, version string) *Metrics {
	m := &Metrics{
		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "http_requests_total",
			Help:      "HTTP requests by route pattern.",
		}, []string{"route"}),

		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "http_request_duration_seconds",
			Help:      "HTTP request latency by route pattern.",
			// The upper buckets matter: a mailq flush or an fts_rescan runs for
			// minutes, and the frontend's own timeout is what operators notice.
			Buckets: []float64{0.005, 0.025, 0.1, 0.5, 1, 2.5, 5, 15, 60, 300},
		}, []string{"route"}),

		actions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "actions_total",
			Help:      "Container actions executed, by registry name and the channel they arrived through.",
		}, []string{"action", "source"}),

		actionDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "action_duration_seconds",
			Help:      "Container action runtime by registry name.",
			Buckets:   []float64{0.005, 0.025, 0.1, 0.5, 1, 2.5, 5, 15, 60, 300},
		}, []string{"action"}),

		actionRejected: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "actions_rejected_total",
			Help:      "Requests that never reached an action, by reason. A rising unknown_call means the frontend is asking for calls this build does not implement.",
		}, []string{"reason", "source"}),

		pubsubMessages: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "pubsub_messages_total",
			Help:      "Messages received on the mailcow channel, by outcome.",
		}, []string{"result"}),

		pubsubReconnects: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "pubsub_reconnects_total",
			Help:      "Times the subscriber had to re-establish its subscription.",
		}),

		statsRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "stats_requests_total",
			Help:      "Statistics served, by kind and whether the cache had them.",
		}, []string{"kind", "result"}),

		info: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "build_info",
			Help:      "Build information, always 1.",
		}, []string{"version"}),
	}

	reg.MustRegister(
		m.httpRequests, m.httpDuration,
		m.actions, m.actionDuration, m.actionRejected,
		m.pubsubMessages, m.pubsubReconnects,
		m.statsRequests,
		m.info,
	)

	m.info.WithLabelValues(version).Set(1)
	return m
}

// ObserveHTTP records one served request. The route is the mux pattern, not the
// path, so container ids do not become label values.
func (m *Metrics) ObserveHTTP(route string, seconds float64) {
	if m == nil {
		return
	}
	m.httpRequests.WithLabelValues(route).Inc()
	m.httpDuration.WithLabelValues(route).Observe(seconds)
}

// ObserveAction records one executed action.
func (m *Metrics) ObserveAction(action, source string, seconds float64) {
	if m == nil {
		return
	}
	m.actions.WithLabelValues(action, source).Inc()
	m.actionDuration.WithLabelValues(action).Observe(seconds)
}

// ObserveRejected records a request that never reached an action.
func (m *Metrics) ObserveRejected(reason, source string) {
	if m == nil {
		return
	}
	m.actionRejected.WithLabelValues(reason, source).Inc()
}

// ObservePubSub records one message from the mailcow channel.
func (m *Metrics) ObservePubSub(result string) {
	if m == nil {
		return
	}
	m.pubsubMessages.WithLabelValues(result).Inc()
}

// ObservePubSubReconnect records a re-established subscription.
func (m *Metrics) ObservePubSubReconnect() {
	if m == nil {
		return
	}
	m.pubsubReconnects.Inc()
}

// ObserveStats records a statistics request. Kinds are "host" and "container",
// results are "hit", "miss" and "timeout".
func (m *Metrics) ObserveStats(kind, result string) {
	if m == nil {
		return
	}
	m.statsRequests.WithLabelValues(kind, result).Inc()
}
