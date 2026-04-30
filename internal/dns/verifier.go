package dns

import (
	"context"
	"net"
	"strings"
	"time"
)

type Resolver interface {
	LookupMX(ctx context.Context, name string) ([]*net.MX, error)
}

type NetResolver struct{}

func (NetResolver) LookupMX(ctx context.Context, name string) ([]*net.MX, error) {
	return net.DefaultResolver.LookupMX(ctx, name)
}

type Verifier struct {
	Resolver Resolver
	Timeout  time.Duration
	MXTarget string
}

func (v Verifier) Verify(ctx context.Context, domain string) (bool, string) {
	timeout := v.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resolver := v.Resolver
	if resolver == nil {
		resolver = NetResolver{}
	}
	records, err := resolver.LookupMX(ctx, domain)
	if err != nil {
		return false, err.Error()
	}
	target := normalize(v.MXTarget)
	for _, mx := range records {
		if normalize(mx.Host) == target {
			return true, ""
		}
	}
	return false, "MX does not point to " + v.MXTarget
}

func normalize(s string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), ".")
}
