package edgeproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"
)

type ConsulServiceInstance struct {
	Service struct {
		Address string `json:"Address"`
		Port    int    `json:"Port"`
	} `json:"Service"`
}

type EdgeProxy struct {
	proxy         *httputil.ReverseProxy
	consulAddress string
}

func NewEdgeProxy(consulAddress string) *EdgeProxy {
	return &EdgeProxy{
		proxy: &httputil.ReverseProxy{
			Director: func(req *http.Request) {
				podUrl := req.Context().Value("pod_url").(string)

				// Adjust the path
				parts := strings.Split(req.URL.Path, "/")
				if len(parts) > 2 {
					req.URL.Path = "/" + strings.Join(parts[2:], "/")
				}

				// Forward the request to the pod
				req.URL.Scheme = "http"
				req.URL.Host = podUrl
				log.Printf("Forwarding request to %s", podUrl)
			},
			ModifyResponse: func(resp *http.Response) error {
				// Measure latency and log it
				latency := time.Since(resp.Request.Context().Value("start_time").(time.Time))
				println(latency)
				// log latency
				return nil
			},
		},
		consulAddress: consulAddress,
	}
}

func getServicePodsFromConsul(serviceName string, consulURL string) ([]ConsulServiceInstance, error) {
	resp, err := http.Get(fmt.Sprintf("%s/v1/health/service/%s?passing=true", consulURL, serviceName))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var pods []ConsulServiceInstance
	err = json.Unmarshal(body, &pods)
	if err != nil {
		return nil, err
	}

	return pods, nil
}

func preprocessRequest(consulAddress string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(r.URL.Path, "/")
		if len(parts) < 2 || parts[1] == "" {
			http.Error(w, "Invalid request path", http.StatusBadRequest)
			return
		}

		serviceName := parts[1]

		pods, err := getServicePodsFromConsul(serviceName, consulAddress)
		if err != nil || len(pods) == 0 {
			http.Error(w, "Failed to obtain pods from Consul", http.StatusInternalServerError)
			return
		}

		pod := pods[0]
		podUrl := fmt.Sprintf("%s:%d", pod.Service.Address, pod.Service.Port)

		ctx := context.WithValue(r.Context(), "pod_url", podUrl)
		ctx = context.WithValue(ctx, "start_time", time.Now())

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (ep *EdgeProxy) Run() {
	// Start the proxy server in a goroutine
	go func() {
		log.Println("Edge proxy server is starting on port 8080...")
		log.Printf("Consul instance on %s...", ep.consulAddress)
		http.Handle("/", preprocessRequest(ep.consulAddress, ep.proxy))

		err := http.ListenAndServe(":8080", nil)
		if err != nil {
			log.Fatalf("Error starting proxy server: %v", err)
		}
	}()
}
