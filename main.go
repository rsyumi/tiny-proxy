package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// --- config (loaded once at init) ---

var (
	port       string
	authMode   string // "none" | "key" | "hash"
	authKey    string
	authSecret string
	authWindow int64
	whitelist  map[string]bool
	wlPatterns []string
	hURL       string // header name for target URL
	hPrefix    string // header prefix for forwarded headers
	hBulk      string // header name for bulk JSON headers
	hAuth      string // header name for auth token
	timeout    time.Duration
)

var pool = sync.Pool{New: func() any { return make([]byte, 8192) }}

var client *http.Client

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func init() {
	port = env("PORT", "8080")
	authMode = env("AUTH_MODE", "none")
	authKey = env("AUTH_KEY", "")
	authSecret = env("AUTH_SECRET", "")
	if w, err := strconv.ParseInt(env("AUTH_WINDOW", "30"), 10, 64); err == nil {
		authWindow = w
	} else {
		authWindow = 30
	}
	hURL = http.CanonicalHeaderKey(env("HEADER_URL", "X-Proxy-Url"))
	hPrefix = http.CanonicalHeaderKey(env("HEADER_PREFIX", "X-Proxy-H-"))
	hBulk = http.CanonicalHeaderKey(env("HEADER_BULK", "X-Proxy-Headers"))
	hAuth = http.CanonicalHeaderKey(env("HEADER_AUTH", "X-Proxy-Auth"))

	if d, err := time.ParseDuration(env("TIMEOUT", "30s")); err == nil {
		timeout = d
	} else {
		timeout = 30 * time.Second
	}

	client = &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:          10,
			MaxIdleConnsPerHost:   5,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: timeout,
			DisableCompression:    true,
			ForceAttemptHTTP2:     true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	wl := env("WHITELIST", "")
	whitelist = make(map[string]bool)
	if wl != "" {
		for _, h := range strings.Split(wl, ",") {
			if h = strings.TrimSpace(strings.ToLower(h)); h != "" {
				if strings.Contains(h, "*") {
					wlPatterns = append(wlPatterns, h)
				} else {
					whitelist[h] = true
				}
			}
		}
	}
}

// --- main ---

func main() {
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           http.HandlerFunc(handle),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 16,
		ErrorLog:          log.New(io.Discard, "", 0),
	}
	log.Printf("tiny-proxy :%s auth=%s whitelist=%d+%d timeout=%s", port, authMode, len(whitelist), len(wlPatterns), timeout)
	log.Fatal(srv.ListenAndServe())
}

// --- handler ---

func handle(w http.ResponseWriter, r *http.Request) {
	// health check
	if r.URL.Path == "/_health" {
		w.Write([]byte("ok"))
		return
	}

	// CORS preflight
	if r.Method == http.MethodOptions {
		cors(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	cors(w)

	// auth
	if !auth(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// target URL from custom header
	target := r.Header.Get(hURL)
	if target == "" {
		http.Error(w, "missing target url header", http.StatusBadRequest)
		return
	}

	// whitelist check
	if len(whitelist) > 0 || len(wlPatterns) > 0 {
		u, err := url.Parse(target)
		if err != nil {
			http.Error(w, "invalid target url", http.StatusBadRequest)
			return
		}
		if !hostAllowed(strings.ToLower(u.Hostname())) {
			http.Error(w, "host not allowed", http.StatusForbidden)
			return
		}
	}

	// build proxy request
	pReq, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// forward headers (bulk JSON, then prefix overrides)
	if raw := r.Header.Get(hBulk); raw != "" {
		if decoded, err := url.QueryUnescape(raw); err == nil {
			var hm map[string]string
			if json.Unmarshal([]byte(decoded), &hm) == nil {
				for k, v := range hm {
					pReq.Header.Set(k, v)
				}
			}
		}
	}
	for k, vs := range r.Header {
		if strings.HasPrefix(k, hPrefix) {
			if name := k[len(hPrefix):]; name != "" {
				pReq.Header[name] = vs
			}
		}
	}

	// set default User-Agent if not provided
	if pReq.Header.Get("User-Agent") == "" {
		pReq.Header.Set("User-Agent", "Mozilla/5.0 (compatible; tiny-proxy)")
	}

	// execute upstream request
	resp, err := client.Do(pReq)
	if err != nil {
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// copy response headers (skip hop-by-hop)
	for k, vs := range resp.Header {
		switch k {
		case "Connection", "Keep-Alive", "Transfer-Encoding", "Te", "Trailer", "Upgrade":
			continue
		}
		w.Header()[k] = vs
	}
	w.WriteHeader(resp.StatusCode)

	// stream body with flush (SSE/chunked/stream support)
	stream(w, resp.Body)
}

// --- streaming ---

func stream(w http.ResponseWriter, src io.Reader) {
	buf := pool.Get().([]byte)
	defer pool.Put(buf)

	f, canFlush := w.(http.Flusher)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, wErr := w.Write(buf[:n]); wErr != nil {
				return // client disconnected
			}
			if canFlush {
				f.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

// --- CORS ---

func cors(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", "*")
	h.Set("Access-Control-Allow-Headers", "*")
	h.Set("Access-Control-Expose-Headers", "*")
	h.Set("Access-Control-Max-Age", "86400")
}

// --- whitelist ---

func hostAllowed(host string) bool {
	if whitelist[host] {
		return true
	}
	for _, p := range wlPatterns {
		if matchHost(p, host) {
			return true
		}
	}
	return false
}

func matchHost(pattern, host string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == host
	}
	// check prefix
	if !strings.HasPrefix(host, parts[0]) {
		return false
	}
	rest := host[len(parts[0]):]
	// check middle parts
	for _, p := range parts[1 : len(parts)-1] {
		i := strings.Index(rest, p)
		if i < 0 {
			return false
		}
		rest = rest[i+len(p):]
	}
	// check suffix
	return strings.HasSuffix(rest, parts[len(parts)-1])
}

// --- auth ---

func auth(r *http.Request) bool {
	switch authMode {
	case "none":
		return true
	case "key":
		got := []byte(r.Header.Get(hAuth))
		return subtle.ConstantTimeCompare(got, []byte(authKey)) == 1
	case "hash":
		token := r.Header.Get(hAuth)
		if token == "" {
			return false
		}
		now := time.Now().Unix()
		w := authWindow
		if w <= 0 {
			w = 30
		}
		cur := now / w
		// check current, previous, and next window
		for _, ts := range [3]int64{cur - 1, cur, cur + 1} {
			mac := hmac.New(sha256.New, []byte(authSecret))
			mac.Write([]byte(strconv.FormatInt(ts, 10)))
			if hmac.Equal([]byte(token), []byte(hex.EncodeToString(mac.Sum(nil)))) {
				return true
			}
		}
		return false
	}
	return false
}
