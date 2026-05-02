package dns

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"time"
)

type Resolver interface {
	LookupMX(ctx context.Context, name string) ([]*net.MX, error)
	LookupTXT(ctx context.Context, name string) ([]string, error)
	LookupIPAddr(ctx context.Context, name string) ([]net.IPAddr, error)
}

type NetResolver struct{}

func (NetResolver) LookupMX(ctx context.Context, name string) ([]*net.MX, error) {
	return net.DefaultResolver.LookupMX(ctx, name)
}

func (NetResolver) LookupTXT(ctx context.Context, name string) ([]string, error) {
	return net.DefaultResolver.LookupTXT(ctx, name)
}

func (NetResolver) LookupIPAddr(ctx context.Context, name string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(ctx, name)
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

func (v Verifier) VerifyA(ctx context.Context, domain string, requiredIP string) (bool, string) {
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
	addrs, err := resolver.LookupIPAddr(ctx, domain)
	if err != nil {
		return false, err.Error()
	}
	required, err := netip.ParseAddr(strings.TrimSpace(requiredIP))
	if err != nil {
		return false, "invalid required IP " + requiredIP
	}
	required = required.Unmap()
	var found []string
	for _, addr := range addrs {
		if len(addr.IP) == 0 {
			continue
		}
		found = append(found, addr.IP.String())
		candidate, ok := netip.AddrFromSlice(addr.IP)
		if ok && candidate.Unmap().Compare(required) == 0 {
			return true, addr.IP.String()
		}
	}
	if len(found) == 0 {
		return false, "A/AAAA record not found"
	}
	return false, "domain resolves to " + strings.Join(found, ", ") + ", expected " + required.String()
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
		field = strings.TrimPrefix(field, "+")
		if field == needle || spfMechanismMatches(field, needle) {
			return true
		}
	}
	return false
}

func spfMechanismMatches(field string, needle string) bool {
	fieldKind, fieldValue, fieldHasValue := strings.Cut(field, ":")
	needleKind, needleValue, needleHasValue := strings.Cut(needle, ":")
	if fieldKind != needleKind {
		return false
	}
	if !fieldHasValue || !needleHasValue {
		return false
	}
	if fieldKind != "ip4" && fieldKind != "ip6" {
		return false
	}
	needleIP := net.ParseIP(needleValue)
	if needleIP == nil {
		return false
	}
	if !strings.Contains(fieldValue, "/") {
		return false
	}
	_, network, err := net.ParseCIDR(fieldValue)
	if err != nil {
		return false
	}
	return network.Contains(needleIP)
}

func normalizeTXTValue(s string) string {
	replacer := strings.NewReplacer(" ", "", "\t", "", "\n", "", "\r", "", "\"", "")
	return replacer.Replace(strings.TrimSpace(s))
}
