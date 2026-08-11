package deployment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

const (
	maxReleaseArchiveBytes  int64 = 1 << 30
	maxReleaseChecksumBytes int64 = 1 << 20
	maxReleaseMetadataBytes int64 = 1 << 20
)

var (
	repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	semverPattern     = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)
	architectureSet   = map[string]struct{}{"amd64": {}, "arm64": {}}
)

// ReleaseArtifact is the exact, checksum-verified archive selected for a
// target. DownloadURL is retained only for the subsequent bounded download.
type ReleaseArtifact struct {
	Version, Architecture, ArchiveName, DownloadURL, SHA256 string
	Size                                                    int64
}

type ReleaseResolver interface {
	Resolve(context.Context, string, string) (ReleaseArtifact, error)
	Download(context.Context, ReleaseArtifact, io.Writer) error
}

type GitHubReleaseResolver struct {
	repository string
	token      string
	client     *http.Client
}

type releaseError struct {
	Code    string
	Message string
}

func (e *releaseError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

func NewGitHubReleaseResolver(repository, token string, client *http.Client) (*GitHubReleaseResolver, error) {
	if !repositoryPattern.MatchString(repository) {
		return nil, fmt.Errorf("invalid GitHub repository")
	}
	if client == nil {
		client = http.DefaultClient
	}
	clone := *client
	return &GitHubReleaseResolver{repository: repository, token: token, client: &clone}, nil
}

func (r *GitHubReleaseResolver) Resolve(ctx context.Context, version, architecture string) (ReleaseArtifact, error) {
	if !semverPattern.MatchString(version) {
		return ReleaseArtifact{}, fmt.Errorf("invalid release version")
	}
	if _, ok := architectureSet[architecture]; !ok {
		return ReleaseArtifact{}, fmt.Errorf("unsupported architecture")
	}

	owner, repo, _ := strings.Cut(r.repository, "/")
	endpoint := "https://api.github.com/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/releases/tags/v" + url.PathEscape(version)
	var release githubRelease
	if err := r.getJSON(ctx, endpoint, &release); err != nil {
		return ReleaseArtifact{}, err
	}
	if release.TagName != "" && release.TagName != "v"+version {
		return ReleaseArtifact{}, fmt.Errorf("release tag does not match requested version")
	}

	archiveName := "aurora-aiops_" + version + "_linux_" + architecture + ".tar.gz"
	var archive, checksums *githubAsset
	for i := range release.Assets {
		asset := &release.Assets[i]
		switch asset.Name {
		case archiveName:
			if archive != nil {
				return ReleaseArtifact{}, fmt.Errorf("duplicate release archive asset")
			}
			archive = asset
		case "checksums.txt":
			if checksums != nil {
				return ReleaseArtifact{}, fmt.Errorf("duplicate checksums asset")
			}
			checksums = asset
		}
	}
	if archive == nil {
		return ReleaseArtifact{}, fmt.Errorf("release archive is missing")
	}
	if checksums == nil {
		return ReleaseArtifact{}, fmt.Errorf("checksums.txt is missing")
	}
	if archive.Size < 0 || archive.Size > maxReleaseArchiveBytes {
		return ReleaseArtifact{}, fmt.Errorf("release archive size exceeds limit")
	}
	if archive.BrowserDownloadURL == "" || checksums.BrowserDownloadURL == "" {
		return ReleaseArtifact{}, fmt.Errorf("release asset URL is missing")
	}

	checksumBody, err := r.downloadBounded(ctx, checksums.BrowserDownloadURL, maxReleaseChecksumBytes)
	if err != nil {
		return ReleaseArtifact{}, fmt.Errorf("read checksums.txt: %w", err)
	}
	digest, err := checksumForArchive(string(checksumBody), archiveName)
	if err != nil {
		return ReleaseArtifact{}, err
	}
	return ReleaseArtifact{
		Version:      version,
		Architecture: architecture,
		ArchiveName:  archiveName,
		DownloadURL:  archive.BrowserDownloadURL,
		SHA256:       digest,
		Size:         archive.Size,
	}, nil
}

func (r *GitHubReleaseResolver) Download(ctx context.Context, artifact ReleaseArtifact, dst io.Writer) error {
	if dst == nil {
		return errors.New("download destination is nil")
	}
	if !semverPattern.MatchString(artifact.Version) {
		return errors.New("invalid release artifact")
	}
	if _, ok := architectureSet[artifact.Architecture]; !ok {
		return errors.New("invalid release artifact")
	}
	wantName := "aurora-aiops_" + artifact.Version + "_linux_" + artifact.Architecture + ".tar.gz"
	if artifact.ArchiveName != wantName {
		return errors.New("invalid release artifact")
	}
	if artifact.Size < 0 || artifact.Size > maxReleaseArchiveBytes {
		return fmt.Errorf("release archive size exceeds limit")
	}
	wantDigest, err := normalizeDigest(artifact.SHA256)
	if err != nil {
		return fmt.Errorf("invalid release checksum")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, artifact.DownloadURL, nil)
	if err != nil {
		return fmt.Errorf("invalid release download URL")
	}
	if err := validateHTTPSURL(request.URL); err != nil {
		return err
	}
	initialHost := strings.ToLower(request.URL.Host)
	client := *r.client
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if err := validateHTTPSURL(next.URL); err != nil {
			return fmt.Errorf("untrusted redirect: %w", err)
		}
		if !allowedReleaseRedirectHost(initialHost, next.URL.Host) {
			return fmt.Errorf("untrusted redirect host")
		}
		// Release assets are public; never forward the API token to a CDN.
		next.Header.Del("Authorization")
		return nil
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("download release archive: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("download release archive: HTTP %d", response.StatusCode)
	}
	if response.ContentLength > maxReleaseArchiveBytes {
		return fmt.Errorf("release archive size exceeds limit")
	}
	hash := sha256.New()
	limited := io.LimitReader(response.Body, maxReleaseArchiveBytes+1)
	n, err := io.Copy(io.MultiWriter(dst, hash), limited)
	if err != nil {
		return fmt.Errorf("download release archive: %w", err)
	}
	if n > maxReleaseArchiveBytes {
		return fmt.Errorf("release archive size exceeds limit")
	}
	if artifact.Size > 0 && n != artifact.Size {
		return fmt.Errorf("release archive size mismatch")
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != wantDigest {
		return fmt.Errorf("release archive checksum mismatch")
	}
	return nil
}

func (r *GitHubReleaseResolver) getJSON(ctx context.Context, endpoint string, dst any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("build GitHub release request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if r.token != "" {
		request.Header.Set("Authorization", "Bearer "+r.token)
	}
	client := *r.client
	apiHost := strings.ToLower(request.URL.Host)
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if err := validateHTTPSURL(next.URL); err != nil {
			return fmt.Errorf("untrusted GitHub API redirect: %w", err)
		}
		if strings.ToLower(next.URL.Host) != apiHost {
			return fmt.Errorf("untrusted GitHub API redirect host")
		}
		return nil
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("request GitHub release: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusForbidden {
		return &releaseError{Code: "RELEASE_RATE_LIMITED", Message: "GitHub release lookup is rate limited; configure the update GitHub token and retry"}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("GitHub release lookup failed with HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxReleaseMetadataBytes+1))
	if err != nil {
		return fmt.Errorf("read GitHub release: %w", err)
	}
	if int64(len(body)) > maxReleaseMetadataBytes {
		return fmt.Errorf("GitHub release metadata exceeds size limit")
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("decode GitHub release: %w", err)
	}
	return nil
}

func (r *GitHubReleaseResolver) downloadBounded(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid asset URL")
	}
	if err := validateHTTPSURL(request.URL); err != nil {
		return nil, err
	}
	initialHost := strings.ToLower(request.URL.Host)
	client := *r.client
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if err := validateHTTPSURL(next.URL); err != nil {
			return fmt.Errorf("untrusted redirect: %w", err)
		}
		if !allowedReleaseRedirectHost(initialHost, next.URL.Host) {
			return fmt.Errorf("untrusted redirect host")
		}
		next.Header.Del("Authorization")
		return nil
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("asset download failed with HTTP %d", response.StatusCode)
	}
	if response.ContentLength > limit {
		return nil, fmt.Errorf("asset exceeds size limit")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("asset exceeds size limit")
	}
	return body, nil
}

func checksumForArchive(body, archiveName string) (string, error) {
	var digest string
	for _, line := range strings.Split(body, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 {
			return "", fmt.Errorf("invalid checksums.txt entry")
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name != archiveName {
			continue
		}
		if digest != "" {
			return "", fmt.Errorf("duplicate checksum entry")
		}
		var err error
		digest, err = normalizeDigest(fields[0])
		if err != nil {
			return "", fmt.Errorf("invalid checksum entry")
		}
	}
	if digest == "" {
		return "", fmt.Errorf("checksum for release archive is missing")
	}
	return digest, nil
}

func normalizeDigest(value string) (string, error) {
	if len(value) != sha256.Size*2 {
		return "", errors.New("checksum must be SHA-256")
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return "", errors.New("checksum must be SHA-256")
	}
	return strings.ToLower(value), nil
}

func validateHTTPSURL(value *url.URL) error {
	if value == nil || value.Scheme != "https" || value.Hostname() == "" || value.User != nil {
		return fmt.Errorf("release asset URL must use HTTPS")
	}
	return nil
}

func allowedReleaseRedirectHost(initial, next string) bool {
	initial = strings.ToLower(strings.TrimSuffix(initial, "."))
	next = strings.ToLower(strings.TrimSuffix(next, "."))
	if initial == next {
		return true
	}
	nextHost := next
	if host, _, err := net.SplitHostPort(next); err == nil {
		nextHost = host
	}
	return nextHost == "github.com" || nextHost == "objects.githubusercontent.com" || nextHost == "release-assets.githubusercontent.com" || nextHost == "github-releases.githubusercontent.com" || strings.HasSuffix(nextHost, ".githubusercontent.com")
}
