-- metric_samples: monitoring time series (see migration 0010).

-- name: InsertMetricSample :exec
INSERT INTO metric_samples (server_id, app_id, tier, bucket_start, samples, data)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (server_id, app_id, tier, bucket_start) DO NOTHING;

-- name: ListMetricSamples :many
SELECT * FROM metric_samples
WHERE server_id = ? AND app_id = ? AND tier = ? AND bucket_start >= ?
ORDER BY bucket_start ASC;

-- name: ListServerAppMetricSamples :many
SELECT * FROM metric_samples
WHERE server_id = ? AND app_id <> '' AND tier = ? AND bucket_start >= ?
ORDER BY app_id ASC, bucket_start ASC;

-- name: DeleteMetricSamplesBefore :execrows
DELETE FROM metric_samples WHERE tier = ? AND bucket_start < ?;

-- name: DeleteMetricSamplesByApp :exec
DELETE FROM metric_samples WHERE app_id = ?;
