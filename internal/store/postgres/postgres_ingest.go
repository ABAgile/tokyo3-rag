package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/abagile/tokyo3-rag/internal/model"
	"github.com/abagile/tokyo3-rag/internal/store"
)

func (db *DB) UpsertIngestJob(ctx context.Context, j *model.IngestJob) error {
	_, err := db.pool.Exec(ctx, `
		INSERT INTO ingest_jobs
			(id, source_path, source_type, language, file_hash, status, error_msg, indexed_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())
		ON CONFLICT (source_path) DO UPDATE SET
			source_type = EXCLUDED.source_type,
			language    = EXCLUDED.language,
			file_hash   = EXCLUDED.file_hash,
			status      = EXCLUDED.status,
			error_msg   = EXCLUDED.error_msg,
			indexed_at  = EXCLUDED.indexed_at
	`, j.ID, j.SourcePath, j.SourceType, nullStr(j.Language), j.FileHash,
		j.Status, nullStr(j.ErrorMsg), j.IndexedAt)
	return err
}

func (db *DB) GetIngestJobByPath(ctx context.Context, sourcePath string) (*model.IngestJob, error) {
	var j model.IngestJob
	var lang, errMsg *string
	err := db.pool.QueryRow(ctx, `
		SELECT id, source_path, source_type, language, file_hash, status, error_msg, indexed_at, created_at
		FROM   ingest_jobs
		WHERE  source_path = $1
	`, sourcePath).Scan(
		&j.ID, &j.SourcePath, &j.SourceType, &lang,
		&j.FileHash, &j.Status, &errMsg, &j.IndexedAt, &j.CreatedAt,
	)
	if err != nil {
		return nil, mapNotFound(err)
	}
	if lang != nil {
		j.Language = *lang
	}
	if errMsg != nil {
		j.ErrorMsg = *errMsg
	}
	return &j, nil
}

func (db *DB) ListIngestJobs(ctx context.Context) ([]*model.IngestJob, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT id, source_path, source_type, language, file_hash, status, error_msg, indexed_at, created_at
		FROM   ingest_jobs
		ORDER  BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []*model.IngestJob
	for rows.Next() {
		var j model.IngestJob
		var lang, errMsg *string
		var indexedAt *time.Time
		if err := rows.Scan(
			&j.ID, &j.SourcePath, &j.SourceType, &lang,
			&j.FileHash, &j.Status, &errMsg, &indexedAt, &j.CreatedAt,
		); err != nil {
			return nil, err
		}
		if lang != nil {
			j.Language = *lang
		}
		if errMsg != nil {
			j.ErrorMsg = *errMsg
		}
		j.IndexedAt = indexedAt
		jobs = append(jobs, &j)
	}
	return jobs, rows.Err()
}

func nullStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func mapNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return store.ErrNotFound
	}
	return err
}
