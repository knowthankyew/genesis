package cli

import (
	"testing"
)

func TestCLI_IsBindingAllInterfaces(t *testing.T) {
	tests := []struct {
		addr     string
		expected bool
	}{
		{":8080", true},
		{"0.0.0.0:8080", true},
		{"0.0.0.0", true},
		{"127.0.0.1:8080", false},
		{"localhost:8080", false},
		{"192.168.1.100:8080", false},
	}

	for _, tc := range tests {
		got := IsBindingAllInterfaces(tc.addr)
		if got != tc.expected {
			t.Errorf("IsBindingAllInterfaces(%q) = %v; expected %v", tc.addr, got, tc.expected)
		}
	}
}
