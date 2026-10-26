package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	fastls "github.com/FastTLS/fastls"
	"github.com/FastTLS/fastls-fetch/internal/responsecontract"
)

func TestConfiguredRequestLimitIncludesTrailingAndChunkedBytes(t *testing.T) {
	var calls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); io.WriteString(w, "ok") }))
	defer target.Close()
	payload := fmt.Sprintf(`{"url":%q}`, target.URL)
	router, err := newFetchRouterWithLimits(fastls.NewClient(), responsecontract.Limits{MaxRequestBytes: int64(len(payload)), MaxInflight: 1})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(router)
	defer server.Close()
	for _, chunked := range []bool{false, true} {
		for _, extra := range []int{0, 1, 8192, 0} {
			body := payload + strings.Repeat(" ", extra)
			request, _ := http.NewRequest("POST", server.URL+"/fetch", strings.NewReader(body))
			if chunked {
				request.ContentLength = -1
			}
			before := calls.Load()
			response, err := server.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(response.Body)
			response.Body.Close()
			want := http.StatusOK
			if extra > 0 {
				want = http.StatusRequestEntityTooLarge
			}
			if err != nil || response.StatusCode != want {
				t.Fatalf("chunked=%v extra=%d status=%d body=%s err=%v", chunked, extra, response.StatusCode, data, err)
			}
			if extra > 0 && calls.Load() != before {
				t.Fatal("oversized request reached target")
			}
		}
	}
	if calls.Load() != 4 {
		t.Fatal("request slots were not reusable", calls.Load())
	}
}
