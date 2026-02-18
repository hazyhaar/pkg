package channels

import "fmt"

// ErrChannelNotFound is returned when an operation targets a channel that
// does not exist in the channels table or the dispatcher's active set.
type ErrChannelNotFound struct {
	Channel string
}

func (e *ErrChannelNotFound) Error() string {
	return fmt.Sprintf("channels: channel not found: %s", e.Channel)
}

// ErrNoPlatformFactory is returned during reload when a channel's platform
// has no registered ChannelFactory.
type ErrNoPlatformFactory struct {
	Channel  string
	Platform string
}

func (e *ErrNoPlatformFactory) Error() string {
	return fmt.Sprintf("channels: no factory for platform %q (channel %s)", e.Platform, e.Channel)
}

// ErrChannelDisabled is returned when Send is called on a disabled channel.
type ErrChannelDisabled struct {
	Channel string
}

func (e *ErrChannelDisabled) Error() string {
	return fmt.Sprintf("channels: channel disabled: %s", e.Channel)
}

// ErrSendFailed is returned when a message could not be delivered to the
// platform.
type ErrSendFailed struct {
	Channel  string
	Platform string
	Cause    error
}

func (e *ErrSendFailed) Error() string {
	return fmt.Sprintf("channels: send failed on %s (%s): %v", e.Channel, e.Platform, e.Cause)
}

func (e *ErrSendFailed) Unwrap() error { return e.Cause }
