package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	fastls "github.com/FastTLS/fastls"
	"github.com/gin-gonic/gin"
)

func TestFetchCORSPreflightAllowsControlHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	request := httptest.NewRequest(http.MethodOptions, "/fetch", nil)
	request.Header.Set("Origin", "https://app.example.test")
	request.Header.Set("Access-Control-Request-Headers", "content-type,x-mitm-browser,x-mitm-proxy")
	recorder := httptest.NewRecorder()
	newFetchRouter(fastls.NewClient()).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d", recorder.Code)
	}
	if origin := recorder.Header().Get("Access-Control-Allow-Origin"); origin != "https://app.example.test" {
		t.Fatalf("allow origin = %q", origin)
	}
	if credentials := recorder.Header().Get("Access-Control-Allow-Credentials"); credentials != "true" {
		t.Fatalf("allow credentials = %q", credentials)
	}
	if vary := recorder.Header().Values("Vary"); !containsHeaderToken(vary, "Origin") {
		t.Fatalf("Vary = %v", vary)
	}
	for _, token := range []string{"Access-Control-Request-Method", "Access-Control-Request-Headers"} {
		if vary := recorder.Header().Values("Vary"); !containsHeaderToken(vary, token) {
			t.Fatalf("Vary is missing %q: %v", token, vary)
		}
	}
	allowedHeaders := strings.ToLower(recorder.Header().Get("Access-Control-Allow-Headers"))
	for _, header := range []string{"x-mitm-browser", "x-mitm-ja3", "x-mitm-ja4r", "x-mitm-h2settings", "x-mitm-proxy"} {
		if !strings.Contains(allowedHeaders, header) {
			t.Fatalf("control header %q missing from %q", header, allowedHeaders)
		}
	}
}

func TestFetchRejectsOversizedRequestBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	payload := `{"url":"https://example.test/","body":"` + strings.Repeat("x", maxRequestBodySize) + `"}`
	request := httptest.NewRequest(http.MethodPost, "/fetch", strings.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	newFetchRouter(fastls.NewClient()).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status = %d, body=%q", recorder.Code, recorder.Body.String())
	}
	var response FetchResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.OK || !strings.Contains(response.Error, "4194304") {
		t.Fatalf("oversized response = %+v", response)
	}
}

func containsHeaderToken(values []string, token string) bool {
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}
