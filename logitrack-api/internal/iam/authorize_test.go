package iam

import (
	"strings"
	"testing"
)

// auditValue keeps a crafted X-Act-On-Tenant value from bloating the audit row or failing its insert
// (jsonb refuses NUL).
func TestAuditValue(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"*", "*"},
		{"0199c000-0000-7000-8000-0000000000ff", "0199c000-0000-7000-8000-0000000000ff"},
		{"a\x00b\tcé", "a?b?c??"},
		{strings.Repeat("y", 500), strings.Repeat("y", auditValueMax)},
		{"", ""},
	} {
		if got := auditValue(tc.in); got != tc.want {
			t.Errorf("auditValue(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
