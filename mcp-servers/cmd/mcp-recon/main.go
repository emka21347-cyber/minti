// minti-mcp-recon — recon tool wrappers (nmap, whois, dig, http probe).
//
// All inputs are validated with strict regexes before being passed to the
// underlying binary. exec.Command is used directly — never /bin/sh — so a
// crafted "target" or "domain" can't introduce shell metacharacters.
//
// Per PRD §6.6: each tool gets the *safest* default flags; potentially
// disruptive flags (nmap -sS raw socket) are gated by policy.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/minti/mcp-servers/internal/audit"
	"github.com/minti/mcp-servers/internal/mcpserve"
	"github.com/minti/mcp-servers/internal/policy"
	"github.com/minti/mcp-servers/internal/proc"
)

var version = "0.1.0-M2"

// Acceptable scan target: hostname, IPv4, IPv6, or CIDR. Conservative — only
// chars that can appear in those forms.
var targetRe = regexp.MustCompile(`^[A-Za-z0-9._:\-/]+$`)

// Acceptable DNS name for whois/dig.
var dnsRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Acceptable dig query type.
var qtypeRe = regexp.MustCompile(`^[A-Z]{1,10}$`)

func main() {
	if err := run(); err != nil {
		log.Fatalf("minti-mcp-recon: %v", err)
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

	srv := mcpserve.New("minti-mcp-recon", version, pol, logger)
	rp := &pol.MCP.Recon

	mcpserve.AddTool(srv, &mcp.Tool{
		Name: "nmap_scan",
		Description: "Run nmap against a target with safe defaults (-sV -T3 --top-ports 1000). " +
			"Set raw_socket=true to request a SYN scan (-sS); requires mcp.recon.allow_raw_socket=true in policy.",
	}, func(ctx context.Context, in NmapIn) (NmapOut, error) {
		return nmapScan(ctx, rp, in)
	})

	mcpserve.AddTool(srv, &mcp.Tool{
		Name:        "whois",
		Description: "Look up WHOIS records for a domain name.",
	}, whoisLookup)

	mcpserve.AddTool(srv, &mcp.Tool{
		Name:        "dig_lookup",
		Description: "Resolve DNS records via dig. Default record type is A.",
	}, digLookup)

	mcpserve.AddTool(srv, &mcp.Tool{
		Name:        "http_probe",
		Description: "Send a HEAD request to a URL. Returns status, server header, and full header map.",
	}, httpProbe)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	return srv.Run(ctx)
}

type NmapIn struct {
	Target    string `json:"target" jsonschema:"hostname, IPv4, IPv6, or CIDR (e.g. 127.0.0.1, scanme.nmap.org, 10.0.0.0/24)"`
	RawSocket bool   `json:"raw_socket,omitempty" jsonschema:"if true, attempt SYN scan (-sS); requires policy"`
}
type NmapOut struct {
	Command string `json:"command"`
	Output  string `json:"output"`
}

func nmapScan(ctx context.Context, rp *policy.ReconPolicy, in NmapIn) (NmapOut, error) {
	if !targetRe.MatchString(in.Target) {
		return NmapOut{}, fmt.Errorf("invalid target: %q", in.Target)
	}
	args := []string{"-sV", "-T3", "--top-ports", "1000"}
	if in.RawSocket {
		// Defense-in-depth: permission.Check already gates this, but re-verify
		// inside the handler so any future caller bypass is still caught.
		if !rp.AllowRawSocket {
			return NmapOut{}, fmt.Errorf("nmap raw-socket scan requires mcp.recon.allow_raw_socket=true")
		}
		args = append([]string{"-sS"}, args[1:]...)
	}
	args = append(args, in.Target)

	cmdCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	out, err := runRecon(cmdCtx, "nmap", args)
	return NmapOut{Command: "nmap " + strings.Join(args, " "), Output: out}, err
}

type WhoisIn struct {
	Domain string `json:"domain"`
}
type WhoisOut struct {
	Raw string `json:"raw"`
}

func whoisLookup(ctx context.Context, in WhoisIn) (WhoisOut, error) {
	if !dnsRe.MatchString(in.Domain) {
		return WhoisOut{}, fmt.Errorf("invalid domain: %q", in.Domain)
	}
	cmdCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := runRecon(cmdCtx, "whois", []string{in.Domain})
	return WhoisOut{Raw: out}, err
}

type DigIn struct {
	Name string `json:"name"`
	Type string `json:"type,omitempty" jsonschema:"DNS record type (A, AAAA, MX, TXT, NS, SOA, ...). Default A."`
}
type DigOut struct {
	Command string `json:"command"`
	Output  string `json:"output"`
}

func digLookup(ctx context.Context, in DigIn) (DigOut, error) {
	if !dnsRe.MatchString(in.Name) {
		return DigOut{}, fmt.Errorf("invalid name: %q", in.Name)
	}
	qtype := strings.ToUpper(in.Type)
	if qtype == "" {
		qtype = "A"
	}
	if !qtypeRe.MatchString(qtype) {
		return DigOut{}, fmt.Errorf("invalid record type: %q", in.Type)
	}
	args := []string{"+short", in.Name, qtype}
	cmdCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := runRecon(cmdCtx, "dig", args)
	return DigOut{Command: "dig " + strings.Join(args, " "), Output: out}, err
}

type ProbeIn struct {
	URL string `json:"url"`
}
type ProbeOut struct {
	Status  int               `json:"status"`
	Server  string            `json:"server,omitempty"`
	Headers map[string]string `json:"headers"`
}

func httpProbe(ctx context.Context, in ProbeIn) (ProbeOut, error) {
	if !strings.HasPrefix(in.URL, "http://") && !strings.HasPrefix(in.URL, "https://") {
		return ProbeOut{}, fmt.Errorf("url must be http:// or https://: %q", in.URL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, in.URL, nil)
	if err != nil {
		return ProbeOut{}, err
	}
	req.Header.Set("User-Agent", "minti-mcp-recon/"+version)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return ProbeOut{}, err
	}
	defer resp.Body.Close()

	out := ProbeOut{
		Status:  resp.StatusCode,
		Server:  resp.Header.Get("Server"),
		Headers: make(map[string]string, len(resp.Header)),
	}
	for k, v := range resp.Header {
		if len(v) > 0 {
			out.Headers[k] = v[0]
		}
	}
	return out, nil
}

func runRecon(ctx context.Context, bin string, args []string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	stdout, stderr, exitCode, err := proc.Run(cmd)
	if ctx.Err() == context.DeadlineExceeded {
		return stdout, fmt.Errorf("%s timed out: %s", bin, strings.TrimSpace(stderr))
	}
	if err != nil && exitCode == 0 {
		return stdout, fmt.Errorf("%s: %w; stderr=%s", bin, err, strings.TrimSpace(stderr))
	}
	if exitCode != 0 {
		return stdout, fmt.Errorf("%s exited %d: %s", bin, exitCode, strings.TrimSpace(stderr))
	}
	return stdout, nil
}
