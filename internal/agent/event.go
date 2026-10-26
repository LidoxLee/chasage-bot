package agent

import (
	"encoding/json"
	"sort"
	"strings"
)

// Event kinds surfaced from the CLI's stream-json output.
const (
	KindAssistant = "assistant"
	KindTool      = "tool"
	KindToolError = "tool_error"
	KindResult    = "result"
)

// Event is one human-readable step of an agent turn.
type Event struct {
	Kind string
	Text string
}

type streamLine struct {
	Type     string                     `json:"type"`
	Subtype  string                     `json:"subtype"`
	Result   string                     `json:"result"`
	Message  *streamMessage             `json:"message"`
	ToolCall map[string]json.RawMessage `json:"tool_call"`
}

type streamMessage struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

type toolBody struct {
	Args   map[string]any             `json:"args"`
	Result map[string]json.RawMessage `json:"result"`
}

// argKeys are the tool arguments that best describe what a tool call is doing, in priority order.
var argKeys = []string{"command", "path", "pattern", "query", "globPattern", "targetDirectory", "url"}

// ParseEvent turns one stream-json line into an Event. Lines with nothing worth showing
// (init, the echoed user prompt, successful tool completions) report false.
func ParseEvent(line string) (Event, bool) {
	var l streamLine
	if err := json.Unmarshal([]byte(line), &l); err != nil {
		return Event{}, false
	}
	switch l.Type {
	case "assistant":
		if l.Message == nil {
			return Event{}, false
		}
		var parts []string
		for _, c := range l.Message.Content {
			if c.Type == "text" && strings.TrimSpace(c.Text) != "" {
				parts = append(parts, strings.TrimSpace(c.Text))
			}
		}
		if len(parts) == 0 {
			return Event{}, false
		}
		return Event{Kind: KindAssistant, Text: strings.Join(parts, "\n")}, true
	case "tool_call":
		name, body, ok := toolCall(l.ToolCall)
		if !ok {
			return Event{}, false
		}
		switch l.Subtype {
		case "started":
			return Event{Kind: KindTool, Text: describeTool(name, body.Args)}, true
		case "completed":
			if _, failed := body.Result["error"]; failed {
				return Event{Kind: KindToolError, Text: describeTool(name, body.Args) + " 失敗"}, true
			}
		}
		return Event{}, false
	case "result":
		return Event{Kind: KindResult, Text: l.Result}, true
	}
	return Event{}, false
}

// toolCall finds the "<name>ToolCall" entry; the CLI keys each tool call by its type.
func toolCall(m map[string]json.RawMessage) (string, toolBody, bool) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !strings.HasSuffix(k, "ToolCall") {
			continue
		}
		var body toolBody
		if err := json.Unmarshal(m[k], &body); err != nil {
			return "", toolBody{}, false
		}
		return strings.TrimSuffix(k, "ToolCall"), body, true
	}
	return "", toolBody{}, false
}

func describeTool(name string, args map[string]any) string {
	for _, k := range argKeys {
		if v, ok := args[k].(string); ok && v != "" {
			return name + " " + v
		}
	}
	return name
}
