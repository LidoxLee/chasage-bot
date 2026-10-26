package bot

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/LidoxLee/chasage-bot/internal/activity"
	"github.com/LidoxLee/chasage-bot/internal/shell"
	"github.com/LidoxLee/chasage-bot/internal/task"
	"github.com/LidoxLee/chasage-bot/internal/telegram"
)

// chunkSize leaves headroom under Telegram's 4096 limit.
const chunkSize = 3800

// ChatNotifier sends to a single Telegram chat.
type ChatNotifier struct {
	TG     *telegram.Client
	ChatID int64
}

// Send implements Notifier.
func (n ChatNotifier) Send(ctx context.Context, text string, replyTo int, buttons [][]telegram.Button) (int, error) {
	return n.TG.Send(ctx, n.ChatID, text, replyTo, buttons)
}

// Typing implements Notifier.
func (n ChatNotifier) Typing(ctx context.Context) error {
	return n.TG.SendTyping(ctx, n.ChatID)
}

// notify sends a message that does not belong to any conversation.
func (b *Bot) notify(ctx context.Context, text string) {
	for _, chunk := range telegram.Split(text, chunkSize) {
		if _, err := b.Out.Send(ctx, chunk, 0, nil); err != nil {
			slog.Error("send", "err", err)
			return
		}
	}
}

// say sends a short message about a conversation and records it.
func (b *Bot) say(ctx context.Context, id int, text string, buttons [][]telegram.Button) {
	b.Activity.Add(id, activity.KindBot, text)
	msgID, err := b.Out.Send(ctx, telegram.Truncate(text, telegram.MaxText), 0, buttons)
	if err != nil {
		slog.Error("send", "conv", id, "err", err)
		return
	}
	if _, err := b.Store.Update(id, func(t *task.Task) error {
		t.MessageIDs = append(t.MessageIDs, msgID)
		return nil
	}); err != nil {
		slog.Error("record message", "conv", id, "err", err)
	}
}

// sayLong sends text split across as many messages as needed; buttons go on the last one.
func (b *Bot) sayLong(ctx context.Context, id int, text string, buttons [][]telegram.Button) {
	chunks := telegram.Split(text, chunkSize)
	if len(chunks) == 0 {
		chunks = []string{"（agent 沒有回覆內容）"}
	}
	for i, c := range chunks {
		var btn [][]telegram.Button
		if i == len(chunks)-1 {
			btn = buttons
		}
		b.say(ctx, id, c, btn)
	}
}

// fail marks a shipping conversation failed and reports the error with the recent log.
func (b *Bot) fail(ctx context.Context, id int, stage string, err error, log *shell.TailWriter) {
	slog.Error("ship failed", "conv", id, "stage", stage, "err", err)
	if _, uerr := b.Store.Update(id, func(t *task.Task) error {
		t.Error = err.Error()
		return t.SetStatus(task.Failed)
	}); uerr != nil {
		slog.Info("not marking failed", "conv", id, "err", uerr)
		return
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "#%d %s失敗：%s\n", id, stage, err)
	if log != nil {
		if tail := log.Tail(30); tail != "" {
			fmt.Fprintf(&sb, "\n最後的 log：\n%s\n", tail)
		}
	}
	fmt.Fprintf(&sb, "\n完整 log：%s\n重試（從合併／部署／打包繼續）：/retry %d，取消：/cancel %d", convLog(id), id, id)
	b.say(ctx, id, sb.String(), nil)
}

func prButtons(id int) [][]telegram.Button {
	return [][]telegram.Button{{
		{Text: fmt.Sprintf("合併 #%d", id), Data: fmt.Sprintf("merge:%d", id)},
		{Text: "取消", Data: fmt.Sprintf("cancel:%d", id)},
	}}
}

func planButtons(id int) [][]telegram.Button {
	return [][]telegram.Button{{
		{Text: "開始實作", Data: fmt.Sprintf("go:%d", id)},
	}}
}

func prLines(t task.Task) string {
	var sb strings.Builder
	for _, p := range t.PRs {
		fmt.Fprintf(&sb, "- %s：%s\n", p.Repo, p.URL)
	}
	return sb.String()
}
