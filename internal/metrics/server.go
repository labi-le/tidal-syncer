package metrics

import (
	"net/http"
	"time"
)

// MetricsPath is the route the Prometheus handler is served on.
const MetricsPath = "/metrics"

const (
	// serverReadHeaderTimeout bounds how long a client may take to send request
	// headers, guarding the scrape endpoint against slow-loris connections.
	serverReadHeaderTimeout = 5 * time.Second
	// serverReadTimeout bounds the whole request read.
	serverReadTimeout = 10 * time.Second
)

// rootPage is the minimal landing page linking to the metrics endpoint.
const rootPage = `<!doctype html><title>tidal-syncer</title>` +
	`<h1>tidal-syncer</h1><p><a href="` + MetricsPath + `">metrics</a></p>`

// NewServer builds the HTTP server that serves h at [MetricsPath] with a small
// landing page at the root and hardened read timeouts. The caller owns its
// lifecycle (ListenAndServe / Shutdown).
func NewServer(addr string, h http.Handler) *http.Server {
	mux := http.NewServeMux()
	mux.Handle(MetricsPath, h)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)

			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(rootPage))
	})

	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: serverReadHeaderTimeout,
		ReadTimeout:       serverReadTimeout,
	}
}
