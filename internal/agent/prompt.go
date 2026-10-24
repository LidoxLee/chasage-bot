package agent

import (
	"fmt"
	"strings"

	"github.com/LidoxLee/chasage-bot/internal/task"
)

// Workspace describes the conversation directory handed to the agent.
type Workspace struct {
	BackendDir string // directory name of the backend worktree
	AppDir     string // directory name of the app worktree
	Branch     string
}

func preamble(ws Workspace) string {
	return fmt.Sprintf(`你是 Chasage 專案的開發夥伴，正透過 Telegram 和使用者對話。使用者在手機上看你的回覆：
用繁體中文、精簡清楚，回覆會以純文字顯示，避免寬表格。

工作目錄有兩個 git worktree，都在 branch %[3]s：
- %[1]s/：Go 後端（merge 到 main 會自動部署到 production EC2）
- %[2]s/：Flutter iOS app

回答或規劃前先讀相關 repo 的 README.md、CLAUDE.md、.cursor/rules、docs/，以實際程式碼為準。

`, ws.BackendDir, ws.AppDir, ws.Branch)
}

func agentRules(ws Workspace) string {
	return fmt.Sprintf(`你現在在實作模式，沒有人能即時回答你，請自行做出合理判斷。需求不清楚到無法動手時，不要改 code，直接回覆你的問題。
實作規則：
1. 遵守既有架構與風格，只改完成需求必要的 repo。
2. 後端改動後在 %[1]s/ 執行 go vet ./... 與 go test ./...，必須通過。
3. app 改動後在 %[2]s/ 執行 fvm flutter pub get、fvm dart run build_runner build --delete-conflicting-outputs、`+
		`fvm flutter analyze lib/、fvm flutter test，必須通過。
4. 資料庫 migration 放在後端既有的 migrations/ 慣例中，並確保向下相容（舊版 app 仍可運作）。
5. 在各自的 worktree 內 commit，訊息使用 feat: / fix: / refactor: 前綴。不要 push，不要切換或修改 main。
6. 不要修改 .github/workflows、部署設定、或任何密鑰檔案。
7. 最後回覆：改了哪些 repo、主要改動、測試結果、需要人工注意的地方。

`, ws.BackendDir, ws.AppDir)
}

// TurnPrompt builds the prompt for one user message. first adds the workspace introduction,
// which the resumed session then remembers. Agent turns always repeat the rules so switching
// from plan to agent cannot lose them.
func TurnPrompt(ws Workspace, mode task.Mode, first bool, text string) string {
	var b strings.Builder
	if first {
		b.WriteString(preamble(ws))
	}
	switch mode {
	case task.Agent:
		b.WriteString(agentRules(ws))
	case task.Plan:
		b.WriteString("你現在在規劃模式：只讀程式碼、討論並產出具體可執行的計畫（要改哪些檔案、怎麼改、怎麼驗證），不要修改任何檔案。" +
			"使用者確認後會切換到實作模式照計畫執行。\n\n")
	case task.Ask:
		b.WriteString("你現在在問答模式：只讀程式碼並回答問題，不要修改任何檔案。\n\n")
	}
	b.WriteString("使用者：\n")
	b.WriteString(strings.TrimSpace(text))
	return b.String()
}

// GoText is sent when the user approves the plan and switches to agent mode.
const GoText = "計畫確認，請依照上面討論的計畫開始實作。"
