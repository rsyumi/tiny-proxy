package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
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
