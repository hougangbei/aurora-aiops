package deployment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGitHubReleaseResolveExactAssetAndDownload(t *testing.T) {
	archive := []byte("aurora-release")
	digest := sha256.Sum256(archive)
	archiveHash := hex.EncodeToString(digest[:])
	var gotArchiveAuth string
	var gotChecksumsAuth string

	download := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/archive":
			gotArchiveAuth = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(archive)
		case "/checksums":
			gotChecksumsAuth = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprintf(w, "%s  aurora-aiops_0.1.2_linux_amd64.tar.gz\n%s  aurora-aiops_0.1.2_linux_arm64.tar.gz\n", archiveHash, archiveHash)
		default:
			http.NotFound(w, r)
		}
	}))
	defer download.Close()

	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/hougangbei/aurora-aiops/releases/tags/v0.1.2" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("API Authorization = %q, want Bearer test-token", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v0.1.2", "assets": []map[string]any{
			{"name": "aurora-aiops_0.1.2_linux_amd64.tar.gz", "browser_download_url": download.URL + "/archive", "size": len(archive)},
			{"name": "aurora-aiops_0.1.2_linux_arm64.tar.gz", "browser_download_url": download.URL, "size": len(archive)},
			{"name": "checksums.txt", "browser_download_url": download.URL + "/checksums"},
		}})
	}))
	defer api.Close()

	client := api.Client()
	client.Transport = rewriteHostTransport{base: client.Transport, target: api.URL}
	resolver, err := NewGitHubReleaseResolver("hougangbei/aurora-aiops", "test-token", client)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := resolver.Resolve(context.Background(), "0.1.2", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if artifact.ArchiveName != "aurora-aiops_0.1.2_linux_amd64.tar.gz" || artifact.SHA256 != archiveHash {
		t.Fatalf("artifact = %+v", artifact)
	}
	var out strings.Builder
	if err := resolver.Download(context.Background(), artifact, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != string(archive) {
		t.Fatalf("download = %q, want %q", out.String(), archive)
	}
	if gotArchiveAuth != "" || gotChecksumsAuth != "" {
		t.Fatalf("download Authorization leaked: archive=%q checksums=%q", gotArchiveAuth, gotChecksumsAuth)
	}
	armArtifact, err := resolver.Resolve(context.Background(), "0.1.2", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	if armArtifact.ArchiveName != "aurora-aiops_0.1.2_linux_arm64.tar.gz" {
		t.Fatalf("arm64 artifact = %+v", armArtifact)
	}
}

func TestGitHubReleaseRejectsMissingDuplicateOversizedAndBadChecksums(t *testing.T) {
	cases := []struct {
		name       string
		assets     []map[string]any
		checksum   string
		wantSubstr string
	}{
		{name: "missing archive", assets: []map[string]any{{"name": "checksums.txt", "browser_download_url": "/checksums"}}, wantSubstr: "archive"},
		{name: "duplicate archive", assets: []map[string]any{{"name": "aurora-aiops_0.1.2_linux_amd64.tar.gz", "browser_download_url": "/archive"}, {"name": "aurora-aiops_0.1.2_linux_amd64.tar.gz", "browser_download_url": "/archive"}, {"name": "checksums.txt", "browser_download_url": "/checksums"}}, wantSubstr: "duplicate"},
		{name: "oversized archive", assets: []map[string]any{{"name": "aurora-aiops_0.1.2_linux_amd64.tar.gz", "browser_download_url": "/archive", "size": maxReleaseArchiveBytes + 1}, {"name": "checksums.txt", "browser_download_url": "/checksums"}}, wantSubstr: "size"},
		{name: "bad checksum", assets: []map[string]any{{"name": "aurora-aiops_0.1.2_linux_amd64.tar.gz", "browser_download_url": "/archive", "size": 1}, {"name": "checksums.txt", "browser_download_url": "/checksums"}}, checksum: "00  aurora-aiops_0.1.2_linux_amd64.tar.gz\n", wantSubstr: "checksum"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/checksums" {
					_, _ = io.WriteString(w, tc.checksum)
					return
				}
				if r.URL.Path == "/archive" {
					_, _ = io.WriteString(w, "x")
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"assets": tc.assets})
			}))
			defer server.Close()
			for _, asset := range tc.assets {
				if path, ok := asset["browser_download_url"].(string); ok && strings.HasPrefix(path, "/") {
					asset["browser_download_url"] = server.URL + path
				}
			}
			client := server.Client()
			client.Transport = rewriteHostTransport{base: client.Transport, target: server.URL}
			resolver, err := NewGitHubReleaseResolver("hougangbei/aurora-aiops", "", client)
			if err != nil {
				t.Fatal(err)
			}
			_, err = resolver.Resolve(context.Background(), "0.1.2", "amd64")
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), tc.wantSubstr) {
				t.Fatalf("error = %v, want substring %q", err, tc.wantSubstr)
			}
		})
	}
}

