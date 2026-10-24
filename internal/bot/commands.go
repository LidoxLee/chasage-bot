package bot

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"unicode"

	"github.com/LidoxLee/chasage-bot/internal/agent"
	"github.com/LidoxLee/chasage-bot/internal/task"
	"github.com/LidoxLee/chasage-bot/internal/telegram"
)

const helpText = `直接傳訊息 = 和目前對話的 agent 說話（新對話預設 plan 模式）。

模式
/plan [訊息]    規劃模式：只讀 code、討論計畫，計畫下方有「開始實作」
/ask [訊息]     問答模式：只讀 code、回答問題
/agent [訊息]   實作模式：改 code、跑測試，有 commit 就開或更新 PR
/go             計畫確認，切到實作模式照計畫做

對話
/new [訊息]     結束目前對話並開新對話
/end            結束目前對話
/mode           目前對話的編號、模式、狀態

發版（編號省略 = 目前對話）
/merge [N]      等 CI 通過後合併、部署、打包上傳 TestFlight
/cancel [N]     中止並關閉 PR
/retry [N]      發版失敗或中斷時從斷點繼續
/release        直接打包 app main 上傳 TestFlight
/buildno N      設定下一個 build number
/status         所有對話與下一個 build number`

type command struct {
	name string // without the leading slash; empty for plain text
	id   int    // first numeric argument, 0 if none
	rest string // remaining text after the name (and id, when present)
}

func parseCommand(text string) command {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return command{rest: text}
	}
	name, rest := cutSpace(text[1:])
	name, _, _ = strings.Cut(name, "@") // "/status@chasage_bot" in groups
	cmd := command{name: strings.ToLower(name), rest: rest}
	first, after := cutSpace(cmd.rest)
	if n, err := strconv.Atoi(strings.TrimPrefix(first, "#")); err == nil {
		cmd.id = n
		cmd.rest = after
	}
	return cmd
}

// cutSpace splits s at the first whitespace (including newlines) and trims both halves.
func cutSpace(s string) (string, string) {
	i := strings.IndexFunc(s, unicode.IsSpace)
	if i < 0 {
		return s, ""
	}
	return s[:i], strings.TrimSpace(s[i:])
}

func (b *Bot) handleUpdate(ctx context.Context, u telegram.Update) {
	switch {
	case u.CallbackQuery != nil:
		b.handleCallback(ctx, u.CallbackQuery)
	case u.Message != nil && u.Message.Text != "":
		b.handleMessage(ctx, u.Message)
	}
}

func (b *Bot) authorized(chatID int64) bool {
	return b.Cfg.AllowedChatID != 0 && chatID == b.Cfg.AllowedChatID
}

func (b *Bot) handleCallback(ctx context.Context, q *telegram.CallbackQuery) {
	if q.Message == nil || !b.authorized(q.Message.Chat.ID) {
		return
	}
	action, idStr, _ := strings.Cut(q.Data, ":")
	id, _ := strconv.Atoi(idStr)
	reply := "收到"
	switch action {
	case "merge":
		b.approve(ctx, id)
		reply = fmt.Sprintf("#%d 開始合併流程", id)
	case "cancel":
		b.cancel(ctx, id)
		reply = fmt.Sprintf("#%d 取消中", id)
	case "go":
		b.goImplement(ctx, id)
		reply = fmt.Sprintf("#%d 開始實作", id)
	}
	if err := b.TG.AnswerCallback(ctx, q.ID, reply); err != nil {
		slog.Warn("answer callback", "err", err)
	}
}

func (b *Bot) handleMessage(ctx context.Context, m *telegram.Message) {
	if b.Cfg.AllowedChatID == 0 {
		// Setup mode: tell whoever writes their chat id so the owner can configure it.
		if _, err := b.TG.Send(ctx, m.Chat.ID, fmt.Sprintf("你的 chat id 是 %d。填入設定檔的 allowed_chat_id 後重啟 bot。", m.Chat.ID), 0, nil); err != nil {
			slog.Warn("setup reply", "err", err)
		}
		return
	}
	if !b.authorized(m.Chat.ID) {
		slog.Warn("ignored message from unknown chat", "chat", m.Chat.ID)
		return
	}
	b.dispatch(ctx, parseCommand(m.Text))
}

