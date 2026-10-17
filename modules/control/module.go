/*
Package control — админка /control веб-версии Amura, как modules/control в StockGo1:

  - свой лейаут CoreUI (views/layouts/control.html), JS страницы — прямо во вьюхе, данные — AJAX POST в экшены;
  - меню — разделы из control_actions, к которым у админа есть право в control_rules (группы — control_categories);
  - доступ к разделу — control.go (Access), раздел = контроллер с тем же именем: /control/<раздел>/<экшен>;
  - всё, что админ меняет, пишется в control_log (раздел «Журнал действий»).

Разделы: Дашборд (index), Пользователи (users), Переписка (chats), Встречи (meetups), Модерация → Жалобы (reports),
Верификация (verification), Фото (photos), Система → Журнал действий (log), Права (permissions).

Первый вход: ./badoo-app control/super <email> (на хостинге — make control-super WHO=<email>), затем /control.

Новый раздел: контроллер modules/control/<раздел>.go с Behaviors() → access("<раздел>", страницы…), вьюха
views/control/<раздел>/…, контроллер — в Controllers() ниже, затем раздел в /control/permissions → «Разделы»
(суперадминам он откроется сам, остальным — включить в правах).
*/
package control

import (
	"embed"
	"io/fs"

	"whales.team/go1"
	"whales.team/go1/web"
)

//go:embed views
var viewsFS embed.FS

type Module struct{}

func New() *Module { return &Module{} }

func (m *Module) ID() string { return "control" }

// DefaultRoute — /control → дашборд.
func (m *Module) DefaultRoute() string { return "index/index" }

func (m *Module) Controllers() []any {
	return []any{
		&IndexController{},
		&UsersController{},
		&ChatsController{},
		&MeetupsController{},
		&ReportsController{},
		&VerificationController{},
		&PhotosController{},
		&LogController{},
		&PermissionsController{},
	}
}

func (m *Module) Rules() web.Rules {
	// Неявный роутинг в приложении выключен (config OnInit), поэтому пути админки — явно:
	// страницы — GET (index, view), данные для JS — только POST (это же проверяет sectionFilter).
	return web.Rules{
		{"GET control", "control/index/index"},
		{"GET control/<controller:[a-z-]+>", "control/<controller>/index"},
		{"GET control/<controller:[a-z-]+>/view", "control/<controller>/view"},
		{"POST control/<controller:[a-z-]+>/<action:[a-z-]+>", "control/<controller>/<action>"},
	}
}

func (m *Module) Views() fs.FS { sub, _ := fs.Sub(viewsFS, "views"); return sub }

func (m *Module) Init(a *go1.Application) error {
	// меню лейаута (со счётчиками очередей модерации): {{range controlMenu .User.Id}}
	a.Server.Views.AddFunc("controlMenu", Menu)
	return nil
}
