package kitutil

import (
	"bytes"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
)

const credentialFieldNames = `api[_ -]?key|x[_-]api[_-]key|authorization|proxy[_-]authorization|access[_-]?token|refresh[_-]?token|id[_-]?token|token|client[_-]?secret|secret|password|private[_-]?key|cookie|set[_-]cookie`

var (
	credentialNamePattern       = regexp.MustCompile(`(?i)^(?:` + credentialFieldNames + `)$`)
	credentialValuePattern      = regexp.MustCompile(`(?i)(["']?(?:` + credentialFieldNames + `)["']?\s*[:=]\s*)(?:"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|(?:Bearer|Basic)[ \t]+[A-Za-z0-9._~+/=-]+|[^\s,;}\]]+)`)
	credentialAuthPattern       = regexp.MustCompile(`(?i)\b(Bearer|Basic)[ \t]+[A-Za-z0-9._~+/=-]+`)
	credentialKeyPattern        = regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]{12,}|AIza[A-Za-z0-9_-]{20,}|eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]{8,})`)
	credentialPrivateKeyPattern = regexp.MustCompile(`(?s)-----BEGIN (?:[A-Z0-9]+ )?PRIVATE KEY-----.*?-----END (?:[A-Z0-9]+ )?PRIVATE KEY-----`)
	credentialURLPattern        = regexp.MustCompile(`https?://[^\s"'<>]+`)
	credentialStatusPrefix      = regexp.MustCompile(`^status_code=[0-9]+,\s*`)
)

// MaskCredentials removes credential fields and common authentication tokens
// from diagnostics. Valid JSON retains its structure and exact numeric values.
func MaskCredentials(value string) string {
	return maskCredentials(value, 0)
}

func maskCredentials(value string, depth int) string {
	if depth > 32 {
		return "***"
	}
	trimmed := strings.TrimSpace(value)
	if prefix := credentialStatusPrefix.FindString(trimmed); prefix != "" {
		return prefix + maskCredentials(trimmed[len(prefix):], depth+1)
	}
	if (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) && Valid([]byte(trimmed)) {
		if masked, changed := maskCredentialJSON(json.RawMessage(trimmed), depth); changed {
			return string(masked)
		}
		return value
	}
	value = credentialPrivateKeyPattern.ReplaceAllString(value, "***")
	value = credentialURLPattern.ReplaceAllStringFunc(value, func(match string) string {
		parsed, err := url.Parse(match)
		if err != nil {
			return "[redacted URL]"
		}
		if parsed.User != nil {
			parsed.User = url.UserPassword("***", "***")
		}
		if parsed.RawQuery != "" {
			query, err := url.ParseQuery(parsed.RawQuery)
			if err != nil {
				parsed.RawQuery = "redacted"
			} else {
				for key, values := range query {
					for i := range values {
						values[i] = "***"
					}
					query[key] = values
				}
				parsed.RawQuery = query.Encode()
			}
		}
		parsed.Fragment = ""
		return parsed.String()
	})
	value = credentialValuePattern.ReplaceAllStringFunc(value, func(match string) string {
		prefix := credentialValuePattern.FindStringSubmatch(match)[1]
		rest := match[len(prefix):]
		if strings.HasPrefix(rest, `"`) {
			return prefix + `"***"`
		}
		if strings.HasPrefix(rest, "'") {
			return prefix + "'***'"
		}
		return prefix + "***"
	})
	value = credentialAuthPattern.ReplaceAllString(value, "${1} ***")
	return credentialKeyPattern.ReplaceAllString(value, "***")
}

func maskCredentialJSON(raw json.RawMessage, depth int) (json.RawMessage, bool) {
	if depth > 32 {
		return json.RawMessage(`"***"`), true
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return raw, false
	}
	var output any
	changed := false
	switch raw[0] {
	case '{':
		var object map[string]json.RawMessage
		if Unmarshal(raw, &object) != nil {
			return raw, false
		}
		for key, value := range object {
			if credentialNamePattern.MatchString(key) {
				object[key] = json.RawMessage(`"***"`)
				changed = true
				continue
			}
			masked, modified := maskCredentialJSON(value, depth+1)
			object[key] = masked
			changed = changed || modified
		}
		output = object
	case '[':
		var array []json.RawMessage
		if Unmarshal(raw, &array) != nil {
			return raw, false
		}
		for i, value := range array {
			masked, modified := maskCredentialJSON(value, depth+1)
			array[i] = masked
			changed = changed || modified
		}
		output = array
	case '"':
		var text string
		if Unmarshal(raw, &text) != nil {
			return raw, false
		}
		masked := maskCredentials(text, depth+1)
		changed = masked != text
		output = masked
	}
	if !changed {
		return raw, false
	}
	encoded, err := Marshal(output)
	if err != nil {
		return json.RawMessage(`"***"`), true
	}
	return encoded, true
}
