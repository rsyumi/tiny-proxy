package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func withProxyTestConfig(t *testing.T) {
	t.Helper()

	oldAuthMode := authMode
	oldWhitelist := whitelist
	oldWLPatterns := wlPatterns
	oldHURL := hURL
	oldHMethod := hMethod
	oldHTransform := hTransform

	authMode = "none"
	whitelist = nil
	wlPatterns = nil
	hURL = http.CanonicalHeaderKey("X-Proxy-Url")
	hMethod = http.CanonicalHeaderKey("X-Test-Method")
	hTransform = http.CanonicalHeaderKey("X-Test-Body-Transform")

	t.Cleanup(func() {
		authMode = oldAuthMode
		whitelist = oldWhitelist
		wlPatterns = oldWLPatterns
		hURL = oldHURL
		hMethod = oldHMethod
		hTransform = oldHTransform
	})
}

func TestHandleTransformsJSONBodyToFormURLEncodedWhenRequested(t *testing.T) {
	withProxyTestConfig(t)

	var gotContentType string
	var gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read upstream body: %v", err)
		}
		gotBody = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	req := httptest.NewRequest(http.MethodPost, "http://proxy.test/", strings.NewReader(`{
		"grant_type": "client_credentials",
		"client_id": "abc",
		"scope": ["read", "write"],
		"enabled": true,
		"count": 3
	}`))
	req.Header.Set(hURL, upstream.URL)
	req.Header.Set(hTransform, "json-to-form-urlencoded")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handle(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("proxy status = %d, want %d", rr.Code, http.StatusNoContent)
	}
	if gotContentType != "application/x-www-form-urlencoded" {
		t.Fatalf("upstream content-type = %q, want application/x-www-form-urlencoded", gotContentType)
	}
	values, err := url.ParseQuery(gotBody)
	if err != nil {
		t.Fatalf("parse upstream form body %q: %v", gotBody, err)
	}
	assertFormValues(t, values, "grant_type", []string{"client_credentials"})
	assertFormValues(t, values, "client_id", []string{"abc"})
	assertFormValues(t, values, "scope", []string{"read", "write"})
	assertFormValues(t, values, "enabled", []string{"true"})
	assertFormValues(t, values, "count", []string{"3"})
}

func TestHandleRejectsNestedJSONForFormTransform(t *testing.T) {
	withProxyTestConfig(t)

	upstreamCalled := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	req := httptest.NewRequest(http.MethodPost, "http://proxy.test/", strings.NewReader(`{"outer":{"inner":"value"}}`))
	req.Header.Set(hURL, upstream.URL)
	req.Header.Set(hTransform, "json-to-form-urlencoded")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handle(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("proxy status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
	if upstreamCalled {
		t.Fatal("upstream was called for unsupported nested JSON")
	}
}

func TestHandleRejectsOversizedJSONForFormTransform(t *testing.T) {
	withProxyTestConfig(t)

	upstreamCalled := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	req := httptest.NewRequest(http.MethodPost, "http://proxy.test/", strings.NewReader(`{"assertion":"`+strings.Repeat("a", maxTransformBodyBytes+1)+`"}`))
	req.Header.Set(hURL, upstream.URL)
	req.Header.Set(hTransform, "json-to-form-urlencoded")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handle(rr, req)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("proxy status = %d, want %d", rr.Code, http.StatusRequestEntityTooLarge)
	}
	if upstreamCalled {
		t.Fatal("upstream was called for oversized transform body")
	}
}

func TestHandleLeavesBodyUntouchedWithoutTransformHeader(t *testing.T) {
	withProxyTestConfig(t)

	var gotContentType string
	var gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read upstream body: %v", err)
		}
		gotBody = string(b)
		w.WriteHeader(http.StatusCreated)
	}))
	defer upstream.Close()

	req := httptest.NewRequest(http.MethodPost, "http://proxy.test/", strings.NewReader(`{"raw":true}`))
	req.Header.Set(hURL, upstream.URL)
	req.Header.Set(hPrefix+"Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handle(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("proxy status = %d, want %d", rr.Code, http.StatusCreated)
	}
	if gotContentType != "application/json" {
		t.Fatalf("upstream content-type = %q, want application/json", gotContentType)
	}
	if gotBody != `{"raw":true}` {
		t.Fatalf("upstream body = %q, want raw JSON body", gotBody)
	}
}

