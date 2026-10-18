#!/bin/bash
# Раз в минуту из crontab: поднять упавшие процессы badoo (web, worker, cron). Аналог systemd на shared-хостинге.
#   * * * * * /путь/к/app/keepalive.sh >/dev/null 2>&1
cd "$(dirname "$0")" || exit 0
BIN=badoo-app
[ -x "./$BIN" ] || exit 0
set -a; . ./.env; set +a
mkdir -p logs
# Ротация: лог больше 50 МБ → .1 (прошлый .1 → .2), сам файл обнуляется. Процессы пишут через >> (O_APPEND),
# поэтому обнулять на ходу безопасно — дальше пишут с начала. Хранится не больше ~150 МБ на лог.
for f in logs/*.log; do
	[ -f "$f" ] || continue
	if [ "$(wc -c < "$f")" -gt 52428800 ]; then
		[ -f "$f.1" ] && mv -f "$f.1" "$f.2"
		cp "$f" "$f.1" && : > "$f"
	fi
done
start() { # $1 — имя лога, остальное — аргументы
	local log=$1; shift
	if ! pgrep -u "$USER" -f "[.]/$BIN $1" >/dev/null 2>&1; then
		nohup "./$BIN" "$@" >> "logs/$log.log" 2>&1 &
	fi
}
start web serve
start worker queue/work --workers=2
start cron cron
