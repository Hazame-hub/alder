package main

import "testing"

func TestIsLoopback(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:8443":   true,
		"localhost:8443":   true,
		"[::1]:8443":       true,
		"127.0.0.2:8443":   true,
		"0.0.0.0:8443":     false,
		"[::]:8443":        false,
		":8443":            false,
		"192.0.2.1:8443":   false,
		"localhost":        false, // no port: not an address Alder can listen on
		"[::1]":            false,
		"example.com:8443": false,
	} {
		if got := isLoopback(addr); got != want {
			t.Errorf("isLoopback(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestDisplayAddr(t *testing.T) {
	for addr, want := range map[string]string{
		":8443":          "localhost:8443",
		"0.0.0.0:8443":   "localhost:8443",
		"[::]:8443":      "localhost:8443",
		"[::1]:8443":     "[::1]:8443",
		"127.0.0.1:8443": "127.0.0.1:8443",
		"host:http":      "host:http",
	} {
		if got := displayAddr(addr); got != want {
			t.Errorf("displayAddr(%q) = %q, want %q", addr, got, want)
		}
	}
}
