package main

import (
	"net"
	"testing"
)

func TestSlotPortPersistsAcrossManagerRestart(t *testing.T) {
	dir := t.TempDir()
	first := NewManager(2, dir, "127.0.0.1")
	first.mu.Lock()
	port, err := first.portForSlotLocked(1)
	first.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	second := NewManager(2, dir, "127.0.0.1")
	second.loadSlotPorts()
	second.mu.Lock()
	restored, err := second.portForSlotLocked(1)
	second.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if restored != port {
		t.Fatalf("port changed across restart: %d -> %d", port, restored)
	}
}

func TestReservedSlotPortIsNeverSilentlyChanged(t *testing.T) {
	dir := t.TempDir()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	m := NewManager(1, dir, "127.0.0.1")
	m.slotPorts[1] = port
	m.mu.Lock()
	got, err := m.portForSlotLocked(1)
	m.mu.Unlock()
	if err == nil {
		t.Fatalf("expected occupied-port error, got port %d", got)
	}
	if m.slotPorts[1] != port {
		t.Fatalf("reserved port changed: %d -> %d", port, m.slotPorts[1])
	}
}
