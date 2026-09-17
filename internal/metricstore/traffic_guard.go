package metricstore

import (
	"sync"

	v2 "github.com/komari-monitor/komari/protocol/v2"
)

// reportUptimeState tracks the last reported host uptime independently from
// the cumulative traffic baseline. The v1 implementation used uptime to avoid
// attributing a reboot boundary to the current sampling interval; that guard
// was lost during the v2-only protocol migration.
type reportUptimeState struct {
	mu sync.Mutex

	initialized bool
	uptime      int64
}

var reportUptimeStates sync.Map

// suppressTrafficOnUptimeRegression rebases traffic deltas when host uptime
// moves backwards. A reboot can coincide with counters being restored from a
// persistent source or changing to a different counter domain, so the raw
// cumulative value may even be larger than the previous sample. Treating that
// boundary as traffic can create TB/PB-scale spikes.
func suppressTrafficOnUptimeRegression(report v2.Report, trafficUp, trafficDown int64) (int64, int64) {
	if report.UUID == "" || report.Uptime < 0 {
		return trafficUp, trafficDown
	}

	stateValue, _ := reportUptimeStates.LoadOrStore(report.UUID, &reportUptimeState{})
	state := stateValue.(*reportUptimeState)
	state.mu.Lock()
	restarted := state.initialized && report.Uptime < state.uptime
	state.initialized = true
	state.uptime = report.Uptime
	state.mu.Unlock()

	if restarted {
		return 0, 0
	}
	return trafficUp, trafficDown
}

func deleteReportUptimeState(entityID string) {
	reportUptimeStates.Delete(entityID)
}

func clearReportUptimeStates() {
	reportUptimeStates.Range(func(key, _ any) bool {
		reportUptimeStates.Delete(key)
		return true
	})
}
