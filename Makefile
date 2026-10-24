LABEL      := com.lido.chasage-bot
BIN_DIR    := $(HOME)/.local/bin
BIN        := $(BIN_DIR)/chasage-bot
CONFIG_DIR := $(HOME)/.config/chasage-bot
CONFIG     := $(CONFIG_DIR)/config.yaml
LOG_DIR    := $(HOME)/Library/Logs/chasage-bot
PLIST      := $(HOME)/Library/LaunchAgents/$(LABEL).plist
DOMAIN     := gui/$(shell id -u)
# launchd does not read shell rc files, so every tool the bot shells out to must be on this PATH.
LAUNCHD_PATH := $(HOME)/.local/bin:/opt/homebrew/bin:/usr/local/go/bin:$(HOME)/fvm/default/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin

.PHONY: build test config install uninstall restart status logs doctor

build: ## Build bin/chasage-bot
	go build -o bin/chasage-bot ./cmd/chasage-bot

test: ## Run unit tests
	go test -race ./...

config: ## Create ~/.config/chasage-bot/config.yaml from the example (never overwrites)
	@mkdir -p $(CONFIG_DIR)
	@if [ -f $(CONFIG) ]; then echo "$(CONFIG) already exists"; else \
	  cp config.example.yaml $(CONFIG) && chmod 600 $(CONFIG) && echo "created $(CONFIG); edit it now"; fi

install: build ## Install the binary and (re)load the LaunchAgent
	@test -f $(CONFIG) || { echo "error: $(CONFIG) missing; run make config"; exit 1; }
	@mkdir -p $(BIN_DIR) $(LOG_DIR) $(dir $(PLIST))
	cp bin/chasage-bot $(BIN)
	sed -e 's|__BIN__|$(BIN)|' -e 's|__CONFIG__|$(CONFIG)|' -e 's|__HOME__|$(HOME)|' \
	    -e 's|__PATH__|$(LAUNCHD_PATH)|' -e 's|__LOGDIR__|$(LOG_DIR)|' \
	    deploy/$(LABEL).plist > $(PLIST)
	-launchctl bootout $(DOMAIN)/$(LABEL) 2>/dev/null
	@# bootout is asynchronous; bootstrapping before the old instance is gone fails with I/O error 5.
	@for i in $$(seq 1 20); do launchctl print $(DOMAIN)/$(LABEL) >/dev/null 2>&1 || break; sleep 0.5; done
	launchctl bootstrap $(DOMAIN) $(PLIST)
	@echo "installed; logs: make logs"

uninstall: ## Stop the LaunchAgent and remove it (state and config are kept)
	-launchctl bootout $(DOMAIN)/$(LABEL)
	rm -f $(PLIST) $(BIN)

restart: ## Restart the running bot
	launchctl kickstart -k $(DOMAIN)/$(LABEL)

status: ## Show launchd status
	launchctl print $(DOMAIN)/$(LABEL) | grep -E 'state|pid|last exit' || true

logs: ## Follow the daemon log
	tail -f $(LOG_DIR)/daemon.log

doctor: ## Check every external tool the bot needs, using the launchd PATH
	@PATH="$(LAUNCHD_PATH)"; for t in git gh agent fvm make xcodebuild pod go caffeinate; do \
	  if command -v $$t >/dev/null; then printf "ok      %s\n" "$$t"; else printf "MISSING %s\n" "$$t"; fi; done
	@PATH="$(LAUNCHD_PATH)" gh auth status >/dev/null 2>&1 && echo "ok      gh auth" || echo "MISSING gh auth (run gh auth login)"
	@PATH="$(LAUNCHD_PATH)" agent status 2>&1 | grep -qi "not logged in" && echo "MISSING agent login (run agent login)" || echo "ok      agent login"
	@pmset -g custom | awk '/AC Power/{ac=1} ac && $$1=="sleep"{print ($$2=="0" ? "ok      AC sleep disabled" : "WARN    AC sleep is " $$2 " min (sudo pmset -c sleep 0)"); exit}'
