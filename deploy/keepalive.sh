#!/bin/bash
# Раз в минуту из crontab: поднять упавшие процессы badoo (web, worker, cron). Аналог systemd на shared-хостинге.
#   * * * * * /путь/к/app/keepalive.sh >/dev/null 2>&1
cd "$(dirname "$0")" || exit 0
BIN=badoo-app
[ -x "./$BIN" ] || exit 0
set -a; . ./.env; set +a
mkdir -p logs
start() { # $1 — имя лога, остальное — аргументы
	local log=$1; shift
	if ! pgrep -u "$USER" -f "[.]/$BIN $1" >/dev/null 2>&1; then
		nohup "./$BIN" "$@" >> "logs/$log.log" 2>&1 &
	fi
}
start web serve
start worker queue/work --workers=2
start cron cron
