package channels

import "iter"

// ChannelInfo describes an active channel as seen by the dispatcher at a
// point in time. The struct is a snapshot; the dispatcher may have reloaded
// since this was created.
type ChannelInfo struct {
	Name      string        `json:"name"`
	Platform  string        `json:"platform"`
	Status    ChannelStatus `json:"status"`
	Connected bool          `json:"connected"`
}

// ListChannels returns an iterator over all active channels in the dispatcher.
func (d *Dispatcher) ListChannels() iter.Seq[ChannelInfo] {
	return func(yield func(ChannelInfo) bool) {
		d.mu.RLock()
		defer d.mu.RUnlock()

		for name, entry := range d.channels {
			status := entry.channel.Status()
			if !yield(ChannelInfo{
				Name:      name,
				Platform:  entry.platform,
				Status:    status,
				Connected: status.Connected,
			}) {
				return
			}
		}
	}
}

// Inspect returns detailed information about a single active channel.
// Returns ok=false if the channel is not active in the dispatcher.
func (d *Dispatcher) Inspect(name string) (info ChannelInfo, ok bool) {
	d.mu.RLock()
	entry, exists := d.channels[name]
	d.mu.RUnlock()

	if !exists {
		return ChannelInfo{}, false
	}

	status := entry.channel.Status()
	return ChannelInfo{
		Name:      name,
		Platform:  entry.platform,
		Status:    status,
		Connected: status.Connected,
	}, true
}
