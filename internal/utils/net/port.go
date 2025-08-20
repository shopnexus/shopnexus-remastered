package net

import (
	"fmt"
	"net"
)

// FindNextAvailablePort tries to find an available TCP port starting from startPort.
// It increments by 1 until it finds a port that can be bound or reaches the maxPort (65535).
func FindNextAvailablePort(startPort int) (int, error) {
	const maxPort = 65535

	// Input validation
	if startPort < 1 || startPort > maxPort {
		return 0, fmt.Errorf("startPort must be between 1 and %d, got %d", maxPort, startPort)
	}

	for port := startPort; port <= maxPort; port++ {
		if !IsPortInUse(port) {
			return port, nil
		}
	}

	return 0, fmt.Errorf("no available port found starting from %d", startPort)
}

// Helper function to check if port is in use by attempting to bind to it
func IsPortInUse(port int) bool {
	// Use net.ListenTCP with explicit address for better control
	addr, err := net.ResolveTCPAddr("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return true // Consider malformed addresses as "in use"
	}

	ln, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return true // Port is in use or unavailable
	}

	ln.Close()
	return false // Port is available
}
