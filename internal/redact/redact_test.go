package redact

import (
	"bytes"
	"testing"
)

func TestRedactor_BuiltinPatterns(t *testing.T) {
	redactor, err := NewRedactor(nil)
	if err != nil {
		t.Fatalf("failed to create redactor: %v", err)
	}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Bearer Token",
			input:    "Authorization: Bearer ya29.a0AfH6SMDIabcdef1234567890",
			expected: "Authorization: Bearer [REDACTED_BEARER]",
		},
		{
			name:     "AWS Access Key",
			input:    "Connected with AKIAIOSFODNN7EXAMPLE key id",
			expected: "Connected with [REDACTED_AWS_KEY] key id",
		},
		{
			name:     "JWT Token",
			input:    "Session token: eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
			expected: "Session token: [REDACTED_JWT]",
		},
		{
			name:     "Email Address",
			input:    "User alice.smith@corp.example.com logged in successfully",
			expected: "User [REDACTED_EMAIL] logged in successfully",
		},
		{
			name:     "Credit Card",
			input:    "Payment processed for card 4532-1234-5678-9012 at checkout",
			expected: "Payment processed for card [REDACTED_CARD] at checkout",
		},
		{
			name:     "Clean log with no secrets",
			input:    "Normal log line without secrets",
			expected: "Normal log line without secrets",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := redactor.Redact([]byte(tc.input))
			if !bytes.Equal(got, []byte(tc.expected)) {
				t.Errorf("\ngot:      %s\nexpected: %s", string(got), tc.expected)
			}
		})
	}
}

func TestRedactor_CustomRegex(t *testing.T) {
	custom := []string{`internal-user-\d+`}
	redactor, err := NewRedactor(custom)
	if err != nil {
		t.Fatalf("failed to compile custom regex: %v", err)
	}

	input := []byte("Job assigned to internal-user-9912 on node 1")
	expected := "Job assigned to [REDACTED_CUSTOM] on node 1"
	got := redactor.Redact(input)

	if string(got) != expected {
		t.Errorf("got %q, expected %q", string(got), expected)
	}
}
