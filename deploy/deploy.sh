#!/usr/bin/env bash
# Выкладка badoo: сборка → заливка → миграции → перезапуск → проверка HEALTH_URL → откат при сбое.
# С Mac: make deploy (SSH по алиасу из ~/.ssh/config). Из GitHub Actions — тот же скрипт.
set -euo pipefail
cd "$(dirname "$0")/.."
. deploy/deploy.env
: "${DEPLOY_SSH:?}" "${DEPLOY_DIR:?}" "${HEALTH_URL:?}"
BIN=badoo-app

echo "=== Сборка (linux/amd64) ==="
mkdir -p build
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o "build/$BIN" .
tar czf build/assets.tgz views web $(ls -d messages 2>/dev/null)

echo "=== Заливка на $DEPLOY_SSH:$DEPLOY_DIR ==="
ssh "$DEPLOY_SSH" "mkdir -p $DEPLOY_DIR/runtime $DEPLOY_DIR/logs"
scp -q "build/$BIN" "$DEPLOY_SSH:$DEPLOY_DIR/$BIN.new"
scp -q build/assets.tgz deploy/keepalive.sh "$DEPLOY_SSH:$DEPLOY_DIR/"

echo "=== Миграции и перезапуск ==="
# [.]/ в шаблоне — чтобы pkill не нашёл саму эту ssh-команду; web/ не удаляем — там загрузки.
# После pkill ждём, пока старый процесс реально завершится (мягкая остановка ядра): иначе keepalive видит
# живой процесс и новый не поднимает, а healthz ждёт зря и деплой откатывается.
ssh "$DEPLOY_SSH" "cd $DEPLOY_DIR && chmod 755 $BIN.new keepalive.sh \
  && rm -rf views messages && tar xzf assets.tgz && rm assets.tgz \
  && set -a && . ./.env && set +a && ./$BIN.new migrate \
  && { [ -f $BIN ] && cp -f $BIN $BIN.prev || true; } && mv -f $BIN.new $BIN \
  && { pkill -u \$USER -f '[.]/$BIN ' || true; } \
  && for i in \$(seq 1 20); do pgrep -u \$USER -f '[.]/$BIN ' >/dev/null || break; sleep 1; done \
  && ./keepalive.sh"

echo "=== Проверка $HEALTH_URL ==="
for _ in $(seq 1 30); do
	if curl -fsS -m 5 "$HEALTH_URL" >/dev/null 2>&1; then
		echo "Готово: $HEALTH_URL отвечает"
		exit 0
	fi
	sleep 3
done

echo "Сайт не ответил за 90 с — откат на прошлую версию"
ssh "$DEPLOY_SSH" "cd $DEPLOY_DIR && [ -f $BIN.prev ] && mv -f $BIN.prev $BIN \
  && { pkill -u \$USER -f '[.]/$BIN ' || true; } \
  && for i in \$(seq 1 20); do pgrep -u \$USER -f '[.]/$BIN ' >/dev/null || break; sleep 1; done \
  && ./keepalive.sh"
echo "Откачено. Миграции, если были, остались — проверь логи: ssh $DEPLOY_SSH tail -50 $DEPLOY_DIR/logs/web.log"
exit 1
