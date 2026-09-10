// Package fetch downloads renter-supplied dataset URLs on behalf of the node.
//
// This is a security boundary, not a convenience wrapper. The URL is chosen by
// an untrusted stranger and fetched by a daemon sitting inside the provider's
// home or datacentre network, so a naive http.Get here would hand every paying
// renter a proxy into the provider's LAN and cloud metadata service.
//
// The container itself never has network access (NetworkMode "none",
// CLAUDE.md invariant 6). Everything downloaded here is staged into a volume
// the job reads at /data.
package fetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

// maxRedirects bounds a redirect chain. Every hop is re-checked against the
// address rules: a public URL that redirects to 169.254.169.254 is the classic
// way to smuggle past a first-hop-only check.
const maxRedirects = 5

// Options configures a Fetcher. Zero values are replaced with the defaults.
type Options struct {
	// MaxBytes rejects anything larger, both by Content-Length and by counting
	// bytes as they arrive — a server may lie or omit the header entirely.
	MaxBytes int64
	// Timeout bounds a whole download, not a single read.
	Timeout time.Duration
	// AllowHTTP permits plaintext http:// URLs. Off by default: a dataset
	// fetched over http can be replaced in flight by anyone on the path.
	AllowHTTP bool
	// AllowPrivate permits addresses in private ranges. Off by default and
	// intended only for an operator running a dataset mirror on their own LAN.
	AllowPrivate bool
	// HostAllowlist, when non-empty, restricts downloads to these exact hosts.
	HostAllowlist []string
}

const (
	defaultMaxBytes = int64(8192) << 20 // 8 GiB
	defaultTimeout  = 30 * time.Minute
)

// Fetcher downloads datasets under a fixed policy.
type Fetcher struct {
	opts   Options
	client *http.Client
}

// New builds a Fetcher whose HTTP client refuses to follow a redirect into a
// blocked address range and whose dialer re-checks every resolved IP.
func New(opts Options) *Fetcher {
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = defaultMaxBytes
	}
	if opts.Timeout <= 0 {
		opts.Timeout = defaultTimeout
	}

	f := &Fetcher{opts: opts}

	transport := &http.Transport{
		// The dialer is the backstop. Checking the URL's host is not enough:
		// DNS can resolve a public name to a private address, and can return a
		// different answer on the second lookup than it did on the first
		// (DNS rebinding). Checking the address we are actually connecting to
		// closes both.
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, fmt.Errorf("resolve %s: %w", host, err)
			}
			var dialer net.Dialer
			var lastErr error
			for _, ip := range ips {
				if err := f.checkAddr(ip.IP); err != nil {
					lastErr = err
					continue
				}
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			if lastErr == nil {
				lastErr = fmt.Errorf("no usable address for %s", host)
			}
			return nil, lastErr
		},
		TLSHandshakeTimeout:   30 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
	}

	f.client = &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("too many redirects (limit %d)", maxRedirects)
			}
			return f.checkURL(req.URL)
		},
	}
	return f
}

// ErrBlocked is returned for a URL the policy refuses. It is reported to the
// renter as a 400 before any payment is requested, so it must say why.
type ErrBlocked struct{ Reason string }

func (e *ErrBlocked) Error() string { return e.Reason }

func blocked(format string, args ...any) error {
	return &ErrBlocked{Reason: fmt.Sprintf(format, args...)}
}

// IsBlocked reports whether an error came from the URL policy rather than from
// the network, which is the difference between "your URL is not allowed" and
// "the server was down".
func IsBlocked(err error) bool {
	var b *ErrBlocked
	return errors.As(err, &b)
}

// checkURL validates a URL's scheme and host before any connection is made.
func (f *Fetcher) checkURL(u *url.URL) error {
	switch u.Scheme {
	case "https":
	case "http":
		if !f.opts.AllowHTTP {
			return blocked("http:// is not allowed (a dataset fetched in the clear can be swapped in transit); use https")
		}
	default:
		return blocked("unsupported URL scheme %q; only https is allowed", u.Scheme)
	}

	host := u.Hostname()
	if host == "" {
		return blocked("URL has no host")
	}

	if len(f.opts.HostAllowlist) > 0 {
		allowed := false
		for _, candidate := range f.opts.HostAllowlist {
			if strings.EqualFold(candidate, host) {
				allowed = true
				break
			}
		}
		if !allowed {
			return blocked("host %q is not on this node's dataset allowlist (%s)",
				host, strings.Join(f.opts.HostAllowlist, ", "))
		}
	}

	// A literal IP in the URL skips DNS entirely, so check it here too.
	if ip := net.ParseIP(host); ip != nil {
		return f.checkAddr(ip)
	}
	return nil
}

