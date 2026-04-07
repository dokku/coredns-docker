package docker

import "time"

// Ready signals when the plugin is ready for use.
func (d *Docker) Ready() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.client == nil {
		log.Debugf("Ready check: not ready (no Docker client)")
		return false
	}
	if d.connected {
		log.Debugf("Ready check: ready (connected to Docker daemon)")
		return true
	}
	ready := !d.lastSyncTime.IsZero()
	if ready {
		log.Debugf("Ready check: ready (serving stale records, last sync: %s)", d.lastSyncTime.Format(time.RFC3339))
	} else {
		log.Debugf("Ready check: not ready (disconnected, no previous sync)")
	}
	return ready
}
