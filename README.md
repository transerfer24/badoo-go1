# Amura (Badoo) на Go1

Перенос бэкенда `~/PhpstormProjects/Badoo/backend` на фреймворк Go1. План и соответствия — `PLAN.md`,
выкладка на https://badoo.whales.team — `deploy/README.md`, стиль кода — `../Go1/docs/style.md`.

```sh
make setup      # зависимости + миграции (база из .env)
make run        # http://localhost:8080
make deploy     # на хостинг
```

Ядро подключено как `replace ../Go1/core` (go.mod): правки ядра видны сразу. Для боевой сборки по версии —
`go1 new --release` / `make core-update`.
