package metricstore

import (
	"context"
	"fmt"
	"time"

	"github.com/komari-monitor/komari/pkg/metric"
)

const (
	// One TiB in a single agent report is intentionally conservative. The known
	// restart regression produced TB/PB-scale deltas, while legitimate traffic
	// accumulated inside a historical rollup must not be judged by its sum.
	DefaultTrafficAnomalyThresholdBytes = float64(1 << 40)
	MinimumTrafficAnomalyThresholdBytes = float64(64 << 30)
	DefaultTrafficAnomalyPreviewLimit   = 100
	MaxTrafficAnomalyPreviewLimit       = 500
	MaxTrafficAnomalyCleanupBuckets     = 10000
	trafficHistoryStableDelay           = 15 * time.Minute
)

// TrafficHistoryMaintenanceReport is returned by both preview and cleanup.
type TrafficHistoryMaintenanceReport struct {
	ThresholdBytes     float64                `json:"threshold_bytes"`
	SafeBefore         time.Time              `json:"safe_before"`
	MatchingBuckets    int64                  `json:"matching_buckets"`
	AffectedEntities   []string               `json:"affected_entities"`
	Preview            []metric.RollupOutlier `json:"preview"`
	Truncated          bool                   `json:"truncated"`
	DeletedBuckets     int64                  `json:"deleted_buckets,omitempty"`
	RemainingBuckets   int64                  `json:"remaining_buckets,omitempty"`
	CleanupLimit       int                    `json:"cleanup_limit"`
}

func trafficHistorySafeBefore(now time.Time) time.Time {
	return now.UTC().Add(-trafficHistoryStableDelay).Truncate(time.Minute)
}

func normalizeTrafficAnomalyThreshold(value float64) (float64, error) {
	if value == 0 {
		return DefaultTrafficAnomalyThresholdBytes, nil
	}
	if value < MinimumTrafficAnomalyThresholdBytes {
		return 0, fmt.Errorf("traffic anomaly threshold must be at least %.0f bytes (64 GiB)", MinimumTrafficAnomalyThresholdBytes)
	}
	return value, nil
}

func normalizeTrafficAnomalyPreviewLimit(limit int) int {
	if limit <= 0 {
		return DefaultTrafficAnomalyPreviewLimit
	}
	if limit > MaxTrafficAnomalyPreviewLimit {
		return MaxTrafficAnomalyPreviewLimit
	}
	return limit
}

func normalizeTrafficHistoryBefore(requested, now time.Time) (time.Time, error) {
	safe := trafficHistorySafeBefore(now)
	if requested.IsZero() {
		return safe, nil
	}
	requested = requested.UTC()
	if requested.After(safe) {
		return time.Time{}, fmt.Errorf("requested cleanup boundary %s overlaps mutable metric history; latest safe boundary is %s", requested.Format(time.RFC3339), safe.Format(time.RFC3339))
	}
	return requested, nil
}

func trafficOutlierQuery(threshold float64, before time.Time, previewLimit int) metric.RollupOutlierQuery {
	return metric.RollupOutlierQuery{
		MetricNames:  []string{MetricTrafficUp, MetricTrafficDown},
		MinValue:     threshold,
		Before:       before,
		PreviewLimit: previewLimit,
	}
}

