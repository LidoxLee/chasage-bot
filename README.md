# chasage-bot

在筆電上常駐的 Telegram 機器人，讓你在手機上和本機的 Cursor agent 對話：先問答、一起規劃，確認後實作並開 PR；你按「合併」後，等 CI 通過再合併，等 backend 部署到 EC2 完成，最後用 app 的 `Makefile` 打包 iOS 並上傳 TestFlight。每個步驟都會通知到 Telegram。

```
對話（plan 討論 → 開始實作 → agent 改 code）→ push + PR → 你按合併
  → 等 CI → 合併 backend → 等 ecr.yml 部署 → 合併 app → make build-ipa → make upload-testflight → 通知
```

## 第一次安裝

1. 工具（`make doctor` 會逐項檢查）：
   - `gh auth login`（需要 `repo`、`workflow` 權限）
   - `agent login`（Cursor CLI）
   - `fvm`、Xcode、CocoaPods：照平常開發 app 的設定即可
2. 電源設定，否則筆電閒置一分鐘就會睡眠：
   ```bash
   sudo pmset -c sleep 0
   sudo pmset -c disksleep 0
   ```
   並到「系統設定 → 一般 → 軟體更新 → 自動更新」，關掉「安裝 macOS 更新」。螢幕可以自己變黑，但不要闔上。
3. 建立 Telegram bot：在 Telegram 找 @BotFather，執行 `/newbot`，拿到 token。
4. 設定檔：
   ```bash
   make config          # 建立 ~/.config/chasage-bot/config.yaml（權限 600）
   ```
   編輯的是 **`~/.config/chasage-bot/config.yaml`**，不是專案資料夾裡的 `config.example.yaml`。
   填入 `telegram_token`、`release.asc_key_id`、`release.asc_issuer_id`。`allowed_chat_id` 先留 0。
   `build_number_start` 要大於 App Store Connect 上已有的最大 build number。
5. 取得 chat id：
   ```bash
   make build && ./bin/chasage-bot
   ```
   在 Telegram 傳任何訊息給 bot，它會回覆你的 chat id。按 Ctrl-C 停掉，把 chat id 填進 `allowed_chat_id`。
6. 安裝成常駐服務：
   ```bash
   make install         # 開機登入後自動啟動，當掉自動重啟
   make logs            # 看 log
   ```

## 使用方式

同一時間只有一個「目前對話」。對話有三種模式，和 Cursor 一樣；切換模式不會換 session，所以 plan 討論過的內容，切到 agent 實作時它都記得。

| 模式 | agent 能做的事 |
|---|---|
| `plan`（新對話預設） | 只讀 code、討論並產出計畫；計畫下方有「開始實作」按鈕 |
| `ask` | 只讀 code、回答問題 |
| `agent` | 改 code、跑測試、commit；有新 commit 就 push 並開或更新 PR |

| 你在 Telegram 做的事 | bot 的動作 |
|---|---|
| 傳一段文字 | 送給目前對話的 agent（用目前模式）。沒有目前對話，或它已進入發版流程，就自動開新對話 |
| `/plan`、`/ask`、`/agent` 加文字 | 切換模式並送出這段文字；不加文字就只切換 |
| 按「開始實作」或 `/go` | 切到 agent 模式，請它照討論好的計畫實作 |
| PR 開著時繼續傳訊息 | agent 模式的修改推到同一個 PR；ask／plan 模式可以問 PR 的問題，不影響 PR |
| 按「合併」或 `/merge` | 等所有 PR 的 CI 通過 → 合併 backend → 等部署 → 合併 app → 打包上傳 |
| 在 GitHub 上自己合併 | 兩分鐘內偵測到，接著跑部署與打包 |
| `/new [文字]` | 結束目前對話（不能有未合併的 PR）並開新對話 |
| `/end` | 結束目前對話、清除 worktree |
| `/mode` | 目前對話的編號、模式、狀態 |
| `/status` | 所有對話與下一個 build number |
| `/retry [N]` | 發版失敗或中斷時，從合併／部署／打包繼續 |
| `/cancel [N]` | 中止 agent、關閉 PR、清除 worktree |
| `/release` | 不經對話，直接打包 app 的 main 上傳 TestFlight |
| `/buildno N` | 手動設定下一個 build number |

編號省略時，指令套用到目前對話。agent 處理中你又傳訊息，會排在後面依序處理；處理期間 Telegram 會顯示「輸入中」。agent 模式沒有產生 commit（例如它在反問你）時就是一般回覆，不算失敗。

## 安全設計

- 只聽 `allowed_chat_id` 傳來的訊息，其他人的訊息只寫進 log。
- 任何 check 失敗，或等了 2 分鐘都沒有任何 check，就拒絕合併。bot 只用 `gh pr merge --squash`，不會直接 push 到 `main`。
- backend 部署失敗時，不會合併 app，也不會打包。
- bot 用自己的 clone（`~/chasage-bot/repos/`、`~/chasage-bot/release/`），不會動你平常開發用的資料夾。
- agent 處理到一半時 bot 重啟，會通知你重新傳送那則訊息。打包、上傳進行到一半時重啟，會標成「中斷」並等你 `/retry`，避免重複上傳。合併和等待部署則會自動接著做。
- agent 用 `--force` 執行，可以不經確認就跑 shell 指令；prompt 會限制它只在對話的 worktree 裡工作，不能改 workflow 或密鑰。ask 和 plan 模式是唯讀的。
- 啟動時會檢查 `AuthKey_<asc_key_id>.p8` 是否存在，找不到就拒絕啟動，避免打包完才發現不能上傳。

## 檔案位置

| 用途 | 路徑 |
|---|---|
| 設定 | `~/.config/chasage-bot/config.yaml` |
| 狀態 | `~/Library/Application Support/chasage-bot/state.json` |
| log | `~/Library/Logs/chasage-bot/`（`daemon.log`、`conv-<id>.log`、`release-*.log`） |
| clone 與 worktree | `~/chasage-bot/` |

## 開發

```bash
make test     # 單元測試加流程測試（git/gh/agent/make 全部用假的實作）
make restart  # 改完程式後：make install 會重新編譯並重啟
```