func TestGitHubReleaseRejectsDuplicateChecksumAndOversizedChecksum(t *testing.T) {
	for _, checksum := range []string{
		"aa  aurora-aiops_0.1.2_linux_amd64.tar.gz\naa  aurora-aiops_0.1.2_linux_amd64.tar.gz\n",
		strings.Repeat("a", int(maxReleaseChecksumBytes)+1),
	} {
		t.Run(fmt.Sprintf("len-%d", len(checksum)), func(t *testing.T) {
			var serverURL string
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/checksums" {
					_, _ = io.WriteString(w, checksum)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"assets": []map[string]any{
					{"name": "aurora-aiops_0.1.2_linux_amd64.tar.gz", "browser_download_url": serverURL + "/archive", "size": 1},
					{"name": "checksums.txt", "browser_download_url": serverURL + "/checksums"},
				}})
			}))
			defer server.Close()
			serverURL = server.URL
			client := server.Client()
			client.Transport = rewriteHostTransport{base: client.Transport, target: server.URL}
			resolver, err := NewGitHubReleaseResolver("hougangbei/aurora-aiops", "", client)
			if err != nil {
				t.Fatal(err)
			}
			_, err = resolver.Resolve(context.Background(), "0.1.2", "amd64")
			if err == nil {
				t.Fatal("expected checksum validation error")
			}
		})
	}
}

func TestGitHubReleaseRejectsUntrustedRedirect(t *testing.T) {
	archiveBytes := []byte("archive")
	digest := sha256.Sum256(archiveBytes)
	archive := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(archiveBytes) }))
	defer archive.Close()
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/checksums" {
			_, _ = fmt.Fprintf(w, "%x  aurora-aiops_0.1.2_linux_amd64.tar.gz\n", digest)
			return
		}
		http.Redirect(w, r, archive.URL, http.StatusFound)
	}))
	defer redirect.Close()
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"assets": []map[string]any{
			{"name": "aurora-aiops_0.1.2_linux_amd64.tar.gz", "browser_download_url": redirect.URL, "size": 7},
			{"name": "checksums.txt", "browser_download_url": redirect.URL + "/checksums"},
		}})
	}))
	defer api.Close()
	client := api.Client()
	client.Transport = rewriteHostTransport{base: client.Transport, target: api.URL}
	resolver, err := NewGitHubReleaseResolver("hougangbei/aurora-aiops", "", client)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := resolver.Resolve(context.Background(), "0.1.2", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := resolver.Download(context.Background(), artifact, &out); err == nil || !strings.Contains(strings.ToLower(err.Error()), "redirect") {
		t.Fatalf("error = %v, want redirect error", err)
	}
}

func TestGitHubReleaseRateLimitErrorIsSafe(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer server.Close()
	client := server.Client()
	client.Transport = rewriteHostTransport{base: client.Transport, target: server.URL}
	resolver, err := NewGitHubReleaseResolver("hougangbei/aurora-aiops", "secret-token", client)
	if err != nil {
		t.Fatal(err)
	}
	_, err = resolver.Resolve(context.Background(), "0.1.2", "amd64")
	if err == nil || !strings.Contains(err.Error(), "RELEASE_RATE_LIMITED") || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("error = %v", err)
	}
}

type rewriteHostTransport struct {
	base   http.RoundTripper
	target string
}

func (t rewriteHostTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	u := *req.URL
	if req.URL.Host == "api.github.com" {
		target, _ := http.NewRequest(http.MethodGet, t.target, nil)
		u.Scheme, u.Host = target.URL.Scheme, target.URL.Host
	}
	clone := req.Clone(req.Context())
	clone.URL = &u
	return t.base.RoundTrip(clone)
}
