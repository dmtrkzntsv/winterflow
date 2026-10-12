-- metric_samples: monitoring time series (see migration 0010).

-- name: InsertMetricSample :exec
INSERT INTO metric_samples (server_id, app_id, tier, bucket_start, samples, data)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (server_id, app_id, tier, bucket_start) DO NOTHING;

-- name: ListMetricSamples :many
SELECT * FROM metric_samples
WHERE server_id = $1 AND app_id = $2 AND tier = $3 AND bucket_start >= $4
ORDER BY bucket_start ASC;

-- name: ListServerAppMetricSamples :many
SELECT * FROM metric_samples
WHERE server_id = $1 AND app_id <> '' AND tier = $2 AND bucket_start >= $3
ORDER BY app_id ASC, bucket_start ASC;

-- name: ListMetricSamplesInRange :many
SELECT * FROM metric_samples
WHERE tier = sqlc.arg(tier) AND bucket_start >= sqlc.arg(from_time) AND bucket_start < sqlc.arg(to_time)
ORDER BY server_id ASC, app_id ASC, bucket_start ASC;

-- name: DeleteMetricSamplesBefore :execrows
DELETE FROM metric_samples WHERE tier = $1 AND bucket_start < $2;

-- name: DeleteMetricSamplesByApp :exec
DELETE FROM metric_samples WHERE app_id = $1;
