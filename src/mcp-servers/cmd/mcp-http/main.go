// minti-mcp-http — HTTP fetch + probe MCP server.
//
// Pure wrapper around net/http: no JS execution, no cookie persistence, no
// content-type interpretation. Response bodies are size-capped per
// mcp.http.max_body_bytes in policy (default 1 MiB).
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/minti/mcp-servers/internal/audit"
	"github.com/minti/mcp-servers/internal/mcpserve"
	"github.com/minti/mcp-servers/internal/policy"
)

var version = "0.1.0-M2"

func main() {
	if err := run(); err != nil {
		log.Fatalf("minti-mcp-http: %v", err)
	}
}

func run() error {
	pol, err := policy.Load()
	if err != nil {
		return err
	}
	logger, err := audit.Default()
	if err != nil {
		return err
	}

	srv := mcpserve.New("minti-mcp-http", version, pol, logger)
	maxBody := pol.MCP.HTTP.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = 1 << 20
	}

	mcpserve.AddTool(srv, &mcp.Tool{
		Name:        "fetch_url",
		Description: "GET a URL and return the response body up to max_bytes. Defaults to mcp.http.max_body_bytes from policy.",
	}, func(ctx context.Context, in FetchIn) (FetchOut, error) {
		return fetchURL(ctx, maxBody, in)
	})

	mcpserve.AddTool(srv, &mcp.Tool{
		Name:        "head_url",
		Description: "HEAD a URL. Returns status, server, content length, and full header map.",
	}, headURL)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	return srv.Run(ctx)
}

type FetchIn struct {
	URL      string `json:"url"`
	MaxBytes int64  `json:"max_bytes,omitempty"`
}
type FetchOut struct {
	Status      int    `json:"status"`
	ContentType string `json:"content_type,omitempty"`
	Body        string `json:"body"`
	Bytes       int64  `json:"bytes"`
	Truncated   bool   `json:"truncated"`
}

func fetchURL(ctx context.Context, policyMax int64, in FetchIn) (FetchOut, error) {
	if !strings.HasPrefix(in.URL, "http://") && !strings.HasPrefix(in.URL, "https://") {
		return FetchOut{}, fmt.Errorf("url must be http:// or https://: %q", in.URL)
	}
	limit := in.MaxBytes
	if limit <= 0 || limit > policyMax {
		limit = policyMax
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, in.URL, nil)
	if err != nil {
		return FetchOut{}, err
	}
	req.Header.Set("User-Agent", "minti-mcp-http/"+version)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return FetchOut{}, err
	}
	defer resp.Body.Close()

	// Read up to limit+1 so we can detect truncation accurately.
	buf, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return FetchOut{}, err
	}
	truncated := int64(len(buf)) > limit
	if truncated {
		buf = buf[:limit]
	}
	return FetchOut{
		Status:      resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		Body:        string(buf),
		Bytes:       int64(len(buf)),
		Truncated:   truncated,
	}, nil
}

type HeadIn struct {
	URL string `json:"url"`
}
type HeadOut struct {
	Status        int               `json:"status"`
	Server        string            `json:"server,omitempty"`
	ContentLength int64             `json:"content_length,omitempty"`
	Headers       map[string]string `json:"headers"`
}

func headURL(ctx context.Context, in HeadIn) (HeadOut, error) {
	if !strings.HasPrefix(in.URL, "http://") && !strings.HasPrefix(in.URL, "https://") {
		return HeadOut{}, fmt.Errorf("url must be http:// or https://: %q", in.URL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, in.URL, nil)
	if err != nil {
		return HeadOut{}, err
	}
	req.Header.Set("User-Agent", "minti-mcp-http/"+version)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return HeadOut{}, err
	}
	defer resp.Body.Close()

	out := HeadOut{
		Status:        resp.StatusCode,
		Server:        resp.Header.Get("Server"),
		ContentLength: resp.ContentLength,
		Headers:       make(map[string]string, len(resp.Header)),
	}
	for k, v := range resp.Header {
		if len(v) > 0 {
			out.Headers[k] = v[0]
		}
	}
	return out, nil
}
