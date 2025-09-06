package secrets

import (
	"fmt"
	"math"
	"regexp"
	"strings"
)

type Finding struct {
	Rule     string
	Match    string
	Line     int
	Snippet  string
	Severity string // "block" | "warn" (wir verwenden aktuell nur "block")
}

type rule struct {
	name     string
	re       *regexp.Regexp
	severity string
}

var rules = []rule{
	// Private Keys
	{name: "PEM private key", re: regexp.MustCompile(`-----BEGIN (?:RSA|EC|DSA|OPENSSH|PGP|PRIVATE) KEY-----`), severity: "block"},
	// AWS
	{name: "AWS Access Key ID", re: regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), severity: "block"},
	{name: "AWS Secret Access Key", re: regexp.MustCompile(`(?i)aws.+(secret|access)_?key[^A-Za-z0-9]{0,3}[=:]\s*[A-Za-z0-9/\+=]{30,}`), severity: "block"},
	// GitHub/GitLab/Slack/Stripe/Google
	{name: "GitHub token", re: regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,}\b`), severity: "block"},
	{name: "GitLab PAT", re: regexp.MustCompile(`\bglpat-[A-Za-z0-9\-_]{20,}\b`), severity: "block"},
	{name: "Slack token", re: regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,48}\b`), severity: "block"},
	{name: "Stripe secret key", re: regexp.MustCompile(`\bsk_(?:live|test)_[A-Za-z0-9]{24}\b`), severity: "block"},
	{name: "Google API key", re: regexp.MustCompile(`\bAIza[0-9A-Za-z\-_]{35}\b`), severity: "block"},
	// JWT
	{name: "JWT", re: regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{6,}\.[A-Za-z0-9_\-]{6,}\.[A-Za-z0-9_\-]{6,}\b`), severity: "block"},
	// Credentials in URLs
	{name: "Credential in URL", re: regexp.MustCompile(`\b[a-z][a-z0-9+\-.]*://[^/\s:@]+:[^/\s:@]+@`), severity: "block"},
	// .env style
	{name: ".env secret-like", re: regexp.MustCompile(`(?i)\b(PASS(WORD)?|SECRET|API[_-]?KEY|TOKEN|AUTH|SESSION)[A-Z0-9_-]*\s*=\s*\S{8,}`), severity: "block"},
	// Azure shared key
	{name: "Azure SharedAccessKey", re: regexp.MustCompile(`(?i)\bSharedAccessKey\s*=\s*[A-Za-z0-9+/=]{20,}\b`), severity: "block"},
}

// High-entropy Kandidaten nach Schlüsselwörtern (Base64/Hex-ähnlich, > 20 chars)
var entCandidate = regexp.MustCompile(`(?i)(password|secret|token|api[_-]?key|auth|session)[^A-Za-z0-9]{0,5}([A-Za-z0-9_\-+/=]{20,})`)

func entropy(s string) float64 {
	if len(s) == 0 {
		return 0
	}
	var freq [256]int
	for i := 0; i < len(s); i++ {
		freq[s[i]]++
	}
	H := 0.0
	n := float64(len(s))
	for _, c := range freq {
		if c == 0 {
			continue
		}
		p := float64(c) / n
		H += -p * math.Log2(p)
	}
	return H
}

// Scan liefert alle Funde. Blocktauglich = len>0.
func Scan(text string) []Finding {
	var out []Finding
	lines := strings.Split(text, "\n")

	// Regelbasierte Treffer
	for li, line := range lines {
		for _, rl := range rules {
			if loc := rl.re.FindStringIndex(line); loc != nil {
				match := line[loc[0]:loc[1]]
				out = append(out, Finding{
					Rule: rl.name, Match: match, Line: li + 1, Snippet: truncate(line, 120), Severity: rl.severity,
				})
			}
		}
	}

	// Entropie-Heuristik (nur wenn nichts Regelbasiertes gegriffen hat, um Noise zu reduzieren)
	if len(out) == 0 {
		for li, line := range lines {
			m := entCandidate.FindAllStringSubmatch(line, -1)
			for _, g := range m {
				val := g[2]
				if entropy(val) >= 3.5 { // grobe Schwelle
					out = append(out, Finding{
						Rule: "High-entropy secret-like value",
						Match: val, Line: li + 1, Snippet: truncate(line, 120), Severity: "block",
					})
				}
			}
		}
	}

	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// Brief formatiert eine kurze Meldung für HTTP-Fehler.
func Brief(fs []Finding, max int) string {
	if len(fs) == 0 {
		return ""
	}
	if max <= 0 {
		max = 5
	}
	var b strings.Builder
	for i, f := range fs {
		if i >= max {
			fmt.Fprintf(&b, "…and %d more\n", len(fs)-max)
			break
		}
		fmt.Fprintf(&b, "- %s (line %d)\n", f.Rule, f.Line)
	}
	return b.String()
}

