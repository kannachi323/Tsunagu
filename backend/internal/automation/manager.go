// Package automation persists download policies independently of any reader UI.
package automation

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
	"tsunagu/backend/internal/db/sqlcgen"
	"tsunagu/backend/internal/download"
)

type Policy struct {
	PauseUpdates           bool     `json:"pauseUpdates"`
	RefreshInterval        string   `json:"refreshInterval"`
	AutoDownload           bool     `json:"autoDownload"`
	DownloadAhead          int      `json:"downloadAhead"`
	DeleteOnRead           bool     `json:"deleteOnRead"`
	DeleteDelayHours       int      `json:"deleteDelayHours"`
	MaxKeepChapters        int      `json:"maxKeepChapters"`
	AutoDownloadScanlators []string `json:"autoDownloadScanlators"`
}
type Manager struct {
	Refresh func(context.Context, int64) error
	db      *sql.DB
	q       *sqlcgen.Queries
	dm      *download.Manager
	mu      sync.Mutex
	Now     func() time.Time
}

func New(db *sql.DB, q *sqlcgen.Queries, dm *download.Manager) *Manager {
	return &Manager{db: db, q: q, dm: dm, Now: time.Now}
}
func (m *Manager) Policy(ctx context.Context, media int64) (Policy, error) {
	p := Policy{RefreshInterval: "manual"}
	for _, id := range []int64{0, media} {
		var data string
		err := m.db.QueryRowContext(ctx, "SELECT policy_json FROM automation_policies WHERE scope_id=?", id).Scan(&data)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return p, err
		}
		previousInterval := p.RefreshInterval
		if err := json.Unmarshal([]byte(data), &p); err != nil {
			return p, err
		}
		if p.RefreshInterval == "global" {
			p.RefreshInterval = previousInterval
		}
	}
	return p, nil
}
func (m *Manager) SetPolicy(ctx context.Context, media int64, raw []byte) error {
	if media < 0 {
		return fmt.Errorf("invalid policy scope")
	}
	if media > 0 {
		if _, err := m.q.GetMedia(ctx, media); err != nil {
			return err
		}
	}
	var p Policy
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&p); err != nil {
		return err
	}
	switch p.RefreshInterval {
	case "", "daily", "weekly", "manual":
	case "global":
		if media == 0 {
			return fmt.Errorf("global policy cannot inherit itself")
		}
	default:
		return fmt.Errorf("invalid refresh interval")
	}
	if p.DownloadAhead < 0 || p.DownloadAhead > 1000 || p.DeleteDelayHours < 0 || p.DeleteDelayHours > 87600 || p.MaxKeepChapters < 0 {
		return fmt.Errorf("invalid policy limits")
	}
	// Keep the original object: omitted fields inherit global policy.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return fmt.Errorf("expected a policy object")
	}
	for _, value := range fields {
		if string(value) == "null" {
			return fmt.Errorf("null policy fields are not supported; omit to inherit")
		}
	}
	_, err := m.db.ExecContext(ctx, "INSERT INTO automation_policies(scope_id,policy_json) VALUES(?,?) ON CONFLICT(scope_id) DO UPDATE SET policy_json=excluded.policy_json", media, string(raw))
	return err
}
func (m *Manager) ChapterEvent(ctx context.Context, media, chapter int64, completed bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch, err := m.q.GetChapter(ctx, chapter)
	if err != nil {
		return err
	}
	if ch.MediaID != media {
		return fmt.Errorf("chapter does not belong to media")
	}
	policy, err := m.Policy(ctx, media)
	if err != nil {
		return err
	}
	if completed && policy.DeleteOnRead {
		_, err = m.db.ExecContext(ctx, "INSERT INTO automation_actions(chapter_id,completed_at) VALUES(?,?) ON CONFLICT(chapter_id) DO NOTHING", chapter, m.Now().Unix())
		if err != nil {
			return err
		}
	} else if !completed {
		// Opening an already completed chapter must not cancel its deletion timer;
		// only an explicit persisted unread state does so.
		_, err = m.db.ExecContext(ctx, "DELETE FROM automation_actions WHERE chapter_id=? AND NOT EXISTS(SELECT 1 FROM reading_progress WHERE chapter_id=? AND completed=1)", chapter, chapter)
		if err != nil {
			return err
		}
	}
	if policy.DownloadAhead > 0 {
		rows, err := m.db.QueryContext(ctx, `SELECT c.id FROM chapters c WHERE c.media_id=? AND COALESCE(c.source_order,0)>? AND NOT EXISTS(SELECT 1 FROM reading_progress p WHERE p.chapter_id=c.id AND p.completed=1) ORDER BY c.source_order,c.id LIMIT ?`, media, ch.SourceOrder.Int64, policy.DownloadAhead)
		if err != nil {
			return err
		}
		ids, err := readIDs(rows)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := m.q.EnqueueDownload(ctx, id); err != nil {
				return err
			}
		}
		m.dm.Wake()
	}
	return nil
}
func readIDs(rows *sql.Rows) ([]int64, error) {
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// NewChapters only receives newly discovered IDs, never the initial backlog.
func (m *Manager) NewChapters(ctx context.Context, media int64, ids []int64) error {
	policy, err := m.Policy(ctx, media)
	if err != nil || !policy.AutoDownload {
		return err
	}
	entry, err := m.q.GetMedia(ctx, media)
	if err != nil {
		return err
	}
	if !entry.AddedAt.Valid {
		return nil
	}
	for _, id := range ids {
		ch, err := m.q.GetChapter(ctx, id)
		if err != nil {
			return err
		}
		allowed := len(policy.AutoDownloadScanlators) == 0
		for _, name := range policy.AutoDownloadScanlators {
			if ch.Scanlator == name {
				allowed = true
			}
		}
		if allowed {
			if _, err := m.q.EnqueueDownload(ctx, id); err != nil {
				return err
			}
		}
	}
	m.dm.Wake()
	return nil
}
func (m *Manager) Tick(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows, err := m.db.QueryContext(ctx, `SELECT a.chapter_id FROM automation_actions a
 JOIN chapters c ON c.id=a.chapter_id
 LEFT JOIN automation_policies p ON p.scope_id=c.media_id
 LEFT JOIN automation_policies g ON g.scope_id=0
 WHERE NOT EXISTS(SELECT 1 FROM reading_progress r WHERE r.chapter_id=a.chapter_id AND r.completed=1)
 OR COALESCE(json_extract(p.policy_json,'$.deleteOnRead'),json_extract(g.policy_json,'$.deleteOnRead'),0)=0
 OR a.completed_at+3600*COALESCE(json_extract(p.policy_json,'$.deleteDelayHours'),json_extract(g.policy_json,'$.deleteDelayHours'),0)<=?
 ORDER BY a.completed_at,a.chapter_id LIMIT 100`, m.Now().Unix())
	if err != nil {
		return err
	}
	ids, err := readIDs(rows)
	if err != nil {
		return err
	}
	for _, id := range ids {
		ch, err := m.q.GetChapter(ctx, id)
		if err != nil {
			return err
		}
		p, err := m.Policy(ctx, ch.MediaID)
		if err != nil {
			return err
		}
		var completed bool
		var at int64
		err = m.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM reading_progress WHERE chapter_id=? AND completed=1), completed_at FROM automation_actions WHERE chapter_id=?", id, id).Scan(&completed, &at)
		if err != nil {
			return err
		}
		if completed && p.DeleteOnRead && m.Now().Unix() < at+int64(p.DeleteDelayHours)*3600 {
			continue
		}
		if completed && p.DeleteOnRead {
			if err := m.remove(ctx, id); err != nil {
				return err
			}
		}
		if _, err := m.db.ExecContext(ctx, "DELETE FROM automation_actions WHERE chapter_id=?", id); err != nil {
			return err
		}
	}
	return nil
}
func (m *Manager) DownloadCompleted(ctx context.Context, chapter int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch, err := m.q.GetChapter(ctx, chapter)
	if err != nil {
		return err
	}
	p, err := m.Policy(ctx, ch.MediaID)
	if err != nil || p.MaxKeepChapters == 0 {
		return err
	}
	rows, err := m.db.QueryContext(ctx, `SELECT c.id FROM chapters c JOIN downloads d ON d.chapter_id=c.id WHERE c.media_id=? AND d.status='done' ORDER BY c.source_order DESC,c.id DESC LIMIT -1 OFFSET ?`, ch.MediaID, p.MaxKeepChapters)
	if err != nil {
		return err
	}
	ids, err := readIDs(rows)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := m.remove(ctx, id); err != nil {
			return err
		}
	}
	return nil
}
func (m *Manager) remove(ctx context.Context, id int64) error {
	if err := m.dm.Cancel(ctx, id); err != nil {
		return err
	}
	if err := m.dm.DeleteChapterFiles(ctx, id); err != nil {
		return err
	}
	return m.q.DeleteDownloadByChapter(ctx, id)
}

