// CLAUDE:SUMMARY Registers horosembed handlers on connectivity.Router + EmbedFactory for TransportFactory dispatch.
// CLAUDE:DEPENDS github.com/hazyhaar/pkg/connectivity
// CLAUDE:EXPORTS RegisterConnectivity, EmbedFactory
package horosembed

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hazyhaar/pkg/connectivity"
)

// RegisterConnectivity registers horosembed service handlers on a connectivity Router.
//
// Registered services:
//
//	horosembed_embed — embed a single text
//	horosembed_batch — embed multiple texts
func RegisterConnectivity(router *connectivity.Router, emb Embedder) {
	router.RegisterLocal("horosembed_embed", handleEmbed(emb))
	router.RegisterLocal("horosembed_batch", handleBatch(emb))
}

// EmbedFactory returns a connectivity.TransportFactory that creates an
// openaiClient per endpoint. Use this to register the "embed" strategy
// on a connectivity Router so embedding backends can be switched at runtime
// via a single SQL UPDATE on the routes table.
//
//	router.RegisterTransport("embed", horosembed.EmbedFactory())
//	go router.Watch(ctx, routesDB, 200*time.Millisecond)
//	// Now router.Call(ctx, "embed", payload) dispatches to the configured backend.
func EmbedFactory() connectivity.TransportFactory {
	return func(endpoint string, config json.RawMessage) (connectivity.Handler, func(), error) {
		var cfg Config
		if len(config) > 0 {
			if err := json.Unmarshal(config, &cfg); err != nil {
				return nil, nil, fmt.Errorf("horosembed: parse config: %w", err)
			}
		}
		cfg.Endpoint = endpoint
		emb := New(cfg)
		return handleBatch(emb), nil, nil
	}
}

func handleEmbed(emb Embedder) connectivity.Handler {
	return func(ctx context.Context, payload []byte) ([]byte, error) {
		var req struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, fmt.Errorf("decode: %w", err)
		}
		vec, err := emb.Embed(ctx, req.Text)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{
			"vector":    vec,
			"dimension": len(vec),
			"model":     emb.Model(),
		})
	}
}

func handleBatch(emb Embedder) connectivity.Handler {
	return func(ctx context.Context, payload []byte) ([]byte, error) {
		var req struct {
			Texts []string `json:"texts"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, fmt.Errorf("decode: %w", err)
		}
		vecs, err := emb.EmbedBatch(ctx, req.Texts)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{
			"vectors":   vecs,
			"count":     len(vecs),
			"dimension": emb.Dimension(),
			"model":     emb.Model(),
		})
	}
}
