package metricstore

import (
	"context"
	"testing"
	"time"

	v2 "github.com/komari-monitor/komari/protocol/v2"
)

func TestReportStoresSystemCapacityMetrics(t *testing.T) {
	ctx := context.Background()
	s := useReportTestStore(t, nil)
	ts := time.Now().UTC().Truncate(time.Second)

	report := v2.Report{
		UUID:      "capacity-node",
		UpdatedAt: ts,
		Ram:       v2.RamReport{Used: 512, Total: 4096},
		Swap:      v2.RamReport{Used: 128, Total: 1024},
		Disk:      v2.DiskReport{Used: 2048, Total: 8192},
	}
	if _, err := WriteReport(ctx, report); err != nil {
		t.Fatalf("write report: %v", err)
	}

	start := ts.Add(-time.Second)
	end := ts.Add(time.Second)
	assertMetricValues(t, s, MetricRAMTotal, report.UUID, start, end, []float64{4096})
	assertMetricValues(t, s, MetricSwapTotal, report.UUID, start, end, []float64{1024})
	assertMetricValues(t, s, MetricDiskTotal, report.UUID, start, end, []float64{8192})
}

func TestReportSuppressesTrafficSpikeWhenUptimeMovesBackwards(t *testing.T) {
	ctx := context.Background()
	s := useReportTestStore(t, nil)
	base := time.Now().UTC().Truncate(time.Minute).Add(5 * time.Second)

	report := v2.Report{
		UUID:      "restart-spike-node",
		UpdatedAt: base,
		Uptime:    1000,
		Network:   v2.NetworkReport{TotalUp: 100, TotalDown: 200},
	}
	if _, err := WriteReport(ctx, report); err != nil {
		t.Fatalf("write initial report: %v", err)
	}

	report.UpdatedAt = base.Add(3 * time.Second)
	report.Uptime = 1003
	report.Network.TotalUp = 150
	report.Network.TotalDown = 260
	if _, err := WriteReport(ctx, report); err != nil {
		t.Fatalf("write continuous report: %v", err)
	}

	// Simulate a reboot/counter-domain change where the new cumulative value is
	// still larger than the old one. Without the uptime guard this would be
	// recorded as a multi-terabyte traffic delta even though it is only a new
	// baseline.
	report.UpdatedAt = base.Add(6 * time.Second)
	report.Uptime = 2
	report.Network.TotalUp = 2 << 40
	report.Network.TotalDown = 3 << 40
	if _, err := WriteReport(ctx, report); err != nil {
		t.Fatalf("write reboot boundary report: %v", err)
	}

	report.UpdatedAt = base.Add(9 * time.Second)
	report.Uptime = 5
	report.Network.TotalUp += 25
	report.Network.TotalDown += 35
	if _, err := WriteReport(ctx, report); err != nil {
		t.Fatalf("write post-reboot report: %v", err)
	}

	start := base.Add(-time.Second)
	end := base.Add(time.Minute)
	assertMetricValues(t, s, MetricTrafficUp, report.UUID, start, end, []float64{0, 50, 0, 25})
	assertMetricValues(t, s, MetricTrafficDown, report.UUID, start, end, []float64{0, 60, 0, 35})
	assertMetricValues(t, s, MetricNetTotalUp, report.UUID, start, end, []float64{100, 150, float64(2 << 40), float64((2 << 40) + 25)})
}