func TestHandleUsesProxyMethodHeaderForUpstreamRequest(t *testing.T) {
	withProxyTestConfig(t)

	var gotMethod string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	req := httptest.NewRequest(http.MethodPost, "http://proxy.test/", nil)
	req.Header.Set(hURL, upstream.URL)
	req.Header.Set(hMethod, http.MethodPatch)
	rr := httptest.NewRecorder()

	handle(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("proxy status = %d, want %d", rr.Code, http.StatusNoContent)
	}
	if gotMethod != http.MethodPatch {
		t.Fatalf("upstream method = %q, want %q", gotMethod, http.MethodPatch)
	}
}

// upstreamCapture records how the upstream saw the proxied request body.
type upstreamCapture struct {
	length   int64
	encoding []string
	body     string
}

func newCapturingUpstream(t *testing.T, got *upstreamCapture) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.length = r.ContentLength
		got.encoding = r.TransferEncoding
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read upstream body: %v", err)
		}
		got.body = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestHandleForwardsRequestContentLength(t *testing.T) {
	withProxyTestConfig(t)

	var got upstreamCapture
	upstream := newCapturingUpstream(t, &got)

	const payload = `{"raw":true}`
	req := httptest.NewRequest(http.MethodPost, "http://proxy.test/", strings.NewReader(payload))
	req.Header.Set(hURL, upstream.URL)
	rr := httptest.NewRecorder()

	handle(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("proxy status = %d, want %d", rr.Code, http.StatusNoContent)
	}
	if got.length != int64(len(payload)) {
		t.Fatalf("upstream content-length = %d, want %d", got.length, len(payload))
	}
	if len(got.encoding) != 0 {
		t.Fatalf("upstream transfer-encoding = %v, want none", got.encoding)
	}
	if got.body != payload {
		t.Fatalf("upstream body = %q, want %q", got.body, payload)
	}
}

func TestHandleKeepsUnknownLengthBodyChunked(t *testing.T) {
	withProxyTestConfig(t)

	var got upstreamCapture
	upstream := newCapturingUpstream(t, &got)

	// A reader of unknown type makes httptest report ContentLength -1,
	// which is what a chunked client upload looks like.
	const payload = `{"raw":true}`
	req := httptest.NewRequest(http.MethodPost, "http://proxy.test/", struct{ io.Reader }{strings.NewReader(payload)})
	req.Header.Set(hURL, upstream.URL)
	rr := httptest.NewRecorder()

	handle(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("proxy status = %d, want %d", rr.Code, http.StatusNoContent)
	}
	if got.length != -1 {
		t.Fatalf("upstream content-length = %d, want -1", got.length)
	}
	if len(got.encoding) != 1 || got.encoding[0] != "chunked" {
		t.Fatalf("upstream transfer-encoding = %v, want [chunked]", got.encoding)
	}
	if got.body != payload {
		t.Fatalf("upstream body = %q, want %q", got.body, payload)
	}
}

func TestHandleSendsTransformedBodyLength(t *testing.T) {
	withProxyTestConfig(t)

	var got upstreamCapture
	upstream := newCapturingUpstream(t, &got)

	// The JSON body is longer than its form encoding, so forwarding the
	// client's length would be wrong here.
	const payload = `{ "grant_type" : "client_credentials" }`
	const want = "grant_type=client_credentials"
	req := httptest.NewRequest(http.MethodPost, "http://proxy.test/", strings.NewReader(payload))
	req.Header.Set(hURL, upstream.URL)
	req.Header.Set(hTransform, "json-to-form-urlencoded")
	rr := httptest.NewRecorder()

	handle(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("proxy status = %d, want %d", rr.Code, http.StatusNoContent)
	}
	if got.body != want {
		t.Fatalf("upstream body = %q, want %q", got.body, want)
	}
	if got.length != int64(len(want)) {
		t.Fatalf("upstream content-length = %d, want %d", got.length, len(want))
	}
	if len(got.encoding) != 0 {
		t.Fatalf("upstream transfer-encoding = %v, want none", got.encoding)
	}
}

