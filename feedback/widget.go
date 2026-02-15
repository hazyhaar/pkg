package feedback

import (
	_ "embed"
	"net/http"
)

//go:embed widget.js
var widgetJS []byte

//go:embed widget.css
var widgetCSS []byte

func (w *Widget) handleWidgetJS(wr http.ResponseWriter, r *http.Request) {
	wr.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	wr.Header().Set("Cache-Control", "public, max-age=3600")
	wr.Write(widgetJS)
}

func (w *Widget) handleWidgetCSS(wr http.ResponseWriter, r *http.Request) {
	wr.Header().Set("Content-Type", "text/css; charset=utf-8")
	wr.Header().Set("Cache-Control", "public, max-age=3600")
	wr.Write(widgetCSS)
}
