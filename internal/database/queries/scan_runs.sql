-- name: CreateScanRun :one
INSERT INTO scan_runs (root, mode, started_at)
VALUES (?, ?, ?)
RETURNING id;

-- name: FinishScanRun :exec
UPDATE scan_runs SET status = ?, finished_at = ? WHERE id = ?;

-- name: GetLatestScanRun :one
SELECT * FROM scan_runs ORDER BY id DESC LIMIT 1;
