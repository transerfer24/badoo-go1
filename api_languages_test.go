package main

import (
	"net/http/httptest"
	"testing"
)

// Языки анкеты: справочник в /meta, сохранение PUT /me/languages (чистка мусора, лимит), показ в чужой анкете,
// и что обычное сохранение анкеты (PUT /me старых сборок) языки не стирает.
func TestApiLanguages(t *testing.T) {
	fake := &fakeNeuronet{secret: "s3cr3t", accounts: map[string]string{}, uids: map[string]int{}, sent: map[string]int{}}
	nsrv := httptest.NewServer(fake)
	defer nsrv.Close()
	h := newTestApp(t, nsrv.URL)

	var meta struct {
		Languages []struct{ Code, Name string } `json:"languages"`
		Levels    []struct{ Key, Label string } `json:"langLevels"`
		Max       int                           `json:"maxLangs"`
	}
	(&client{t: t, h: h}).do("GET", "/api/v1/meta", nil, &meta)
	if len(meta.Languages) < 50 || meta.Languages[0].Code != "uk" || len(meta.Levels) != 4 || meta.Max != 5 {
		t.Fatalf("meta языки: %d %+v %+v %d", len(meta.Languages), meta.Languages[:1], meta.Levels, meta.Max)
	}
	for _, l := range meta.Levels {
		if l.Key == "native" {
			t.Fatal("уровня «родной» быть не должно")
		}
	}

	type lang struct {
		Code       string `json:"code"`
		Name       string `json:"name"`
		Level      string `json:"level"`
		LevelLabel string `json:"levelLabel"`
	}
	type withLangs struct {
		Id        int64  `json:"id"`
		Languages []lang `json:"languages"`
	}
	a := register(t, h, "lang-a@n.io", "Аня")
	me := onboard(t, a, "Аня", "f", "m")
	var got withLangs
	if code := a.do("GET", "/api/v1/me", nil, &got); code != 200 || got.Languages == nil || len(got.Languages) != 0 {
		t.Fatalf("без языков — пустой массив: %d %+v", code, got)
	}

	// мусор отбрасывается: неизвестный код, повтор, неизвестный уровень
	in := map[string]any{"languages": []map[string]string{
		{"code": "es", "level": "learn"}, {"code": "uk", "level": "fluent"}, {"code": "xx", "level": "fluent"},
		{"code": "es", "level": "read"}, {"code": "de", "level": "native"},
	}}
	if code := a.do("PUT", "/api/v1/me/languages", in, &got); code != 200 || len(got.Languages) != 2 ||
		got.Languages[0].Code != "es" || got.Languages[0].LevelLabel != "учу" || got.Languages[1].Name != "Украинский" {
		t.Fatalf("сохранение языков: %d %+v", code, got.Languages)
	}

	// больше пяти — ошибка, выбор не меняется
	six := []map[string]string{}
	for _, c := range []string{"uk", "ru", "en", "es", "pl", "de"} {
		six = append(six, map[string]string{"code": c, "level": "speak"})
	}
	var e map[string]string
	if code := a.do("PUT", "/api/v1/me/languages", map[string]any{"languages": six}, &e); code != 400 || e["error"] != "validation" {
		t.Fatalf("6 языков: %d %v", code, e)
	}

	// PUT /me без поля языков (старые сборки) их не стирает
	onboard(t, a, "Аня", "f", "m")
	a.do("GET", "/api/v1/me", nil, &got)
	if len(got.Languages) != 2 {
		t.Fatalf("PUT /me стёр языки: %+v", got.Languages)
	}

	// в чужой анкете видно
	b := register(t, h, "lang-b@n.io", "Боря")
	onboard(t, b, "Боря", "m", "f")
	var other struct {
		User withLangs `json:"user"`
	}
	if code := b.do("GET", "/api/v1/users/"+itoa(me.Id), nil, &other); code != 200 || len(other.User.Languages) != 2 {
		t.Fatalf("языки в чужой анкете: %d %+v", code, other)
	}
}
