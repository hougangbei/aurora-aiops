package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCiliumDownloaderVerifiesNameSizeAndDigest(t *testing.T) {
	payload := []byte("cilium-cli archive")
	sum := sha256.Sum256(payload)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cilium-linux-amd64.tar.gz":
			_, _ = w.Write(payload)
		case "/cilium-linux-amd64.tar.gz.sha256sum":
			_, _ = fmt.Fprintf(w, "%s  cilium-linux-amd64.tar.gz\n", hex.EncodeToString(sum[:]))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	d := NewCiliumDownloader(server.Client(), server.URL)
	got, err := d.Download(context.Background(), "amd64")
	if err != nil || string(got) != string(payload) {
		t.Fatalf("Download=(%q,%v)", got, err)
	}
}

func TestCiliumDownloaderRejectsDigestRedirectAndOversizedResponse(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler http.Handler
	}{
		{"digest", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, ".sha256sum") {
				_, _ = io.WriteString(w, strings.Repeat("0", 64)+"  cilium-linux-amd64.tar.gz\n")
				return
			}
			_, _ = io.WriteString(w, "not archive")
		})},
		{"name", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, ".sha256sum") {
				_, _ = io.WriteString(w, strings.Repeat("0", 64)+"  unexpected.tar.gz\n")
				return
			}
			_, _ = io.WriteString(w, "archive")
		})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(tc.handler)
			defer s.Close()
			if _, err := NewCiliumDownloader(s.Client(), s.URL).Download(context.Background(), "amd64"); err == nil {
				t.Fatal("invalid artifact accepted")
			}
		})
	}
}
