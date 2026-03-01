// CLAUDE:SUMMARY Registers docpipe extract and detect handlers on a connectivity Router for inter-service RPC.
package docpipe

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hazyhaar/pkg/connectivity"
)

// RegisterConnectivity registers docpipe service handlers on a connectivity Router.
//
// Registered services:
//
//	docpipe_extract — extract content from a document file
//	docpipe_detect  — detect document format
func (p *Pipeline) RegisterConnectivity(router *connectivity.Router) {
	router.RegisterLocal("docpipe_extract", p.handleExtract)
	router.RegisterLocal("docpipe_detect", p.handleDetect)
}

func (p *Pipeline) handleExtract(ctx context.Context, payload []byte) ([]byte, error) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	doc, err := p.Extract(ctx, req.Path)
	if err != nil {
		return nil, err
	}
	return json.Marshal(doc)
}

func (p *Pipeline) handleDetect(_ context.Context, payload []byte) ([]byte, error) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	format, err := p.Detect(req.Path)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"format": string(format)})
}
