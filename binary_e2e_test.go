package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFetchBinaryContract(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_, _ = io.WriteString(w, strings.Join([]string{
			r.Method,
			string(body),
			r.Header.Get("X-Binary-Test"),
			r.UserAgent(),
		}, "|"))
	}))
	defer target.Close()

	binary := buildTestBinary(t, ".", "fastls-fetch")
	address := reserveLoopbackAddress(t)
	process := startTestBinary(t, binary, "-addr", address)
	waitForHTTPStatus(t, "http://"+address+"/health", http.StatusOK, process)

	payload, err := json.Marshal(FetchRequest{
		URL:       target.URL,
		Method:    http.MethodPost,
		Body:      "binary-payload",
		Headers:   map[string]string{"X-Binary-Test": "present"},
		UserAgent: "binary-client/1.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Post("http://"+address+"/fetch", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("binary fetch request failed: %v\n%s", err, process.logs())
	}
	defer response.Body.Close()
	var result FetchResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !result.OK || result.Body != "POST|binary-payload|present|binary-client/1.0" {
		t.Fatalf("binary response: HTTP %d %+v", response.StatusCode, result)
	}
}

type testBinaryProcess struct {
	command *exec.Cmd
	logPath string
	logFile *os.File
	stop    sync.Once
}

func buildTestBinary(t testing.TB, packagePath, name string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	command := exec.Command("go", "build", "-trimpath", "-o", path, packagePath)
	command.Env = append(os.Environ(), "GOWORK=off")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build test binary: %v\n%s", err, output)
	}
	return path
}

func reserveLoopbackAddress(t testing.TB) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func startTestBinary(t testing.TB, binary string, arguments ...string) *testBinaryProcess {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "process.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, arguments...)
	command.Stdout = logFile
	command.Stderr = logFile
	if err := command.Start(); err != nil {
		logFile.Close()
		t.Fatal(err)
	}
	process := &testBinaryProcess{command: command, logPath: logPath, logFile: logFile}
	t.Cleanup(process.close)
	return process
}

func (p *testBinaryProcess) close() {
	p.stop.Do(func() {
		if p.command.Process != nil {
			_ = p.command.Process.Kill()
			_, _ = p.command.Process.Wait()
		}
		_ = p.logFile.Close()
	})
}

func (p *testBinaryProcess) logs() string {
	p.close()
	data, _ := os.ReadFile(p.logPath)
	return string(data)
}

func waitForHTTPStatus(t testing.TB, endpoint string, expected int, process *testBinaryProcess) {
	t.Helper()
	var lastErr error
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		response, err := http.Get(endpoint)
		if err == nil {
			response.Body.Close()
			if response.StatusCode == expected {
				return
			}
			lastErr = &unexpectedHTTPStatusError{status: response.StatusCode}
		} else {
			lastErr = err
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("service did not become ready: %v\n%s", lastErr, process.logs())
}

type unexpectedHTTPStatusError struct{ status int }

func (e *unexpectedHTTPStatusError) Error() string {
	return http.StatusText(e.status)
}
