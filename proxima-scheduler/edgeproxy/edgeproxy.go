package edgeproxy

import (
	"context"
	"log"
	"net/http"
	"net/http/httputil"
	"time"
)

type EdgeProxy struct {
	proxy *httputil.ReverseProxy
}

func NewEdgeProxy() *EdgeProxy {
	return &EdgeProxy{
		proxy: &httputil.ReverseProxy{
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

func (ep *EdgeProxy) latencyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		ctx = context.WithValue(ctx, "start_time", time.Now())
		r = r.WithContext(ctx)
		next.ServeHTTP(w, r)
	})
}

func (ep *EdgeProxy) Run() {
	go func() {
		log.Println("Edge proxy server is starting on port 8080...")

		// Use the proxy with latency middleware
		http.Handle("/", ep.latencyMiddleware(ep.proxy))

		// Start the proxy server
		err := http.ListenAndServe(":8080", nil)
		if err != nil {
			log.Fatalf("Error starting proxy server: %v", err)
		}
	}()
}
