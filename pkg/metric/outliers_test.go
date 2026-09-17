package metric

import (
	"context"
	"testing"
	"time"
)

func TestRollupOutlierScanAndCleanup(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, SQLite(":memory:", WithRollupPolicy(RollupPolicy{
		RawRetention: time.Minute,
		Tiers:        []RollupTier{{Interval: time.Minute, Retention: 24 * time.Hour}},
		Compression:  30,
	})))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	for _, name := range []string{"traffic.up", "traffic.down", "unrelated"} {
		if err := s.CreateMetric(ctx, Definition{Name: name, Type: TypeGauge, Unit: "bytes", RetentionDays: 1}); err != nil {
			t.Fatalf("create metric %s: %v", name, err)
		}
	}

	now := time.Now().UTC()
	old := now.Add(-time.Hour).Truncate(time.Minute).Add(5 * time.Second)
	const oneTiB = float64(1 << 40)
	if err := s.WriteBatch(ctx, []Point{
		{MetricName: "traffic.up", EntityID: "node-bad", Timestamp: old, Value: 2 * oneTiB},
		{MetricName: "traffic.down", EntityID: "node-safe", Timestamp: old.Add(time.Second), Value: 32 * 1024 * 1024},
		{MetricName: "unrelated", EntityID: "node-other", Timestamp: old.Add(2 * time.Second), Value: 4 * oneTiB},
	}); err != nil {
		t.Fatalf("write points: %v", err)
	}

	query := RollupOutlierQuery{
		MetricNames:  []string{"traffic.up", "traffic.down"},
		MinValue:     oneTiB,
		Before:       now.Add(-15 * time.Minute),
		PreviewLimit: 10,
	}
	scan, err := s.FindRollupOutliers(ctx, query)
	if err != nil {
		t.Fatalf("scan outliers: %v", err)
	}
	if scan.TotalMatches != 1 {
		t.Fatalf("total matches = %d, want 1; preview=%#v", scan.TotalMatches, scan.Preview)
	}
	if len(scan.Preview) != 1 || scan.Preview[0].MetricName != "traffic.up" || scan.Preview[0].EntityID != "node-bad" {
		t.Fatalf("preview = %#v, want node-bad traffic.up", scan.Preview)
	}
	if len(scan.AffectedEntities) != 1 || scan.AffectedEntities[0] != "node-bad" {
		t.Fatalf("affected entities = %#v, want [node-bad]", scan.AffectedEntities)
	}

	deleted, err := s.DeleteRollupOutliers(ctx, query, 100)
	if err != nil {
		t.Fatalf("delete outliers: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}

	after, err := s.FindRollupOutliers(ctx, query)
	if err != nil {
		t.Fatalf("rescan outliers: %v", err)
	}
	if after.TotalMatches != 0 {
		t.Fatalf("matches after cleanup = %d, want 0", after.TotalMatches)
	}

	// The large point on an unrelated metric must not be touched by a traffic-only cleanup.
	unrelated, err := s.FindRollupOutliers(ctx, RollupOutlierQuery{
		MetricNames:  []string{"unrelated"},
		MinValue:     oneTiB,
		Before:       query.Before,
		PreviewLimit: 10,
	})
	if err != nil {
		t.Fatalf("scan unrelated metric: %v", err)
	}
	if unrelated.TotalMatches != 1 {
		t.Fatalf("unrelated matches = %d, want 1", unrelated.TotalMatches)
	}
}

func TestDeleteRollupOutliersHonorsBlastRadiusLimit(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, SQLite(":memory:", WithRollupPolicy(RollupPolicy{
		RawRetention: time.Minute,
		Tiers:        []RollupTier{{Interval: time.Minute, Retention: 24 * time.Hour}},
		Compression:  30,
	})))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	if err := s.CreateMetric(ctx, Definition{Name: "traffic.up", Type: TypeGauge, RetentionDays: 1}); err != nil {
		t.Fatalf("create metric: %v", err)
	}

	now := time.Now().UTC()
	old := now.Add(-time.Hour).Truncate(time.Minute)
	const oneTiB = float64(1 << 40)
	if err := s.WriteBatch(ctx, []Point{
		{MetricName: "traffic.up", EntityID: "node-1", Timestamp: old.Add(5 * time.Second), Value: 2 * oneTiB},
		{MetricName: "traffic.up", EntityID: "node-2", Timestamp: old.Add(65 * time.Second), Value: 3 * oneTiB},
	}); err != nil {
		t.Fatalf("write points: %v", err)
	}

	query := RollupOutlierQuery{
		MetricNames: []string{"traffic.up"},
		MinValue:    oneTiB,
		Before:      now.Add(-15 * time.Minute),
	}
	if _, err := s.DeleteRollupOutliers(ctx, query, 1); err == nil {
		t.Fatal("cleanup unexpectedly succeeded despite maxDelete=1")
	}
	scan, err := s.FindRollupOutliers(ctx, query)
	if err != nil {
		t.Fatalf("scan after rejected cleanup: %v", err)
	}
	if scan.TotalMatches != 2 {
		t.Fatalf("matches after rejected cleanup = %d, want 2", scan.TotalMatches)
	}
}
