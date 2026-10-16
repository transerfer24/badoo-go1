package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Неявный роутинг выключен: экшен доступен только по правилу из config.App.Rules с его глаголом.
// Иначе GET /people/block?id=5 сработал бы с cookie по обычной ссылке (CSRF ядро проверяет лишь не-GET).
func TestNoImplicitRoutes(t *testing.T) {
	nsrv := httptest.NewServer(&fakeNeuronet{secret: "s3cr3t", accounts: map[string]string{}, uids: map[string]int{}, sent: map[string]int{}})
	defer nsrv.Close()
	h := newTestApp(t, nsrv.URL)
	c := register(t, h, "sec@example.com", "Сек")
	onboard(t, c, "Сек", "m", "f")

	for _, p := range []string{"/people/block?id=5", "/me/photo-delete?id=1", "/me/premium-activate", "/api-people/block?id=5", "/api-me/index", "/admin/user/delete"} {
		for _, m := range []string{http.MethodGet, http.MethodHead} {
			req := httptest.NewRequest(m, p, nil)
			req.Header.Set("Authorization", "Bearer "+c.token)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != 404 && w.Code != 405 { // 405 — правило есть, но только для POST/DELETE
				t.Errorf("%s %s: ждали 404/405 (экшен не выполнен), получили %d", m, p, w.Code)
			}
		}
	}
	// а по правилу — работает
	if st := c.do(http.MethodGet, "/api/v1/me", nil, nil); st != 200 {
		t.Fatalf("GET /api/v1/me по правилу: %d", st)
	}
}
