package mobile

import (
	"tsunagu/backend/internal/config"
	"tsunagu/backend/internal/db/sqlcgen"
	"tsunagu/backend/internal/tracker"
)

func newTrackers(q *sqlcgen.Queries, c *config.Config, baseURL string) *tracker.Manager {
	return tracker.NewManager(q, c.AniListClientID, tracker.MALConfig{ClientID: c.MALClientID, ClientSecret: c.MALClientSecret, CallbackURL: baseURL + "/api/tracker/mal/callback"})
}