func (b *Bot) dispatch(ctx context.Context, cmd command) {
	switch cmd.name {
	case "":
		b.sendToActive(ctx, cmd.rest, "")
	case "plan", "ask", "agent":
		mode, _ := task.ParseMode(cmd.name)
		b.switchMode(ctx, mode, cmd.rest)
	case "go":
		b.goImplement(ctx, b.idOrActive(cmd.id))
	case "new":
		b.newConversation(ctx, cmd.rest)
	case "end":
		b.endConversation(ctx)
	case "mode":
		b.notify(ctx, b.modeText())
	case "start", "help":
		b.notify(ctx, helpText)
	case "status":
		b.notify(ctx, b.statusText())
	case "merge":
		b.approve(ctx, b.idOrActive(cmd.id))
	case "retry":
		b.retry(ctx, b.idOrActive(cmd.id))
	case "cancel":
		b.cancel(ctx, b.idOrActive(cmd.id))
	case "release":
		b.ship <- 0
		b.notify(ctx, "已排入手動發版。")
	case "buildno":
		b.setBuildNo(ctx, cmd.id)
	default:
		b.notify(ctx, "看不懂這個指令。\n\n"+helpText)
	}
}

func (b *Bot) idOrActive(id int) int {
	if id != 0 {
		return id
	}
	if t, ok := b.Store.Active(); ok {
		return t.ID
	}
	return 0
}

// activeChat returns the active conversation if it still accepts messages, creating a new one
// (in mode, or plan by default) otherwise.
func (b *Bot) activeChat(ctx context.Context, firstText string, mode task.Mode) (task.Task, bool) {
	if t, ok := b.Store.Active(); ok && t.Status.Chatting() {
		return t, true
	}
	if mode == "" {
		mode = task.Plan
	}
	t, err := b.Store.Create(firstText, mode)
	if err != nil {
		b.notify(ctx, "開新對話失敗："+err.Error())
		return task.Task{}, false
	}
	b.say(ctx, t.ID, fmt.Sprintf("已開新對話 #%d（%s 模式）。", t.ID, t.Mode), nil)
	return t, true
}

// sendToActive sends text to the active conversation using mode, or its current mode when empty.
func (b *Bot) sendToActive(ctx context.Context, text string, mode task.Mode) {
	if strings.TrimSpace(text) == "" {
		return
	}
	t, ok := b.activeChat(ctx, text, mode)
	if !ok {
		return
	}
	t, err := b.Store.Update(t.ID, func(x *task.Task) error {
		if mode != "" {
			x.Mode = mode
		}
		if x.Prompt == "" {
			x.Prompt = text
		}
		return nil
	})
	if err != nil {
		b.notify(ctx, err.Error())
		return
	}
	if b.enqueueTurn(turnJob{id: t.ID, mode: t.Mode, text: text}) {
		b.say(ctx, t.ID, fmt.Sprintf("#%d 上一則還在處理，完成後會接著處理這則。", t.ID), nil)
	}
}

func (b *Bot) switchMode(ctx context.Context, mode task.Mode, text string) {
	if text != "" {
		b.sendToActive(ctx, text, mode)
		return
	}
	t, ok := b.activeChat(ctx, "", mode)
	if !ok {
		return
	}
	if _, err := b.Store.Update(t.ID, func(x *task.Task) error {
		x.Mode = mode
		return nil
	}); err != nil {
		b.notify(ctx, err.Error())
		return
	}
	b.say(ctx, t.ID, fmt.Sprintf("#%d 切換到 %s 模式。", t.ID, mode), nil)
}

// goImplement approves the plan: switch to agent mode and ask it to implement.
func (b *Bot) goImplement(ctx context.Context, id int) {
	t, ok := b.Store.Get(id)
	if !ok || !t.Status.Chatting() {
		b.notify(ctx, "沒有進行中的對話可以實作。")
		return
	}
	if active, ok := b.Store.Active(); !ok || active.ID != id {
		b.notify(ctx, fmt.Sprintf("#%d 已經不是目前的對話。", id))
		return
	}
	b.sendToActive(ctx, agent.GoText, task.Agent)
}

