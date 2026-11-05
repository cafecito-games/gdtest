// Package pathglob matches project-relative slash paths against the glob
// patterns used by gdtest.json, so every package shares one matching policy.
package pathglob

import (
	"path"
	"regexp"
	"strings"
)

// MatchesAny reports whether filename matches at least one pattern.
func MatchesAny(filename string, patterns []string) bool {
	for _, pattern := range patterns {
		if Match(pattern, filename) {
			return true
		}
	}
	return false
}

// Match reports whether filename matches pattern. Patterns without "**" follow
// path.Match. A "**/" segment matches zero or more directories, and "**"
// matches any text, including slashes.
func Match(pattern, filename string) bool {
	if !strings.Contains(pattern, "**") {
		matched, err := path.Match(pattern, filename)
		return err == nil && matched
	}
	var expression strings.Builder
	expression.WriteByte('^')
	for index := 0; index < len(pattern); {
		if strings.HasPrefix(pattern[index:], "**/") {
			expression.WriteString("(?:.*/)?")
			index += 3
			continue
		}
		if strings.HasPrefix(pattern[index:], "**") {
			expression.WriteString(".*")
			index += 2
			continue
		}
		switch pattern[index] {
		case '*':
			expression.WriteString("[^/]*")
		case '?':
			expression.WriteString("[^/]")
		default:
			expression.WriteString(regexp.QuoteMeta(pattern[index : index+1]))
		}
		index++
	}
	expression.WriteByte('$')
	matched, err := regexp.MatchString(expression.String(), filename)
	return err == nil && matched
}
