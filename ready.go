package docker

// Ready signals when the plugin is ready for use.
func (d *Docker) Ready() bool {
	return d.client != nil
}
