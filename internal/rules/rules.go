package rules

import (
	"fmt"
	"regexp"
)

// Violation represents a rule violation with a descriptive message.
type Violation struct {
	Rule    string
	Message string
}

func (v Violation) Error() string {
	return fmt.Sprintf("[%s] %s", v.Rule, v.Message)
}

// Rule is a function that inspects a KV write and returns a Violation if rejected.
type Rule func(key string, value []byte) *Violation

// RuleSet holds all configured rules.
type RuleSet struct {
	rules []Rule
}

// NewRuleSet creates a RuleSet from the provided rules.
func NewRuleSet(rules ...Rule) *RuleSet {
	return &RuleSet{rules: rules}
}

// Evaluate runs all rules against a KV write. Returns the first violation found.
func (rs *RuleSet) Evaluate(key string, value []byte) *Violation {
	for _, rule := range rs.rules {
		if v := rule(key, value); v != nil {
			return v
		}
	}
	return nil
}

// MaxValueSize rejects values exceeding the given byte size.
func MaxValueSize(maxBytes int) Rule {
	return func(key string, value []byte) *Violation {
		if len(value) > maxBytes {
			return &Violation{
				Rule:    "max-value-size",
				Message: fmt.Sprintf("value size %d bytes exceeds maximum of %d bytes for key %q", len(value), maxBytes, key),
			}
		}
		return nil
	}
}

// NoSensitiveData rejects values matching known sensitive data patterns.
func NoSensitiveData() Rule {
	patterns := []struct {
		name    string
		pattern *regexp.Regexp
	}{
		{
			name:    "aws-access-key",
			pattern: regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
		},
		{
			name:    "aws-secret-key",
			pattern: regexp.MustCompile(`(?i)aws.{0,20}secret.{0,20}[0-9a-zA-Z/+]{40}`),
		},
		{
			name:    "github-token",
			pattern: regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{36}`),
		},
		{
			name:    "stripe-key",
			pattern: regexp.MustCompile(`sk_(live|test)_[0-9a-zA-Z]{24,}`),
		},
		{
			name:    "private-key",
			pattern: regexp.MustCompile(`-----BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY-----`),
		},
		{
			name:    "api-token",
			pattern: regexp.MustCompile(`(?i)(api[_-]?key|api[_-]?token|access[_-]?token)\s*[:=]\s*[0-9a-zA-Z\-_]{20,}`),
		},
		{
			name:    "password",
			pattern: regexp.MustCompile(`(?i)(password|passwd|pwd)\s*[:=]\s*\S{8,}`),
		},
	}

	return func(key string, value []byte) *Violation {
		for _, p := range patterns {
			if p.pattern.Match(value) {
				return &Violation{
					Rule:    "no-sensitive-data",
					Message: fmt.Sprintf("value for key %q matches sensitive data pattern %q", key, p.name),
				}
			}
		}
		return nil
	}
}
