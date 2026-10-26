package activity

import "testing"

func TestHub(t *testing.T) {
	h := New(3)
	h.Add(1, KindUser, "a")
	h.Add(2, KindUser, "other")
	h.Add(1, "tool", "b")
	h.Add(1, "assistant", "c")
	h.Add(1, KindBot, "d")

	all := h.Since(1, 0)
	if len(all) != 3 || all[0].Text != "b" || all[2].Text != "d" {
		t.Fatalf("all = %+v", all)
	}
	newer := h.Since(1, all[1].Seq)
	if len(newer) != 1 || newer[0].Text != "d" {
		t.Errorf("newer = %+v", newer)
	}
	if got := h.Since(9, 0); got == nil || len(got) != 0 {
		t.Errorf("unknown conversation = %#v, want empty non-nil", got)
	}
}

func TestNilHubIgnoresAdd(t *testing.T) {
	var h *Hub
	h.Add(1, KindUser, "x")
}
