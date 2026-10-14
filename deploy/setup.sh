#!/usr/bin/env bash
# Первая выкладка Amura на shared-хостинг (dreampro): каталог → .env из локального .env → crontab → deploy.sh.
# Запуск с Mac: make setup-server. Повторный запуск безопасен: .env на хостинге не перезаписывает без FORCE_ENV=1.
set -euo pipefail
cd "$(dirname "$0")/.."
. deploy/deploy.env
# Одно SSH-соединение на всю выкладку (ControlMaster): shared-хостинг банит на минуту за серию новых подключений.
SSH_CM="-o ControlMaster=auto -o ControlPath=$HOME/.ssh/cm-%r@%h:%p -o ControlPersist=180"
ssh() { command ssh $SSH_CM "$@"; }
scp() { command scp $SSH_CM "$@"; }
: "${DEPLOY_SSH:?}" "${DEPLOY_DIR:?}"
[ -f .env ] || { echo "нет локального .env — секреты берутся из него"; exit 1; }

echo "=== Каталог $DEPLOY_SSH:$DEPLOY_DIR ==="
ssh "$DEPLOY_SSH" "mkdir -p $DEPLOY_DIR/runtime $DEPLOY_DIR/logs $DEPLOY_DIR/web/uploads"

if [ "${FORCE_ENV:-0}" = 1 ] || ! ssh "$DEPLOY_SSH" "test -s $DEPLOY_DIR/.env"; then
	echo "=== .env для хостинга: секреты из локального .env, прод-настройки сверху ==="
	rand() { openssl rand -base64 36 | tr -d '/+=' | cut -c1-40; }
	mkdir -p build
	{
		echo "# /home/dreampro/whales.team/badoo/app/.env — собран deploy/setup.sh $(date +%F) из локального .env"
		echo "APP_ENV=prod"
		echo "# панель хостинга → badoo.whales.team → проксирование HTTP-трафика в приложение"
		echo "ADDR=127.1.10.226:3000"
		echo "TRUST_PROXY=1"
		echo "BASE_URL=https://badoo.whales.team"
		echo "COOKIE_KEY=$(rand)"
		echo "METRICS_TOKEN=$(rand)"
		# всё остальное (база, Neuronet, S3, режимы) — как локально; служебные строки убираем
		# значения в одинарных кавычках: keepalive.sh и deploy.sh делают `. ./.env`, а в DSN есть ( ) & % — bash на них ломается
		grep -Ev '^\s*(#|$|APP_ENV=|ADDR=|TRUST_PROXY=|BASE_URL=|COOKIE_KEY=|METRICS_TOKEN=)' .env \
			| sed -E "s/^([A-Za-z_][A-Za-z0-9_]*)=['\"]?(.*[^'\"])?['\"]?$/\1='\2'/"
	} > build/.env.hosting
	scp -q build/.env.hosting "$DEPLOY_SSH:$DEPLOY_DIR/.env"
	ssh "$DEPLOY_SSH" "chmod 600 $DEPLOY_DIR/.env"
	rm -f build/.env.hosting
	echo "   .env залит ($(ssh "$DEPLOY_SSH" "grep -c = $DEPLOY_DIR/.env") строк)"
else
	echo "=== .env на хостинге уже есть — не трогаю (FORCE_ENV=1 — пересобрать) ==="
fi

echo "=== crontab: keepalive раз в минуту ==="
ssh "$DEPLOY_SSH" "(crontab -l 2>/dev/null | grep -v '$DEPLOY_DIR/keepalive.sh'; echo '* * * * * $DEPLOY_DIR/keepalive.sh >/dev/null 2>&1') | crontab -"
ssh "$DEPLOY_SSH" "crontab -l | grep -c keepalive"

echo "=== Выкладка ==="
exec deploy/deploy.sh
