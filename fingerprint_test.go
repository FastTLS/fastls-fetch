package main

import (
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	fastls "github.com/FastTLS/fastls"
)

func TestBrowserSelectorsUseGeneratorProfiles(t *testing.T) {
	tests := map[string]string{
		"chrome":     "chrome/160/windows",
		"chrome120":  "chrome/120/windows",
		"edge155":    "edge/155/windows",
		"firefox147": "firefox/147/windows",
		"safari":     "safari/18.7.3/ipados",
		"opera":      "opera/145/windows",
		"unknown":    "firefox/158/windows",
	}
	for input, want := range tests {
		t.Run(input, func(t *testing.T) {
			if got := browserSelector(input); got != want {
				t.Fatalf("selector = %s, want %s", got, want)
			}
			options := fastls.Options{Headers: map[string]string{"X-Caller": "keep"}}
			if err := applyBrowserImitate(&options, input); err != nil {
				t.Fatal(err)
			}
			if options.Profile == nil && options.Fingerprint == nil {
				t.Fatal("generator did not apply a protocol profile")
			}
			if options.Headers["X-Caller"] != "keep" || options.UserAgent == "" {
				t.Fatalf("caller headers or generated user agent lost: %#v", options.Headers)
			}
		})
	}
}

func TestBrowserProfilePerformsHTTPSRequest(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.UserAgent() == "" {
			t.Error("generated user agent is empty")
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	options := fastls.Options{Timeout: 5, VerifyTLS: true, TLSRootCAs: roots}
	if err := applyBrowserImitate(&options, "chrome160"); err != nil {
		t.Fatal(err)
	}
	response, err := fastls.NewClient().Do(server.URL, options, http.MethodGet)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	defer response.Client.CloseIdleConnections()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.Status != http.StatusOK || string(body) != "ok" {
		t.Fatalf("status=%d body=%q err=%v", response.Status, body, err)
	}
}

func TestExplicitFingerprintStillWins(t *testing.T) {
	options := fastls.Options{}
	err := applyImitateConfig(&options, &RequestFingerprintConfig{JA3: "771,4865,0,29,0", Browser: "chrome", Override: true})
	if err != nil {
		t.Fatal(err)
	}
	if options.Fingerprint == nil || options.Fingerprint.Value() != "771,4865,0,29,0" || options.Profile != nil {
		t.Fatalf("explicit JA3 did not win: %#v", options)
	}
}
