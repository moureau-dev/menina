package registry

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strings"
	"time"
)

var manifestAccepts = []string{
	"application/vnd.oci.image.manifest.v1+json",
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.docker.distribution.manifest.v2+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
}

type Client struct {
	BaseURL  string
	Username string
	Password string
	Insecure bool
	HTTP     *http.Client
}

func NewClient(baseURL, username, password string, insecure bool) *Client {
	return &Client{
		BaseURL:  strings.TrimRight(baseURL, "/"),
		Username: username,
		Password: password,
		Insecure: insecure,
		HTTP: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure},
			},
		},
	}
}

func (c *Client) ResolveTag(ctx context.Context, repository, tag string) (string, error) {
	url := fmt.Sprintf("%s/v2/%s/manifests/%s", c.BaseURL, c.repoPath(repository), tag)
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return "", err
	}
	for _, a := range manifestAccepts {
		req.Header.Add("Accept", a)
	}
	if c.Username != "" {
		req.SetBasicAuth(c.Username, c.Password)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return "", fmt.Errorf("registry authentication failed for %s:%s", repository, tag)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("resolve %s:%s: unexpected status %d", repository, tag, resp.StatusCode)
	}
	digest := resp.Header.Get("Docker-Content-Digest")
	if digest == "" {
		body, _ := io.ReadAll(resp.Body)
		digest = digestFromBody(body, resp.Header.Get("Content-Type"))
	}
	if digest == "" {
		return "", fmt.Errorf("registry did not return a digest for %s:%s", repository, tag)
	}
	return digest, nil
}

func (c *Client) ListTags(ctx context.Context, repository string) ([]string, error) {
	url := fmt.Sprintf("%s/v2/%s/tags/list", c.BaseURL, c.repoPath(repository))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if c.Username != "" {
		req.SetBasicAuth(c.Username, c.Password)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list tags %s: unexpected status %d", repository, resp.StatusCode)
	}
	var out struct {
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Tags, nil
}

func (c *Client) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/v2/", nil)
	if err != nil {
		return err
	}
	if c.Username != "" {
		req.SetBasicAuth(c.Username, c.Password)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusUnauthorized {
		return fmt.Errorf("registry ping: unexpected status %d", resp.StatusCode)
	}
	return nil
}

// repoPath strips a leading registry host from a configured repository so that
// "registry.example.com/project/api" becomes "project/api" for the /v2 API.
func (c *Client) repoPath(repository string) string {
	repo := strings.TrimPrefix(strings.TrimSpace(repository), "/")
	if c.BaseURL != "" {
		if u, err := neturl.Parse(c.BaseURL); err == nil && u.Host != "" {
			repo = strings.TrimPrefix(repo, u.Host+"/")
		}
	}
	return repo
}

func digestFromBody(body []byte, contentType string) string {
	return sha256Hex(body)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}
