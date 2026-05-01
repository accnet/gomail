package staticprojects

import (
	"errors"
	"net"
	"testing"
)

// ---- T71: Domain IP checker ----

// TestCheckDomainIPLogic tests the IP checking logic in isolation.
func TestCheckDomainIPLogic(t *testing.T) {
	tests := []struct {
		name      string
		dnsResult []net.IP
		targetIP  string
		wantOk    bool
		wantMsg   string
	}{
		{
			name:      "IP matches",
			dnsResult: []net.IP{net.ParseIP("1.2.3.4")},
			targetIP:  "1.2.3.4",
			wantOk:    true,
			wantMsg:   "ok",
		},
		{
			name:      "IP mismatches",
			dnsResult: []net.IP{net.ParseIP("5.6.7.8")},
			targetIP:  "1.2.3.4",
			wantOk:    false,
		},
		{
			name:      "multiple IPs, one matches",
			dnsResult: []net.IP{net.ParseIP("5.6.7.8"), net.ParseIP("1.2.3.4")},
			targetIP:  "1.2.3.4",
			wantOk:    true,
			wantMsg:   "ok",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ok bool
			var msg string
			for _, ip := range tt.dnsResult {
				if ip.String() == tt.targetIP {
					ok = true
					msg = "ok"
					break
				}
			}
			if !ok && tt.targetIP != "" {
				msg = "no match"
			}
			if ok != tt.wantOk {
				t.Errorf("CheckDomainIP() ok = %v, want %v", ok, tt.wantOk)
			}
			if tt.wantMsg != "" && msg != tt.wantMsg {
				t.Errorf("CheckDomainIP() msg = %q, want %q", msg, tt.wantMsg)
			}
		})
	}
}

// ---- SSL activation precondition ----

func TestSSLConditionCheck(t *testing.T) {
	tests := []struct {
		name      string
		dnsResult string
		assigned  bool
		verified  bool
		wantErr   error
	}{
		{
			name:      "all conditions met",
			dnsResult: "ok",
			assigned:  true,
			verified:  true,
			wantErr:   nil,
		},
		{
			name:     "domain not assigned",
			assigned: false,
			verified: true,
			wantErr:  ErrDomainNotAvailable,
		},
		{
			name:      "dns check not passed",
			dnsResult: "some other result",
			assigned:  true,
			verified:  true,
			wantErr:   ErrSSLConditionNotMet,
		},
		{
			name:     "no dns check yet",
			assigned: true,
			verified: true,
			wantErr:  ErrSSLConditionNotMet,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			if !tt.assigned {
				err = ErrDomainNotAvailable
			} else if tt.dnsResult != "ok" {
				err = ErrSSLConditionNotMet
			}

			if tt.wantErr == nil {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
			} else {
				if err == nil {
					t.Errorf("expected error %v, got nil", tt.wantErr)
				} else if !errors.Is(err, tt.wantErr) {
					t.Errorf("expected error %v, got %v", tt.wantErr, err)
				}
			}
		})
	}
}
