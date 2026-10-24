package agent

import (
	"strings"
	"testing"

	"github.com/LidoxLee/chasage-bot/internal/task"
)

func TestTurnPrompt(t *testing.T) {
	ws := Workspace{BackendDir: "be", AppDir: "app", Branch: "bot/conv-1"}

	first := TurnPrompt(ws, task.Plan, true, "  加兒童模式  ")
	if !strings.Contains(first, "bot/conv-1") || !strings.Contains(first, "規劃模式") ||
		!strings.HasSuffix(first, "使用者：\n加兒童模式") {
		t.Errorf("first plan prompt:\n%s", first)
	}
	if strings.Contains(first, "實作規則") {
		t.Error("plan prompt must not include agent rules")
	}

	later := TurnPrompt(ws, task.Agent, false, GoText)
	if strings.Contains(later, "bot/conv-1") {
		t.Error("later turns must not repeat the preamble")
	}
	if !strings.Contains(later, "實作規則") || !strings.Contains(later, "go test") {
		t.Errorf("agent prompt missing rules:\n%s", later)
	}

	if ask := TurnPrompt(ws, task.Ask, false, "q"); !strings.Contains(ask, "問答模式") {
		t.Errorf("ask prompt:\n%s", ask)
	}
}
