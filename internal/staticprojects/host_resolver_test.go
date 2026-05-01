package staticprojects

import (
	"testing"
)

func TestIsStaticHost(t *testing.T) {
	tests := []struct {
		name       string
		host       string
		baseDomain string
		saasDomain string
		want       bool
	}{
		{name: "subdomain matches base", host: "myproject.example.com", baseDomain: "example.com", want: true},
		{name: "no match", host: "other.com", baseDomain: "example.com", want: false},
		{name: "port stripped", host: "myproject.example.com:8080", baseDomain: "example.com", want: true},
		{name: "subdomain matches saas", host: "myproject.saas.com", saasDomain: "saas.com", want: true},
		{name: "exact domain not subdomain", host: "example.com", baseDomain: "example.com", want: false},
		{name: "no configs", host: "example.com", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &HostResolver{BaseDomain: tt.baseDomain, SaaSDomain: tt.saasDomain}
			got := r.IsStaticHost(tt.host)
			if got != tt.want {
				t.Errorf("IsStaticHost(%q) = %v, want %v (base=%q, saas=%q)", tt.host, got, tt.want, tt.baseDomain, tt.saasDomain)
			}
		})
	}
}
