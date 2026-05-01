package handlers

import (
	"context"
	"net"
	"testing"
	"time"

	"gomail/internal/db"
	"gomail/internal/dns"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type mxResolverStub struct {
	records map[string][]*net.MX
	errs    map[string]error
}

func (m mxResolverStub) LookupMX(_ context.Context, name string) ([]*net.MX, error) {
	if err := m.errs[name]; err != nil {
		return nil, err
	}
	return m.records[name], nil
}

func TestRecheckDomainsOnceUpdatesStatuses(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(database); err != nil {
		t.Fatal(err)
	}

	pending := db.Domain{Name: "pending.test", Status: "pending", MXTarget: "mx.test.local"}
	verified := db.Domain{Name: "verified.test", Status: "verified", MXTarget: "mx.test.local"}
	if err := database.Create(&pending).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&verified).Error; err != nil {
		t.Fatal(err)
	}

	verifier := dns.Verifier{
		MXTarget: "mx.test.local",
		Timeout:  time.Second,
		Resolver: mxResolverStub{
			records: map[string][]*net.MX{
				"pending.test":  {{Host: "mx.test.local.", Pref: 10}},
				"verified.test": {{Host: "other.test.local.", Pref: 10}},
			},
		},
	}

	recheckDomainsOnce(context.Background(), database, verifier)

	var gotPending db.Domain
	var gotVerified db.Domain
	if err := database.First(&gotPending, "id = ?", pending.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.First(&gotVerified, "id = ?", verified.ID).Error; err != nil {
		t.Fatal(err)
	}
	if gotPending.Status != "verified" || gotPending.WarningStatus != "" {
		t.Fatalf("unexpected pending domain after recheck: %+v", gotPending)
	}
	if gotVerified.Status != "verified" || gotVerified.WarningStatus != "verified_warning" {
		t.Fatalf("unexpected verified domain after recheck: %+v", gotVerified)
	}
}
