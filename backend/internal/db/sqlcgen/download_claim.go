package sqlcgen

import "context"

// ClaimDownload uses a compare-and-set, so simultaneous workers cannot both own a job.
func (q *Queries) ClaimDownload(ctx context.Context, id int64) (bool, error) {
	result, err := q.db.ExecContext(ctx, "UPDATE downloads SET status = 'downloading', progress = 0 WHERE id = ? AND status = 'queued'", id)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}
