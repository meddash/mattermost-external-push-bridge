package main

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
)

type metrics struct {
	hookReceived      atomic.Int64
	hookEnqueued      atomic.Int64
	hookDeduplicated  atomic.Int64
	delivered         atomic.Int64
	retries           atomic.Int64
	permanentFailures atomic.Int64
	queueDropped      atomic.Int64
	requestDurationMS atomic.Int64
	requestCount      atomic.Int64
}

func (m *metrics) render(queueDepth int) string {
	var b strings.Builder
	lines := []string{
		"# TYPE external_push_bridge_hook_received_total counter",
		fmt.Sprintf("external_push_bridge_hook_received_total %d", m.hookReceived.Load()),
		"# TYPE external_push_bridge_hook_enqueued_total counter",
		fmt.Sprintf("external_push_bridge_hook_enqueued_total %d", m.hookEnqueued.Load()),
		"# TYPE external_push_bridge_hook_deduplicated_total counter",
		fmt.Sprintf("external_push_bridge_hook_deduplicated_total %d", m.hookDeduplicated.Load()),
		"# TYPE external_push_bridge_delivered_total counter",
		fmt.Sprintf("external_push_bridge_delivered_total %d", m.delivered.Load()),
		"# TYPE external_push_bridge_retries_total counter",
		fmt.Sprintf("external_push_bridge_retries_total %d", m.retries.Load()),
		"# TYPE external_push_bridge_permanent_failures_total counter",
		fmt.Sprintf("external_push_bridge_permanent_failures_total %d", m.permanentFailures.Load()),
		"# TYPE external_push_bridge_queue_dropped_total counter",
		fmt.Sprintf("external_push_bridge_queue_dropped_total %d", m.queueDropped.Load()),
		"# TYPE external_push_bridge_queue_depth gauge",
		fmt.Sprintf("external_push_bridge_queue_depth %d", queueDepth),
	}
	for _, line := range lines {
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

func writeTextResponse(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}
