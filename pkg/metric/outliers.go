package metric

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"math"
	"sort"
	"strings"
	"time"
)

// RollupOutlierQuery selects persisted rollup buckets whose maximum individual
// sample is at or above MinValue. Before is an exclusive safety boundary for
// the complete bucket: only buckets whose end time is <= Before are eligible.
//
// Using max_val instead of sum intentionally distinguishes a single implausible
// sample from a legitimately large aggregate bucket.
type RollupOutlierQuery struct {
	MetricNames  []string
	MinValue     float64
	Before       time.Time
	PreviewLimit int
}

// RollupOutlier describes one persisted rollup bucket selected as anomalous.
type RollupOutlier struct {
	MetricName      string    `json:"metric_name"`
	EntityID        string    `json:"entity_id"`
	ResolutionMilli int64     `json:"resolution_milli"`
	BucketStart     time.Time `json:"bucket_start"`
	Count           int64     `json:"count"`
	Sum             float64   `json:"sum"`
	MaxValue        float64   `json:"max_value"`
}

// RollupOutlierScan is a bounded preview plus exact aggregate counts. Fingerprint
// commits to the complete matching bucket set, including identities and values,
// so a destructive follow-up can fail closed if the preview has gone stale.
type RollupOutlierScan struct {
	TotalMatches     int64           `json:"total_matches"`
	AffectedEntities []string        `json:"affected_entities"`
	Preview          []RollupOutlier `json:"preview"`
	Truncated        bool            `json:"truncated"`
	Fingerprint      string          `json:"fingerprint"`
}

type rollupOutlierKey struct {
	seriesID     int64
	resolutionID int64
	labelID      int64
	bucketMilli  int64
	count        int64
	sum          float64
	maxValue     float64
}

func (q RollupOutlierQuery) normalized() (RollupOutlierQuery, error) {
	if q.MinValue <= 0 {
		return RollupOutlierQuery{}, fmt.Errorf("%w: minimum outlier value must be positive", ErrInvalidArgument)
	}
	if q.Before.IsZero() {
		return RollupOutlierQuery{}, fmt.Errorf("%w: before time is required", ErrInvalidArgument)
	}
	if q.PreviewLimit < 0 {
		return RollupOutlierQuery{}, fmt.Errorf("%w: preview limit cannot be negative", ErrInvalidArgument)
	}
	seen := make(map[string]struct{}, len(q.MetricNames))
	names := make([]string, 0, len(q.MetricNames))
	for _, raw := range q.MetricNames {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	if len(names) == 0 {
		return RollupOutlierQuery{}, fmt.Errorf("%w: at least one metric name is required", ErrInvalidArgument)
	}
	sort.Strings(names)
	q.MetricNames = names
	q.Before = q.Before.UTC()
	return q, nil
}

func (s *Store) rollupOutlierWhere(q RollupOutlierQuery) (string, []any) {
	args := make([]any, 0, len(q.MetricNames)+2)
	metricPlaceholders := make([]string, 0, len(q.MetricNames))
	for _, name := range q.MetricNames {
		args = append(args, name)
		metricPlaceholders = append(metricPlaceholders, s.dialect.placeholder(len(args)))
	}
	args = append(args, q.MinValue)
	minPlaceholder := s.dialect.placeholder(len(args))
	args = append(args, q.Before.UnixMilli())
	beforePlaceholder := s.dialect.placeholder(len(args))
	return fmt.Sprintf(
		"s.metric_name IN (%s) AND r.max_val >= %s AND (r.bucket_milli + d.resolution_milli) <= %s",
		strings.Join(metricPlaceholders, ", "), minPlaceholder, beforePlaceholder,
	), args
}

// RollupMaintenanceSafeBefore returns the newest boundary for which every
// configured rollup tier is guaranteed to be sealed. The largest tier matters:
// a minute bucket can already be persisted while its 5m/hour/day parent is
// still held in memory and could later reproduce the same anomaly. Aligning the
// boundary to the largest tier after the coarse-rollup grace period prevents
// historical maintenance from racing those in-memory parents.
func (s *Store) RollupMaintenanceSafeBefore(now time.Time) time.Time {
	now = now.UTC()
	maxInterval := time.Minute
	for _, tier := range s.cfg.RollupPolicy.Tiers {
		if tier.Interval > maxInterval {
			maxInterval = tier.Interval
		}
	}
	sealedThrough := now.Add(-coarseRollupGrace)
	boundaryMilli := bucketStartMillis(sealedThrough.UnixMilli(), maxInterval.Milliseconds())
	return time.UnixMilli(boundaryMilli).UTC()
}

func writeOutlierHashInt64(h hash.Hash, value int64) {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], uint64(value))
	_, _ = h.Write(buf[:])
}

