package dns

import (
	"context"
	"net"
	"testing"
)

type fakeResolver []*net.MX

func (f fakeResolver) LookupMX(ctx context.Context, name string) ([]*net.MX, error) {
	return []*net.MX(f), nil
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
