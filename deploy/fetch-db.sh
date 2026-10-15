#!/usr/bin/env bash
# Снять копию боевой базы Amura с hermes1 (SQLite /opt/badoo/data/amura.db) в data/amura.db — для импорта в MySQL
# (этап 6, ./app import data/amura.db). Копия делается через backup API SQLite (python3 на сервере есть всегда),
# поэтому снимок целостный даже при работающей службе. Запуск с Mac: make fetch-db
set -euo pipefail
cd "$(dirname "$0")/.."
SRV=${SRV:-hermes1}
SRC=${SRC:-/opt/badoo/data/amura.db}
DST=data/amura.db
mkdir -p data

echo "=== Снимок базы на $SRV ($SRC) ==="
ssh "$SRV" "sudo python3 - <<'PY'
import sqlite3, os
src = sqlite3.connect('$SRC')
dst = sqlite3.connect('/tmp/amura-copy.db')
src.backup(dst)
dst.close(); src.close()
os.chmod('/tmp/amura-copy.db', 0o644)
c = sqlite3.connect('/tmp/amura-copy.db')
for t in ['users','photos','votes','matches','messages','visits','blocks','reports','sessions','meetups','meetup_going','push_tokens']:
    try: print('  %-13s %6d' % (t, c.execute('SELECT COUNT(*) FROM '+t).fetchone()[0]))
    except Exception as e: print('  %-13s   нет (%s)' % (t, e))
PY
gzip -kf /tmp/amura-copy.db"

echo "=== Загрузка ==="
scp -q "$SRV:/tmp/amura-copy.db.gz" "$DST.gz"
gunzip -f "$DST.gz"
ssh "$SRV" "rm -f /tmp/amura-copy.db /tmp/amura-copy.db.gz"
ls -la "$DST"
echo "Готово: $DST — дальше ./app import $DST (этап 6)"
