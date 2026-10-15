# Amura (Badoo) на Go1 — план переноса

Исходник: `~/PhpstormProjects/Badoo/backend` — 30 файлов, 7 000 строк на голом `net/http` + SQLite, боевой
адрес https://badoo.surfer.team (hermes1), мобильное приложение Expo (`Badoo/mobile`) ходит в `/api/v1/*`.
Цель: тот же продукт на Go1 + MySQL хостинга, адрес https://badoo.whales.team, потом переключить приложение.

Правила переноса — `Go1/docs/style.md` (стиль экшенов, «Перенос с Yii»). Здесь исходник не Yii, а Go,
поэтому переносится проще: SQL и логика копируются, меняется только «обвязка» — роутер, сессии, шаблоны, JSON.

## Что уже есть (этап 0 — готово)

- Проект из скелета `go1 new badoo`: `config/app.go`, `main.go`, `Makefile`, `deploy/`.
- `migrations/m261014_190000_init.go` — вся схема (users, photos, votes, matches, messages, visits, blocks, reports,
  auth_flows, sessions, meetups, meetup_going, push_tokens) + auth_* для админки. Проверена на SQLite; на MySQL —
  первым `make setup`.
- `components/auth` — identity: веб по cookie-сессии, мобильное приложение по Bearer из `sessions` (как раньше).
- Вход **только через Neuronet** (как wealthbook.app/signin и «Я есть»): почта → пароль или код → регистрация в
  Neuronet. Своей регистрации и паролей нет (локальный режим старого бэкенда убран). Нужны `NEURONET_SERVICE` и
  `NEURONET_SECRET` — запись сервиса badoo.whales.team в `ts_services` Neuronet.
- Фото — общий бакет S3, у Amura своя папка `S3_PREFIX=badoo/prod/`.
- `.env` с базой `dreampro_wbadoo`, `deploy/deploy.env` → `/home/dreampro/whales.team/badoo/app`, `deploy/README.md`.

## Соответствие старого бэкенда и Go1

| Было (`backend/`) | Становится | Чем ядро помогает |
|---|---|---|
| `main.go` routes(), `m.HandleFunc("GET /u/{id}", a.auth(true, …))` | контроллеры + `web.Named` правила; `auth(true)` → `AccessControl{Roles:"@"}` + фильтр `onboarded` | роутер, Behaviors |
| `a.auth(...)`, cookie `sess`, своя CSRF | `c.User`, сессии Go1, CSRF автоматически | web/auth, session, csrf |
| `db.go` schema строкой, `hasColumn` | миграции (сделано) | db/migrate |
| `models.go` User/Card/Msg + `scan…` | `db.Row` + `Row.Scan(&u)` в структуры там, где структура нужна (Card для JSON) | db/scan.go |
| `render.go` (html/template, layout.html, partials) | `views/layouts/main.html`, `views/<ctrl>/<action>.html`, `c.Render` | ViewEngine |
| `api_v1.go` + `authapi.go` (JSON, `corsV1`) | `ApiController` (`return any, error` → JSON), `web.Cors` middleware | ApiController, OpenAPI бесплатно |
| `authflow.go` (Neuronet: email → пароль/код → register) | `components/neuronet` + `AuthController` (web) и `ApiAuthController` (v1); `auth_flows` таблица та же | `web.SafeHTTPClient`, rate limit |
| `hub.go` (SSE `/api/events`, long-poll `/api/v1/events?since=`) | пакет `ws` ядра (`ws.ToUser`) для веба + long-poll как был для мобильного; буфер событий — `app.Cache` | ws, cache |
| `push.go` (Expo, очередь в памяти, антиспам 20 с) | `queue` ядра: задача `push.send`, воркер `queue/work`; антиспам — `app.Cache` | queue, cache |
| `images.go` (ресайз, jpeg) + `storage.go` (свой S3 SigV4) | `modules/files` (S3 из ядра, `Thumb`, MIME по содержимому, `MaxPixels`) | files |
| `backup.go` (VACUUM INTO → S3 раз в сутки) | не нужен: база — MySQL хостинга, бэкапы делает хостинг | — |
| `observe.go` (/metrics, медленные маршруты) | `metrics` ядра: `/metrics`, `/healthz`, `/readyz`, debug-панель в dev | metrics |
| `seed.go`, `testpeople.go` | консольные команды `commands/seed.go`: `./app seed`, `./app testpeople/add` | console |
| `smoke.go` (дым по API после выкладки) | `deploy.sh` проверяет `/healthz`; дым по API — `go test ./e2e` с `GO1_E2E_URL` | — |
| `AMURA_*` переменные | `.env` → `app.Params["neuronet.*", "push.*", "amura.*"]` (config/app.go) | config |

## Этапы

Каждый этап заканчивается: `go test ./...` зелёный, `make deploy`, проверка на https://badoo.whales.team.

