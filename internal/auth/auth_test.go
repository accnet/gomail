package auth

import "testing"

func TestPasswordHash(t *testing.T) {
	hash, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckPassword(hash, "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	if err := CheckPassword(hash, "wrong"); err == nil {
		t.Fatal("expected wrong password to fail")
	}
}
