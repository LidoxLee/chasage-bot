package agent

import "testing"

func TestParseOutput(t *testing.T) {
	out := "some log line\n" +
		`{"type":"result","subtype":"success","is_error":false,"result":" 完成 ","session_id":"abc"}`
	res, err := ParseOutput(out)
	if err != nil || res.Text != "完成" || res.SessionID != "abc" {
		t.Fatalf("got %+v %v", res, err)
	}
}

// Captured from Cursor CLI 2026.10.22 (`agent -p --output-format json`).
func TestParseOutputRealCLI(t *testing.T) {
	out := `{"type":"result","subtype":"success","is_error":false,"duration_ms":3122,"duration_api_ms":3122,` +
		`"result":"42","session_id":"49139458-b156-43d6-817f-b1110d25267b",` +
		`"request_id":"a39a5508-e67e-48f4-9602-5e2f3ae6d196","usage":{"inputTokens":4,"outputTokens":3}}`
	res, err := ParseOutput(out)
	if err != nil || res.Text != "42" || res.SessionID != "49139458-b156-43d6-817f-b1110d25267b" {
		t.Fatalf("got %+v %v", res, err)
	}
}

func TestParseOutputError(t *testing.T) {
	res, err := ParseOutput(`{"type":"result","is_error":true,"result":"boom","session_id":"s"}`)
	if err == nil || res.SessionID != "s" {
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