// checkAddr rejects addresses that would let a renter reach something that is
// not theirs to reach.
func (f *Fetcher) checkAddr(ip net.IP) error {
	if f.opts.AllowPrivate {
		return nil
	}
	switch {
	case ip.IsLoopback():
		return blocked("refusing to fetch from a loopback address (%s)", ip)
	case ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast():
		// 169.254.169.254 is the cloud metadata service on AWS, GCP and Azure.
		// Reaching it would hand a renter the provider's instance credentials.
		return blocked("refusing to fetch from a link-local address (%s)", ip)
	case ip.IsPrivate():
		return blocked("refusing to fetch from a private address (%s)", ip)
	case ip.IsUnspecified(), ip.IsMulticast(), ip.IsInterfaceLocalMulticast():
		return blocked("refusing to fetch from a non-routable address (%s)", ip)
	case isCGNAT(ip):
		return blocked("refusing to fetch from a carrier-grade NAT address (%s)", ip)
	}
	return nil
}

// cgnat is 100.64.0.0/10, which net.IP.IsPrivate does not cover.
var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

func isCGNAT(ip net.IP) bool {
	v4 := ip.To4()
	return v4 != nil && cgnat.Contains(v4)
}

// Preflight checks a dataset URL cheaply, before the renter is asked to pay.
//
// It returns the advertised size, or -1 when the server does not say. A failure
// here costs the renter nothing, which is the entire point: a typo, a 404, a
// login wall or a file far too large is caught while the job is still free to
// reject. The expensive GET only happens once payment has settled.
func (f *Fetcher) Preflight(ctx context.Context, rawURL string) (int64, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return 0, blocked("dataset url is not a valid URL: %v", err)
	}
	if err := f.checkURL(parsed); err != nil {
		return 0, err
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	size, err := f.head(ctx, parsed.String())
	if err == nil {
		return size, f.checkSize(size)
	}

	// Plenty of hosts (and most presigned URLs) reject HEAD. Fall back to a
	// ranged GET for the first byte, which every static host answers.
	size, rangeErr := f.probeWithRange(ctx, parsed.String())
	if rangeErr != nil {
		return 0, fmt.Errorf("dataset url is not reachable: %w", err)
	}
	return size, f.checkSize(size)
}

