package extract

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/html"
)

type LinkPreview struct {
	URL         string `json:"url"`
	FinalURL    string `json:"final_url"`
	Title       string `json:"title"`
	Description string `json:"description"`
	SiteName    string `json:"site_name"`
	ImageURL    string `json:"image_url"`
	Text        string `json:"-"`
}

// ValidateURL accepts only absolute http(s) URLs.
func ValidateURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("please provide a valid http(s) link")
	}
	return u, nil
}

// safeDialer refuses to connect to private, loopback or link-local addresses
// (SSRF protection), checked after DNS resolution on every connection.
var safeDialer = &net.Dialer{
	Timeout: 10 * time.Second,
	Control: func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip := net.ParseIP(host)
		if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
			ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() ||
			ip.Equal(net.IPv4bcast) || isCGNAT(ip) {
			return errors.New("refusing to fetch a private network address")
		}
		return nil
	},
}

func isCGNAT(ip net.IP) bool {
	v4 := ip.To4()
	return v4 != nil && v4[0] == 100 && v4[1]&0xc0 == 64
}

var linkClient = &http.Client{
	Timeout: 20 * time.Second,
	Transport: &http.Transport{
		DialContext:           safeDialer.DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		Proxy:                 nil,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return errors.New("redirect to unsupported scheme")
		}
		return nil
	},
}

// FetchLink downloads a page and extracts its metadata and readable text.
func FetchLink(ctx context.Context, raw string) (*LinkPreview, error) {
	u, err := ValidateURL(raw)
	if err != nil {
		return nil, err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	req.Header.Set("User-Agent", "MemoryBot/1.0 (+https://memory.app)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.5")
	resp, err := linkClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not fetch link: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("link returned HTTP %d", resp.StatusCode)
	}
	p := &LinkPreview{URL: u.String(), FinalURL: resp.Request.URL.String()}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "html") {
		p.Title = resp.Request.URL.Host + resp.Request.URL.Path
		return p, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 3<<20))
	if err != nil {
		return nil, fmt.Errorf("could not read link: %w", err)
	}
	parseMeta(body, p)
	p.Text, _ = HTMLText(body)
	if p.Title == "" {
		_, p.Title = HTMLText(body)
	}
	if p.ImageURL != "" {
		if iu, err := resp.Request.URL.Parse(p.ImageURL); err == nil {
			p.ImageURL = iu.String()
		}
	}
	return p, nil
}

func parseMeta(body []byte, p *LinkPreview) {
	z := html.NewTokenizer(bytes.NewReader(body))
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			return
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			if string(name) == "body" {
				return
			}
			if string(name) != "meta" || !hasAttr {
				continue
			}
			attrs := map[string]string{}
			for {
				k, v, more := z.TagAttr()
				attrs[strings.ToLower(string(k))] = string(v)
				if !more {
					break
				}
			}
			key := attrs["property"]
			if key == "" {
				key = attrs["name"]
			}
			val := strings.TrimSpace(attrs["content"])
			switch strings.ToLower(key) {
			case "og:title", "twitter:title":
				if p.Title == "" {
					p.Title = val
				}
			case "og:description", "twitter:description", "description":
				if p.Description == "" {
					p.Description = val
				}
			case "og:site_name":
				p.SiteName = val
			case "og:image", "twitter:image":
				if p.ImageURL == "" {
					p.ImageURL = val
				}
			}
		}
	}
}

// FetchImage downloads a remote image (e.g. og:image) with SSRF protection.
func FetchImage(ctx context.Context, raw string, maxBytes int64) ([]byte, error) {
	u, err := ValidateURL(raw)
	if err != nil {
		return nil, err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	req.Header.Set("User-Agent", "MemoryBot/1.0")
	resp, err := linkClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "image/") {
		return nil, fmt.Errorf("not an image")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("image too large")
	}
	return data, nil
}
