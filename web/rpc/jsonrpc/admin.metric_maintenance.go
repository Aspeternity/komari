package jsonrpc

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/internal/metricstore"
	"github.com/komari-monitor/komari/pkg/rpc"
)

func init() {
	reg("scanTrafficHistoryAnomalies", adminScanTrafficHistoryAnomalies, "Preview historical traffic rollup anomalies without changing data")
	reg("cleanupTrafficHistoryAnomalies", adminCleanupTrafficHistoryAnomalies, "Delete historical traffic rollup anomaly buckets after an explicit preview confirmation")
}

type trafficHistoryScanParams struct {
	ThresholdBytes float64 `json:"threshold_bytes"`
	SafeBefore     string  `json:"safe_before"`
	PreviewLimit   int     `json:"preview_limit"`
}

type trafficHistoryCleanupParams struct {
	ThresholdBytes      float64 `json:"threshold_bytes"`
	SafeBefore          string  `json:"safe_before"`
	PreviewLimit        int     `json:"preview_limit"`
	ExpectedMatches     int64   `json:"expected_matches"`
	ExpectedFingerprint string  `json:"expected_fingerprint"`
	Confirm             bool    `json:"confirm"`
}

func parseTrafficHistoryBoundary(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("safe_before must be RFC3339: %w", err)
	}
	return parsed.UTC(), nil
}

func adminScanTrafficHistoryAnomalies(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params trafficHistoryScanParams
	if err := req.BindParams(&params); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request body: "+err.Error(), nil)
	}
	before, err := parseTrafficHistoryBoundary(params.SafeBefore)
	if err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, err.Error(), nil)
	}
	report, err := metricstore.ScanTrafficHistoryAnomalies(ctx, params.ThresholdBytes, before, params.PreviewLimit)
	if err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Failed to scan traffic history: "+err.Error(), nil)
	}

	actor, ip := auditActor(ctx)
	auditlog.Log(ip, actor, fmt.Sprintf("preview traffic history anomalies: threshold=%.0f matches=%d before=%s", report.ThresholdBytes, report.MatchingBuckets, report.SafeBefore.Format(time.RFC3339)), "info")
	return report, nil
}

func adminCleanupTrafficHistoryAnomalies(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params trafficHistoryCleanupParams
	if err := req.BindParams(&params); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request body: "+err.Error(), nil)
	}
	if !params.Confirm {
		return nil, rpc.MakeError(rpc.InvalidParams, "confirm=true is required after reviewing a preview", nil)
	}
	if strings.TrimSpace(params.ExpectedFingerprint) == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "expected_fingerprint from the preview is required", nil)
	}
	before, err := parseTrafficHistoryBoundary(params.SafeBefore)
	if err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, err.Error(), nil)
	}
	if before.IsZero() {
		return nil, rpc.MakeError(rpc.InvalidParams, "safe_before from the preview is required", nil)
	}

	report, err := metricstore.CleanupTrafficHistoryAnomalies(
		ctx,
		params.ThresholdBytes,
		before,
		params.PreviewLimit,
		params.ExpectedMatches,
		params.ExpectedFingerprint,
	)
	if err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Failed to clean traffic history: "+err.Error(), nil)
	}

	actor, ip := auditActor(ctx)
	auditlog.Log(ip, actor, fmt.Sprintf(
		"cleanup traffic history anomalies: threshold=%.0f confirmed=%d deleted=%d remaining=%d before=%s",
		report.ThresholdBytes,
		params.ExpectedMatches,
		report.DeletedBuckets,
		report.RemainingBuckets,
		report.SafeBefore.Format(time.RFC3339),
	), "warn")
	return report, nil
}
