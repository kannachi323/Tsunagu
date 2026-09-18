package metadata

// SetProvider replaces or adds a provider; in-flight work keeps its current one.
func (m *Manager) SetProvider(provider Provider) {
	m.providerMu.Lock()
	defer m.providerMu.Unlock()
	m.providers[provider.Key()] = provider
}
func (m *Manager) provider(key string) (Provider, bool) {
	m.providerMu.RLock()
	defer m.providerMu.RUnlock()
	p, ok := m.providers[key]
	return p, ok
}
func (m *Manager) defaultProvider() Provider { p, _ := m.provider(DefaultProvider); return p }
