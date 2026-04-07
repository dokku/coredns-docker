package docker

// Ready signals when the plugin is ready for use.
func (d *Docker) Ready() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.client == nil {
		return false
	}
	if d.connected {
		return true
	}
	return !d.lastSyncTime.IsZero()
}