func (f *Fetcher) head(ctx context.Context, rawURL string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, rawURL, nil)
	if err != nil {
		return 0, err
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("HEAD returned %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	return contentLength(resp), nil
}

func (f *Fetcher) probeWithRange(ctx context.Context, rawURL string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Range", "bytes=0-0")

	resp, err := f.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("GET returned %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}

	// A 206 answers with the total in Content-Range: "bytes 0-0/1234".
	if _, _, total, ok := parseContentRange(resp.Header.Get("Content-Range")); ok {
		return total, nil
	}
	return contentLength(resp), nil
}

func contentLength(resp *http.Response) int64 {
	raw := resp.Header.Get("Content-Length")
	if raw == "" {
		return -1
	}
	size, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return -1
	}
	return size
}

func parseContentRange(header string) (first, last, total int64, ok bool) {
	_, spec, found := strings.Cut(header, " ")
	if !found {
		return 0, 0, 0, false
	}
	span, totalRaw, found := strings.Cut(spec, "/")
	if !found || totalRaw == "*" {
		return 0, 0, 0, false
	}
	firstRaw, lastRaw, found := strings.Cut(span, "-")
	if !found {
		return 0, 0, 0, false
	}
	first, err1 := strconv.ParseInt(firstRaw, 10, 64)
	last, err2 := strconv.ParseInt(lastRaw, 10, 64)
	total, err3 := strconv.ParseInt(totalRaw, 10, 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, 0, 0, false
	}
	return first, last, total, true
}

func (f *Fetcher) checkSize(size int64) error {
	if size >= 0 && size > f.opts.MaxBytes {
		return blocked("dataset is %s, above this node's limit of %s",
			HumanBytes(size), HumanBytes(f.opts.MaxBytes))
	}
	return nil
}

// Progress reports download progress. Total is -1 when the server never said
// how big the file is.
type Progress struct {
	Downloaded int64
	Total      int64
}

// Download streams a URL to a local file, verifying size and checksum as it goes.
//
// onProgress, when non-nil, is called roughly once a second — often enough for
// a live display, rarely enough not to flood a log stream.
func (f *Fetcher) Download(
	ctx context.Context,
	rawURL, destPath, expectedSHA256 string,
	onProgress func(Progress),
) (written int64, sum string, err error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return 0, "", blocked("dataset url is not a valid URL: %v", err)
	}
	if err := f.checkURL(parsed); err != nil {
		return 0, "", err
	}

	ctx, cancel := context.WithTimeout(ctx, f.opts.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return 0, "", err
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("download %s: %w", redact(parsed), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, "", fmt.Errorf("download %s returned %d %s",
			redact(parsed), resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	total := contentLength(resp)
	if err := f.checkSize(total); err != nil {
		return 0, "", err
	}

	file, err := os.Create(destPath)
	if err != nil {
		return 0, "", fmt.Errorf("create %s: %w", destPath, err)
	}
	defer file.Close()

	digest := sha256.New()
	written, err = f.copyBounded(ctx, file, resp.Body, digest, total, onProgress)
	if err != nil {
		return written, "", err
	}
	if err := file.Sync(); err != nil {
		return written, "", fmt.Errorf("flush %s: %w", destPath, err)
	}

	sum = hex.EncodeToString(digest.Sum(nil))
	if expectedSHA256 != "" && !strings.EqualFold(sum, expectedSHA256) {
		return written, sum, fmt.Errorf(
			"dataset checksum mismatch: expected %s, got %s — the file at that URL is not the one you asked for",
			strings.ToLower(expectedSHA256), sum)
	}
	return written, sum, nil
}

// copyBounded copies while counting bytes, hashing, and reporting progress. The
// byte counter is the real size limit: Content-Length is a hint a hostile or
// chunked server need not honour.
func (f *Fetcher) copyBounded(
	ctx context.Context,
	dst io.Writer,
	src io.Reader,
	digest hash.Hash,
	total int64,
	onProgress func(Progress),
) (int64, error) {
	buffer := make([]byte, 256<<10)
	var written int64
	lastReport := time.Now()

	for {
		if err := ctx.Err(); err != nil {
			return written, fmt.Errorf("download interrupted after %s: %w", HumanBytes(written), err)
		}

		n, readErr := src.Read(buffer)
		if n > 0 {
			written += int64(n)
			if written > f.opts.MaxBytes {
				return written, blocked("dataset exceeded this node's limit of %s while downloading",
					HumanBytes(f.opts.MaxBytes))
			}
			if _, err := dst.Write(buffer[:n]); err != nil {
				return written, fmt.Errorf("write dataset: %w", err)
			}
			digest.Write(buffer[:n])

			if onProgress != nil && time.Since(lastReport) >= time.Second {
				onProgress(Progress{Downloaded: written, Total: total})
				lastReport = time.Now()
			}
		}
		if readErr == io.EOF {
			if onProgress != nil {
				onProgress(Progress{Downloaded: written, Total: total})
			}
			return written, nil
		}
		if readErr != nil {
			return written, fmt.Errorf("read dataset after %s: %w", HumanBytes(written), readErr)
		}
	}
}

// redact strips query parameters before an error message reaches a log. Dataset
// URLs are routinely presigned, and the signature is a bearer credential.
func redact(u *url.URL) string {
	clone := *u
	clone.RawQuery = ""
	clone.Fragment = ""
	return clone.String()
}

// FilenameFor picks the on-disk name for a dataset: the renter's choice if they
// gave one, otherwise the URL's last path segment.
func FilenameFor(rawURL, requested string) string {
	if requested != "" {
		return path.Base(requested)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "dataset"
	}
	name := path.Base(parsed.Path)
	if name == "" || name == "." || name == "/" {
		return "dataset"
	}
	return name
}

// HumanBytes renders a byte count for an operator or a renter to read.
func HumanBytes(bytes int64) string {
	if bytes < 0 {
		return "unknown size"
	}
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit && exp < 4; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(bytes)/float64(div), "KMGTP"[exp])
}
