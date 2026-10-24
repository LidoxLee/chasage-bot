package gh

import (
	"slices"
	"testing"
)

func TestEvaluateChecks(t *testing.T) {
	cases := []struct {
		name   string
		checks []Check
		want   ChecksVerdict
		failed []string
	}{
		{"none", nil, ChecksNone, nil},
		{"pass", []Check{{"test", "pass"}, {"lint", "skipping"}}, ChecksPassed, nil},
		{"pending", []Check{{"test", "pass"}, {"lint", "pending"}}, ChecksPending, nil},
		{"fail wins over pending", []Check{{"test", "fail"}, {"lint", "pending"}}, ChecksFailed, []string{"test"}},
		{"cancel is failure", []Check{{"build", "cancel"}}, ChecksFailed, []string{"build"}},
	}
	for _, c := range cases {
		got, failed := EvaluateChecks(c.checks)
		if got != c.want || !slices.Equal(failed, c.failed) {
			t.Errorf("%s: got %v %v, want %v %v", c.name, got, failed, c.want, c.failed)
		}
	}
}

func TestPRNumberFromURL(t *testing.T) {
	n, err := PRNumberFromURL("https://github.com/LidoxLee/chasage/pull/12\n")
	if err != nil || n != 12 {
		t.Errorf("got %d %v", n, err)
	}
	if _, err := PRNumberFromURL("https://github.com/LidoxLee/chasage"); err == nil {
		t.Error("want error")
	}
}