func TestHandleStreamsResponseLargerThanBuffer(t *testing.T) {
	withProxyTestConfig(t)

	want := make([]byte, 3*streamBufSize+123)
	for i := range want {
		want[i] = byte(i % 251)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(want)
	}))
	defer upstream.Close()

	req := httptest.NewRequest(http.MethodGet, "http://proxy.test/", nil)
	req.Header.Set(hURL, upstream.URL)
	rr := httptest.NewRecorder()

	handle(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("proxy status = %d, want %d", rr.Code, http.StatusOK)
	}
	if !bytes.Equal(rr.Body.Bytes(), want) {
		t.Fatalf("proxied body differs from upstream body: got %d bytes, want %d", rr.Body.Len(), len(want))
	}
}

func TestResolveMaxProcs(t *testing.T) {
	tests := []struct {
		name    string
		setting string
		current int
		want    int
	}{
		{"unset on many cores is capped", "", 32, defaultMaxProcs},
		{"unset on one core stays at one", "", 1, 1},
		{"explicit value below the cap", "1", 32, 1},
		{"explicit value above the cap is honored", "8", 32, 8},
		{"non-numeric setting falls back to the cap", "abc", 32, defaultMaxProcs},
		{"zero falls back to the cap", "0", 4, defaultMaxProcs},
		{"negative falls back to the current value", "-1", 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveMaxProcs(tt.setting, tt.current); got != tt.want {
				t.Fatalf("resolveMaxProcs(%q, %d) = %d, want %d", tt.setting, tt.current, got, tt.want)
			}
		})
	}
}

func TestListenAddr(t *testing.T) {
	tests := []struct {
		name, host, port, want string
	}{
		{"empty host listens on all interfaces", "", "8080", ":8080"},
		{"IPv4 host", "127.0.0.1", "8080", "127.0.0.1:8080"},
		{"hostname", "localhost", "9000", "localhost:9000"},
		{"bare IPv6 host", "::1", "8080", "[::1]:8080"},
		{"bracketed IPv6 host", "[::1]", "8080", "[::1]:8080"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := listenAddr(tt.host, tt.port); got != tt.want {
				t.Fatalf("listenAddr(%q, %q) = %q, want %q", tt.host, tt.port, got, tt.want)
			}
		})
	}
}

func TestEnvDuration(t *testing.T) {
	const key = "TINY_PROXY_TEST_DURATION"
	const fallback = 7 * time.Second
	tests := []struct {
		name  string
		value string
		want  time.Duration
	}{
		{"empty uses the default", "", fallback},
		{"valid duration", "5s", 5 * time.Second},
		{"zero is allowed", "0", 0},
		{"negative falls back to the default", "-1s", fallback},
		{"malformed falls back to the default", "abc", fallback},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Always set the variable, even to "", so a value in the
			// caller's environment cannot leak into the test.
			t.Setenv(key, tt.value)
			if got := envDuration(key, fallback); got != tt.want {
				t.Fatalf("envDuration(%q) = %s, want %s", tt.value, got, tt.want)
			}
		})
	}
}

