#!/usr/bin/env bash
# ship.sh — показать изменения, добавить всё, закоммитить и запушить одной командой.
#   ./ship.sh "сообщение коммита"     — со своим сообщением
#   ./ship.sh                          — сообщение сгенерится с датой
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

msg="$*"
[ -z "$msg" ] && msg="update $(date '+%Y-%m-%d %H:%M')"

echo "=== Что меняется ==="
git status --short
git diff --stat
echo

if [ -n "$(git status --porcelain)" ]; then
  git add -A
  git commit -m "$msg"
else
  echo "Нечего коммитить."
fi

echo
echo "=== Пуш на GitHub ==="
git push
echo "Готово."
