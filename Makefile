.PHONY: setup run migrate test build core-update deploy setup-server logs-server fetch-db import-prod
export GOTOOLCHAIN=local

setup:           ## первый запуск: зависимости + таблицы и администратор
	go mod tidy
	go run . migrate

run:             ## dev-сервер
	go run . serve

migrate:
	go run . migrate

test:             ## правила URL (на пустой SQLite, без .env-базы) + тесты
	DB_DSN="file:$$(mktemp -d)/routes.db" go run . routes --check | tail -1
	go test -race ./...

build:           ## бинарник под Linux-сервер
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o build/app .

core-update:     ## обновить ядро Go1: make core-update (последний патч — безопасность) или V=v1.1.0
	go get whales.team/go1@$(or $(V),patch)
	go mod tidy
	go test ./...

deploy:          ## выложить на сервер: сборка, миграции, перезапуск, проверка /healthz, откат при сбое (deploy/deploy.env)
	./deploy/deploy.sh

setup-server:    ## первый раз: каталог, .env и crontab на хостинге, затем deploy (deploy/setup.sh)
	./deploy/setup.sh

fetch-db:        ## снять копию боевой SQLite-базы с hermes1 в data/amura.db (deploy/fetch-db.sh)
	./deploy/fetch-db.sh

import-prod:     ## залить data/amura.db на хостинг и импортировать в MySQL (повтор: WIPE=1 make import-prod)
	scp -q data/amura.db dreampro:/home/dreampro/whales.team/badoo/app/runtime/amura.db
	ssh dreampro 'cd /home/dreampro/whales.team/badoo/app && set -a && . ./.env && set +a && ./badoo-app import runtime/amura.db $(if $(WIPE),--wipe,) --photos=$(or $(PHOTOS),amur/prod) && rm -f runtime/amura.db'

logs-server:     ## хвост web.log на хостинге
	ssh dreampro 'tail -n 100 -f /home/dreampro/whales.team/badoo/app/logs/web.log'
