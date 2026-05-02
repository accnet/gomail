package dns

import (
	"context"
	"net"
	"reflect"
	"testing"
)

type fakeResolver []*net.MX

func (f fakeResolver) LookupMX(ctx context.Context, name string) ([]*net.MX, error) {
	return []*net.MX(f), nil
}

func (f fakeResolver) LookupTXT(ctx context.Context, name string) ([]string, error) {
	return nil, nil
}

func (f fakeResolver) LookupIPAddr(ctx context.Context, name string) ([]net.IPAddr, error) {
	return nil, nil
}

type fakeTXTResolver []string

func (f fakeTXTResolver) LookupMX(ctx context.Context, name string) ([]*net.MX, error) {
	return nil, nil
}

func (f fakeTXTResolver) LookupTXT(ctx context.Context, name string) ([]string, error) {
	return []string(f), nil
}

func (f fakeTXTResolver) LookupIPAddr(ctx context.Context, name string) ([]net.IPAddr, error) {
	return nil, nil
}

func TestVerifierRequiresExpectedMXTarget(t *testing.T) {
	v := Verifier{Resolver: fakeResolver{{Host: "mx.example.com."}}, MXTarget: "mx.example.com"}
	ok, msg := v.Verify(context.Background(), "user.test")
	if !ok || msg != "" {
		t.Fatalf("expected verify ok, got ok=%v msg=%q", ok, msg)
	}
}

func TestVerifierRejectsWrongMXTarget(t *testing.T) {
	v := Verifier{Resolver: fakeResolver{{Host: "other.example.com."}}, MXTarget: "mx.example.com"}
	ok, _ := v.Verify(context.Background(), "user.test")
	if ok {
		t.Fatal("expected verify failure")
	}
}

func TestVerifierChecksSPFRequiredMechanism(t *testing.T) {
	v := Verifier{Resolver: fakeTXTResolver{`v=spf1 ip4:203.0.113.10 mx -all`}}
	ok, msg := v.VerifySPF(context.Background(), "example.com", "ip4:203.0.113.10")
	if !ok || msg != "" {
		t.Fatalf("expected SPF verify ok, got ok=%v msg=%q", ok, msg)
	}
}

func TestVerifierChecksSPFRequiredMechanismWithDefaultQualifier(t *testing.T) {
	v := Verifier{Resolver: fakeTXTResolver{`v=spf1 +ip4:203.0.113.10/32 mx -all`}}
	ok, msg := v.VerifySPF(context.Background(), "example.com", "ip4:203.0.113.10")
	if !ok || msg != "" {
		t.Fatalf("expected SPF verify ok, got ok=%v msg=%q", ok, msg)
	}
}

func TestVerifierRejectsMultipleSPFRecords(t *testing.T) {
	v := Verifier{Resolver: fakeTXTResolver{`v=spf1 mx -all`, `v=spf1 ip4:203.0.113.10 -all`}}
	ok, _ := v.VerifySPF(context.Background(), "example.com", "ip4:203.0.113.10")
	if ok {
		t.Fatal("expected SPF verify failure")
	}
}

func TestVerifierChecksDKIMPublicKey(t *testing.T) {
	v := Verifier{Resolver: fakeTXTResolver{`v=DKIM1; k=rsa; p=abc123`}}
	ok, msg := v.VerifyDKIM(context.Background(), "gomail._domainkey.example.com", "abc123")
	if !ok || msg != "" {
		t.Fatalf("expected DKIM verify ok, got ok=%v msg=%q", ok, msg)
	}
}

func TestNewNetResolverNormalizesServers(t *testing.T) {
	resolver := NewNetResolver([]string{"1.1.1.1", " 8.8.8.8:5353 ", ""})
	want := []string{"1.1.1.1:53", "8.8.8.8:5353"}
	if !reflect.DeepEqual(resolver.Servers, want) {
		t.Fatalf("Servers = %v want %v", resolver.Servers, want)
	}
}
