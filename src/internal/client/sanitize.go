package client

import (
	"html"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxServerTextBytes = 2048
	maxLogLines        = 40
	redactedKey        = "${1}[REDACTED]"
	maskedAuthHeader   = "Bearer [REDACTED]"
	redactedJWT        = "[REDACTED-JWT]"
)

var (
	ansiSequence  = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-Z\\-_]`)
	scriptBlock   = regexp.MustCompile(`(?is)<(script|style)\b[^>]*>.*?</(?:script|style)>`)
	htmlTag       = regexp.MustCompile(`</?[A-Za-z!][^>]*>`)
	whitespaceRun = regexp.MustCompile(`[ \t\r\f\v]+`)
	blankLineRun  = regexp.MustCompile(`\n{3,}`)
	apiKeySecret  = regexp.MustCompile(`(ddi_(?:uk|ak)_)[A-Za-z0-9._-]+`)
	bearerSecret  = regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/=-]{8,}`)
	jwtSecret     = regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)
)

func sanitizeServerText(s string) string {
	return capHead(cleanText(s, false), maxServerTextBytes)
}

func sanitizeServerLogs(s string) string {
	cleaned := cleanText(s, true)
	lines := strings.Split(cleaned, "\n")
	if len(lines) > maxLogLines {
		lines = lines[len(lines)-maxLogLines:]
	}
	return capTail(strings.TrimSpace(strings.Join(lines, "\n")), maxServerTextBytes)
}

func cleanText(s string, keepNewlines bool) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "?")
	}
	s = ansiSequence.ReplaceAllString(s, "")
	s = scriptBlock.ReplaceAllString(s, " ")
	if htmlTag.MatchString(s) {
		s = html.UnescapeString(htmlTag.ReplaceAllString(s, " "))
	}
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' && keepNewlines:
			return r
		case r == '\n' || r == '\t' || r == '\r':
			return ' '
		case unicode.IsControl(r) || isBidiControl(r):
			return -1
		}
		return r
	}, s)
	s = whitespaceRun.ReplaceAllString(s, " ")
	if keepNewlines {
		s = blankLineRun.ReplaceAllString(s, "\n\n")
	} else {
		s = strings.Join(strings.Fields(s), " ")
	}
	s = apiKeySecret.ReplaceAllString(s, redactedKey)
	s = bearerSecret.ReplaceAllString(s, maskedAuthHeader)
	s = jwtSecret.ReplaceAllString(s, redactedJWT)
	return strings.TrimSpace(s)
}

func isBidiControl(r rune) bool {
	return r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 || r == 0x200e || r == 0x200f
}

func capHead(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}

func capTail(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := len(s) - limit
	for cut < len(s) && !utf8.RuneStart(s[cut]) {
		cut++
	}
	return "..." + s[cut:]
}

func redirectHost(location string) string {
	parsed, err := url.Parse(strings.TrimSpace(location))
	if err != nil || parsed.Host == "" {
		return "an unknown location"
	}
	return parsed.Host
}
