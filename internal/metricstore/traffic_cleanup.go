package metricstore

import (
	"context"
	"fmt"
	"strings"
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
)

// TrafficHistoryMaintenanceReport is returned by both preview and cleanup.
type TrafficHistoryMaintenanceReport struct {
	ThresholdBytes     float64                `json:"threshold_bytes"`
	SafeBefore         time.Time              `json:"safe_before"`
	MatchingBuckets    int64                  `json:"matching_buckets"`
	AffectedEntities   []string               `json:"affected_entities"`
	Preview            []metric.RollupOutlier `json:"preview"`
	Truncated          bool                   `json:"truncated"`
	Fingerprint        string                 `json:"fingerprint"`
	DeletedBuckets     int64                  `json:"deleted_buckets,omitempty"`
	RemainingBuckets   int64                  `json:"remaining_buckets,omitempty"`
	CleanupLimit       int                    `json:"cleanup_limit"`
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

func normalizeTrafficHistoryBefore(requested, safe time.Time) (time.Time, error) {
	safe = safe.UTC()
	if requested.IsZero() {
		return safe, nil
	}
	requested = requested.UTC()
	if requested.After(safe) {
		return time.Time{}, fmt.Errorf("requested cleanup boundary %s overlaps mutable rollup history; latest fully sealed boundary is %s", requested.Format(time.RFC3339), safe.Format(time.RFC3339))
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
// traffic rollups. It never removes data. The safe boundary is derived from
// the largest configured rollup tier, so a matching minute cannot still be
// retained inside an in-memory 5m/hour/day parent waiting to be persisted.
func ScanTrafficHistoryAnomalies(ctx context.Context, threshold float64, before time.Time, previewLimit int) (TrafficHistoryMaintenanceReport, error) {
	threshold, err := normalizeTrafficAnomalyThreshold(threshold)
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
	before, err = normalizeTrafficHistoryBefore(before, store.RollupMaintenanceSafeBefore(time.Now().UTC()))
	if err != nil {
		return TrafficHistoryMaintenanceReport{}, err
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
		Fingerprint:      scan.Fingerprint,
		CleanupLimit:     MaxTrafficAnomalyCleanupBuckets,
	}, nil
}

// CleanupTrafficHistoryAnomalies removes only persisted traffic rollup buckets
// matched by an already reviewed preview. The expected count and fingerprint
// are both revalidated inside the deletion transaction, so a same-cardinality
// but different candidate set is also rejected.
func CleanupTrafficHistoryAnomalies(ctx context.Context, threshold float64, before time.Time, previewLimit int, expectedMatches int64, expectedFingerprint string) (TrafficHistoryMaintenanceReport, error) {
	if before.IsZero() {
		return TrafficHistoryMaintenanceReport{}, fmt.Errorf("safe_before from a completed preview is required")
	}
	if expectedMatches < 0 {
		return TrafficHistoryMaintenanceReport{}, fmt.Errorf("expected_matches cannot be negative")
	}
	if strings.TrimSpace(expectedFingerprint) == "" {
		return TrafficHistoryMaintenanceReport{}, fmt.Errorf("expected_fingerprint from a completed preview is required")
	}
	threshold, err := normalizeTrafficAnomalyThreshold(threshold)
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
	before, err = normalizeTrafficHistoryBefore(before, store.RollupMaintenanceSafeBefore(time.Now().UTC()))
	if err != nil {
		return TrafficHistoryMaintenanceReport{}, err
	}

	query := trafficOutlierQuery(threshold, before, previewLimit)
	if expectedMatches > MaxTrafficAnomalyCleanupBuckets {
		return TrafficHistoryMaintenanceReport{}, fmt.Errorf("refusing to delete %d buckets in one operation; safety limit is %d", expectedMatches, MaxTrafficAnomalyCleanupBuckets)
	}
	deleted, err := store.DeleteRollupOutliers(
		ctx,
		query,
		MaxTrafficAnomalyCleanupBuckets,
		expectedMatches,
		expectedFingerprint,
	)
	if err != nil {
		return TrafficHistoryMaintenanceReport{}, err
	}
	remaining, err := store.FindRollupOutliers(ctx, query)
	if err != nil {
		return TrafficHistoryMaintenanceReport{}, err
	}
	return TrafficHistoryMaintenanceReport{
		ThresholdBytes:     threshold,
		SafeBefore:         before,
		MatchingBuckets:    expectedMatches,
		AffectedEntities:   remaining.AffectedEntities,
		Preview:            remaining.Preview,
		Truncated:          remaining.Truncated,
		Fingerprint:        remaining.Fingerprint,
		DeletedBuckets:     deleted,
		RemainingBuckets:   remaining.TotalMatches,
		CleanupLimit:       MaxTrafficAnomalyCleanupBuckets,
	}, nil
}
