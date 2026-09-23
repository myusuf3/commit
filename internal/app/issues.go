package app

import (
	"fmt"
	"regexp"
	"strings"
)

var issuePattern = regexp.MustCompile(`^(?:[1-9][0-9]*|[A-Z][A-Z0-9]*-[1-9][0-9]*)$`)
var closingPattern = regexp.MustCompile(`(?i)\b(?:fix(?:es|ed)?|close(?:s|d)?|resolve(?:s|d)?|linear:)\s+(#[1-9][0-9]*|[A-Z][A-Z0-9]*-[1-9][0-9]*)\b`)

// ParseIssues supports GitHub numbers and arbitrary Linear team keys.
func ParseIssues(values []string) ([]string, error) {
	var result []string
	seen := make(map[string]bool)
	for _, value := range values {
		for _, part := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
			issue := strings.ToUpper(strings.TrimPrefix(strings.TrimSpace(part), "#"))
			if !issuePattern.MatchString(issue) {
				return nil, fmt.Errorf("invalid issue %q: use a GitHub number or TEAM-123", part)
			}
			if !seen[issue] {
				result = append(result, issue)
				seen[issue] = true
			}
		}
	}
	return result, nil
}

func ExtractIssues(body string) []string {
	var values []string
	for _, match := range closingPattern.FindAllStringSubmatch(body, -1) {
		values = append(values, match[1])
	}
	issues, _ := ParseIssues(values)
	return issues
}

func LinkIssues(body string, issues []string) string {
	seen := make(map[string]bool)
	for _, issue := range ExtractIssues(body) {
		seen[issue] = true
	}
	for _, issue := range issues {
		if seen[issue] {
			continue
		}
		if strings.Contains(issue, "-") {
			body += "\n\nCloses " + issue
		} else {
			body += "\n\nFixes #" + issue
		}
		seen[issue] = true
	}
	return body
}
