package mobile

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strings"
	"time"
)

type browserSession struct {
	ID          string `json:"id"`
	ExtensionID string `json:"extensionId"`
	URL         string `json:"url"`
	State       string `json:"state"`
	expires     time.Time
}

// BeginBrowserVerification returns public session metadata, never cookies.
func (b *Backend) BeginBrowserVerification(extensionID, target string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.services == nil || b.sc == nil || b.server == nil {
		return "", errors.New("source runtime is unavailable")
	}
	parsed, err := url.Parse(target)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Hostname() == "" || parsed.User != nil {
		return "", errors.New("invalid source URL")
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return "", errors.New("browser verification requires a public source URL")
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast()) {
		return "", errors.New("browser verification requires a public source URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := b.services.Q.GetExtensionByPackageName(ctx, extensionID); err != nil {
		return "", errors.New("unknown source")
	}
	if b.browserSessions == nil {
		b.browserSessions = map[string]*browserSession{}
	}
	for id, session := range b.browserSessions {
		if time.Now().After(session.expires) {
			delete(b.browserSessions, id)
		}
	}
	if len(b.browserSessions) >= 8 {
		return "", errors.New("too many pending browser sessions")
	}
	secret := make([]byte, 16)
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	session := &browserSession{ID: hex.EncodeToString(secret), ExtensionID: extensionID, URL: target, State: "pending", expires: time.Now().Add(15 * time.Minute)}
	b.browserSessions[session.ID] = session
	body, err := json.Marshal(session)
	return string(body), err
}

// BrowserCookies is a native-only bridge. Product APIs must never expose its result.
func (b *Backend) BrowserCookies(id string) (string, error) { return b.browserCookies(id, "", false) }
func (b *Backend) CompleteBrowserVerification(id, stateJSON string) error {
	_, err := b.browserCookies(id, stateJSON, true)
	return err
}
func (b *Backend) browserCookies(id, state string, apply bool) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	session := b.browserSessions[id]
	if session == nil || session.State != "pending" || time.Now().After(session.expires) || b.sc == nil {
		return "", errors.New("browser session expired or unavailable")
	}
	if len(state) > 262144 {
		return "", errors.New("browser state is too large")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := b.sc.Ensure(ctx)
	if err != nil {
		return "", err
	}
	result, err := client.BrowserCookies(ctx, session.URL, state, apply)
	if err == nil && apply {
		delete(b.browserSessions, id)
	}
	return result, err
}
func (b *Backend) CancelBrowserVerification(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.browserSessions, id)
}
