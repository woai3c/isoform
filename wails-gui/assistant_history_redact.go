package main

import (
	"math"
	"regexp"
	"strings"
	"unicode"
)

// assistant_history_redact.go drops lines from assistant session text that
// look like credentials. It runs before anything from a session is stored or
// sent to a model: a line that matches is removed whole, and the rest of the
// session stays. It is deliberately line-based; a partial mask would still
// leave the surrounding words of a secret behind.

var assistantHistorySecretPatterns = []*regexp.Regexp{
	// Provider keys with well-known prefixes.
	regexp.MustCompile(`\b(?:sk|pk|rk)[-_][A-Za-z0-9_-]{16,}`),
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}`),
	regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{16,}`),
	regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`),
	regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`),
	regexp.MustCompile(`AGE-SECRET-KEY-[A-Z0-9]+`),
	// Private key blocks and HTTP bearer headers.
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY`),
	regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{12,}`),
	regexp.MustCompile(`(?i)\bauthorization\s*:\s*\S{8,}`),
	// Key=value and key: value pairs, including .env names such as
	// OPENAI_API_KEY=..., DB_PASSWORD: ..., and "token": "...".
	regexp.MustCompile(`(?i)(?:password|passwd|passphrase|secret|token|api[_-]?key|access[_-]?key|private[_-]?key|client[_-]?secret|session[_-]?secret)["']?\s*[:=]\s*["']?[^\s"',;]{6,}`),
	// Chinese labels for passwords.
	regexp.MustCompile(`(?:密码|口令|密钥)\s*[:：=]\s*\S{4,}`),
}

// assistantHistoryTokenPattern is a whitespace-free run that could be a key
// or token: long, from the base64 and hex-like alphabet, with no path or
// sentence characters in it.
var assistantHistoryTokenPattern = regexp.MustCompile(`[A-Za-z0-9+/_=-]{24,}`)

// assistantHistoryLooksLikeSecret reports whether one line should be dropped.
func assistantHistoryLooksLikeSecret(line string) bool {
	for _, re := range assistantHistorySecretPatterns {
		if re.MatchString(line) {
			return true
		}
	}
	for _, token := range assistantHistoryTokenPattern.FindAllString(line, -1) {
		if assistantHistoryRandomToken(token) {
			return true
		}
	}
	return false
}

// assistantHistoryRandomToken is true for a long token with lower case, upper
// case and digits mixed, and enough entropy that it is not a word or a hash
// of one. Commit hashes and UUIDs have fewer character classes or lower
// entropy, so they pass through.
func assistantHistoryRandomToken(token string) bool {
	var lower, upper, digit bool
	counts := map[rune]int{}
	for _, r := range token {
		switch {
		case unicode.IsLower(r):
			lower = true
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsDigit(r):
			digit = true
		}
		counts[r]++
	}
	if !(lower && upper && digit) {
		return false
	}
	total := float64(len([]rune(token)))
	entropy := 0.0
	for _, n := range counts {
		p := float64(n) / total
		entropy -= p * math.Log2(p)
	}
	return entropy >= 3.5
}

// redactAssistantHistoryText removes every line that looks like a secret and
// reports how many lines it dropped. Lines are kept byte for byte otherwise.
func redactAssistantHistoryText(text string) (string, int) {
	if text == "" {
		return "", 0
	}
	kept := make([]string, 0, strings.Count(text, "\n")+1)
	dropped := 0
	for _, line := range strings.Split(text, "\n") {
		if assistantHistoryLooksLikeSecret(line) {
			dropped++
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n"), dropped
}
