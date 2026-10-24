// Package telegram is a minimal Telegram Bot API client (long polling only).
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxText is Telegram's message length limit.
const MaxText = 4096

// User is a Telegram user.
type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

// Chat is a Telegram chat.
type Chat struct {
	ID int64 `json:"id"`
}

// Message is an incoming or sent message.
type Message struct {
	MessageID      int      `json:"message_id"`
	From           *User    `json:"from"`
	Chat           Chat     `json:"chat"`
	Text           string   `json:"text"`
	ReplyToMessage *Message `json:"reply_to_message"`
}

// CallbackQuery is an inline keyboard button press.
type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

// Update is one item from getUpdates.
type Update struct {
	UpdateID      int            `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

// Button is an inline keyboard button carrying callback data.
type Button struct {
	Text string `json:"text"`
	Data string `json:"callback_data"`
}

// Client talks to the Bot API.
type Client struct {
	base string
	http *http.Client
}

// New returns a client for the bot token.
func New(token string) *Client {
	return &Client{
		base: "https://api.telegram.org/bot" + token + "/",
		http: &http.Client{Timeout: 90 * time.Second},
	}
}

type apiResponse struct {
	OK          bool            `json:"ok"`
	Description string          `json:"description"`
	Result      json.RawMessage `json:"result"`
}

func (c *Client) call(ctx context.Context, method string, params, out any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		// The URL contains the token; never surface it.
		return fmt.Errorf("telegram %s: request failed", method)
	}
	defer resp.Body.Close()
	var r apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return fmt.Errorf("telegram %s: decode: %w", method, err)
	}
	if !r.OK {
		return fmt.Errorf("telegram %s: %s", method, r.Description)
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

// GetUpdates long-polls for updates starting at offset.
func (c *Client) GetUpdates(ctx context.Context, offset int, timeout time.Duration) ([]Update, error) {
	var ups []Update
	err := c.call(ctx, "getUpdates", map[string]any{
		"offset":          offset,
		"timeout":         int(timeout.Seconds()),
		"allowed_updates": []string{"message", "callback_query"},
	}, &ups)
	return ups, err
}

// Send posts a plain-text message and returns its id.
func (c *Client) Send(ctx context.Context, chatID int64, text string, replyTo int, buttons [][]Button) (int, error) {
	params := map[string]any{
		"chat_id":                  chatID,
		"text":                     Truncate(text, MaxText),
		"disable_web_page_preview": true,
	}
	if replyTo != 0 {
		params["reply_parameters"] = map[string]any{"message_id": replyTo, "allow_sending_without_reply": true}
	}
	if len(buttons) > 0 {
		params["reply_markup"] = map[string]any{"inline_keyboard": buttons}
	}
	var m Message
	if err := c.call(ctx, "sendMessage", params, &m); err != nil {
		return 0, err
	}
	return m.MessageID, nil
}

// SendTyping shows the "typing…" indicator for about five seconds.
func (c *Client) SendTyping(ctx context.Context, chatID int64) error {
	return c.call(ctx, "sendChatAction", map[string]any{"chat_id": chatID, "action": "typing"}, nil)
}

// AnswerCallback acknowledges a button press so the client stops its spinner.
func (c *Client) AnswerCallback(ctx context.Context, id, text string) error {
	return c.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": id, "text": text}, nil)
}

// Split breaks s into chunks of at most n runes, preferring to cut at line breaks.
func Split(s string, n int) []string {
	var chunks []string
	r := []rune(strings.TrimSpace(s))
	for len(r) > n {
		cut := n
		for i := n; i > n/2; i-- {
			if r[i-1] == '\n' {
				cut = i
				break
			}
		}
		if c := strings.TrimSpace(string(r[:cut])); c != "" {
			chunks = append(chunks, c)
		}
		r = []rune(strings.TrimLeft(string(r[cut:]), "\n"))
	}
	if len(r) > 0 {
		chunks = append(chunks, string(r))
	}
	return chunks
}

// Truncate shortens s to at most n runes, marking the cut.
func Truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-2]) + "\n…"
}
