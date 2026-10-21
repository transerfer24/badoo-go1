package users

import (
	"strings"
	"sync"
	"time"

	"whales.team/go1/app"
)

/*
Языки анкеты. Справочник — таблица languages (правится в /control/languages), выбор человека — users.languages:
«uk:fluent,ru:fluent,en:speak» (код:уровень, до MaxLangs, в порядке выбора). Справочник маленький и меняется
редко — держим его в памяти и перечитываем раз в LangTTL или сразу после правки в админке (LangsChanged).
*/

// MaxLangs — сколько языков можно указать в анкете.
const MaxLangs = 5

// LangLevel — уровень владения. «Родной» не используем: слово политизированное, «свободно» его покрывает.
type LangLevel struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

var LangLevels = []LangLevel{{"fluent", "свободно"}, {"speak", "говорю"}, {"read", "читаю"}, {"learn", "учу"}}

// Lang — язык из справочника.
type Lang struct {
	Code   string `json:"code" db:"code"`
	Name   string `json:"name" db:"name"`
	Native string `json:"native" db:"native"`
	Active bool   `json:"-" db:"active"`
}

// UserLang — язык в анкете с уровнем.
type UserLang struct {
	Code       string `json:"code"`
	Name       string `json:"name"`
	Level      string `json:"level"`
	LevelLabel string `json:"levelLabel"`
}

const LangTTL = 5 * time.Minute

var langCache struct {
	sync.Mutex
	at   time.Time
	list []Lang          // активные, по pos
	all  map[string]Lang // все, включая скрытые (у кого выбран скрытый — он остаётся в анкете)
}

// LangsChanged — сбросить кеш (после правки справочника в админке).
func LangsChanged() {
	langCache.Lock()
	langCache.at = time.Time{}
	langCache.Unlock()
}

func langsLoad() ([]Lang, map[string]Lang) {
	langCache.Lock()
	defer langCache.Unlock()
	if langCache.all != nil && time.Since(langCache.at) < LangTTL {
		return langCache.list, langCache.all
	}
	rows, err := app.Db.CreateCommand("SELECT code, name, native, active FROM {{languages}} ORDER BY pos, name").QueryAll()
	if err != nil {
		app.Log.Error("languages: справочник", "err", err)
		if langCache.all != nil {
			return langCache.list, langCache.all
		}
		return nil, map[string]Lang{}
	}
	list, all := []Lang{}, make(map[string]Lang, len(rows))
	for _, r := range rows {
		l := Lang{Code: r.Str("code"), Name: r.Str("name"), Native: r.Str("native"), Active: r.Bool("active")}
		all[l.Code] = l
		if l.Active {
			list = append(list, l)
		}
	}
	langCache.list, langCache.all, langCache.at = list, all, time.Now()
	return list, all
}

// Languages — активные языки для выбора, в порядке справочника.
func Languages() []Lang { list, _ := langsLoad(); return list }

// LevelLabel — подпись уровня («говорю»); "" — нет такого.
func LevelLabel(key string) string {
	for _, l := range LangLevels {
		if l.Key == key {
			return l.Label
		}
	}
	return ""
}

// ParseLangs — users.languages → список для анкеты (неизвестные коды и уровни пропускаются).
func ParseLangs(s string) []UserLang {
	out := []UserLang{}
	if s == "" {
		return out
	}
	_, all := langsLoad()
	for _, part := range strings.Split(s, ",") {
		code, level, _ := strings.Cut(part, ":")
		l, ok := all[code]
		if lbl := LevelLabel(level); ok && lbl != "" {
			out = append(out, UserLang{Code: code, Name: l.Name, Level: level, LevelLabel: lbl})
		}
	}
	return out
}

// NormalizeLangs — выбор из формы/API → строка для users.languages: только языки справочника (скрытый можно
// оставить, если он уже был выбран — prev), только известные уровни, без повторов, не больше MaxLangs.
func NormalizeLangs(pick []UserLang, prev string) string {
	_, all := langsLoad()
	had := map[string]bool{}
	for _, l := range ParseLangs(prev) {
		had[l.Code] = true
	}
	seen := map[string]bool{}
	parts := []string{}
	for _, p := range pick {
		l, ok := all[p.Code]
		if !ok || (!l.Active && !had[p.Code]) || seen[p.Code] || LevelLabel(p.Level) == "" {
			continue
		}
		seen[p.Code] = true
		parts = append(parts, p.Code+":"+p.Level)
		if len(parts) == MaxLangs {
			break
		}
	}
	return strings.Join(parts, ",")
}