func writeOutlierHashFloat64(h hash.Hash, value float64) {
	writeOutlierHashInt64(h, int64(math.Float64bits(value)))
}

func writeOutlierFingerprintRow(h hash.Hash, key rollupOutlierKey) {
	writeOutlierHashInt64(h, key.seriesID)
	writeOutlierHashInt64(h, key.resolutionID)
	writeOutlierHashInt64(h, key.labelID)
	writeOutlierHashInt64(h, key.bucketMilli)
	writeOutlierHashInt64(h, key.count)
	writeOutlierHashFloat64(h, key.sum)
	writeOutlierHashFloat64(h, key.maxValue)
}

func finishOutlierFingerprint(h hash.Hash) string {
	return hex.EncodeToString(h.Sum(nil))
}

// FindRollupOutliers scans only persisted rollups. It does not inspect or
// mutate the active raw/hot/coarse windows.
func (s *Store) FindRollupOutliers(ctx context.Context, query RollupOutlierQuery) (RollupOutlierScan, error) {
	if err := s.ensureOpen(); err != nil {
		return RollupOutlierScan{}, err
	}
	q, err := query.normalized()
	if err != nil {
		return RollupOutlierScan{}, err
	}

	s.retentionMu.RLock()
	defer s.retentionMu.RUnlock()
	s.rollupViewMu.RLock()
	defer s.rollupViewMu.RUnlock()

	where, args := s.rollupOutlierWhere(q)
	sqlText := fmt.Sprintf(`SELECT r.series_id, r.resolution_id, r.label_id,
		s.metric_name, s.entity_id, d.resolution_milli,
		r.bucket_milli, r.count, r.sum, r.max_val
		FROM %s r
		JOIN %s s ON s.id = r.series_id
		JOIN %s d ON d.id = r.resolution_id
		WHERE %s
		ORDER BY r.bucket_milli ASC, r.series_id ASC, r.resolution_id ASC, r.label_id ASC`,
		s.tables.rollups, s.tables.series, s.tables.resolutions, where)

	rows, err := s.reader().QueryContext(ctx, sqlText, args...)
	if err != nil {
		return RollupOutlierScan{}, err
	}
	defer rows.Close()

	previewLimit := q.PreviewLimit
	if previewLimit == 0 {
		previewLimit = 100
	}
	result := RollupOutlierScan{Preview: make([]RollupOutlier, 0, previewLimit)}
	entities := make(map[string]struct{})
	fingerprint := sha256.New()
	for rows.Next() {
		var item RollupOutlier
		var key rollupOutlierKey
		if err := rows.Scan(
			&key.seriesID,
			&key.resolutionID,
			&key.labelID,
			&item.MetricName,
			&item.EntityID,
			&item.ResolutionMilli,
			&key.bucketMilli,
			&key.count,
			&key.sum,
			&key.maxValue,
		); err != nil {
			return RollupOutlierScan{}, err
		}
		item.BucketStart = time.UnixMilli(key.bucketMilli).UTC()
		item.Count = key.count
		item.Sum = key.sum
		item.MaxValue = key.maxValue
		writeOutlierFingerprintRow(fingerprint, key)
		result.TotalMatches++
		entities[item.EntityID] = struct{}{}
		if len(result.Preview) < previewLimit {
			result.Preview = append(result.Preview, item)
		}
	}
	if err := rows.Err(); err != nil {
		return RollupOutlierScan{}, err
	}
	result.Fingerprint = finishOutlierFingerprint(fingerprint)
	result.Truncated = result.TotalMatches > int64(len(result.Preview))
	result.AffectedEntities = make([]string, 0, len(entities))
	for entityID := range entities {
		result.AffectedEntities = append(result.AffectedEntities, entityID)
	}
	sort.Strings(result.AffectedEntities)
	return result, nil
}

