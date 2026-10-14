# Выкладка Amura на shared-хостинг (dreampro) — по аналогии с go1.whales.team

```
/home/dreampro/whales.team/badoo/app/     DEPLOY_DIR (deploy/deploy.env)
├── badoo-app        бинарник (linux/amd64, статический, собирается на Маке)
├── badoo-app.prev   прошлая версия — для отката
├── .env             секреты (chmod 600) — из deploy/env.shared
├── keepalive.sh     раз в минуту из crontab поднимает упавшие процессы (web, worker, cron)
├── views/ web/      шаблоны и статика (web/uploads не трогаем — там фото без S3)
└── logs/            web.log worker.log cron.log
```

## Один раз

```sh
make setup-server    # deploy/setup.sh: каталог на хостинге → .env (секреты из локального .env + прод-настройки,
                     # свежие COOKIE_KEY/METRICS_TOKEN) → keepalive в crontab → первая выкладка (deploy.sh)
```

Проксирование в панели уже включено: badoo.whales.team → 127.1.10.226:3000 (ADDR). Готовый `.env` на хостинге
скрипт второй раз не трогает; пересобрать — `FORCE_ENV=1 make setup-server`. Поправить одну переменную руками:
`ssh dreampro 'nano /home/dreampro/whales.team/badoo/app/.env'` и `ssh dreampro pkill -f badoo-app` (keepalive поднимет).

## Каждый раз

```sh
make deploy          # сборка → scp → migrate → перезапуск → проверка https://badoo.whales.team/healthz → откат при сбое
ssh dreampro tail -50 /home/dreampro/whales.team/badoo/app/logs/web.log
```

`make deploy` — это `deploy/deploy.sh` из скелета Go1: тот же скрипт, что гоняет GitHub Actions
(`.github/workflows/deploy.yml`, секреты `GO1_READ_TOKEN`, `DEPLOY_SSH_KEY`).

## Переключение приложения

Мобильное приложение ходит на `PROD_URL` в `Badoo/mobile/src/lib/config.ts` (`https://badoo.surfer.team`).
Когда API v1 на Go1 проходит дым (PLAN.md, этап 7) — меняем на `https://badoo.whales.team`, собираем приложение.
Старый сервер на hermes1 остаётся до выхода новой сборки у пользователей.