// RefreshDue executes at most one overdue title per tick. Failed providers retry
// after five minutes, rather than on every foreground scheduler tick.
func (m *Manager) RefreshDue(ctx context.Context) error {
	if m.Refresh == nil {
		return nil
	}
	ids, err := m.q.ListUpdateTargetMediaIDs(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		p, err := m.Policy(ctx, id)
		if err != nil {
			return err
		}
		if p.PauseUpdates {
			continue
		}
		var interval time.Duration
		switch p.RefreshInterval {
		case "daily":
			interval = 24 * time.Hour
		case "weekly":
			interval = 7 * 24 * time.Hour
		default:
			continue
		}
		key := fmt.Sprintf("library:%d", id)
		var last int64
		err = m.db.QueryRowContext(ctx, "SELECT last_run FROM service_schedule WHERE name=?", key).Scan(&last)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if last > 0 && m.Now().Unix()-last < int64(interval/time.Second) {
			continue
		}
		attempted := m.Now().Unix()
		if _, err := m.db.ExecContext(ctx, "INSERT INTO service_schedule(name,last_run) VALUES(?,?) ON CONFLICT(name) DO UPDATE SET last_run=excluded.last_run", key, attempted); err != nil {
			return err
		}
		err = m.Refresh(ctx, id)
		if err != nil {
			_, _ = m.db.ExecContext(ctx, "UPDATE service_schedule SET last_run=? WHERE name=?", attempted-int64(interval/time.Second)+300, key)
		}
		return err
	}
	return nil
}