### 1. Каркас и вход (веб) — ГОТОВО (14.10)
- `components/neuronet`: перенос `authflow.go` (запросы к neuronet.app подписанные секретом, шаги, тикет, `auth_flows`).
- `AuthController`: `/login` (форма), `POST /login/<step>`, `/logout`, `/onboarding`. Шаблоны `signin/login/onboarding` из `templates/`.
- `layouts/main.html` из `templates/layout.html`, `partials.html` → `views/_*.html`.
- Фильтр `onboarded` (аналог `auth(true)`) — `Behaviors()` базового `AppController`.
- **Приёмка**: регистрация через Neuronet и вход на badoo.whales.team, анкета заполняется.
- Сделано: `components/neuronet` (шаги 1:1 с authflow.go, пароли pbkdf2 совместимы со старыми хешами),
  `components/users` (User + справочники + `ParseProfileForm`, `me`/`counts` для лейаута), `controllers/{app,auth,site,encounters}.go`,
  `views/layouts/main.html`, `views/_partials.html`, `views/auth/{signin,onboarding}.html`, `views/site/{landing,error}.html`,
  `web/static/{app.css,app.js,logo.svg}`. Проверено локально: почта → регистрация → онбординг → /encounters (с подменой Neuronet; живой вход — после NEURONET_* в .env).
  `EncountersController` — заглушка до этапа 3; `/me` временно ведёт туда же.

### 2. Анкета и фото — ГОТОВО (14.10)
- `MeController`: `/me`, `/me/photos/*`, `/settings`, `/settings/delete`, `/verify`, `/premium/*` (handlers_me.go).
- Фото через `modules/files` в тот же бакет S3 (`amur/prod/`), ресайз из `images.go` → `files.Thumb`.
- `users.premium_until`, `verified` — те же поля; `AMURA_DEV` поведение сохранить.
- **Приёмка**: анкета с фото, селфи-верификация, Premium в dev.
- Сделано: `components/photos` (перекодирование в JPEG ≤1280 px без EXIF + `modules/files` ядра: S3 общего бакета,
  папка `S3_PREFIX`; селфи — в `<prefix>/private`), `controllers/me.go` (все 11 экшенов handlers_me.go),
  `site/avatar` (SVG-аватары), `views/me/{index,settings,premium,verify}.html`. Удаление аккаунта чистит связанные
  таблицы явно (в SQLite это делал ON DELETE CASCADE). Прогнано локально: фото → анкета → настройки → Premium →
  верификация → удаление (файлы и строки `file` убраны).

### 3. Знакомства — ГОТОВО (14.10)
- `EncountersController` (`/encounters`, `api/encounters`, `api/vote`, `api/undo`) и `PeopleController` (`/nearby`, `/u/<id>`,
  vote/block/report) — из handlers_discovery.go; `SocialController` (`/likes`, `/visitors`, `/messages`, `/chat/<id>`,
  `api/chat/*`, `api/counts`, `api/events`) — из handlers_social.go.
- Логика в компонентах: `components/discovery` (`Candidates`, `Rank`, `Vote` с мэтчем, `Undo`, `Block`, `Report`, `Visit`,
  SQL тот же), `components/chat` (`Threads`, `Messages`, `Send`, `MarkRead`), `components/hub` (события в памяти — перенос
  hub.go один в один; `ws` ядра не понадобился: веб слушает SSE `/api/events` прямо из экшена через `c.Hijacked`,
  Gzip/ETag ядра `text/event-stream` не трогают). Буфер событий на пользователя — 100, `hub.Offline` — крючок для push (этап 5).
- Шаблоны `views/encounters/index.html`, `views/people/{nearby,profile}.html`, `views/social/{likes,visitors,messages,chat}.html`;
  `web/static/app.js` как был — JSON-ключи карточек/событий совпадают со старыми.
- Прогнано локально двумя анкетами: визит → лайк → гости/симпатии/счётчики → ответный лайк через API → мэтч (SSE-событие
  `match`) → чат (`message` по SSE, прочитано) → undo без Premium = 402 → жалоба с блокировкой: анкета и чат пропадают.
- **Приёмка**: лайк → мэтч → чат между двумя анкетами, уведомление в реальном времени.

### 4. API v1 для приложения — ГОТОВО (14.10)
- `controllers/api*.go`: `ApiController` (база: Bearer через `auth.Finder.FindIdentityByAccessToken`, 401 `unauthorized` /
  403 `onboarding`, лимит) и `ApiAuthController` (`/api/v1/auth/*` — шаги Neuronet, logout; `signup`/`login` → 410),
  `ApiMeController` (анкета, фото, настройки, верификация, push-токены), `ApiPeopleController` (encounters, votes, undo,
  nearby, users/<id>*), `ApiSocialController` (likes, visitors, counts, chats*, events long-poll), `ApiPremiumController`,
  `ApiMetaController`. Пути и JSON-ответы — **те же**, что в api_v1.go/authapi.go, ошибки `{"error", "message"}` (apiFail).
