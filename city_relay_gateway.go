package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const cityRelayGatewayVersion = 1

var cityRelaySlugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

type cityRelayGatewayRoute struct {
	CityID   string `json:"cityId"`
	Slug     string `json:"slug"`
	Host     string `json:"host"`
	Upstream string `json:"upstream"`
}

type cityRelayGatewayRegistry struct {
	Version int                     `json:"version"`
	Routes  []cityRelayGatewayRoute `json:"routes"`
}

func cityRelayGatewayConfigured() bool {
	return os.Getenv("RELAY_CITY_GATEWAY_REGISTRY") != ""
}

func loadCityRelayGatewayRegistry(path string) (map[string]*httputil.ReverseProxy, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	limited := io.LimitReader(file, 64*1024+1)
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	var registry cityRelayGatewayRegistry
	if err := decoder.Decode(&registry); err != nil {
		return nil, errors.New("invalid city relay gateway registry")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("invalid trailing city relay gateway data")
	}
	if registry.Version != cityRelayGatewayVersion || len(registry.Routes) > 500 {
		return nil, errors.New("unsupported city relay gateway registry")
	}
	proxies := make(map[string]*httputil.ReverseProxy, len(registry.Routes))
	for _, route := range registry.Routes {
		if !uuidPattern.MatchString(route.CityID) || !cityRelaySlugPattern.MatchString(route.Slug) || len(route.Slug) > 63 {
			return nil, errors.New("invalid city relay gateway route identity")
		}
		host := route.Slug + ".bitcoinwalk.org"
		upstream := "http://bitcoinwalk-relay-" + route.Slug + ":3345"
		if route.Host != host || route.Upstream != upstream {
			return nil, errors.New("city relay gateway route is not canonical")
		}
		if _, exists := proxies[host]; exists {
			return nil, errors.New("duplicate city relay gateway hostname")
		}
		target, _ := url.Parse(upstream)
		proxy := httputil.NewSingleHostReverseProxy(target)
		proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "city relay unavailable", http.StatusBadGateway)
		}
		proxies[host] = proxy
	}
	return proxies, nil
}

func cityRelayGatewayHandler(path string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.ToLower(r.Host)
		if name, _, err := net.SplitHostPort(host); err == nil {
			host = name
		}
		if r.URL.Path == "/__bitcoinwalk_gateway_healthz" && host == "gateway.internal" {
			if _, err := loadCityRelayGatewayRegistry(path); err != nil {
				http.Error(w, "registry unavailable", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `{"status":"ok","mode":"city-relay-gateway"}`)
			return
		}
		proxies, err := loadCityRelayGatewayRegistry(path)
		if err != nil {
			http.Error(w, "city relay registry unavailable", http.StatusServiceUnavailable)
			return
		}
		proxy := proxies[host]
		if proxy == nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		proxy.ServeHTTP(w, r)
	})
}

func runCityRelayGateway() error {
	registry := os.Getenv("RELAY_CITY_GATEWAY_REGISTRY")
	if !strings.HasPrefix(registry, "/") || strings.Contains(registry, "..") {
		return errors.New("city relay gateway registry requires a clean absolute path")
	}
	if _, err := loadCityRelayGatewayRegistry(registry); err != nil {
		return fmt.Errorf("load city relay gateway registry: %w", err)
	}
	listen := env("RELAY_CITY_GATEWAY_LISTEN", "0.0.0.0:8080")
	host, _, err := net.SplitHostPort(listen)
	if err != nil || net.ParseIP(host) == nil || !(net.ParseIP(host).IsLoopback() || net.ParseIP(host).Equal(net.IPv4zero)) {
		return errors.New("invalid city relay gateway listen address")
	}
	server := &http.Server{Addr: listen, Handler: cityRelayGatewayHandler(registry), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	log.Printf("BitcoinWalk city relay gateway listening on %s", listen)
	select {
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
	return nil
}
