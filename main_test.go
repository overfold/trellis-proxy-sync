package main

import (
	"testing"

	"github.com/clofour/trellis/internal/api"
)

func TestSelectHostPort(t *testing.T) {
	ports := []api.PortMapping{
		{HostPort: 31000, ContainerPort: 8080},
		{HostPort: 32000, ContainerPort: 9090},
	}

	if got, ok := selectHostPort(ports, 0); !ok || got != 31000 {
		t.Fatalf("default port = %d, %v; want 31000, true", got, ok)
	}
	if got, ok := selectHostPort(ports, 9090); !ok || got != 32000 {
		t.Fatalf("selected port = %d, %v; want 32000, true", got, ok)
	}
	if got, ok := selectHostPort(ports, 7070); ok || got != 0 {
		t.Fatalf("missing port = %d, %v; want 0, false", got, ok)
	}
}
