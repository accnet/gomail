package dns

import (
	"context"
	"net"
	"strings"
	"time"
)

type Resolver interface {
	LookupMX(ctx context.Context, name string) ([]*net.MX, error)
	LookupTXT(ctx context.Context, name string) ([]string, error)
}

type NetResolver struct{}

func (NetResolver) LookupMX(ctx context.Context, name string) ([]*net.MX, error) {
	return net.DefaultResolver.LookupMX(ctx, name)
}

func (NetResolver) LookupTXT(ctx context.Context, name string) ([]string, error) {
	return net.DefaultResolver.LookupTXT(ctx, name)
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

func (v Verifier) VerifySPF(ctx context.Context, domain string, requiredMechanism string) (bool, string) {
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
	records, err := resolver.LookupTXT(ctx, domain)
	if err != nil {
		return false, err.Error()
	}
	var spfRecords []string
	for _, record := range records {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(record)), "v=spf1") {
			spfRecords = append(spfRecords, record)
		}
	}
	if len(spfRecords) == 0 {
		return false, "SPF TXT record not found"
	}
	if len(spfRecords) > 1 {
		return false, "multiple SPF TXT records found"
	}
	if requiredMechanism == "" {
		return true, ""
	}
	if !spfContainsMechanism(spfRecords[0], requiredMechanism) {
		return false, "SPF record does not include " + requiredMechanism
	}
	return true, ""
}

func (v Verifier) VerifyDKIM(ctx context.Context, recordName string, publicKey string) (bool, string) {
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
	records, err := resolver.LookupTXT(ctx, recordName)
	if err != nil {
		return false, err.Error()
	}
	needle := "p=" + normalizeTXTValue(publicKey)
	for _, record := range records {
		value := normalizeTXTValue(record)
		if strings.Contains(strings.ToLower(value), "v=dkim1") && strings.Contains(value, needle) {
			return true, ""
		}
	}
	return false, "DKIM TXT record not found or public key does not match"
}

func normalize(s string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), ".")
}

func normalizeSPF(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(s))), " ")
}

func spfContainsMechanism(record string, mechanism string) bool {
	needle := normalizeSPF(mechanism)
	for _, field := range strings.Fields(normalizeSPF(record)) {
		if field == needle {
			return true
		}
	}
	return false
}

func normalizeTXTValue(s string) string {
	replacer := strings.NewReplacer(" ", "", "\t", "", "\n", "", "\r", "", "\"", "")
	return replacer.Replace(strings.TrimSpace(s))
}
