package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// cleanupOrphanOpenVPN removes only OpenVPN processes launched with this
// fanout work directory. It deliberately leaves unrelated VPN tasks alone.
func cleanupOrphanOpenVPN(workDir string) int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	prefix := filepath.Clean(workDir) + string(os.PathSeparator) + "fo"
	killed := 0
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if err != nil {
			continue
		}
		cmdline := string(bytes.ReplaceAll(raw, []byte{0}, []byte{' '}))
		if !strings.Contains(cmdline, "openvpn") || !strings.Contains(cmdline, prefix) || !strings.Contains(cmdline, ".ovpn") {
			continue
		}
		if syscall.Kill(pid, syscall.SIGKILL) == nil {
			killed++
		}
	}
	return killed
}
