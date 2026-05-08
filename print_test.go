package main

import (
	"testing"
	"time"
)

func TestHumanAge(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{500 * time.Millisecond, "now"},
		{0, "now"},
		{1 * time.Second, "1s ago"},
		{59 * time.Second, "59s ago"},
		{1 * time.Minute, "1m ago"},
		{59 * time.Minute, "59m ago"},
		{1 * time.Hour, "1h ago"},
		{23 * time.Hour, "23h ago"},
		{24 * time.Hour, "1d ago"},
		{72 * time.Hour, "3d ago"},
	}
	for _, tc := range cases {
		if got := humanAge(tc.d); got != tc.want {
			t.Errorf("humanAge(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}
