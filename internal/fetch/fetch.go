// Package fetch performs SSRF-safe outbound HTTP for pulling provider-returned
// asset URLs into the BlobStore (spec §12 安全加固; the image adapter is the
// only place studio fetches an externally-supplied URL). Ported from
// llm-agent-kb/internal/fetch: scheme allowlist (http/https), DNS resolution
// with a non-public-IP block, dialing the VALIDATED IP directly to defeat DNS
// rebinding, per-hop redirect re-validation, timeouts, a max body cap, and a
// response Content-Type allowlist.
package fetch

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Config configures a Fetcher.
type Config struct {
	Timeout             time.Duration
	MaxBytes            int64
	AllowedContentTypes []string // prefix-matched against the response media type; empty = allow all

	// resolve overrides DNS resolution (tests inject a stub). nil → net.DefaultResolver.
	resolve func(ctx context.Context, host string) ([]net.IP, error)
	// allowLoopback permits loopback IPs (test-only; httptest servers are loopback).
	allowLoopback bool
}

// Fetcher fetches remote bytes safely.
type Fetcher struct {
	cfg    Config
	client *http.Client
}

// New builds a Fetcher whose transport dials only validated, resolved IPs.
func New(cfg Config) *Fetcher {
	if cfg.resolve == nil {
		cfg.resolve = func(ctx context.Context, host string) ([]net.IP, error) {
			return net.DefaultResolver.LookupIP(ctx, "ip", host)
		}
	}
	f := &Fetcher{cfg: cfg}
	dialer := &net.Dialer{Timeout: cfg.Timeout}
	transport := &http.Transport{
		// Resolve the host ourselves, validate every candidate IP, and dial the
		// validated IP directly — the IP we checked is the IP we connect to (no
		// TOCTOU / DNS-rebinding window).
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ip, err := f.resolveAndValidate(ctx, host)
			if err != nil {
				return nil, err
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		},
		TLSHandshakeTimeout:   cfg.Timeout,
		ResponseHeaderTimeout: cfg.Timeout,
		DisableKeepAlives:     true,
	}
	f.client = &http.Client{
		Timeout:   cfg.Timeout,
		Transport: transport,
		// Re-validate every redirect hop's scheme BEFORE following it; IP
		// validation happens again in DialContext on the new connection.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("fetch: too many redirects")
			}
			if err := validateScheme(req.URL); err != nil {
				return err
			}
			// Per-hop host re-validation: any host change drops Authorization (and
			// every {{secret}}-bearing header) so a 3xx to an attacker host cannot
			// exfiltrate the injected credential. IP re-validation still happens in
			// DialContext on the new connection.
			if len(via) > 0 && req.URL.Host != via[len(via)-1].URL.Host {
				req.Header.Del("Authorization")
				for k := range req.Header {
					// Drop ALL caller-supplied headers on host change (we re-add none).
					// Keep only Host/User-Agent which net/http manages.
					if k == "User-Agent" {
						continue
					}
					req.Header.Del(k)
				}
			}
			return nil
		},
	}
	return f
}

// NewLoopbackForTest builds a Fetcher that permits loopback IPs, for tests that
// must reach an httptest server. NOT for production use.
func NewLoopbackForTest(timeout time.Duration, maxBytes int64, allowed []string) *Fetcher {
	return New(Config{Timeout: timeout, MaxBytes: maxBytes, AllowedContentTypes: allowed, allowLoopback: true})
}

// Get fetches the URL and returns the (capped) body + response content type.
func (f *Fetcher) Get(ctx context.Context, rawURL string) ([]byte, string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, "", fmt.Errorf("fetch: parse url: %w", err)
	}
	if err := validateScheme(u); err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "llm-agent-studio/asset-pull")
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("fetch: get %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("fetch: status %d for %s", resp.StatusCode, rawURL)
	}
	ct := resp.Header.Get("Content-Type")
	if !f.contentTypeAllowed(ct) {
		return nil, "", fmt.Errorf("fetch: content-type %q not allowed", ct)
	}
	// Read one byte past the cap to distinguish "at cap" from "over cap" and
	// reject oversized bodies instead of silently truncating.
	body, err := io.ReadAll(io.LimitReader(resp.Body, f.cfg.MaxBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("fetch: read body: %w", err)
	}
	if int64(len(body)) > f.cfg.MaxBytes {
		return nil, "", fmt.Errorf("fetch: body exceeds %d byte cap", f.cfg.MaxBytes)
	}
	return body, ct, nil
}

// Request is a general SSRF-safe outbound request for the http custom-node kind.
type Request struct {
	Method  string
	URL     string
	Headers map[string]string
	Body    []byte
}

// Response is the result of Do. Status is the raw HTTP status (non-2xx is NOT an
// error — the caller decides). Body is the capped response body.
type Response struct {
	Status      int
	Body        []byte
	ContentType string
}

