.PHONY: setup run migrate test build core-update deploy setup-server logs-server
export GOTOOLCHAIN=local

setup:           ## первый запуск: зависимости + таблицы и администратор
	go mod tidy
	go run . migrate

run:             ## dev-сервер
	go run . serve

migrate:
	go run . migrate

test:
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

logs-server:     ## хвост web.log на хостинге
	ssh dreampro 'tail -n 100 -f /home/dreampro/whales.team/badoo/app/logs/web.log'
