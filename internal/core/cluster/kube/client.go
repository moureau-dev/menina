package kube

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"time"
)

type Config struct {
	Server    string
	Token     string
	Username  string
	Password  string
	CAData    []byte
	CertData  []byte
	KeyData   []byte
	Insecure  bool
	Namespace string
}

type Client struct {
	base   *url.URL
	http   *http.Client
	token  string
	user   string
	pass   string
	ca     []byte
	server string
}

func NewClient(cfg *Config) (*Client, error) {
	base, err := url.Parse(cfg.Server)
	if err != nil {
		return nil, err
	}
	tlsCfg := &tls.Config{InsecureSkipVerify: cfg.Insecure, MinVersion: tls.VersionTLS12}
	if len(cfg.CAData) > 0 {
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(cfg.CAData)
		tlsCfg.RootCAs = pool
	}
	if len(cfg.CertData) > 0 && len(cfg.KeyData) > 0 {
		if cert, err := tls.X509KeyPair(cfg.CertData, cfg.KeyData); err == nil {
			tlsCfg.Certificates = []tls.Certificate{cert}
		}
	}
	transport := &http.Transport{
		TLSClientConfig:       tlsCfg,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   64,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ForceAttemptHTTP2:     true,
		ExpectContinueTimeout: time.Second,
	}
	return &Client{
		base:   base,
		http:   &http.Client{Transport: transport},
		token:  cfg.Token,
		user:   cfg.Username,
		pass:   cfg.Password,
		ca:     cfg.CAData,
		server: cfg.Server,
	}, nil
}

// NewInsecureClient is a convenience for tests against a plain-HTTP fake API.
func NewInsecureClient(server string) (*Client, error) {
	return NewClient(&Config{Server: server, Insecure: true})
}

func (c *Client) Server() string { return c.server }
func (c *Client) CAData() []byte { return c.ca }

func (c *Client) request(ctx context.Context, method, path string, q url.Values, body any) (*http.Response, error) {
	u := *c.base
	u.Path = path
	if q != nil {
		u.RawQuery = q.Encode()
	}
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	} else if c.user != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		resp.Body.Close()
		return nil, parseStatus(resp.StatusCode, data)
	}
	return resp, nil
}

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	resp, err := c.request(ctx, http.MethodGet, path, q, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) Get(ctx context.Context, path string, out any) error {
	return c.get(ctx, path, nil, out)
}

func (c *Client) List(ctx context.Context, path, labelSelector string, out any) error {
	var q url.Values
	if labelSelector != "" {
		q = url.Values{"labelSelector": {labelSelector}}
	}
	return c.get(ctx, path, q, out)
}

func (c *Client) Create(ctx context.Context, path string, body, out any) error {
	resp, err := c.request(ctx, http.MethodPost, path, nil, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) Update(ctx context.Context, path string, body, out any) error {
	resp, err := c.request(ctx, http.MethodPut, path, nil, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) Delete(ctx context.Context, path string) error {
	resp, err := c.request(ctx, http.MethodDelete, path, nil, nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (c *Client) Stream(ctx context.Context, path string, q url.Values) (io.ReadCloser, error) {
	resp, err := c.request(ctx, http.MethodGet, path, q, nil)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (c *Client) Version(ctx context.Context) (string, error) {
	var v struct {
		GitVersion string `json:"gitVersion"`
	}
	if err := c.get(ctx, "/version", nil, &v); err != nil {
		return "", err
	}
	return v.GitVersion, nil
}

// ToMap converts a typed object to a generic map (used to merge desired fields
// into an object fetched from the API, preserving server-assigned fields).
func ToMap(v any) map[string]any {
	data, err := json.Marshal(v)
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return map[string]any{}
	}
	return m
}
