package metricstore

import (
	"sync"

	v2 "github.com/komari-monitor/komari/protocol/v2"
)

// reportUptimeState tracks the last reported host uptime independently from
// the cumulative traffic baseline. Uptime regression gives us an additional
// signal that a large cumulative-counter jump may be a new counter domain
// rather than real traffic.
type reportUptimeState struct {
	mu sync.Mutex

	initialized bool
	uptime      int64
}

var reportUptimeStates sync.Map

// suppressTrafficOnUptimeRegression rejects only implausibly large traffic
// deltas at a host-restart boundary. Small deltas are preserved because some
// counter sources survive a reboot and can legitimately continue increasing.
// A reboot combined with a restored or changed counter domain can instead make
// the new cumulative value numerically much larger than the previous sample;
// without this guard that discontinuity becomes a TB/PB-scale history spike.
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

	if !restarted {
		return trafficUp, trafficDown
	}
	if trafficUp > maxResetAwareDelta {
		trafficUp = 0
	}
	if trafficDown > maxResetAwareDelta {
		trafficDown = 0
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
