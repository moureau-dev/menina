package provision

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const cloudflareAPIBase = "https://api.cloudflare.com/client/v4"

type CloudflareClient struct {
	APIToken string
	BaseURL  string
	HTTP     *http.Client
}

func NewCloudflareClient(apiToken string) *CloudflareClient {
	return &CloudflareClient{
		APIToken: apiToken,
		BaseURL:  cloudflareAPIBase,
		HTTP:     &http.Client{Timeout: 30 * time.Second},
	}
}

type cfEnvelope struct {
	Success bool            `json:"success"`
	Errors  []cfError       `json:"errors"`
	Result  json.RawMessage `json:"result"`
}

type cfError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *CloudflareClient) do(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var env cfEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("cloudflare: decode response: %w", err)
	}
	if !env.Success {
		if len(env.Errors) > 0 {
			return nil, fmt.Errorf("cloudflare API error %d: %s", env.Errors[0].Code, env.Errors[0].Message)
		}
		return nil, fmt.Errorf("cloudflare API request failed (status %d)", resp.StatusCode)
	}
	return env.Result, nil
}

type DNSRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
	TTL     int    `json:"ttl"`
}

func (c *CloudflareClient) FindZoneID(ctx context.Context, zone string) (string, error) {
	result, err := c.do(ctx, http.MethodGet, "/zones?name="+url.QueryEscape(zone), nil)
	if err != nil {
		return "", err
	}
	var zones []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(result, &zones); err != nil {
		return "", err
	}
	if len(zones) == 0 {
		return "", fmt.Errorf("cloudflare zone %q not found", zone)
	}
	return zones[0].ID, nil
}

func (c *CloudflareClient) UpsertDNSRecord(ctx context.Context, zoneID, recordType, name, content string, proxied bool) error {
	result, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/zones/%s/dns_records?name=%s", zoneID, url.QueryEscape(name)), nil)
	if err != nil {
		return err
	}
	var records []DNSRecord
	if err := json.Unmarshal(result, &records); err != nil {
		return err
	}
	// Never clobber an unrelated record that already owns this name.
	var matches []DNSRecord
	for _, r := range records {
		if !strings.EqualFold(r.Type, recordType) {
			return fmt.Errorf("DNS name %q already has a %s record; refusing to overwrite it with a %s record", name, r.Type, recordType)
		}
		matches = append(matches, r)
	}
	payload := map[string]any{"type": recordType, "name": name, "content": content, "proxied": proxied, "ttl": 1}
	if len(matches) == 0 {
		_, err := c.do(ctx, http.MethodPost, fmt.Sprintf("/zones/%s/dns_records", zoneID), payload)
		return err
	}
	_, err = c.do(ctx, http.MethodPut, fmt.Sprintf("/zones/%s/dns_records/%s", zoneID, matches[0].ID), payload)
	return err
}

type Tunnel struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

func (c *CloudflareClient) ListTunnels(ctx context.Context, accountID string) ([]Tunnel, error) {
	result, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/accounts/%s/cfd_tunnel?is_deleted=false", accountID), nil)
	if err != nil {
		return nil, err
	}
	var tunnels []Tunnel
	if err := json.Unmarshal(result, &tunnels); err != nil {
		return nil, err
	}
	return tunnels, nil
}

func (c *CloudflareClient) TunnelStatus(ctx context.Context, accountID, tunnelID string) (string, error) {
	result, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/accounts/%s/cfd_tunnel/%s", accountID, tunnelID), nil)
	if err != nil {
		return "", err
	}
	var t Tunnel
	if err := json.Unmarshal(result, &t); err != nil {
		return "", err
	}
	return t.Status, nil
}
