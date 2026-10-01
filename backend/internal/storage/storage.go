// Package storage talks to Supabase Storage with the service role key.
// The bucket is private; clients only ever receive short-lived signed URLs.
package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	base   string // {SUPABASE_URL}/storage/v1
	key    string
	bucket string
	http   *http.Client
}

func New(supabaseURL, serviceKey, bucket string) *Client {
	return &Client{
		base:   supabaseURL + "/storage/v1",
		key:    serviceKey,
		bucket: bucket,
		http:   &http.Client{Timeout: 5 * time.Minute},
	}
}

var ErrNotFound = errors.New("storage object not found")

// OriginalPath and ThumbnailPath define the per-user layout.
func OriginalPath(userID, memoryID string) string {
	return fmt.Sprintf("users/%s/memories/%s/original", userID, memoryID)
}

func ThumbnailPath(userID, memoryID string) string {
	return fmt.Sprintf("users/%s/memories/%s/thumbnail", userID, memoryID)
}

func UserPrefix(userID string) string { return fmt.Sprintf("users/%s/", userID) }

func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

func (c *Client) do(ctx context.Context, method, path string, body io.Reader, contentType string, hdr map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("apikey", c.key)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return c.http.Do(req)
}

func readErr(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if resp.StatusCode == http.StatusNotFound || bytes.Contains(b, []byte("not_found")) || bytes.Contains(b, []byte("Object not found")) {
		return ErrNotFound
	}
	return fmt.Errorf("storage: status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
}

// Download returns the object bytes, refusing objects larger than maxBytes.
func (c *Client) Download(ctx context.Context, path string, maxBytes int64) ([]byte, string, error) {
	resp, err := c.do(ctx, http.MethodGet, "/object/authenticated/"+c.bucket+"/"+escapePath(path), nil, "", nil)
	if err != nil {
		return nil, "", fmt.Errorf("storage download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", readErr(resp)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("storage download: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, "", fmt.Errorf("file exceeds %d bytes", maxBytes)
	}
	return data, resp.Header.Get("Content-Type"), nil
}

// Stat returns the size of an object, or ErrNotFound.
func (c *Client) Stat(ctx context.Context, path string) (int64, string, error) {
	resp, err := c.do(ctx, http.MethodHead, "/object/authenticated/"+c.bucket+"/"+escapePath(path), nil, "", nil)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusBadRequest {
			return 0, "", ErrNotFound
		}
		return 0, "", fmt.Errorf("storage stat: status %d", resp.StatusCode)
	}
	return resp.ContentLength, resp.Header.Get("Content-Type"), nil
}

func (c *Client) Upload(ctx context.Context, path, contentType string, data []byte) error {
	resp, err := c.do(ctx, http.MethodPost, "/object/"+c.bucket+"/"+escapePath(path), bytes.NewReader(data), contentType,
		map[string]string{"x-upsert": "true", "cache-control": "private, max-age=3600"})
	if err != nil {
		return fmt.Errorf("storage upload: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return readErr(resp)
	}
	return nil
}

// SignedURL returns a time-limited download URL for a private object.
func (c *Client) SignedURL(ctx context.Context, path string, ttl time.Duration, download string) (string, error) {
	body, _ := json.Marshal(map[string]any{"expiresIn": int(ttl.Seconds())})
	resp, err := c.do(ctx, http.MethodPost, "/object/sign/"+c.bucket+"/"+escapePath(path), bytes.NewReader(body), "application/json", nil)
	if err != nil {
		return "", fmt.Errorf("storage sign: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", readErr(resp)
	}
	var out struct {
		SignedURL string `json:"signedURL"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	u := c.base + out.SignedURL
	if download != "" {
		u += "&download=" + url.QueryEscape(download)
	}
	return u, nil
}

// SignedUploadURL returns a one-time URL the client can PUT the file to.
func (c *Client) SignedUploadURL(ctx context.Context, path string) (string, error) {
	resp, err := c.do(ctx, http.MethodPost, "/object/upload/sign/"+c.bucket+"/"+escapePath(path), nil, "", map[string]string{"x-upsert": "true"})
	if err != nil {
		return "", fmt.Errorf("storage sign upload: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", readErr(resp)
	}
	var out struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return c.base + out.URL, nil
}

// Delete removes the given object paths (missing objects are ignored).
func (c *Client) Delete(ctx context.Context, paths []string) error {
	for len(paths) > 0 {
		n := min(len(paths), 500)
		body, _ := json.Marshal(map[string]any{"prefixes": paths[:n]})
		resp, err := c.do(ctx, http.MethodDelete, "/object/"+c.bucket, bytes.NewReader(body), "application/json", nil)
		if err != nil {
			return fmt.Errorf("storage delete: %w", err)
		}
		if resp.StatusCode >= 300 {
			err := readErr(resp)
			resp.Body.Close()
			if !errors.Is(err, ErrNotFound) {
				return err
			}
		} else {
			resp.Body.Close()
		}
		paths = paths[n:]
	}
	return nil
}

// ListAll returns every object path under prefix (recursively).
func (c *Client) ListAll(ctx context.Context, prefix string) ([]string, error) {
	prefix = strings.TrimSuffix(prefix, "/")
	var out []string
	const page = 1000
	for offset := 0; ; offset += page {
		body, _ := json.Marshal(map[string]any{"prefix": prefix, "limit": page, "offset": offset})
		resp, err := c.do(ctx, http.MethodPost, "/object/list/"+c.bucket, bytes.NewReader(body), "application/json", nil)
		if err != nil {
			return nil, fmt.Errorf("storage list: %w", err)
		}
		var items []struct {
			Name string  `json:"name"`
			ID   *string `json:"id"`
		}
		if resp.StatusCode >= 300 {
			err := readErr(resp)
			resp.Body.Close()
			return nil, err
		}
		err = json.NewDecoder(resp.Body).Decode(&items)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			full := prefix + "/" + it.Name
			if it.ID == nil { // folder
				sub, err := c.ListAll(ctx, full)
				if err != nil {
					return nil, err
				}
				out = append(out, sub...)
			} else {
				out = append(out, full)
			}
		}
		if len(items) < page {
			return out, nil
		}
	}
}

// SignedURLs signs many paths in one request. The result maps path → URL;
// paths that could not be signed are omitted.
func (c *Client) SignedURLs(ctx context.Context, paths []string, ttl time.Duration) (map[string]string, error) {
	out := map[string]string{}
	if len(paths) == 0 {
		return out, nil
	}
	body, _ := json.Marshal(map[string]any{"expiresIn": int(ttl.Seconds()), "paths": paths})
	resp, err := c.do(ctx, http.MethodPost, "/object/sign/"+c.bucket, bytes.NewReader(body), "application/json", nil)
	if err != nil {
		return nil, fmt.Errorf("storage sign: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, readErr(resp)
	}
	var items []struct {
		Path      string  `json:"path"`
		SignedURL *string `json:"signedURL"`
		Error     *string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, err
	}
	for _, it := range items {
		if it.SignedURL != nil && it.Error == nil {
			out[it.Path] = c.base + *it.SignedURL
		}
	}
	return out, nil
}
