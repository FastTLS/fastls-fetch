package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	fastls "github.com/FastTLS/fastls"
	"github.com/FastTLS/fastls-fetch/internal/responsecontract"
)

func TestFetchResponseContractAndOperatorLimit(t *testing.T) {
	binary := []byte{0, 255, 128, 1}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	writer.Write([]byte(strings.Repeat("x", 4096)))
	writer.Close()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Set-Cookie", "a=1")
		w.Header().Add("Set-Cookie", "b=2")
		w.Header().Add("X-Repeat", "one")
		w.Header().Add("X-Repeat", "two")
		w.Header().Set("Trailer", "X-Final")
		switch r.URL.Path {
		case "/gzip":
			w.Header().Set("Content-Encoding", "gzip")
			w.Write(compressed.Bytes())
		case "/broken":
			w.Header().Set("Content-Encoding", "gzip")
			io.WriteString(w, "invalid gzip")
		default:
			w.Write(binary)
		}
		w.Header().Set("X-Final", "complete")
	}))
	defer target.Close()
	for _, test := range []struct {
		path    string
		maximum int64
		code    string
	}{{"/binary", 0, ""}, {"/binary", 4, ""}, {"/binary", 3, "response_too_large"}, {"/gzip", 128, "response_too_large"}, {"/broken", 0, "decode_error"}} {
		router, err := newFetchRouterWithLimits(fastls.NewClient(), responsecontract.Limits{MaxResponseBytes: test.maximum})
		if err != nil {
			t.Fatal(err)
		}
		payload, _ := json.Marshal(map[string]any{"url": target.URL + test.path, "include_body_base64": true})
		request := httptest.NewRequest("POST", "/fetch", bytes.NewReader(payload))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		var result FetchResponse
		if err = json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.ErrorCode != test.code || result.OK != (test.code == "") {
			t.Fatalf("%+v => %+v", test, result)
		}
		if test.code == "" {
			body, err := base64.StdEncoding.DecodeString(result.BodyBase64)
			if err != nil || !bytes.Equal(body, binary) {
				t.Fatalf("binary body changed: %q %v", body, err)
			}
			if !reflect.DeepEqual(result.HeaderValues.Values("X-Repeat"), []string{"one", "two"}) || len(result.HeaderValues.Values("Set-Cookie")) != 2 {
				t.Fatal("lost repeated headers", result.HeaderValues)
			}
			if result.Trailers.Get("X-Final") != "complete" || result.FinalURL != target.URL+test.path || result.Protocol != "HTTP/1.1" {
				t.Fatalf("metadata lost: %+v", result)
			}
		}
	}
}

func TestFetchConcurrencyLimitKeepsHealthAndReleasesOnCancel(t *testing.T) {
	started := make(chan struct{}, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/hold" {
			started <- struct{}{}
			<-r.Context().Done()
			return
		}
		io.WriteString(w, "ok")
	}))
	defer target.Close()
	router, err := newFetchRouterWithLimits(fastls.NewClient(), responsecontract.Limits{MaxInflight: 1})
	if err != nil {
		t.Fatal(err)
	}
	call := func(ctx context.Context, path string) *httptest.ResponseRecorder {
		payload, _ := json.Marshal(map[string]any{"url": target.URL + path})
		request := httptest.NewRequest("POST", "/fetch", bytes.NewReader(payload)).WithContext(ctx)
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		return recorder
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { call(ctx, "/hold"); close(done) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not start")
	}
	blocked := call(context.Background(), "/fast")
	var result FetchResponse
	json.Unmarshal(blocked.Body.Bytes(), &result)
	if blocked.Code != 503 || result.ErrorCode != "overloaded" {
		t.Fatalf("overload=%d %+v", blocked.Code, result)
	}
	health := httptest.NewRecorder()
	router.ServeHTTP(health, httptest.NewRequest("GET", "/health", nil))
	if health.Code != 200 {
		t.Fatal("health used fetch budget")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation retained slot")
	}
	fresh := call(context.Background(), "/fast")
	json.Unmarshal(fresh.Body.Bytes(), &result)
	if !result.OK {
		t.Fatalf("slot not released: %+v", result)
	}
}
