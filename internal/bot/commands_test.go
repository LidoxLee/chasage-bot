package bot

import "testing"

func TestParseCommand(t *testing.T) {
	cases := []struct {
		in   string
		want command
	}{
		{"做兒童模式", command{rest: "做兒童模式"}},
		{"/status", command{name: "status"}},
		{"/status@chasage_bot", command{name: "status"}},
		{"/merge", command{name: "merge"}},
		{"/merge 3", command{name: "merge", id: 3}},
		{"/merge #3", command{name: "merge", id: 3}},
		{"/plan 加兒童模式\n細節", command{name: "plan", rest: "加兒童模式\n細節"}},
		{"/ask\n循環播放在哪？", command{name: "ask", rest: "循環播放在哪？"}},
		{"/agent", command{name: "agent"}},
		{"/go", command{name: "go"}},
		{"/new 新想法", command{name: "new", rest: "新想法"}},
		{"/Retry 5", command{name: "retry", id: 5}},
		{"/buildno 12", command{name: "buildno", id: 12}},
	}
	for _, c := range cases {
		if got := parseCommand(c.in); got != c.want {
			t.Errorf("parseCommand(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}
