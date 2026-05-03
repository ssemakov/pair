package main

import (
	"reflect"
	"testing"
)

func TestExtractAgent(t *testing.T) {
	cases := []struct {
		name    string
		in      []string
		wantCLI string
		wantRest []string
		wantErr bool
	}{
		{"empty", nil, "", []string{}, false},
		{"only n", []string{"3"}, "", []string{"3"}, false},
		{"only agent", []string{"claude"}, "claude", []string{}, false},
		{"agent then n", []string{"claude", "2"}, "claude", []string{"2"}, false},
		{"n then agent", []string{"2", "codex"}, "codex", []string{"2"}, false},
		{"unknown stays", []string{"foo", "claude"}, "claude", []string{"foo"}, false},
		{"two agents errors", []string{"claude", "codex"}, "", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cli, rest, err := extractAgent(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cli != tc.wantCLI {
				t.Errorf("cli=%q, want %q", cli, tc.wantCLI)
			}
			if !reflect.DeepEqual(rest, tc.wantRest) {
				t.Errorf("rest=%v, want %v", rest, tc.wantRest)
			}
		})
	}
}

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
