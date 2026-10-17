"use strict";
(() => {
  const $ = (s, el = document) => el.querySelector(s);
  const $$ = (s, el = document) => [...el.querySelectorAll(s)];
  const csrf = $('meta[name="csrf-token"]')?.content || "";

  async function api(url, body) {
    const opt = body === undefined ? {} : {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf },
      body: JSON.stringify(body),
    };
    const r = await fetch(url, opt);
    let data = null;
    try { data = await r.json(); } catch (_) {}
    if (!r.ok) { const e = new Error("http " + r.status); e.status = r.status; e.data = data; throw e; }
    return data;
  }

  let toastTimer;
  function toast(html, ms = 3500) {
    const t = $("#toast");
    if (!t) return;
    t.innerHTML = html;
    t.hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => (t.hidden = true), ms);
  }
  const esc = (s) => String(s).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

  // ---------- Тема (как в макете dating2) ----------
  // Три положения: как в системе → светлая → тёмная. Пока выбор не сделан, data-theme нет и работает @media.
  // Начальное значение ставит скрипт в <head> лейаута до отрисовки; здесь — кнопки переключателя.
  const THEME_KEY = "amura-theme", root = document.documentElement;
  function applyTheme(mode) {
    if (mode === "light" || mode === "dark") root.setAttribute("data-theme", mode);
    else root.removeAttribute("data-theme");
    $$(".theme button").forEach((b) => {
      const on = b.dataset.theme === mode;
      b.classList.toggle("on", on);
      b.setAttribute("aria-pressed", on ? "true" : "false");
    });
  }
  let themeNow = "system";
  try { themeNow = localStorage.getItem(THEME_KEY) || "system"; } catch (_) {}
  applyTheme(themeNow);
  document.addEventListener("click", (e) => {
    const b = e.target.closest(".theme button");
    if (!b) return;
    // гасим переходы на кадр: смена темы мгновенная, а не перекрашивание каждого элемента по очереди
    root.classList.add("no-anim");
    applyTheme(b.dataset.theme);
    try { localStorage.setItem(THEME_KEY, b.dataset.theme); } catch (_) {}
    requestAnimationFrame(() => requestAnimationFrame(() => root.classList.remove("no-anim")));
  });

  // ---------- Окна (<dialog>) ----------
  // [data-open="id"] открывает окно, [data-close] внутри — закрывает; нажатие по затемнению тоже закрывает.
  document.addEventListener("click", (e) => {
    const o = e.target.closest("[data-open]");
    if (o) { const d = document.getElementById(o.dataset.open); if (d) { e.preventDefault(); d.showModal(); } return; }
    const c = e.target.closest("[data-close]");
    if (c) { c.closest("dialog")?.close(); return; }
    if (e.target.tagName === "DIALOG" && e.target.open) e.target.close();
  });

  // ---------- Мелочи ----------
  $$("[data-back]").forEach((a) => a.addEventListener("click", (e) => {
    if (history.length > 1 && document.referrer.startsWith(location.origin)) { e.preventDefault(); history.back(); }
  }));
  $$("[data-range]").forEach((r) => {
    const out = r.closest("label").querySelector("[data-range-out]");
    r.addEventListener("input", () => (out.textContent = r.value));
  });
  $$("form[data-autosubmit] input[type=file]").forEach((i) => i.addEventListener("change", () => i.files.length && i.form.submit()));

  // Обратный отсчёт на «Отправить ещё раз»
  $$("[data-countdown]").forEach((b) => {
    let left = Number(b.dataset.countdown) || 0;
    const label = b.textContent;
    const tick = () => {
      if (left <= 0) { b.disabled = false; b.textContent = label; return; }
      b.disabled = true; b.textContent = `${label} через ${left} с`; left--; setTimeout(tick, 1000);
    };
    tick();
  });

  // Геолокация: точные координаты + ближайший город
  $$("form[data-geo]").forEach((form) => {
    const btn = $("[data-geo-btn]", form), status = $("[data-geo-status]", form);
    if (!btn) return;
    if (!navigator.geolocation) { btn.hidden = true; return; }
    btn.addEventListener("click", () => {
      status.textContent = "Определяем…";
      navigator.geolocation.getCurrentPosition((p) => {
        const { latitude: lat, longitude: lng } = p.coords;
        let best = null, bestD = Infinity;
        $$('select[name="city"] option[data-lat]', form).forEach((o) => {
          const d = Math.hypot(o.dataset.lat - lat, (o.dataset.lng - lng) * Math.cos(lat * Math.PI / 180));
          if (d < bestD) { bestD = d; best = o; }
        });
        if (best && bestD * 111 < 60) {
          best.selected = true;
          form.lat.value = lat.toFixed(5);
          form.lng.value = lng.toFixed(5);
          status.textContent = "Готово: " + best.value + ". Другим видно только расстояние.";
        } else {
          status.textContent = "Вы далеко от городов из списка — выберите ближайший вручную.";
        }
      }, () => (status.textContent = "Не удалось получить геопозицию. Выберите город вручную."), { timeout: 10000 });
    });
  });


  // ---------- Реалтайм: счётчики, тосты ----------
  const handlers = { message: [], match: [], like: [], read: [] };
  function setCount(name, n) {
    $$(`[data-count="${name}"]`).forEach((b) => { b.textContent = n; b.hidden = !n; });
  }
  async function refreshCounts() {
    try { const c = await api("/api/counts"); setCount("likes", c.Likes); setCount("unread", c.Unread); } catch (_) {}
  }
  // поток событий — только в кабинете (есть меню): гостю /api/events отвечает страницей входа, и EventSource падает
  if (csrf && window.EventSource && $(".nav")) {
    const es = new EventSource("/api/events");
    for (const ev of Object.keys(handlers)) {
      es.addEventListener(ev, (e) => {
        const data = JSON.parse(e.data);
        let handled = false;
        handlers[ev].forEach((h) => { if (h(data)) handled = true; });
        if (!handled) {
          if (ev === "message") toast(`<a href="/chat/${data.matchId}">${esc(data.from)}: ${esc(data.msg.body.slice(0, 60))}</a>`);
          if (ev === "match") toast(`<a href="/chat/${data.matchId}">Это взаимно с ${esc(data.name)}! Напишите первым</a>`, 6000);
          if (ev === "like") toast(`<a href="/likes">Кому-то понравилась ваша анкета</a>`);
        }
        refreshCounts();
      });
    }
  }

  // ---------- Просмотр фото (lightbox из макета dating2) ----------
  // Один <dialog id="photo-view"> на страницу: список снимков передают при открытии (галерея анкеты, мои фото).
  const LB = (() => {
    const dlg = $("#photo-view");
    if (!dlg) return null;
    const img = $(".lb-img", dlg), count = $("[data-lb-count]", dlg), prev = $(".lb-prev", dlg), next = $(".lb-next", dlg);
    let list = [], at = 0;
    function draw() {
      at = Math.max(0, Math.min(at, list.length - 1));
      img.src = list[at].src; img.alt = list[at].alt || "";
      count.textContent = `${at + 1} из ${list.length}`;
      prev.disabled = at === 0; next.disabled = at === list.length - 1;
      prev.hidden = next.hidden = list.length < 2;
    }
    const step = (d) => { at += d; draw(); };
    prev.addEventListener("click", () => step(-1));
    next.addEventListener("click", () => step(1));
    $(".lb-x", dlg).addEventListener("click", () => dlg.close());
    document.addEventListener("keydown", (e) => {
      if (!dlg.open) return;
      if (e.key === "ArrowLeft") step(-1);
      if (e.key === "ArrowRight") step(1);
    });
    let x0 = null;   // пролистывание пальцем
    img.addEventListener("dragstart", (e) => e.preventDefault());
    img.addEventListener("pointerdown", (e) => { x0 = e.clientX; e.preventDefault(); });
    img.addEventListener("pointerup", (e) => {
      if (x0 === null) return;
      const dx = e.clientX - x0; x0 = null;
      if (Math.abs(dx) > 50) step(dx > 0 ? -1 : 1);
    });
    return { open(items, i) { list = items; at = i || 0; draw(); if (!dlg.open) dlg.showModal(); } };
  })();

  // ---------- Галерея анкеты: миниатюра меняет крупный снимок, крупный открывает просмотр ----------
  $$("[data-gal]").forEach((gal) => {
    const main = $("[data-gal-main]", gal), at = $("[data-gal-at]", gal), thumbs = $$(".gal-thumb", gal);
    const shots = thumbs.length ? thumbs.map((t) => ({ src: $("img", t).src, alt: "" })) : [{ src: main.src, alt: main.alt }];
    let i = 0;
    function show(n) {
      i = n;
      const fig = main.parentElement;
      fig.classList.add("swapping");
      setTimeout(() => { main.src = shots[i].src; main.alt = `Фотография ${i + 1}`; fig.classList.remove("swapping"); }, 120);
      if (at) at.textContent = i + 1;
      thumbs.forEach((t, k) => { t.classList.toggle("on", k === i); t.setAttribute("aria-pressed", k === i ? "true" : "false"); });
    }
    thumbs.forEach((t, k) => t.addEventListener("click", () => show(k)));
    $(".gal-zoom", gal)?.addEventListener("click", () => LB && LB.open(shots, i));
  });

  // ---------- Мой профиль ----------
  // Мои фото крупно: тот же просмотр, список — все снимки сетки по порядку.
  const myPhotos = $("[data-photos]");
  if (myPhotos && LB) myPhotos.addEventListener("click", (e) => {
    const b = e.target.closest("[data-photo-i]");
    if (!b) return;
    const items = $$(".photo img", myPhotos).map((im) => ({ src: im.src, alt: im.alt }));
    LB.open(items, Number(b.dataset.photoI));
  });
  // Опасные формы спрашивают подтверждение (удаление фото): data-confirm="текст".
  document.addEventListener("submit", (e) => {
    const f = e.target.closest("form[data-confirm]");
    if (f && !confirm(f.dataset.confirm)) e.preventDefault();
  });
  // Чипсы выбора: плашка помечена .on вместе с галочкой внутри — не только цветом.
  $$(".chips-pick input[type=checkbox]").forEach((i) => i.addEventListener("change", () => i.closest(".pill")?.classList.toggle("on", i.checked)));
  // Окна правки анкеты (внутри формы #me-form): «Сохранить» отправляет форму целиком, а закрытие без сохранения
  // (Отмена, Esc, клик мимо) возвращает поля окна как были — иначе недоделанная правка уехала бы со следующим сохранением.
  const meForm = $("#me-form");
  if (meForm) {
    let sending = false;
    meForm.addEventListener("submit", () => (sending = true));
    $$("dialog", meForm).forEach((d) => {
      let snap = [];
      const fields = () => $$("input, select, textarea", d);
      new MutationObserver(() => {
        if (d.open) snap = fields().map((f) => (f.type === "radio" || f.type === "checkbox" ? f.checked : f.value));
      }).observe(d, { attributes: true, attributeFilter: ["open"] });
      d.addEventListener("close", () => {
        if (sending) return;
        fields().forEach((f, k) => { if (f.type === "radio" || f.type === "checkbox") f.checked = snap[k]; else f.value = snap[k]; });
      });
    });
  }

  // ---------- Сердце на плитке анкеты: симпатия без ухода со страницы (тот же /api/vote, что у колоды) ----------
  document.addEventListener("click", async (e) => {
    const b = e.target.closest("[data-tile-like]");
    if (!b || b.disabled) return;
    b.disabled = true;
    try {
      const res = await api("/api/vote", { target: Number(b.dataset.tileLike), liked: true });
      b.classList.add("liked"); b.setAttribute("aria-pressed", "true");
      toast(res && res.match ? `<a href="/chat/${res.matchId}">Это взаимно! Напишите первым</a>` : "Симпатия отправлена", res && res.match ? 6000 : 2500);
    } catch (_) { b.disabled = false; toast("Не получилось отправить симпатию."); }
  });

  // ---------- Встречи (разметка макета dating2: encounters.php) ----------
  // Сверху колоды — ПОСЛЕДНЯЯ карточка в DOM (.card:nth-last-child в стилях): queue[0] добавляется последней.
  // Перетаскивание ведёт верхнюю карточку, следующая подтягивается по --drag на колоде; порог — 90px.
  const enc = $("[data-encounters]");
  if (enc) {
    const deck = $(".deck", enc), tpl = $("#card-tpl"), dlg = $("[data-match-dialog]");
    const loading = $("[data-loading]", enc), empty = $("[data-empty]", enc), info = $("[data-info]", enc);
    const THRESHOLD = 90;
    let queue = [], busy = false, fetching = false, exhausted = false;
    const seen = new Set();

    function showShot(el, i) {
      const imgs = $$("[data-shots] img", el), bars = $$(".card-bars button", el);
      if (!imgs.length) return;
      i = Math.max(0, Math.min(i, imgs.length - 1));
      imgs.forEach((im, k) => (im.hidden = k !== i));
      bars.forEach((b, k) => b.classList.toggle("on", k === i));
      el.dataset.shot = i;
    }

    function build(c) {
      const el = tpl.content.firstElementChild.cloneNode(true);
      el.dataset.id = c.id;
      const ph = $("[data-shots]", el), bars = $(".card-bars", el);
      const shots = c.photos.length ? c.photos : ["/avatar/" + c.id + ".svg"];
      shots.forEach((src, k) => {
        const im = new Image(); im.src = src; im.alt = `${c.name}, фото ${k + 1}`; im.draggable = false; im.hidden = k > 0; ph.append(im);
        if (shots.length > 1) {
          const b = document.createElement("button");
          b.type = "button"; b.dataset.shot = k; b.setAttribute("aria-label", `Фото ${k + 1} из ${shots.length}`);
          if (!k) b.className = "on";
          bars.append(b);
        }
      });
      $(".card-name", el).textContent = `${c.name}, ${c.age}`;
      $("[data-online]", el).hidden = !c.online;
      $("[data-verified]", el).hidden = !c.verified;
      $("[data-meta]", el).textContent = [c.dist || c.city, c.dist ? c.city : "", c.height ? c.height + " см" : "", c.intentLabel ? "ищет: " + c.intentLabel.toLowerCase() : ""].filter(Boolean).join(" · ");
      $(".card-bio", el).textContent = c.bio || "";
      const chips = $(".chips", el);
      c.interests.slice(0, 3).forEach((t) => { const li = document.createElement("li"); li.className = "pill"; li.textContent = t; chips.append(li); });
      $("[data-open]", el).href = "/u/" + c.id;
      return el;
    }

    function render() {
      const visible = queue.slice(0, 3);
      $$(".card", deck).forEach((c) => { if (!c.classList.contains("gone") && !visible.some((q) => q.el === c)) c.remove(); });
      visible.slice().reverse().forEach((q) => deck.append(q.el));   // верхняя (queue[0]) — последней
      visible.forEach((q, i) => {
        if (i === 0) { q.el.removeAttribute("inert"); q.el.removeAttribute("aria-hidden"); }
        else { q.el.setAttribute("inert", ""); q.el.setAttribute("aria-hidden", "true"); }
      });
      loading.hidden = true;
      const none = queue.length === 0 && exhausted;
      empty.hidden = !none;
      $("[data-actions]", enc).hidden = none;
      if (queue[0]) info.href = "/u/" + queue[0].c.id;
      if (queue.length < 4 && !exhausted) load();
    }

    async function load() {
      if (fetching) return;
      fetching = true;
      try {
        const d = await api("/api/encounters");
        const fresh = d.cards.filter((c) => !seen.has(c.id));
        fresh.forEach((c) => { seen.add(c.id); queue.push({ c, el: build(c) }); });
        if (fresh.length === 0) exhausted = true;
      } catch (e) {
        toast("Не удалось загрузить анкеты. Проверьте соединение.");
        exhausted = true;
      } finally {
        fetching = false;
        render();
      }
    }

    async function decide(liked) {
      const top = queue[0];
      if (!top || busy) return;
      busy = true;
      const el = top.el;
      const stamp = $(liked ? ".stamp-like" : ".stamp-pass", el);
      stamp.classList.add("show");
      el.style.transform = "";
      el.classList.add("gone", liked ? "gone-r" : "gone-l");
      deck.style.setProperty("--drag", 0);
      queue.shift();
      render();
      setTimeout(() => { el.remove(); busy = false; }, 320);
      try {
        const res = await api("/api/vote", { target: top.c.id, liked });
        if (res.match) showMatch(res);
      } catch (_) { toast("Не получилось сохранить выбор."); }
    }

    function showMatch(m) {
      $("[data-match-other]", dlg).src = m.avatar;
      $("[data-match-name]", dlg).textContent = m.name;
      $("[data-match-chat]", dlg).href = "/chat/" + m.matchId;
      if (!dlg.open) dlg.showModal();
    }
    $("[data-match-close]", dlg).addEventListener("click", () => dlg.close());
    handlers.match.push((m) => { showMatch(m); return true; });

    async function undo() {
      if (enc.dataset.premium !== "true") {
        toast('Вернуть анкету можно с <a href="/premium">Premium</a>');
        return;
      }
      try {
        const d = await api("/api/undo", {});
        if (!d.card) { toast("Нечего возвращать"); return; }
        queue.unshift({ c: d.card, el: build(d.card) });
        exhausted = false;
        render();
      } catch (_) { toast("Не получилось вернуть анкету."); }
    }

    // полоски фото — настоящие кнопки
    deck.addEventListener("click", (e) => {
      const b = e.target.closest(".card-bars button");
      if (b) showShot(b.closest(".card"), +b.dataset.shot);
    });

    // ---- перетаскивание верхней карточки (на колоде, как в макете) ----
    let held = null, x0 = 0, y0 = 0, dx = 0;
    deck.addEventListener("dragstart", (e) => e.preventDefault());
    function stamps(el, kind, o) {
      $$(".stamp-like, .stamp-pass", el).forEach((n) => (n.style.opacity = ""));
      if (kind) $(kind === "like" ? ".stamp-like" : ".stamp-pass", el).style.opacity = o;
    }
    deck.addEventListener("pointerdown", (e) => {
      if (e.target.closest("button, a") || (e.button !== undefined && e.button !== 0) || busy) return;
      held = queue[0] ? queue[0].el : null;
      if (!held || !held.contains(e.target)) { held = null; return; }
      e.preventDefault();
      x0 = e.clientX; y0 = e.clientY; dx = 0;
      held.style.transition = "none";
      deck.classList.add("deck-drag");
      deck.setPointerCapture?.(e.pointerId);
    });
    deck.addEventListener("pointermove", (e) => {
      if (!held) return;
      dx = e.clientX - x0;
      const dy = (e.clientY - y0) * 0.25;
      held.style.transform = `translate(${dx}px, ${dy}px) rotate(${dx / 24}deg)`;
      const o = Math.min(Math.abs(dx) / THRESHOLD, 1);
      stamps(held, Math.abs(dx) > 12 ? (dx > 0 ? "like" : "pass") : null, o);
      deck.style.setProperty("--drag", o.toFixed(3));   // следующая подтягивается по мере ухода верхней
    });
    function drop(e) {
      if (!held) return;
      const el = held; held = null;
      deck.classList.remove("deck-drag");
      try { e && e.pointerId !== undefined && deck.releasePointerCapture(e.pointerId); } catch (_) {}
      el.style.transition = "";
      stamps(el, null, 0);
      if (Math.abs(dx) > THRESHOLD) { decide(dx > 0); }
      else {
        el.style.transform = "";               // пружиной обратно
        deck.style.setProperty("--drag", 0);
        // касание без ведения — следующее/предыдущее фото, а не промахнувшаяся симпатия
        if (Math.abs(dx) < 8 && e && e.target && !e.target.closest("button, a")) {
          const r = el.getBoundingClientRect();
          showShot(el, (+el.dataset.shot || 0) + (e.clientX < r.left + r.width / 2 ? -1 : 1));
        }
      }
      dx = 0;
    }
    deck.addEventListener("pointerup", drop);
    deck.addEventListener("pointercancel", drop);
    window.addEventListener("pointerup", drop);
    window.addEventListener("blur", () => held && drop());

    $("[data-like]", enc).addEventListener("click", () => decide(true));
    $("[data-pass]", enc).addEventListener("click", () => decide(false));
    $("[data-undo]", enc).addEventListener("click", undo);
    document.addEventListener("keydown", (e) => {
      if (dlg.open || e.target.closest("input, textarea, select")) return;
      if (e.key === "ArrowRight") decide(true);
      else if (e.key === "ArrowLeft") decide(false);
      else if (e.key === "Backspace") { e.preventDefault(); undo(); }
    });
    load();
  }

  // ---------- Чат ----------
  const chat = $("[data-chat]");
  if (chat) {
    const mid = Number(chat.dataset.chat);
    const log = $("[data-log]", chat), form = $("[data-chat-form]", chat);
    let lastId = 0, lastDay = "";
    const fmtDay = (d) => {
      const today = new Date(); const y = new Date(Date.now() - 864e5);
      if (d.toDateString() === today.toDateString()) return "Сегодня";
      if (d.toDateString() === y.toDateString()) return "Вчера";
      return d.toLocaleDateString("ru-RU", { day: "numeric", month: "long" });
    };
    function add(m, pending) {
      $("[data-chat-empty]", chat)?.remove();
      const d = new Date(m.at);
      const day = fmtDay(d);
      if (day !== lastDay) { const s = document.createElement("div"); s.className = "chat-day"; s.textContent = day; log.append(s); lastDay = day; }
      const b = document.createElement("div");
      b.className = "bubble " + (m.mine ? "bubble-out" : "bubble-in") + (pending ? " pending" : "");
      b.dataset.id = m.id || "";
      b.textContent = m.body;
      const t = document.createElement("span");
      t.className = "bubble-time";
      t.textContent = d.toLocaleTimeString("ru-RU", { hour: "2-digit", minute: "2-digit" }) + (m.mine ? (m.read ? " · прочитано" : " · отправлено") : "");
      b.append(t);
      log.append(b);
      if (m.id) lastId = Math.max(lastId, m.id);
      log.scrollTop = log.scrollHeight;
      return b;
    }
    JSON.parse($("#chat-data")?.textContent || "[]").forEach((m) => add(m));

    handlers.message.push((d) => {
      if (d.matchId !== mid) return false;
      if (d.msg.id > lastId) add({ ...d.msg, mine: false });
      api(`/api/chat/${mid}/read`, {}).catch(() => {});
      return true;
    });
    handlers.read.push((d) => {
      if (d.matchId !== mid) return false;
      $$(".bubble-out .bubble-time", log).forEach((t) => (t.textContent = t.textContent.replace("отправлено", "прочитано")));
      return true;
    });
    // догружаем пропущенное, если вкладка была неактивна
    document.addEventListener("visibilitychange", async () => {
      if (document.hidden) return;
      try {
        const list = await api(`/api/chat/${mid}/messages?after=${lastId}`);
        list.forEach((m) => add(m));
        if (list.some((m) => !m.mine)) api(`/api/chat/${mid}/read`, {}).catch(() => {});
      } catch (_) {}
    });

    if (form) {
      const ta = form.body;
      const grow = () => { ta.style.height = "auto"; ta.style.height = Math.min(160, ta.scrollHeight) + "px"; };
      ta.addEventListener("input", grow);
      ta.addEventListener("keydown", (e) => { if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); form.requestSubmit(); } });
      form.addEventListener("submit", async (e) => {
        e.preventDefault();
        const body = ta.value.trim();
        if (!body) return;
        ta.value = ""; grow();
        const el = add({ body, mine: true, at: new Date().toISOString() }, true);
        try {
          const m = await api(`/api/chat/${mid}/send`, { body });
          el.classList.remove("pending");
          el.dataset.id = m.id; lastId = Math.max(lastId, m.id);
        } catch (err) {
          el.remove();
          ta.value = body;
          toast(err.data?.error === "verify" ? 'Сначала <a href="/verify">подтвердите фото</a>' : "Не отправилось. Попробуйте ещё раз.");
        }
      });
      ta.focus();
    }
  }
})();
