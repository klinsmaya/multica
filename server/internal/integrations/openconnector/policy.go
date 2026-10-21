package openconnector

import (
	"regexp"
	"sort"
	"strings"
)

const (
	maxAllowedTools   = 200
	maxAllowedToolLen = 120
)

// Action names as open-connector lists them: letters, digits, `_`, `-`, `.`
// and a trailing `*` for a prefix match.
var toolNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+\*?$`)

// NormalizeAllowedTools validates and canonicalizes a policy tool allowlist
// for service: entries are trimmed, a leading `<service>.` prefix is dropped
// so `github.create_issue` and `create_issue` are the same tool, duplicates
// collapse and the result is sorted. An empty result means every action.
func NormalizeAllowedTools(service string, raw []string) ([]string, error) {
	prefix := normalizeService(service) + "."
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, t := range raw {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(t), prefix) {
			t = t[len(prefix):]
		}
		if t == "" || t == "*" {
			// `*` alone is the same as no restriction; an empty list says that.
			return []string{}, nil
		}
		if len(t) > maxAllowedToolLen || !toolNameRe.MatchString(t) {
			return nil, ErrInvalidInput
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	if len(out) > maxAllowedTools {
		return nil, ErrInvalidInput
	}
	sort.Strings(out)
	return out, nil
}
