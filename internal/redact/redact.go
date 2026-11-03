package redact

import (
	"regexp"
)

type Redactor struct {
	patterns []*replacementRule
}

type replacementRule struct {
	re   *regexp.Regexp
	repl string
}

// NewRedactor creates an engine with standard built-in credential and PII sanitization rules
func NewRedactor(customRegexes []string) (*Redactor, error) {
	rules := []*replacementRule{
		// 1. Bearer Tokens
		{
			re:   regexp.MustCompile(`(?i)\b(bearer\s+)[A-Za-z0-9\-_.~+/]+=*`),
			repl: "${1}[REDACTED_BEARER]",
		},
		// 2. AWS Access Key IDs
		{
			re:   regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
			repl: "[REDACTED_AWS_KEY]",
		},
		// 3. AWS Secret Access Keys
		{
			re:   regexp.MustCompile(`(?i)(aws_secret_access_key\s*[:=]\s*)[A-Za-z0-9/+=]{40}`),
			repl: "${1}[REDACTED_AWS_SECRET]",
		},
		// 4. JSON Web Tokens (JWT)
		{
			re:   regexp.MustCompile(`\beyJ[A-Za-z0-9\-_=]+\.eyJ[A-Za-z0-9\-_=]+\.[A-Za-z0-9\-_=]+\b`),
			repl: "[REDACTED_JWT]",
		},
		// 5. Emails
		{
			re:   regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b`),
			repl: "[REDACTED_EMAIL]",
		},
		// 6. Credit Card Numbers (16-digit formatted or continuous)
		{
			re:   regexp.MustCompile(`\b(?:\d{4}[ -]?){3}\d{4}\b`),
			repl: "[REDACTED_CARD]",
		},
	}

	for _, pattern := range customRegexes {
		if pattern == "" {
			continue
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, err
		}
		rules = append(rules, &replacementRule{
			re:   re,
			repl: "[REDACTED_CUSTOM]",
		})
	}

	return &Redactor{patterns: rules}, nil
}

// Redact applies all sanitization rules to input byte slice
func (r *Redactor) Redact(input []byte) []byte {
	if r == nil || len(r.patterns) == 0 || len(input) == 0 {
		return input
	}

	res := input
	for _, rule := range r.patterns {
		if rule.re.Match(res) {
			res = rule.re.ReplaceAll(res, []byte(rule.repl))
		}
	}
	return res
}
