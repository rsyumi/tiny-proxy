package main

import (
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
