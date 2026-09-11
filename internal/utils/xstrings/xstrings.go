// xstrings.go provides string-slice helpers: split with trim and empty-item
// filtering, used for parsing comma-separated configuration values.
package xstrings

import "strings"

// SplitTrimDropEmpty splits strings by sep, trims whitespace, and drops empty items.
// Commonly used for parsing comma-separated configs like "a, b, ,c,".
func SplitTrimDropEmpty(sep string, parts ...string) []string {
	out := make([]string, 0)
	for _, p := range parts {
		if p == "" {
			continue
		}
		for _, item := range strings.Split(p, sep) {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			out = append(out, item)
		}
	}
	return out
}

// SplitDropEmpty 按分隔符拆分非空字符串，不修改或过滤拆分后的内容。
func SplitDropEmpty(sep string, parts ...string) []string {
	out := make([]string, 0)
	for _, part := range parts {
		if part != "" {
			out = append(out, strings.Split(part, sep)...)
		}
	}
	return out
}

// TrimDropEmpty trims whitespace and drops empty items in a string slice.
func TrimDropEmpty(items []string) []string {
	out := make([]string, 0, len(items))
	for _, s := range items {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	return out
}