- Общее с вебом вынесено в компоненты: `users/account.go` (настройки, Premium, верификация, удаление, палитра),
  `photos` (Mine/Add/Remove/SetMain), `discovery` (Encounters/Nearby/Relate/Likers/Matches/Visitors); веб-контроллеры
  переведены на них — логика одна, два транспорта.
- CORS только на `/api/v1/` и `/uploads/` (`apiCors` в config/app.go, Origin `*`, без cookie).
- `api_v1_test.go` — приложение в процессе на SQLite + поддельный Neuronet: meta → вход (новый/существующий, ошибки) →
  онбординг → фото → настройки → колода → визит/лайк/мэтч → события long-poll → чат/прочитано/счётчики → Premium →
  верификация → жалоба+блок → logout → удаление. `make test` гоняет его.
- Отличие от оригинала: `meetups` в ответе `users/<id>` пока `[]` (этап 5); `PUT /me` без `height` не сбрасывает рост.
- **Приёмка**: мобильное приложение с `PROD_URL=https://badoo.whales.team` проходит все экраны — после `make deploy`
  и переключения `src/lib/config.ts` (этап 6).

### 5. Встречи и push — ГОТОВО (15.10)
- `components/meetups` (перенос meetups.go): `Nearby`, `DTOs` (4 запроса на любой список; лица — до 6 на событие
  режутся в Go вместо ROW_NUMBER, которого нет в MySQL 5.7), `Attendees`, `Create/Delete/Join/Leave/Checkin`, `OfUser`
  (общие события в анкете — `meetups` в `users/<id>` теперь настоящий), `EnsureDemo` (тестовые события в dev).
  `ApiMeetupsController` — `/api/v1/meetups*`, те же ответы. Даты — DATETIME, «не отметился» — NULL.
- `components/push` (перенос push.go): `hub.Offline = push.Notify` → задача `push.send` в очереди ядра → воркер
  `./app queue/work` шлёт пачками в Expo, `DeviceNotRegistered` удаляет токен; повтор при 5xx/429 — backoff очереди.
  Антиспам лайков/мэтчей (20 с) — `app.Cache.Add`. На хостинге воркер и cron держит keepalive.sh.
- `commands/cron.go`: `meetups.demo` раз в 6 часов (было demoMeetupLoop) и команда `./app meetups/demo`.
- `api_meetups_test.go`: создание → список рядом → «Пойду» (лимит мест) → участники → «Я здесь» → анкета с общим
  событием → отмена; лайк без открытой ленты → пуш в подменный Expo через очередь, мёртвый токен удалён.
- **Приёмка**: сообщение приходит push-ем на телефон; встречи видны в приложении — после `make deploy`.

### 6. Данные и переключение — импорт ГОТОВ (15.10), переключение после проверки
- `make fetch-db` → `data/amura.db` (снимок SQLite с hermes1 через backup API), `./app import data/amura.db [--wipe]
  [--photos=amur/prod]` (commands/import.go): даты ISO → DATETIME, `''` → NULL в read_at/premium_until/checked_in_at,
  имена фото → ключи `<старая папка>/photos/<имя>` в том же бакете (файлы не копируются), auth_key генерируется,
  sessions переносятся (приложение остаётся залогиненным), id сохраняются. Повторный прогон — только с `--wipe`.
- Проверено на снимке от 15.10 (101 анкета, 229 фото, 19 событий): вход старым Bearer-токеном, колода, чаты, встречи.
- `make import-prod` — залить снимок на хостинг и выполнить импорт там (с его .env → MySQL).
- Дальше: заморозка старого бэкенда → `make fetch-db && make import-prod` → дым с телефона → `PROD_URL` в
  `mobile/src/lib/config.ts` → сборка приложения. hermes1 остаётся на 2–4 недели (старые сборки), потом выключить.

### Под нагрузку (15.10)
- Экран чатов и счётчик непрочитанных — без подзапросов к messages: `matches.last_msg_id/last_at/unread_a/unread_b`
  (миграция m261015_120000, обновляются в `chat.Send`/`MarkRead`, пересчёт `chat.Rebuild` после импорта). Один запрос
  по индексу (user_a|user_b, last_at) + JOIN по PK. `meetups.Attendees` — голоса и мэтчи одним IN вместо N+1.
- Следующие кандидаты, когда понадобится: кеш колоды и `Counts` в Redis (`Config.Redis` — ядро само переключит кеш,
  сессии и лимиты), реплика на чтение.

### 7. Потом
- Админка: `make:crud users`, `make:crud reports` — модерация селфи и жалоб (сейчас их нет вообще).
- `audit`, `flags` модули — по желанию.

## Команды

```sh
make setup      # go mod tidy + миграции в dreampro_wbadoo (первый раз)
make run        # http://localhost:8080
make test
make deploy     # на хостинг: deploy/README.md (один раз — каталог, .env, crontab)
go run . routes --check
```
