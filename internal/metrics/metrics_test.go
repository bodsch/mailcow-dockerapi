package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestObserveHTTP(t *testing.T) {
	m := New(prometheus.NewRegistry(), Build{Version: "test"})

	m.ObserveHTTP("GET /host/stats", 0.01)
	m.ObserveHTTP("GET /host/stats", 0.02)

	if got := testutil.ToFloat64(m.httpRequests.WithLabelValues("GET /host/stats")); got != 2 {
		t.Errorf("request count = %v, want 2", got)
	}
}

func TestObserveAction(t *testing.T) {
	m := New(prometheus.NewRegistry(), Build{Version: "test"})

	m.ObserveAction("container_post__exec__mailq__flush", SourcePubSub, 1.5)
	m.ObserveAction("container_post__restart", SourceHTTP, 0.4)

	if got := testutil.ToFloat64(m.actions.WithLabelValues("container_post__restart", SourceHTTP)); got != 1 {
		t.Errorf("action count = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.actions.WithLabelValues("container_post__exec__mailq__flush", SourcePubSub)); got != 1 {
		t.Errorf("action count = %v, want 1", got)
	}
}

// A rising unknown_call is the signal that the frontend expects calls this build
// does not implement, so it has its own counter rather than only a log line.
func TestObserveRejected(t *testing.T) {
	m := New(prometheus.NewRegistry(), Build{Version: "test"})

	m.ObserveRejected(ReasonUnknownCall, SourceHTTP)
	m.ObserveRejected(ReasonUnknownCall, SourceHTTP)
	m.ObserveRejected(ReasonNoTarget, SourcePubSub)

	if got := testutil.ToFloat64(m.actionRejected.WithLabelValues(ReasonUnknownCall, SourceHTTP)); got != 2 {
		t.Errorf("unknown_call count = %v, want 2", got)
	}
	if got := testutil.ToFloat64(m.actionRejected.WithLabelValues(ReasonNoTarget, SourcePubSub)); got != 1 {
		t.Errorf("no_target count = %v, want 1", got)
	}
}

func TestObservePubSub(t *testing.T) {
	m := New(prometheus.NewRegistry(), Build{Version: "test"})

	m.ObservePubSub(PubSubHandled)
	m.ObservePubSub(PubSubMalformed)
	m.ObservePubSubReconnect()

	if got := testutil.ToFloat64(m.pubsubMessages.WithLabelValues(PubSubHandled)); got != 1 {
		t.Errorf("handled count = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.pubsubMessages.WithLabelValues(PubSubMalformed)); got != 1 {
		t.Errorf("malformed count = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.pubsubReconnects); got != 1 {
		t.Errorf("reconnect count = %v, want 1", got)
	}
}

func TestObserveStats(t *testing.T) {
	m := New(prometheus.NewRegistry(), Build{Version: "test"})

	m.ObserveStats("host", "hit")
	m.ObserveStats("container", "miss")

	if got := testutil.ToFloat64(m.statsRequests.WithLabelValues("host", "hit")); got != 1 {
		t.Errorf("host hit count = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.statsRequests.WithLabelValues("container", "miss")); got != 1 {
		t.Errorf("container miss count = %v, want 1", got)
	}
}

func TestBuildInfo(t *testing.T) {
	reg := prometheus.NewRegistry()
	New(reg, Build{Version: "1.2.3", Date: "2026-08-17"})

	want := map[string]string{"version": "1.2.3", "build_date": "2026-08-17"}

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, family := range families {
		if family.GetName() != "mailcow_dockerapi_build_info" {
			continue
		}
		for _, metric := range family.GetMetric() {
			got := map[string]string{}
			for _, label := range metric.GetLabel() {
				got[label.GetName()] = label.GetValue()
			}
			for name, value := range want {
				if got[name] != value {
					t.Errorf("build_info label %s = %q, want %q", name, got[name], value)
				}
			}
			return
		}
	}
	t.Error("build_info was not exposed at all")
}

// Instrumentation is optional: the packages that record metrics are also used in
// tests that do not build a registry, so every method has to tolerate a nil
// receiver.
func TestNilMetricsAreSafe(t *testing.T) {
	var m *Metrics

	m.ObserveHTTP("GET /host/stats", 0.1)
	m.ObserveAction("container_post__stop", SourceHTTP, 0.1)
	m.ObserveRejected(ReasonMalformed, SourceHTTP)
	m.ObservePubSub(PubSubUnknown)
	m.ObservePubSubReconnect()
	m.ObserveStats("host", "timeout")
}
