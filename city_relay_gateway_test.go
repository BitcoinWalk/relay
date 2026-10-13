package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func gatewayRegistry(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "registry.json")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCityRelayGatewayRejectsNonCanonicalRoutes(t *testing.T) {
	city := "e62592ac-a5fc-42df-8f31-94416f6ee8d5"
	valid := `{"version":1,"routes":[{"cityId":"` + city + `","slug":"graz","host":"graz.bitcoinwalk.org","upstream":"http://bitcoinwalk-relay-graz:3345"}]}`
	if routes, err := loadCityRelayGatewayRegistry(gatewayRegistry(t, valid)); err != nil || routes["graz.bitcoinwalk.org"] == nil {
		t.Fatalf("valid registry rejected: %v", err)
	}
	for _, changed := range []string{
		strings.Replace(valid, "graz.bitcoinwalk.org", "evil.example", 1),
		strings.Replace(valid, "bitcoinwalk-relay-graz", "nginx", 1),
		strings.Replace(valid, `"slug":"graz"`, `"slug":"../graz"`, 1),
		strings.Replace(valid, `"version":1`, `"version":2`, 1),
		strings.Replace(valid, `}]}`, `,"extra":true}]}`, 1),
	} {
		if _, err := loadCityRelayGatewayRegistry(gatewayRegistry(t, changed)); err == nil {
			t.Fatal("changed registry was accepted")
		}
	}
}

func TestCityRelayGatewayFailsClosedForUnknownHostsAndBadRegistry(t *testing.T) {
	path := gatewayRegistry(t, `{"version":1,"routes":[]}`)
	handler := cityRelayGatewayHandler(path)
	request := httptest.NewRequest(http.MethodGet, "http://unknown.bitcoinwalk.org/", nil)
	request.Host = "unknown.bitcoinwalk.org"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("unknown host status = %d", response.Code)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"routes":`), 0600); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("bad registry status = %d", response.Code)
	}
}

func TestCityRelayGatewayHealthRequiresInternalHost(t *testing.T) {
	path := gatewayRegistry(t, `{"version":1,"routes":[]}`)
	handler := cityRelayGatewayHandler(path)
	for host, status := range map[string]int{"gateway.internal": http.StatusOK, "graz.bitcoinwalk.org": http.StatusNotFound} {
		request := httptest.NewRequest(http.MethodGet, "http://"+host+"/__bitcoinwalk_gateway_healthz", nil)
		request.Host = host
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != status {
			t.Fatalf("host %s status = %d", host, response.Code)
		}
	}
}
