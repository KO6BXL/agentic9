package fusefs

import "testing"

func TestJoinPath(t *testing.T) {
	tests := []struct {
		base string
		name string
		want string
	}{
		{"/", "a", "/a"},
		{"/a", "b", "/a/b"},
		{"/a", "../b", "/b"},
	}
	for _, tc := range tests {
		if got := JoinPath(tc.base, tc.name); got != tc.want {
			t.Fatalf("JoinPath(%q, %q)=%q want %q", tc.base, tc.name, got, tc.want)
		}
	}
}
