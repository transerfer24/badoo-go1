package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Быстрый вход: верный ключ + почта из списка → сессия и редирект; чужой ключ или почта не из списка → 404.
func TestQuickauth(t *testing.T) {
	t.Setenv("QUICKAUTH_KEY", "test-key-0123456789-0123456789-0123456789")
	t.Setenv("QUICKAUTH_EMAILS", "me@example.com, other@example.com")
	nsrv := httptest.NewServer(&fakeNeuronet{secret: "s3cr3t", accounts: map[string]string{}, uids: map[string]int{}, sent: map[string]int{}})
	defer nsrv.Close()
	h := newTestApp(t, nsrv.URL)
	c := register(t, h, "me@example.com", "Денис")
	onboard(t, c, "Денис", "m", "f")

	get := func(q string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/quickauth/login?"+q, nil))
		return w
	}
	if w := get("key=wrong&email=me@example.com"); w.Code != 404 {
		t.Fatalf("чужой ключ: %d", w.Code)
	}
	if w := get("key=test-key-0123456789-0123456789-0123456789&email=stranger@example.com"); w.Code != 404 {
		t.Fatalf("почта не из списка: %d", w.Code)
	}
	w := get("key=test-key-0123456789-0123456789-0123456789&email=ME@example.com&to=/nearby")
	if w.Code != 302 || w.Header().Get("Location") != "/nearby" {
		t.Fatalf("вход: %d %s", w.Code, w.Header().Get("Location"))
	}
	cookie := strings.Join(w.Header().Values("Set-Cookie"), "; ")
	req := httptest.NewRequest(http.MethodGet, "/api/counts", nil) // JSON кабинета: в тестовом приложении нет вьюх
	req.Header.Set("Cookie", cookie)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req)
	if w2.Code != 200 {
		t.Fatalf("после входа /api/counts: %d %s", w2.Code, w2.Body.String())
	}
}
