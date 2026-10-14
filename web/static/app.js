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

  // Галерея профиля
  $$("[data-gallery]").forEach((g) => {
    const imgs = $$("img", g), bars = $$(".card-bars i", g);
    let i = 0;
    const show = (n) => {
      i = (n + imgs.length) % imgs.length;
      imgs.forEach((im, k) => (im.hidden = k !== i));
      bars.forEach((b, k) => b.classList.toggle("on", k === i));
    };
    $(".gal-prev", g)?.addEventListener("click", () => show(i - 1));
    $(".gal-next", g)?.addEventListener("click", () => show(i + 1));
  });

  // ---------- Реалтайм: счётчики, тосты ----------
  const handlers = { message: [], match: [], like: [], read: [] };
  function setCount(name, n) {
    $$(`[data-count="${name}"]`).forEach((b) => { b.textContent = n; b.hidden = !n; });
  }
  async function refreshCounts() {
    try { const c = await api("/api/counts"); setCount("likes", c.Likes); setCount("unread", c.Unread); } catch (_) {}
  }
  if (csrf && window.EventSource) {
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

  // ---------- Встречи ----------
  const enc = $("[data-encounters]");
  if (enc) {
    const deck = $(".deck", enc), tpl = $("#card-tpl"), dlg = $("[data-match-dialog]");
    const loading = $("[data-loading]", enc), empty = $("[data-empty]", enc);
    let queue = [], busy = false, fetching = false, exhausted = false;
    const seen = new Set();

    function build(c) {
      const el = tpl.content.firstElementChild.cloneNode(true);
      el.dataset.id = c.id;
      const ph = $(".card-photos", el), bars = $(".card-bars", el);
      c.photos.forEach((src, k) => {
        const im = new Image(); im.src = src; im.alt = `${c.name}, фото ${k + 1}`; im.draggable = false; im.hidden = k > 0; ph.append(im);
        if (c.photos.length > 1) { const b = document.createElement("i"); if (!k) b.className = "on"; bars.append(b); }
      });
      $(".card-name", el).textContent = `${c.name}, ${c.age}`;
      $(".card-verified", el).hidden = !c.verified;
      $(".card-meta", el).textContent = [c.online ? "● онлайн" : "", c.city, c.dist, c.height ? c.height + " см" : ""].filter(Boolean).join(" · ");
      $(".card-bio", el).textContent = c.bio || c.intent || "";
      const chips = $(".card-chips", el);
      c.interests.slice(0, 4).forEach((t) => { const li = document.createElement("li"); li.textContent = t; chips.append(li); });
      $(".card-info", el).href = "/u/" + c.id;
      // тап по левой/правой части фото листает фото
      let pi = 0;
      ph.parentElement.addEventListener("click", (e) => {
        if (e.target.closest(".card-info") || el.dataset.moved === "1" || c.photos.length < 2) return;
        const r = el.getBoundingClientRect();
        pi = (pi + (e.clientX - r.left < r.width / 2 ? -1 : 1) + c.photos.length) % c.photos.length;
        $$("img", ph).forEach((im, k) => (im.hidden = k !== pi));
        $$("i", bars).forEach((b, k) => b.classList.toggle("on", k === pi));
      });
      drag(el);
      return el;
    }

    function render() {
      const visible = queue.slice(0, 3);
      $$(".card", deck).forEach((c) => { if (!visible.some((q) => q.el === c)) c.remove(); });
      // верхняя карточка (queue[0]) — последняя в DOM
      visible.slice().reverse().forEach((q) => deck.append(q.el));
      visible.forEach((q, i) => q.el.classList.toggle("top", i === 0));
      loading.hidden = true;
      const none = queue.length === 0 && exhausted;
      empty.hidden = !none;
      deck.style.aspectRatio = none ? "auto" : "";
      $(".deck-actions", enc).hidden = none;
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
      el.style.transform = `translate(${liked ? 140 : -140}%, 0) rotate(${liked ? 24 : -24}deg)`;
      el.style.opacity = "0";
      queue.shift();
      setTimeout(() => { el.remove(); el.style.transform = ""; el.style.opacity = ""; render(); busy = false; }, 280);
      try {
        const res = await api("/api/vote", { target: top.c.id, liked });
        if (res.match) showMatch(res);
      } catch (_) { toast("Не получилось сохранить выбор."); }
    }

    function showMatch(m) {
      $("[data-match-other]", dlg).src = m.avatar;
      $("[data-match-name]", dlg).textContent = m.name;
      $("[data-match-chat]", dlg).href = "/chat/" + m.matchId;
      dlg.showModal();
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

    function drag(el) {
      let x0 = 0, y0 = 0, dx = 0, active = false;
      const like = $(".stamp-like", el), pass = $(".stamp-pass", el);
      el.addEventListener("pointerdown", (e) => {
        if (!el.classList.contains("top") || e.button !== 0) return;
        active = true; x0 = e.clientX; y0 = e.clientY; dx = 0; el.dataset.moved = "0";
        el.setPointerCapture(e.pointerId); el.classList.add("dragging");
      });
      el.addEventListener("pointermove", (e) => {
        if (!active) return;
        dx = e.clientX - x0;
        const dy = e.clientY - y0;
        if (Math.abs(dx) > 6) el.dataset.moved = "1";
        el.style.transform = `translate(${dx}px, ${dy * 0.2}px) rotate(${dx / 18}deg)`;
        like.style.opacity = Math.max(0, Math.min(1, dx / 100));
        pass.style.opacity = Math.max(0, Math.min(1, -dx / 100));
      });
      const end = () => {
        if (!active) return;
        active = false; el.classList.remove("dragging");
        like.style.opacity = pass.style.opacity = 0;
        if (Math.abs(dx) > 110) decide(dx > 0);
        else el.style.transform = "";
        setTimeout(() => (el.dataset.moved = "0"), 50);
      };
      el.addEventListener("pointerup", end);
      el.addEventListener("pointercancel", end);
    }

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
      if (day !== lastDay) { const s = document.createElement("div"); s.className = "day"; s.textContent = day; log.append(s); lastDay = day; }
      const b = document.createElement("div");
      b.className = "bubble" + (m.mine ? " mine" : "") + (pending ? " pending" : "");
      b.dataset.id = m.id || "";
      b.textContent = m.body;
      const t = document.createElement("time");
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
      $$(".bubble.mine time", log).forEach((t) => (t.textContent = t.textContent.replace("отправлено", "прочитано")));
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
