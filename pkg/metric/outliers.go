package metric

import (
	"context"
	"database/sql"
	"fmt"
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

// RollupOutlierScan is a bounded preview plus exact aggregate counts.
type RollupOutlierScan struct {
	TotalMatches     int64           `json:"total_matches"`
	AffectedEntities []string        `json:"affected_entities"`
	Preview          []RollupOutlier `json:"preview"`
	Truncated        bool            `json:"truncated"`
}

type rollupOutlierKey struct {
	seriesID     int64
	resolutionID int64
	labelID      int64
	bucketMilli  int64
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

// FindRollupOutliers scans only persisted rollups. It does not inspect or
// mutate the active raw/hot windows.
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
	sqlText := fmt.Sprintf(`SELECT s.metric_name, s.entity_id, d.resolution_milli,
		r.bucket_milli, r.count, r.sum, r.max_val
		FROM %s r
		JOIN %s s ON s.id = r.series_id
		JOIN %s d ON d.id = r.resolution_id
		WHERE %s
		ORDER BY r.bucket_milli ASC, s.entity_id ASC, s.metric_name ASC, d.resolution_milli ASC`,
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
	for rows.Next() {
		var item RollupOutlier
		var bucketMilli int64
		if err := rows.Scan(
			&item.MetricName,
			&item.EntityID,
			&item.ResolutionMilli,
			&bucketMilli,
			&item.Count,
			&item.Sum,
			&item.MaxValue,
		); err != nil {
			return RollupOutlierScan{}, err
		}
		item.BucketStart = time.UnixMilli(bucketMilli).UTC()
		result.TotalMatches++
		entities[item.EntityID] = struct{}{}
		if len(result.Preview) < previewLimit {
			result.Preview = append(result.Preview, item)
		}
	}
	if err := rows.Err(); err != nil {
		return RollupOutlierScan{}, err
	}
	result.Truncated = result.TotalMatches > int64(len(result.Preview))
	result.AffectedEntities = make([]string, 0, len(entities))
	for entityID := range entities {
		result.AffectedEntities = append(result.AffectedEntities, entityID)
	}
	sort.Strings(result.AffectedEntities)
	return result, nil
}

// DeleteRollupOutliers deletes only persisted rollup buckets matched by the
// query. maxDelete is a caller supplied blast-radius guard; when it is positive
// and the match count exceeds it, no rows are changed.
func (s *Store) DeleteRollupOutliers(ctx context.Context, query RollupOutlierQuery, maxDelete int) (int64, error) {
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

	// Block writes/compaction while selecting and deleting exact persisted
	// bucket identities. Callers intentionally keep Before outside the mutable
	// raw/hot horizon, so no in-memory state needs to be rewritten here.
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
	selectSQL := fmt.Sprintf(`SELECT r.series_id, r.resolution_id, r.label_id, r.bucket_milli
		FROM %s r
		JOIN %s s ON s.id = r.series_id
		JOIN %s d ON d.id = r.resolution_id
		WHERE %s
		ORDER BY r.bucket_milli ASC`,
		s.tables.rollups, s.tables.series, s.tables.resolutions, where)
	rows, err := tx.QueryContext(ctx, selectSQL, args...)
	if err != nil {
		return 0, err
	}
	keys := make([]rollupOutlierKey, 0)
	for rows.Next() {
		var key rollupOutlierKey
		if err := rows.Scan(&key.seriesID, &key.resolutionID, &key.labelID, &key.bucketMilli); err != nil {
			_ = rows.Close()
			return 0, err
		}
		keys = append(keys, key)
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
		if err != nil && err != sql.ErrNoRows {
			return deleted, err
		}
		deleted += n
	}
	if err := tx.Commit(); err != nil {
		return deleted, err
	}
	return deleted, nil
}