// Do performs a general SSRF-safe request reusing the same validated-IP transport
// as Get. Unlike Get it: honors Method/Headers/Body, returns the status (non-2xx
// is not an error), and applies the same body cap + per-hop redirect re-validation
// + Authorization-strip-on-host-change. err is ONLY a transport / SSRF / cap
// failure. Callers that inject secrets MUST map err to an opaque enum (NEVER %w
// the raw error, which embeds the URL) before surfacing it.
func (f *Fetcher) Do(ctx context.Context, in Request) (Response, error) {
	u, err := url.Parse(in.URL)
	if err != nil {
		return Response{}, fmt.Errorf("fetch: parse url")
	}
	if err := validateScheme(u); err != nil {
		return Response{}, err
	}
	method := in.Method
	if method == "" {
		method = http.MethodGet
	}
	var bodyReader io.Reader
	if len(in.Body) > 0 {
		bodyReader = bytes.NewReader(in.Body)
	}
	req, err := http.NewRequestWithContext(ctx, method, in.URL, bodyReader)
	if err != nil {
		return Response{}, fmt.Errorf("fetch: build request")
	}
	req.Header.Set("User-Agent", "llm-agent-studio/custom-http")
	for k, v := range in.Headers {
		req.Header.Set(k, v)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		// NOTE: this error embeds the URL — callers injecting secrets must map it to
		// an opaque enum before surfacing (the worker does, B2).
		return Response{}, fmt.Errorf("fetch: do: %w", err)
	}
	defer resp.Body.Close()
	ct := resp.Header.Get("Content-Type")
	if !f.contentTypeAllowed(ct) {
		return Response{}, fmt.Errorf("fetch: content-type %q not allowed", ct)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, f.cfg.MaxBytes+1))
	if err != nil {
		return Response{}, fmt.Errorf("fetch: read body")
	}
	if int64(len(body)) > f.cfg.MaxBytes {
		return Response{}, fmt.Errorf("fetch: body exceeds %d byte cap", f.cfg.MaxBytes)
	}
	return Response{Status: resp.StatusCode, Body: body, ContentType: ct}, nil
}

func (f *Fetcher) contentTypeAllowed(ct string) bool {
	if len(f.cfg.AllowedContentTypes) == 0 {
		return true
	}
	mt := strings.ToLower(strings.TrimSpace(strings.SplitN(ct, ";", 2)[0]))
	for _, allowed := range f.cfg.AllowedContentTypes {
		if strings.HasPrefix(mt, strings.ToLower(allowed)) {
			return true
		}
	}
	return false
}

// resolveAndValidate resolves host to IPs and returns the first that passes the
// SSRF block check; if every candidate is blocked it errors.
func (f *Fetcher) resolveAndValidate(ctx context.Context, host string) (net.IP, error) {
	if literal := net.ParseIP(host); literal != nil {
		if f.blocked(literal) {
			return nil, fmt.Errorf("fetch: blocked IP %s", literal)
		}
		return literal, nil
	}
	ips, err := f.cfg.resolve(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("fetch: resolve %s: %w", host, err)
	}
	for _, ip := range ips {
		if !f.blocked(ip) {
			return ip, nil
		}
	}
	return nil, fmt.Errorf("fetch: all resolved IPs for %s are blocked", host)
}

func (f *Fetcher) blocked(ip net.IP) bool {
	if f.cfg.allowLoopback && ip.IsLoopback() {
		return false
	}
	return isBlockedIP(ip)
}

func validateScheme(u *url.URL) error {
	switch u.Scheme {
	case "http", "https":
		return nil
	default:
		return fmt.Errorf("fetch: scheme %q not allowed (http/https only)", u.Scheme)
	}
}

// cgnat is the RFC 6598 carrier-grade NAT range (100.64.0.0/10).
var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// nat64 is the well-known NAT64 prefix (RFC 6052, 64:ff9b::/96). An attacker can
// smuggle an IPv4 metadata target (e.g. 64:ff9b::169.254.169.254) past an
// IPv4-only check, so embedded IPv4 must be extracted and re-checked.
var nat64 = &net.IPNet{IP: net.ParseIP("64:ff9b::"), Mask: net.CIDRMask(96, 128)}

// isBlockedIP returns true for any IP that is NOT a routable public address:
// loopback, private, link-local (incl. 169.254.169.254 metadata), multicast,
// unspecified, interface-local, RFC 6598 CGNAT. IPv4-mapped IPv6 and NAT64
// (64:ff9b::/96) embeddings are normalized to their embedded IPv4 and re-checked
// (else 64:ff9b::169.254.169.254 / ::ffff:169.254.169.254 would slip past an
// IPv4-only test).
func isBlockedIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	// IPv4-mapped IPv6 (::ffff:a.b.c.d): To4() returns the embedded v4; check it
	// against CGNAT (the std predicates above already see through ::ffff:).
	if v4 := ip.To4(); v4 != nil {
		if cgnat.Contains(v4) {
			return true
		}
		return false
	}
	// NAT64 (64:ff9b::a.b.c.d): the last 4 bytes are an embedded IPv4 — extract and
	// recurse so all v4 rules (loopback/private/link-local/CGNAT) apply.
	if nat64.Contains(ip) {
		embedded := net.IPv4(ip[12], ip[13], ip[14], ip[15])
		return isBlockedIP(embedded)
	}
	return false
}
