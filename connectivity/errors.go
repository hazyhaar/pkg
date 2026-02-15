package connectivity

import "fmt"

// ErrServiceNotFound is returned when Call targets a service with no route
// and no local handler.
type ErrServiceNotFound struct {
	Service string
}

func (e *ErrServiceNotFound) Error() string {
	return fmt.Sprintf("connectivity: service not routable: %s", e.Service)
}

// ErrNoFactory is returned during Reload when a route's strategy has no
// registered TransportFactory.
type ErrNoFactory struct {
	Service  string
	Strategy string
}

func (e *ErrNoFactory) Error() string {
	return fmt.Sprintf("connectivity: no transport factory for strategy %q (service %s)", e.Strategy, e.Service)
}

// ErrFactoryFailed is returned when a TransportFactory returns an error
// while building a handler for a route.
type ErrFactoryFailed struct {
	Service  string
	Strategy string
	Endpoint string
	Cause    error
}

func (e *ErrFactoryFailed) Error() string {
	return fmt.Sprintf("connectivity: factory %q failed for service %s (endpoint %s): %v",
		e.Strategy, e.Service, e.Endpoint, e.Cause)
}

func (e *ErrFactoryFailed) Unwrap() error { return e.Cause }

// ErrCallTimeout is returned when a remote call exceeds its configured
// timeout_ms from the route's config JSON.
type ErrCallTimeout struct {
	Service string
}

func (e *ErrCallTimeout) Error() string {
	return fmt.Sprintf("connectivity: call timeout: %s", e.Service)
}

// ErrCircuitOpen is returned when the circuit breaker for a service is open,
// rejecting the call without attempting the remote handler.
type ErrCircuitOpen struct {
	Service string
}

func (e *ErrCircuitOpen) Error() string {
	return fmt.Sprintf("connectivity: circuit open: %s", e.Service)
}