// DeleteRollupOutliers deletes only persisted rollup buckets matched by the
// query. maxDelete is a caller supplied blast-radius guard. expectedMatches and
// expectedFingerprint bind the mutation to an exact previously reviewed scan;
// when either differs, no rows are changed.
func (s *Store) DeleteRollupOutliers(ctx context.Context, query RollupOutlierQuery, maxDelete int, expectedMatches int64, expectedFingerprint string) (int64, error) {
	if err := s.ensureOpen(); err != nil {
		return 0, err
	}
	q, err := query.normalized()
	if err != nil {
		return 0, err
	}
	if maxDelete < 0 {
		return 0, fmt.Errorf("%w: max delete cannot be negative", ErrInvalidArgument)
	}
	if expectedMatches < 0 {
		return 0, fmt.Errorf("%w: expected match count cannot be negative", ErrInvalidArgument)
	}
	if strings.TrimSpace(expectedFingerprint) == "" {
		return 0, fmt.Errorf("%w: expected fingerprint is required", ErrInvalidArgument)
	}
	if safe := s.RollupMaintenanceSafeBefore(time.Now().UTC()); q.Before.After(safe) {
		return 0, fmt.Errorf("%w: cleanup boundary %s is newer than sealed rollup boundary %s", ErrInvalidArgument, q.Before.Format(time.RFC3339), safe.Format(time.RFC3339))
	}

	// Block writes/compaction while selecting and deleting exact persisted
	// bucket identities. The sealed boundary guarantees that matching minute
	// samples cannot still be retained inside a mutable coarse parent.
	s.retentionMu.Lock()
	defer s.retentionMu.Unlock()
	s.rollupViewMu.Lock()
	defer s.rollupViewMu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	where, args := s.rollupOutlierWhere(q)
	selectSQL := fmt.Sprintf(`SELECT r.series_id, r.resolution_id, r.label_id,
		r.bucket_milli, r.count, r.sum, r.max_val
		FROM %s r
		JOIN %s s ON s.id = r.series_id
		JOIN %s d ON d.id = r.resolution_id
		WHERE %s
		ORDER BY r.bucket_milli ASC, r.series_id ASC, r.resolution_id ASC, r.label_id ASC`,
		s.tables.rollups, s.tables.series, s.tables.resolutions, where)
	rows, err := tx.QueryContext(ctx, selectSQL, args...)
	if err != nil {
		return 0, err
	}
	keys := make([]rollupOutlierKey, 0)
	fingerprint := sha256.New()
	for rows.Next() {
		var key rollupOutlierKey
		if err := rows.Scan(
			&key.seriesID,
			&key.resolutionID,
			&key.labelID,
			&key.bucketMilli,
			&key.count,
			&key.sum,
			&key.maxValue,
		); err != nil {
			_ = rows.Close()
			return 0, err
		}
		keys = append(keys, key)
		writeOutlierFingerprintRow(fingerprint, key)
		if maxDelete > 0 && len(keys) > maxDelete {
			_ = rows.Close()
			return 0, fmt.Errorf("%w: cleanup would delete %d+ rollup buckets; limit is %d", ErrInvalidArgument, len(keys), maxDelete)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}

	actualFingerprint := finishOutlierFingerprint(fingerprint)
	if int64(len(keys)) != expectedMatches || actualFingerprint != expectedFingerprint {
		return 0, fmt.Errorf("%w: cleanup candidate set changed after preview; scan again before cleanup", ErrInvalidArgument)
	}
	if len(keys) == 0 {
		return 0, nil
	}

	deleteSQL := fmt.Sprintf(`DELETE FROM %s WHERE series_id = %s AND resolution_id = %s AND label_id = %s AND bucket_milli = %s`,
		s.tables.rollups,
		s.dialect.placeholder(1),
		s.dialect.placeholder(2),
		s.dialect.placeholder(3),
		s.dialect.placeholder(4),
	)
	stmt, err := tx.PrepareContext(ctx, deleteSQL)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	var deleted int64
	for _, key := range keys {
		res, err := stmt.ExecContext(ctx, key.seriesID, key.resolutionID, key.labelID, key.bucketMilli)
		if err != nil {
			return deleted, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return deleted, err
		}
		deleted += n
	}
	if err := tx.Commit(); err != nil {
		return deleted, err
	}
	return deleted, nil
}