func (b *Bot) newConversation(ctx context.Context, text string) {
	if t, ok := b.Store.Active(); ok && t.Status.Chatting() {
		if err := b.closeConv(ctx, t.ID); err != nil {
			b.notify(ctx, err.Error())
			return
		}
	}
	if err := b.clearActive(); err != nil {
		b.notify(ctx, err.Error())
		return
	}
	if text != "" {
		b.sendToActive(ctx, text, task.Plan)
		return
	}
	b.activeChat(ctx, "", task.Plan)
}

func (b *Bot) clearActive() error {
	if t, ok := b.Store.Active(); ok {
		return b.Store.ClearActive(t.ID)
	}
	return nil
}

func (b *Bot) endConversation(ctx context.Context) {
	t, ok := b.Store.Active()
	if !ok {
		b.notify(ctx, "目前沒有對話。")
		return
	}
	if t.Status.Chatting() {
		if err := b.closeConv(ctx, t.ID); err != nil {
			b.notify(ctx, err.Error())
			return
		}
		b.notify(ctx, fmt.Sprintf("#%d 對話已結束。", t.ID))
		return
	}
	if err := b.Store.ClearActive(t.ID); err != nil {
		b.notify(ctx, err.Error())
		return
	}
	b.notify(ctx, fmt.Sprintf("#%d 不再是目前對話（狀態：%s）。", t.ID, t.Status.Label()))
}

func (b *Bot) retry(ctx context.Context, id int) {
	if _, err := b.Store.Update(id, func(t *task.Task) error {
		if t.Status != task.Failed && t.Status != task.Interrupted {
			return fmt.Errorf("#%d 目前是「%s」，不需要重試", t.ID, t.Status.Label())
		}
		return t.SetStatus(task.Merging)
	}); err != nil {
		b.notify(ctx, err.Error())
		return
	}
	b.say(ctx, id, fmt.Sprintf("#%d 重試發版流程。", id), nil)
	b.ship <- id
}

func (b *Bot) setBuildNo(ctx context.Context, n int) {
	if n <= 0 {
		b.notify(ctx, fmt.Sprintf("用法：/buildno N。目前下一個 build number 是 %d。", b.Store.BuildNumber()))
		return
	}
	if err := b.Store.SetBuildNumber(n); err != nil {
		b.notify(ctx, "設定失敗："+err.Error())
		return
	}
	b.notify(ctx, fmt.Sprintf("下一個 build number 設為 %d。", n))
}

func (b *Bot) modeText() string {
	t, ok := b.Store.Active()
	if !ok {
		return "目前沒有對話。直接傳訊息會開新對話（plan 模式）。"
	}
	s := fmt.Sprintf("目前對話 #%d：%s\n模式：%s\n狀態：%s", t.ID, t.Title(), t.Mode, t.Status.Label())
	if t.Turn != "" {
		s += fmt.Sprintf("\nagent 正在處理（%s 模式）", t.Turn)
	}
	if len(t.PRs) > 0 {
		s += "\n" + prLines(t)
	}
	return s
}

func (b *Bot) statusText() string {
	activeID := 0
	if a, ok := b.Store.Active(); ok {
		activeID = a.ID
	}
	var open, recent []string
	for _, t := range b.Store.List() {
		marker := ""
		if t.ID == activeID {
			marker = "（目前）"
		}
		line := fmt.Sprintf("#%d%s [%s/%s] %s", t.ID, marker, t.Status.Label(), t.Mode, t.Title())
		for _, p := range t.PRs {
			line += fmt.Sprintf("\n    %s：%s", p.Repo, p.URL)
		}
		if t.Status.Terminal() {
			recent = append(recent, line)
		} else {
			open = append(open, line)
		}
	}
	if len(recent) > 5 {
		recent = recent[len(recent)-5:]
	}
	var sb strings.Builder
	sb.WriteString("進行中：\n")
	if len(open) == 0 {
		sb.WriteString("（無）\n")
	}
	for _, l := range open {
		sb.WriteString(l + "\n")
	}
	if len(recent) > 0 {
		sb.WriteString("\n最近結束：\n" + strings.Join(recent, "\n") + "\n")
	}
	fmt.Fprintf(&sb, "\n下一個 build number：%d", b.Store.BuildNumber())
	return sb.String()
}
