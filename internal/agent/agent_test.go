package agent

import "testing"

// Lines captured from Cursor CLI 2026.10.22 (`agent -p --output-format stream-json`).
const (
	streamInit      = `{"type":"system","subtype":"init","apiKeySource":"login","cwd":"/tmp/s","session_id":"312bc349","model":"Auto","permissionMode":"default"}`
	streamUser      = `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"讀 note.txt"}]},"session_id":"312bc349"}`
	streamToolStart = `{"type":"tool_call","subtype":"started","call_id":"toolu_01","tool_call":{"readToolCall":{"args":{"path":"/tmp/s/note.txt"}},"hookAdditionalContexts":[],"toolCallId":"toolu_01","startedAtMs":"1792851673344"},"session_id":"312bc349","timestamp_ms":1}`
	streamToolDone  = `{"type":"tool_call","subtype":"completed","call_id":"toolu_01","tool_call":{"readToolCall":{"args":{"path":"/tmp/s/note.txt"},"result":{"success":{"content":"hello\n","isEmpty":false}}}},"session_id":"312bc349","timestamp_ms":2}`
	streamAssistant = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"內容是 hello。"}]},"session_id":"312bc349"}`
	streamResult    = `{"type":"result","subtype":"success","duration_ms":4531,"is_error":false,"result":"內容是 hello。","session_id":"312bc349","request_id":"r","usage":{"inputTokens":6}}`
)

func TestParseEvent(t *testing.T) {
	cases := []struct {
		line string
		ok   bool
		want Event
	}{
		{streamInit, false, Event{}},
		{streamUser, false, Event{}},
		{streamToolStart, true, Event{KindTool, "read /tmp/s/note.txt"}},
		{streamToolDone, false, Event{}},
		{streamAssistant, true, Event{KindAssistant, "內容是 hello。"}},
		{streamResult, true, Event{KindResult, "內容是 hello。"}},
		{`{"type":"tool_call","subtype":"started","tool_call":{"shellToolCall":{"args":{"command":"go test ./..."}}}}`,
			true, Event{KindTool, "shell go test ./..."}},
		{`{"type":"tool_call","subtype":"completed","tool_call":{"shellToolCall":{"args":{"command":"go test"},"result":{"error":{"message":"x"}}}}}`,
			true, Event{KindToolError, "shell go test 失敗"}},
		{"not json", false, Event{}},
	}
	for _, c := range cases {
		got, ok := ParseEvent(c.line)
		if ok != c.ok || got != c.want {
			t.Errorf("ParseEvent(%.60s) = %+v %v, want %+v %v", c.line, got, ok, c.want, c.ok)
		}
	}
}

func TestParseOutputStream(t *testing.T) {
	out := streamInit + "\n" + streamUser + "\n" + streamToolStart + "\n" + streamToolDone + "\n" +
		streamAssistant + "\n" + streamResult + "\n"
	res, err := ParseOutput(out)
	if err != nil || res.Text != "內容是 hello。" || res.SessionID != "312bc349" {
		t.Fatalf("got %+v %v", res, err)
	}
}

func TestParseOutputError(t *testing.T) {
	res, err := ParseOutput(`{"type":"result","is_error":true,"result":"boom","session_id":"s"}`)
	if err == nil || res.SessionID != "s" {
		t.Fatalf("got %+v %v", res, err)
	}
}

func TestParseOutputWithoutResultKeepsSession(t *testing.T) {
	// A turn killed mid-way has events but no result; the session must survive for resuming.
	res, err := ParseOutput(streamInit + "\n" + streamToolStart)
	if err == nil || res.SessionID != "312bc349" {
		t.Fatalf("got %+v %v", res, err)
	}
}

func TestParseOutputUnparseable(t *testing.T) {
	if _, err := ParseOutput(""); err == nil {
		t.Error("empty: want error")
	}
	res, err := ParseOutput("plain text")
	if err == nil || res.Text != "plain text" {
		t.Errorf("plain: got %+v %v", res, err)
	}
}
