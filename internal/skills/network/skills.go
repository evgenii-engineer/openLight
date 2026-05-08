package network

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"

	"openlight/internal/skills"
)

type portCheckSkill struct{ manager Manager }

func NewPortCheckSkill(m Manager) skills.Skill { return &portCheckSkill{manager: m} }

func (s *portCheckSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "port_check",
		Group:       skills.GroupNetwork,
		Description: "Check whether a TCP port is reachable on an allowlisted host.",
		Aliases:     []string{"check port", "tcp check"},
		Usage:       "/port_check host:port",
		Examples:    []string{"/port_check raspberrypi.local:22", "/port_check example.com:443"},
	}
}

func (s *portCheckSkill) UI() skills.UIDescriptor {
	return skills.UIDescriptor{
		Inputs: []skills.InputField{
			{Name: "target", Prompt: "Which host:port?", Placeholder: "raspberrypi.local:22"},
		},
	}
}

func (s *portCheckSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	host, port, err := resolveTargetArg(input)
	if err != nil {
		return skills.Result{}, err
	}
	res, err := s.manager.PortCheck(ctx, host, port)
	if err != nil {
		return skills.Result{}, err
	}
	state := "OPEN"
	if !res.Open {
		state = "CLOSED"
	}
	return skills.Result{Text: fmt.Sprintf("%s:%d %s (%s)", res.Host, res.Port, state, res.Latency.Round(1_000_000))}, nil
}

type httpCheckSkill struct{ manager Manager }

func NewHTTPCheckSkill(m Manager) skills.Skill { return &httpCheckSkill{manager: m} }

func (s *httpCheckSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "http_check",
		Group:       skills.GroupNetwork,
		Description: "Fetch an allowlisted URL and report status code, latency, and optional body match.",
		Aliases:     []string{"check http", "http get"},
		Usage:       "/http_check <url> [expect=text]",
		Examples:    []string{"/http_check https://example.com", "/http_check https://api.example.com/health expect=ok"},
	}
}

func (s *httpCheckSkill) UI() skills.UIDescriptor {
	return skills.UIDescriptor{
		Inputs: []skills.InputField{
			{Name: "url", Prompt: "Which URL?", Placeholder: "https://example.com"},
			{Name: "expect", Prompt: "Body must contain (optional)", Placeholder: "ok"},
		},
	}
}

func (s *httpCheckSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	url := firstNonEmpty(input.Args["url"], firstField(input.RawText))
	if url == "" {
		return skills.Result{}, fmt.Errorf("%w: url is required", skills.ErrInvalidArguments)
	}
	res, err := s.manager.HTTPCheck(ctx, url, input.Args["expect"])
	if err != nil {
		return skills.Result{}, err
	}

	lines := []string{
		fmt.Sprintf("GET %s", res.URL),
		fmt.Sprintf("Status: %d", res.StatusCode),
		fmt.Sprintf("Latency: %s", res.Latency.Round(1_000_000)),
	}
	if res.ExpectedText != "" {
		match := "no"
		if res.BodyMatch {
			match = "yes"
		}
		lines = append(lines, fmt.Sprintf("Body contains %q: %s", res.ExpectedText, match))
	}
	return skills.Result{Text: strings.Join(lines, "\n")}, nil
}

type certCheckSkill struct{ manager Manager }

func NewCertCheckSkill(m Manager) skills.Skill { return &certCheckSkill{manager: m} }

func (s *certCheckSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "cert_check",
		Group:       skills.GroupNetwork,
		Description: "Inspect the TLS certificate served by an allowlisted host (defaults to port 443).",
		Aliases:     []string{"check cert", "tls check"},
		Usage:       "/cert_check host[:port]",
		Examples:    []string{"/cert_check example.com", "/cert_check imap.example.com:993"},
	}
}

func (s *certCheckSkill) UI() skills.UIDescriptor {
	return skills.UIDescriptor{
		Inputs: []skills.InputField{
			{Name: "target", Prompt: "Which host[:port]?", Placeholder: "example.com:443"},
		},
	}
}

