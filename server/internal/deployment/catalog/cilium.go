package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
)

const ciliumMaxArchiveSize int64 = 256 << 20

const defaultCiliumReleaseOrigin = "https://github.com/cilium/cilium-cli/releases/download/v" + ciliumCLIVersion

type CiliumDownloader struct {
	client  *http.Client
	baseURL *url.URL
	maxSize int64
}

func NewCiliumDownloader(client *http.Client, baseURL string) *CiliumDownloader {
	if client == nil {
		client = http.DefaultClient
	}
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		parsed = &url.URL{}
	}
	return &CiliumDownloader{client: cloneCiliumClient(client, parsed), baseURL: parsed, maxSize: ciliumMaxArchiveSize}
}

func NewDefaultCiliumDownloader() *CiliumDownloader {
	return NewCiliumDownloader(http.DefaultClient, defaultCiliumReleaseOrigin)
}

func cloneCiliumClient(client *http.Client, base *url.URL) *http.Client {
	clone := *client
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	clone.Transport = transport
	previous := client.CheckRedirect
	clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if previous != nil {
			if err := previous(req, via); err != nil {
				return err
			}
		}
		if base == nil || req.URL.Host != base.Host || req.URL.Scheme != base.Scheme {
			return fmt.Errorf("cilium artifact redirect changed origin")
		}
		return nil
	}
	return &clone
}

func (d *CiliumDownloader) Download(ctx context.Context, architecture string) ([]byte, error) {
	if d == nil || d.baseURL == nil || d.baseURL.Scheme == "" || d.baseURL.Host == "" {
		return nil, fmt.Errorf("cilium artifact origin is invalid")
	}
	if architecture != "amd64" && architecture != "arm64" {
		return nil, fmt.Errorf("unsupported cilium architecture")
	}
	if d.baseURL.Scheme != "https" && !isLoopbackHost(d.baseURL.Hostname()) {
		return nil, fmt.Errorf("cilium artifact origin must use HTTPS")
	}
	name := "cilium-linux-" + architecture + ".tar.gz"
	archiveURL := *d.baseURL
	archiveURL.Path = path.Join(d.baseURL.Path, name)
	archive, err := d.fetch(ctx, archiveURL.String())
	if err != nil {
		return nil, err
	}
	digestURL := *d.baseURL
	digestURL.Path = path.Join(d.baseURL.Path, name+".sha256sum")
	digestText, err := d.fetchText(ctx, digestURL.String())
	if err != nil {
		return nil, err
	}
	expected, expectedName, ok := parseCiliumDigest(digestText)
	if !ok || expectedName != name {
		return nil, fmt.Errorf("cilium artifact checksum file is invalid")
	}
	actual := sha256.Sum256(archive)
	if !strings.EqualFold(expected, hex.EncodeToString(actual[:])) {
		return nil, fmt.Errorf("cilium artifact checksum mismatch")
	}
	return archive, nil
}

func (d *CiliumDownloader) fetch(ctx context.Context, endpoint string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("request cilium artifact: %w", err)
	}
	response, err := d.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("download cilium artifact: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cilium artifact returned HTTP %d", response.StatusCode)
	}
	limit := d.maxSize
	if limit <= 0 || limit > ciliumMaxArchiveSize {
		limit = ciliumMaxArchiveSize
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read cilium artifact: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("cilium artifact exceeds size limit")
	}
	return data, nil
}

func (d *CiliumDownloader) fetchText(ctx context.Context, endpoint string) (string, error) {
	data, err := d.fetchWithLimit(ctx, endpoint, 64<<10)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (d *CiliumDownloader) fetchWithLimit(ctx context.Context, endpoint string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("request cilium checksum: %w", err)
	}
	response, err := d.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("download cilium checksum: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cilium checksum returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("cilium checksum exceeds size limit")
	}
	return data, nil
}

func parseCiliumDigest(text string) (digest, name string, ok bool) {
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || len(fields[0]) != sha256.Size*2 || !isHex(fields[0]) {
			continue
		}
		name = strings.TrimPrefix(fields[1], "*")
		return strings.ToLower(fields[0]), path.Base(name), true
	}
	return "", "", false
}

func isHex(value string) bool {
	_, err := hex.DecodeString(value)
	return err == nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
