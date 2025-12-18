package docker

// Ready signals when the plugin is ready for use.
func (d *Docker) Ready() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.client != nil && d.connected
}
