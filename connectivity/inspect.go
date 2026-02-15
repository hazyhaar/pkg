package connectivity

import "iter"

// ServiceInfo describes a routed service as seen by the router at a point
// in time. The struct is a snapshot; the router may have reloaded since
// this was created.
type ServiceInfo struct {
	Name     string `json:"name"`
	Strategy string `json:"strategy"`
	Endpoint string `json:"endpoint"`
	HasLocal bool   `json:"has_local"`
}

// ListServices returns an iterator over all services known to the router.
// This includes services with remote routes (from SQLite) and services
// with local-only handlers (registered via RegisterLocal).
func (r *Router) ListServices() iter.Seq[ServiceInfo] {
	return func(yield func(ServiceInfo) bool) {
		r.mu.RLock()
		defer r.mu.RUnlock()

		seen := make(map[string]bool, len(r.routeSnap)+len(r.localHandlers))

		// Services from the routes table.
		for name, rt := range r.routeSnap {
			seen[name] = true
			_, hasLocal := r.localHandlers[name]
			if !yield(ServiceInfo{
				Name:     name,
				Strategy: rt.Strategy,
				Endpoint: rt.Endpoint,
				HasLocal: hasLocal,
			}) {
				return
			}
		}

		// Local-only services (not in routes table).
		for name := range r.localHandlers {
			if seen[name] {
				continue
			}
			if !yield(ServiceInfo{
				Name:     name,
				Strategy: "local",
				HasLocal: true,
			}) {
				return
			}
		}
	}
}

// Inspect returns detailed information about a single service.
// Returns ok=false if the service is not registered in any form.
func (r *Router) Inspect(service string) (info ServiceInfo, ok bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	rt, hasRoute := r.routeSnap[service]
	_, hasLocal := r.localHandlers[service]

	if !hasRoute && !hasLocal {
		return ServiceInfo{}, false
	}

	info = ServiceInfo{
		Name:     service,
		HasLocal: hasLocal,
	}

	if hasRoute {
		info.Strategy = rt.Strategy
		info.Endpoint = rt.Endpoint
	} else {
		info.Strategy = "local"
	}

	return info, true
}
