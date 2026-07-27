package main

import "testing"

func TestResolveSocksAddressIPv4(t *testing.T) {
	got, err := resolveSocksAddress("192.0.2.10:443")
	if err != nil {
		t.Fatal(err)
	}
	if got != "192.0.2.10:443" {
		t.Fatalf("unexpected address %q", got)
	}
}

func TestResolveSocksAddressRejectsIPv6(t *testing.T) {
	if _, err := resolveSocksAddress("[2001:db8::1]:443"); err == nil {
		t.Fatal("expected IPv6 to be rejected")
	}
}