// ScanTrafficHistoryAnomalies performs a read-only dry-run over persisted
// traffic rollups. It never removes data.
func ScanTrafficHistoryAnomalies(ctx context.Context, threshold float64, before time.Time, previewLimit int) (TrafficHistoryMaintenanceReport, error) {
	threshold, err := normalizeTrafficAnomalyThreshold(threshold)
	if err != nil {
		return TrafficHistoryMaintenanceReport{}, err
	}
	before, err = normalizeTrafficHistoryBefore(before, time.Now().UTC())
	if err != nil {
		return TrafficHistoryMaintenanceReport{}, err
	}
	previewLimit = normalizeTrafficAnomalyPreviewLimit(previewLimit)

	if err := storeOperations.Acquire(ctx); err != nil {
		return TrafficHistoryMaintenanceReport{}, err
	}
	defer storeOperations.Release()
	store := GetStore()
	if store == nil {
		return TrafficHistoryMaintenanceReport{}, fmt.Errorf("metric store not initialized")
	}

	scan, err := store.FindRollupOutliers(ctx, trafficOutlierQuery(threshold, before, previewLimit))
	if err != nil {
		return TrafficHistoryMaintenanceReport{}, err
	}
	return TrafficHistoryMaintenanceReport{
		ThresholdBytes:   threshold,
		SafeBefore:       before,
		MatchingBuckets:  scan.TotalMatches,
		AffectedEntities: scan.AffectedEntities,
		Preview:          scan.Preview,
		Truncated:        scan.Truncated,
		CleanupLimit:     MaxTrafficAnomalyCleanupBuckets,
	}, nil
}

// CleanupTrafficHistoryAnomalies removes only persisted traffic rollup buckets
// that match an already previewed threshold and safety boundary. expectedMatches
// makes the operation fail closed if the data changed between preview and
// confirmation.
func CleanupTrafficHistoryAnomalies(ctx context.Context, threshold float64, before time.Time, previewLimit int, expectedMatches int64) (TrafficHistoryMaintenanceReport, error) {
	if before.IsZero() {
		return TrafficHistoryMaintenanceReport{}, fmt.Errorf("safe_before from a completed preview is required")
	}
	if expectedMatches < 0 {
		return TrafficHistoryMaintenanceReport{}, fmt.Errorf("expected_matches cannot be negative")
	}
	threshold, err := normalizeTrafficAnomalyThreshold(threshold)
	if err != nil {
		return TrafficHistoryMaintenanceReport{}, err
	}
	before, err = normalizeTrafficHistoryBefore(before, time.Now().UTC())
	if err != nil {
		return TrafficHistoryMaintenanceReport{}, err
	}
	previewLimit = normalizeTrafficAnomalyPreviewLimit(previewLimit)

	if err := storeOperations.Acquire(ctx); err != nil {
		return TrafficHistoryMaintenanceReport{}, err
	}
	defer storeOperations.Release()
	store := GetStore()
	if store == nil {
		return TrafficHistoryMaintenanceReport{}, fmt.Errorf("metric store not initialized")
	}

	query := trafficOutlierQuery(threshold, before, previewLimit)
	scan, err := store.FindRollupOutliers(ctx, query)
	if err != nil {
		return TrafficHistoryMaintenanceReport{}, err
	}
	if scan.TotalMatches != expectedMatches {
		return TrafficHistoryMaintenanceReport{}, fmt.Errorf("traffic anomaly set changed after preview: expected %d buckets, found %d; scan again before cleanup", expectedMatches, scan.TotalMatches)
	}
	if scan.TotalMatches > MaxTrafficAnomalyCleanupBuckets {
		return TrafficHistoryMaintenanceReport{}, fmt.Errorf("refusing to delete %d buckets in one operation; safety limit is %d", scan.TotalMatches, MaxTrafficAnomalyCleanupBuckets)
	}

	deleted, err := store.DeleteRollupOutliers(ctx, query, MaxTrafficAnomalyCleanupBuckets)
	if err != nil {
		return TrafficHistoryMaintenanceReport{}, err
	}
	remaining, err := store.FindRollupOutliers(ctx, query)
	if err != nil {
		return TrafficHistoryMaintenanceReport{}, err
	}
	return TrafficHistoryMaintenanceReport{
		ThresholdBytes:   threshold,
		SafeBefore:       before,
		MatchingBuckets:  scan.TotalMatches,
		AffectedEntities: scan.AffectedEntities,
		Preview:          scan.Preview,
		Truncated:        scan.Truncated,
		DeletedBuckets:   deleted,
		RemainingBuckets: remaining.TotalMatches,
		CleanupLimit:     MaxTrafficAnomalyCleanupBuckets,
	}, nil
}
