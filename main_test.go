package main

import "testing"

func TestResumeArgv(t *testing.T) {
	cases := []struct {
		cli  string
		id   string
		want []string
	}{
		{"claude", "abc", []string{"claude", "--resume", "abc"}},
		{"codex", "xyz", []string{"codex", "resume", "xyz"}},
		{"other", "id", []string{"other", "resume", "id"}},
	}
	for _, tc := range cases {
		got := resumeArgv(tc.cli, tc.id)
		if len(got) != len(tc.want) {
			t.Errorf("%s: len mismatch %v vs %v", tc.cli, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: argv[%d] = %q, want %q", tc.cli, i, got[i], tc.want[i])
			}
		}
	}
}
