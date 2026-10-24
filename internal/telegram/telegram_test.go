package telegram

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSplitShort(t *testing.T) {
	if got := Split("  hello \n", 10); len(got) != 1 || got[0] != "hello" {
		t.Errorf("got %q", got)
	}
	if got := Split("", 10); len(got) != 0 {
		t.Errorf("empty: got %q", got)
	}
}

func TestSplitPrefersLineBreaks(t *testing.T) {
	text := strings.Repeat("字", 8) + "\n" + strings.Repeat("中", 8) + "\n" + strings.Repeat("文", 8)
	got := Split(text, 20)
	if len(got) != 2 || got[0] != strings.Repeat("字", 8)+"\n"+strings.Repeat("中", 8) || got[1] != strings.Repeat("文", 8) {
		t.Errorf("got %q", got)
	}
}

func TestSplitHardCutWithoutBreaks(t *testing.T) {
	got := Split(strings.Repeat("a", 25), 10)
	if len(got) != 3 || strings.Join(got, "") != strings.Repeat("a", 25) {
		t.Errorf("got %q", got)
	}
	for _, c := range got {
		if utf8.RuneCountInString(c) > 10 {
			t.Errorf("chunk too long: %q", c)
		}
	}
}
