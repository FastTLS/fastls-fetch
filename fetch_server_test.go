package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	fastls "github.com/FastTLS/fastls"
	"github.com/FastTLS/fastls-fetch/internal/testsupport"
	"github.com/gin-gonic/gin"
)

func TestFetchContractAgainstLocalTarget(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/echo":
			body, _ := io.ReadAll(r.Body)
			cookie, _ := r.Cookie("session")
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, strings.Join([]string{
				r.Method, string(body), r.Header.Get("X-Contract"), cookie.Value, r.UserAgent(),
			}, "|"))
		case "/gzip":
			w.Header().Set("Content-Encoding", "gzip")
			writer := gzip.NewWriter(w)
			_, _ = writer.Write([]byte("compressed-response"))
			_ = writer.Close()
		case "/redirect":
			http.Redirect(w, r, "/final", http.StatusFound)
		case "/final":
			_, _ = io.WriteString(w, "redirected")
		case "/metadata":
			_, _ = io.ReadAll(r.Body)
			w.Header().Set("Trailer", "X-Response-Trailer")
			_, _ = io.WriteString(w, r.Trailer.Get("X-Request-Trailer"))
			w.Header().Set("X-Response-Trailer", "response-value")
		default:
			http.NotFound(w, r)
		}
	}))
	defer target.Close()

	response, status := runFetchPayload(t, context.Background(), map[string]any{
		"url": target.URL + "/echo", "method": http.MethodPost, "body": "payload",
		"headers":   map[string]string{"X-Contract": "shared"},
		"cookies":   []map[string]string{{"name": "session", "value": "cookie-value"}},
		"userAgent": "contract-test/1.0",
	}, nil)
	if status != http.StatusOK || !response.OK || response.Body != "POST|payload|shared|cookie-value|contract-test/1.0" {
		t.Fatalf("unexpected echo response: status=%d response=%+v", status, response)
	}

	response, status = runFetch(t, context.Background(), FetchRequest{URL: target.URL + "/gzip"}, nil)
	if status != http.StatusOK || !response.OK || response.Body != "compressed-response" {
		t.Fatalf("unexpected gzip response: status=%d response=%+v", status, response)
	}

	response, status = runFetch(t, context.Background(), FetchRequest{
		URL: target.URL + "/redirect", DisableRedirect: true,
	}, nil)
	if status != http.StatusOK || !response.OK || response.Status != http.StatusFound {
		t.Fatalf("redirect was not disabled: status=%d response=%+v", status, response)
	}

	response, status = runFetch(t, context.Background(), FetchRequest{
		URL: target.URL + "/metadata", Method: http.MethodPost, Body: "payload",
		Trailers: http.Header{"X-Request-Trailer": {"request-value"}},
	}, nil)
	if status != http.StatusOK || !response.OK || response.Body != "request-value" ||
		response.Protocol != "HTTP/1.1" || response.FinalURL != target.URL+"/metadata" ||
		response.Trailers.Get("X-Response-Trailer") != "response-value" {
		t.Fatalf("unexpected metadata response: status=%d response=%+v", status, response)
	}
}

func TestFetchBodyWiresHTTP2Settings(t *testing.T) {
	response, status := runFetch(t, context.Background(), FetchRequest{
		URL: "https://example.test/", HTTP2SettingsString: "not-settings",
	}, nil)
	if status != http.StatusBadRequest || response.OK || !strings.Contains(response.Error, "HTTP/2") {
		t.Fatalf("unexpected response: status=%d response=%+v", status, response)
	}
	response, status = runFetch(t, context.Background(), FetchRequest{URL: "https://example.test/"}, map[string]string{
		"X-Mitm-H2Settings": "not-settings",
	})
	if status != http.StatusBadRequest || response.OK || !strings.Contains(response.Error, "HTTP/2") {
		t.Fatalf("unexpected control-header response: status=%d response=%+v", status, response)
	}
}

func TestFetchProxyPrecedence(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "proxied")
	}))
	defer target.Close()
	proxy := testsupport.NewConnectProxy(t)

	t.Run("parent_proxy wins over proxy", func(t *testing.T) {
		response, status := runFetch(t, context.Background(), FetchRequest{
			URL: target.URL, Proxy: "http://127.0.0.1:1", ParentProxy: proxy.URL(),
		}, nil)
		if status != http.StatusOK || !response.OK || response.Body != "proxied" {
			t.Fatalf("unexpected response: status=%d response=%+v", status, response)
		}
	})

	t.Run("control header wins over body", func(t *testing.T) {
		response, status := runFetch(t, context.Background(), FetchRequest{
			URL: target.URL, ParentProxy: "http://127.0.0.1:1",
		}, map[string]string{"X-Mitm-Proxy": proxy.URL()})
		if status != http.StatusOK || !response.OK || response.Body != "proxied" {
			t.Fatalf("unexpected response: status=%d response=%+v", status, response)
		}
	})

	if proxy.Connects() != 2 {
		t.Fatalf("CONNECT count = %d, want 2", proxy.Connects())
	}
}

func TestFetchHonorsCallerCancellation(t *testing.T) {
	started := make(chan struct{})
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer target.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan FetchResponse, 1)
	go func() {
		response, _ := runFetch(t, ctx, FetchRequest{URL: target.URL, Timeout: 5}, nil)
		done <- response
	}()
	select {
	case <-started:
		cancel()
	case <-time.After(3 * time.Second):
		t.Fatal("downstream request did not start")
	}
	select {
	case response := <-done:
		if response.OK || !strings.Contains(strings.ToLower(response.Error), "context canceled") {
			t.Fatalf("unexpected cancellation response: %+v", response)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled request did not return")
	}
}

func runFetch(t testing.TB, ctx context.Context, request FetchRequest, headers map[string]string) (FetchResponse, int) {
	t.Helper()
	return runFetchPayload(t, ctx, request, headers)
}

func runFetchPayload(t testing.TB, ctx context.Context, request any, headers map[string]string) (FetchResponse, int) {
	t.Helper()
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	httpRequest := httptest.NewRequest(http.MethodPost, "/fetch", bytes.NewReader(payload)).WithContext(ctx)
	httpRequest.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		httpRequest.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/fetch", func(c *gin.Context) { handleFetch(c, fastls.NewClient()) })
	engine.ServeHTTP(recorder, httpRequest)
	var response FetchResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response %q: %v", recorder.Body.String(), err)
	}
	return response, recorder.Code
}
