package contentfilter

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

type SourceOverrides struct {
	Enabled bool    `json:"enabled"`
	Allowed []int64 `json:"allowed"`
	Blocked []int64 `json:"blocked"`
}

func LoadSourceOverrides(ctx context.Context, db *sql.DB) (SourceOverrides, error) {
	var result SourceOverrides
	var raw string
	err := db.QueryRowContext(ctx, "SELECT value FROM app_settings WHERE key='mobile_source_overrides'").Scan(&raw)
	if err == sql.ErrNoRows {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	err = json.Unmarshal([]byte(raw), &result)
	return result, err
}
func SaveSourceOverrides(ctx context.Context, db *sql.DB, value SourceOverrides) error {
	if len(value.Allowed)+len(value.Blocked) > 10000 {
		return fmt.Errorf("too many sources")
	}
	seen := map[int64]bool{}
	for _, ids := range [][]int64{value.Allowed, value.Blocked} {
		for _, id := range ids {
			if id <= 0 || seen[id] {
				return fmt.Errorf("invalid or duplicate source")
			}
			seen[id] = true
		}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, "INSERT INTO app_settings(key,value) VALUES('mobile_source_overrides',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", string(raw))
	return err
}

// Decision is -1 for blocked, 1 for allowed, and 0 for the global rules.
func (value SourceOverrides) Decision(id int64) int {
	if !value.Enabled {
		return 0
	}
	for _, source := range value.Blocked {
		if source == id {
			return -1
		}
	}
	for _, source := range value.Allowed {
		if source == id {
			return 1
		}
	}
	return 0
}
