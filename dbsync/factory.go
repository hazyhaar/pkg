package dbsync

import (
	"context"
	"encoding/json"

	"github.com/hazyhaar/pkg/connectivity"
)

// DBSyncFactory returns a connectivity.TransportFactory for the "dbsync"
// strategy. When the router calls a dbsync route, it returns the status of
// the sync (last version, hash, etc.) rather than performing a data operation.
//
// This allows the LLM to check sync health via:
//
//	resp, _ := router.Call(ctx, "dbsync:fo-1", nil)
//	// resp contains {"last_version": N, "status": "ok"}
//
// The factory requires a reference to the Publisher so it can report status.
func DBSyncFactory(pub *Publisher) connectivity.TransportFactory {
	return func(endpoint string, config json.RawMessage) (connectivity.Handler, func(), error) {
		handler := func(ctx context.Context, payload []byte) ([]byte, error) {
			status := pub.Status()
			status["endpoint"] = endpoint
			status["status"] = "ok"

			resp, err := json.Marshal(status)
			if err != nil {
				return nil, err
			}
			return resp, nil
		}

		return handler, nil, nil
	}
}

// SubscriberFactory returns a connectivity.TransportFactory for FO-side
// status reporting. Similar to DBSyncFactory but reports subscriber state.
func SubscriberFactory(sub *Subscriber) connectivity.TransportFactory {
	return func(endpoint string, config json.RawMessage) (connectivity.Handler, func(), error) {
		handler := func(ctx context.Context, payload []byte) ([]byte, error) {
			status := sub.Status()
			status["status"] = "ok"

			resp, err := json.Marshal(status)
			if err != nil {
				return nil, err
			}
			return resp, nil
		}

		return handler, nil, nil
	}
}