func TestHealthReportsUnavailableWhileDraining(t *testing.T) {
	t.Cleanup(func() { draining.Store(false) })

	rr := httptest.NewRecorder()
	handle(rr, httptest.NewRequest(http.MethodGet, "http://proxy.test/_health", nil))
	if rr.Code != http.StatusOK || rr.Body.String() != "ok" {
		t.Fatalf("health before shutdown = %d %q, want 200 \"ok\"", rr.Code, rr.Body.String())
	}

	draining.Store(true)
	rr = httptest.NewRecorder()
	handle(rr, httptest.NewRequest(http.MethodGet, "http://proxy.test/_health", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("health while draining = %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}
}

func TestShutdownWaitsForInFlightStream(t *testing.T) {
	release := make(chan struct{})
	upstream := newStreamingUpstream(t, release)
	proxy := startDrainTestProxy(t)
	body := startStream(t, proxy, upstream.URL)

	done := make(chan error, 1)
	go func() { done <- shutdown(proxy.Config, 0, 5*time.Second) }()

	waitUntilNotAccepting(t, proxy.Listener.Addr().String())
	select {
	case err := <-done:
		t.Fatalf("shutdown returned %v while a stream was still open", err)
	default:
	}

	close(release)
	rest, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read rest of stream: %v", err)
	}
	if string(rest) != "second\n" {
		t.Fatalf("rest of stream = %q, want %q", rest, "second\n")
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("shutdown = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not return after the stream ended")
	}
}

func TestShutdownClosesStreamsAfterTimeout(t *testing.T) {
	upstream := newStreamingUpstream(t, nil) // never sends the second part
	proxy := startDrainTestProxy(t)
	body := startStream(t, proxy, upstream.URL)

	err := shutdown(proxy.Config, 0, 100*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown = %v, want %v", err, context.DeadlineExceeded)
	}

	readErr := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(body)
		readErr <- err
	}()
	select {
	case err := <-readErr:
		if err == nil {
			t.Fatal("stream ended cleanly, want it cut off")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream still open after shutdown timed out")
	}
}

func TestShutdownKeepsServingDuringDelay(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "served")
	}))
	t.Cleanup(upstream.Close)
	proxy := startDrainTestProxy(t)

	done := make(chan error, 1)
	go func() { done <- shutdown(proxy.Config, time.Second, time.Second) }()

	deadline := time.Now().Add(5 * time.Second)
	for !draining.Load() {
		if time.Now().After(deadline) {
			t.Fatal("draining was never set")
		}
		time.Sleep(time.Millisecond)
	}

	resp, err := proxy.Client().Get(proxy.URL + "/_health")
	if err != nil {
		t.Fatalf("health check during the delay: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("health during the delay = %d, want %d", resp.StatusCode, http.StatusServiceUnavailable)
	}

	// Only /_health reports draining; proxied requests are still served.
	req, err := http.NewRequest(http.MethodGet, proxy.URL, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set(hURL, upstream.URL)
	resp, err = proxy.Client().Do(req)
	if err != nil {
		t.Fatalf("proxy request during the delay: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read proxied body: %v", err)
	}
	if resp.StatusCode != http.StatusOK || string(body) != "served" {
		t.Fatalf("proxy during the delay = %d %q, want 200 %q", resp.StatusCode, body, "served")
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("shutdown = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not return")
	}
	if conn, err := net.DialTimeout("tcp", proxy.Listener.Addr().String(), time.Second); err == nil {
		conn.Close()
		t.Fatal("still accepting connections after shutdown")
	}
}

// startDrainTestProxy serves handle on a real listener so shutdown can be
// exercised end to end, and clears the draining flag afterwards.
func startDrainTestProxy(t *testing.T) *httptest.Server {
	t.Helper()
	withProxyTestConfig(t)

	proxy := httptest.NewServer(http.HandlerFunc(handle))
	t.Cleanup(func() {
		proxy.Close()
		draining.Store(false)
	})
	return proxy
}

// newStreamingUpstream flushes "first\n" and sends "second\n" once release is
// closed. A nil release keeps the stream open until the request is cancelled.
func newStreamingUpstream(t *testing.T, release <-chan struct{}) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "first\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
			io.WriteString(w, "second\n")
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// startStream opens a proxied stream and returns its body after the first
// line has arrived, so the request is known to be in flight.
func startStream(t *testing.T, proxy *httptest.Server, upstreamURL string) *bufio.Reader {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, proxy.URL, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set(hURL, upstreamURL)
	resp, err := proxy.Client().Do(req)
	if err != nil {
		t.Fatalf("proxy request: %v", err)
	}
	// Runs before the server cleanups, so a failed test does not leave
	// handlers blocked on an open stream.
	t.Cleanup(func() { resp.Body.Close() })

	body := bufio.NewReader(resp.Body)
	line, err := body.ReadString('\n')
	if err != nil || line != "first\n" {
		t.Fatalf("first line = %q, %v; want %q", line, err, "first\n")
	}
	return body
}

// waitUntilNotAccepting polls until addr stops accepting TCP connections.
func waitUntilNotAccepting(t *testing.T, addr string) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err != nil {
			return
		}
		conn.Close()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s still accepting connections", addr)
}

func assertFormValues(t *testing.T, values url.Values, key string, want []string) {
	t.Helper()

	got, ok := values[key]
	if !ok {
		t.Fatalf("form key %q missing", key)
	}
	if len(got) != len(want) {
		t.Fatalf("form key %q values = %v, want %v", key, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("form key %q values = %v, want %v", key, got, want)
		}
	}
}
