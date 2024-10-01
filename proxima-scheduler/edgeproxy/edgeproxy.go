package edgeproxy

import (
	"context"
	"log"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"
)

type EdgeProxy struct {
	proxy *httputil.ReverseProxy
}

func NewEdgeProxy() *EdgeProxy {
	return &EdgeProxy{
		proxy: &httputil.ReverseProxy{
			Director: func(req *http.Request) {
				// Set start time in the request context
				ctx := context.WithValue(req.Context(), "start_time", time.Now())
				req = req.WithContext(ctx)

				parts := strings.Split(req.URL.Path, "/")
				if len(parts) < 2 || parts[1] == "" {
					log.Println("Invalid path, cannot extract service name")
					req.URL.Host = "" // Force a failure by clearing the Host
					return
				}

				serviceName := parts[1] + ".default.svc.cluster.local"
				req.URL.Scheme = "http"
				req.URL.Host = serviceName
				log.Printf("Forwarding request to %s", serviceName)
			},
			ModifyResponse: func(resp *http.Response) error {
				// Measure latency and log it
				latency := time.Since(resp.Request.Context().Value("start_time").(time.Time))
				println(latency)
				// log latency
				return nil
			},
		},
	}
}

func (ep *EdgeProxy) Run() {
	// Start the proxy server in a goroutine
	go func() {
		log.Println("Edge proxy server is starting on port 8080...")

		// Set up the HTTP handler to use the proxy
		http.Handle("/", ep.proxy)

		// Start listening and serving on port 8080
		err := http.ListenAndServe(":8080", nil)
		if err != nil {
			log.Fatalf("Error starting proxy server: %v", err)
		}
	}()
}