func (s *certCheckSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	host, port, err := resolveTargetArgDefaultPort(input, 443)
	if err != nil {
		return skills.Result{}, err
	}
	res, err := s.manager.CertCheck(ctx, host, port)
	if err != nil {
		return skills.Result{}, err
	}
	lines := []string{
		fmt.Sprintf("Host: %s:%d", res.Host, res.Port),
		fmt.Sprintf("Subject: %s", res.Subject),
		fmt.Sprintf("Issuer: %s", res.Issuer),
		fmt.Sprintf("Expires: %s (%d days left)", res.NotAfter.UTC().Format("2006-01-02"), res.DaysLeft),
	}
	if len(res.DNSNames) > 0 {
		lines = append(lines, "SANs: "+strings.Join(res.DNSNames, ", "))
	}
	return skills.Result{Text: strings.Join(lines, "\n")}, nil
}

type dnsCheckSkill struct{ manager Manager }

func NewDNSCheckSkill(m Manager) skills.Skill { return &dnsCheckSkill{manager: m} }

func (s *dnsCheckSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "dns_check",
		Group:       skills.GroupNetwork,
		Description: "Resolve an allowlisted hostname to its A/AAAA records.",
		Aliases:     []string{"check dns", "dns lookup"},
		Usage:       "/dns_check host",
		Examples:    []string{"/dns_check example.com"},
	}
}

func (s *dnsCheckSkill) UI() skills.UIDescriptor {
	return skills.UIDescriptor{
		Inputs: []skills.InputField{
			{Name: "host", Prompt: "Which host?", Placeholder: "example.com"},
		},
	}
}

func (s *dnsCheckSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	host := firstNonEmpty(input.Args["host"], firstField(input.RawText))
	if host == "" {
		return skills.Result{}, fmt.Errorf("%w: host is required", skills.ErrInvalidArguments)
	}
	res, err := s.manager.DNSCheck(ctx, host)
	if err != nil {
		return skills.Result{}, err
	}
	lines := []string{fmt.Sprintf("Host: %s (%s)", res.Host, res.Latency.Round(1_000_000))}
	if len(res.IPv4) > 0 {
		lines = append(lines, "A:    "+strings.Join(res.IPv4, ", "))
	}
	if len(res.IPv6) > 0 {
		lines = append(lines, "AAAA: "+strings.Join(res.IPv6, ", "))
	}
	if len(res.IPv4) == 0 && len(res.IPv6) == 0 {
		lines = append(lines, "No A or AAAA records returned.")
	}
	return skills.Result{Text: strings.Join(lines, "\n")}, nil
}

// ---- helpers --------------------------------------------------------------

func resolveTargetArg(input skills.Input) (string, int, error) {
	return parseHostPortArg(input, 0)
}

func resolveTargetArgDefaultPort(input skills.Input, defaultPort int) (string, int, error) {
	return parseHostPortArg(input, defaultPort)
}

func parseHostPortArg(input skills.Input, defaultPort int) (string, int, error) {
	target := firstNonEmpty(input.Args["target"], input.Args["host"], firstField(input.RawText))
	if target == "" {
		return "", 0, fmt.Errorf("%w: target host[:port] is required", skills.ErrInvalidArguments)
	}
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		// No port given.
		if defaultPort == 0 {
			return "", 0, fmt.Errorf("%w: target must be host:port", skills.ErrInvalidArguments)
		}
		return strings.ToLower(strings.TrimSpace(target)), defaultPort, nil
	}
	port, perr := strconv.Atoi(portStr)
	if perr != nil || port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("%w: invalid port %q", skills.ErrInvalidArguments, portStr)
	}
	return strings.ToLower(strings.TrimSpace(host)), port, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if t := strings.TrimSpace(v); t != "" {
			return t
		}
	}
	return ""
}

func firstField(s string) string {
	fields := strings.Fields(strings.TrimSpace(s))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}
