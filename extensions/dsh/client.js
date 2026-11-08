// dsh-deja, browser half: the lines deja writes for the person.
//
// The host half hands the model its recall and keeps deja's own lines for the
// person — the per-prompt receipt, the build and week notes — on a
// board behind /api/deja-notes. This draws them in a small card just above the
// input, the way Claude Code prints a hook's systemMessage. Plain JS in dsh's
// module wrapper, so the package needs no build step: React comes from the
// shell's module table, colours and sizes from dsh's own theme variables, so
// light and dark both follow the app.
//
// Nothing here may break the page. A host without the route answers 404, a
// page without a session draws nothing, and any failure leaves the dock empty.
// Text is always inserted as text, and a link is only drawn for an http(s)
// address.
window.__ModuleLoader__.load({
  id: "dsh-deja",
  factory: (require) => {
    var module = { exports: {} };
    const React = require("react");
    const h = React.createElement;

    // The panels above the input — dsh's queue and todo docks sit here.
    const SLOT = "conversation.input.dock";
    // Lines shown above the footer before "more": the receipt and one note.
    // With the footer that is three lines at most.
    const VISIBLE = 2;

    // ---- what the lines say ----------------------------------------------

    // The card's header already names deja, so the name in front of each line
    // is dropped when it is drawn. The server's text is not changed.
    function stripName(line) {
      const rest = String(line).replace(/^deja(?:-vu)?(?:\s+—\s+|\s*:\s+|\s+)/i, "");
      if (rest === line || !rest) return line;
      return rest.charAt(0).toUpperCase() + rest.slice(1);
    }

    // safeHref accepts an http(s) address, with or without its scheme, and
    // nothing else: no javascript:, no data:, no relative path.
    function safeHref(raw) {
      let text = String(raw || "").trim();
      if (!text || /\s/.test(text)) return null;
      if (!/^[a-z][a-z0-9+.-]*:/i.test(text)) text = "https://" + text;
      let url;
      try {
        url = new URL(text);
      } catch {
        return null;
      }
      if (url.protocol !== "https:" && url.protocol !== "http:") return null;
      if (!url.hostname || !url.hostname.includes(".")) return null;
      return url.href;
    }

    // "deja-vu — from Codex CLI, 3d ago: "…" (via a, b)"; an older deja led
    // with "you have been here" and kept the date in the parentheses.
    const RECEIPT = /^deja-vu\s+—\s+([^"]+?):\s+"(.+)"(?:\s+\((.+)\))?$/;
    // "label: address · label: address", every part a link.
    const LINK_PART = /^([A-Za-z][\w ]{0,20}):\s+(\S+)$/;
    // A sentence that ends on its address: " — github.com/…".
    const TRAILING_LINK = /^(.*\S)\s+—\s+(\S+)$/;

    // parseNotes sorts the board's lines into what the card draws: the
    // receipt, plain notes, a row of links and a sentence that is a link.
    function parseNotes(lines) {
      const out = { receipt: null, notes: [], links: [], linked: [] };
      for (const raw of lines || []) {
        const line = String(raw || "").trim();
        if (!line) continue;
        const r = RECEIPT.exec(line);
        if (r) {
          // Only the brand goes; the lead says what the quote is.
          out.receipt = { lead: r[1].charAt(0).toUpperCase() + r[1].slice(1) + ":", topic: r[2], meta: (r[3] || "").replace(/\bvia:\s+/, "via ") };
          continue;
        }
        const parts = line.split(/\s+·\s+/);
        const links = parts.map((p) => {
          const m = LINK_PART.exec(p);
          const href = m && safeHref(m[2]);
          return href ? { label: m[1].charAt(0).toUpperCase() + m[1].slice(1), href } : null;
        });
        // Link parts at the end of a line ("… · docs: address") go to the
        // link row; a line of nothing but links is all link row.
        let keep = parts.length;
        while (keep > 0 && links[keep - 1]) keep--;
        out.links.push(...links.slice(keep));
        if (keep === 0) continue;
        const rest = keep < parts.length ? parts.slice(0, keep).join(" · ") : line;
        const t = TRAILING_LINK.exec(rest);
        const href = t && safeHref(t[2]);
        if (href) {
          out.linked.push({ text: t[1], href });
          continue;
        }
        out.notes.push(stripName(rest));
      }
      return out;
    }

    // ---- fetching ----------------------------------------------------------

    function notesUrl(sessionId) {
      // Relative, as dsh's own /api calls are: the page may be mounted below
      // the server root.
      return "api/deja-notes?session=" + encodeURIComponent(String(sessionId));
    }

    function pull(sessionId) {
      return fetch(notesUrl(sessionId), {
        headers: { accept: "application/json" },
        credentials: "same-origin",
        cache: "no-store",
      })
        .then((res) => (res.ok ? res.json() : null))
        .then((body) => (body && Array.isArray(body.notes) ? body.notes.filter((n) => typeof n === "string") : []))
        .catch(() => []);
    }

    // ---- drawing -----------------------------------------------------------

    // Styled with dsh's theme variables, as its own todo and queue docks are,
    // so the card follows light and dark. Fallbacks only matter on a host that
    // defines none of them.
    const STYLES = `
.deja-settings { padding: 12px 24px; max-width: 640px; color: var(--dsw-alias-label-primary, CanvasText); }
.deja-settings p, .deja-setting-hint { color: var(--dsw-alias-label-secondary, GrayText); line-height: 1.5; }
.deja-setting { display: flex; align-items: flex-start; gap: 12px; padding: 16px 0; cursor: pointer; }
.deja-setting input { margin-top: 4px; accent-color: var(--dsw-alias-state-business-primary, Highlight); }
.deja-setting-hint { display: block; font-size: 13px; margin-top: 4px; }
.deja-keybinding { flex-wrap: wrap; cursor: default; border-top: 1px solid var(--dsw-alias-border-l2, GrayText); }
.deja-keybinding > div { flex: 1 1 200px; }
.deja-keybinding > [role="status"] { flex-basis: 100%; }
.deja-keybinding button { font: inherit; padding: 6px 10px; cursor: pointer; }
.deja-endpoint { margin: 6px 0 12px; }
.deja-install-command { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; background: var(--dsw-alias-bg-module-platform, rgba(127,127,127,.12)); padding: 4px 8px; border-radius: 6px; }
.deja-sidebar-results { flex: 1; min-height: 0; overflow: auto; color: var(--dsw-alias-label-primary, CanvasText); }
.deja-sidebar-results[hidden], [data-slot="sidebar.workspaces"] [class*="_listArea"][hidden] { display: none !important; }
.deja-results { padding: 8px; font-size: 13px; }
.deja-results ul { list-style: none; padding: 0; margin: 0; }
.deja-search-label { font-size: 11px; color: var(--dsw-alias-label-secondary, GrayText); padding: 4px 6px 8px; }
.deja-results p { padding: 6px; line-height: 1.5; }
.deja-hit { display: block; width: 100%; text-align: start; background: transparent; color: inherit; border: 0; border-radius: 6px; padding: 10px 6px; cursor: pointer; font: inherit; }
.deja-hit:hover { background: var(--dsw-alias-interactive-bg-hover, ButtonFace); }
.deja-hit:focus-visible { outline: 2px solid var(--dsw-focus-ring-color, Highlight); outline-offset: -2px; }
.deja-hit strong, .deja-hit span { display: block; overflow-wrap: anywhere; white-space: pre-wrap; }
.deja-hit span { color: var(--dsw-alias-label-secondary, GrayText); margin-top: 4px; font-size: 12px; line-height: 1.5; }
.deja-card {
  box-sizing: border-box;
  width: calc(100% - 2 * var(--dsh-composer-side-clearance, 16px) - 4 * var(--dsh-composer-dock-inset, 8px));
  max-width: calc(var(--dsh-composer-card-max-width, 780px) - 4 * var(--dsh-composer-dock-inset, 8px));
  max-height: 40vh;
  overflow-y: auto;
  margin: 0 auto;
  padding: 6px 12px 8px;
  border-radius: var(--dsw-radius-lg, 10px);
  background: var(--dsw-specific-menu, Canvas);
  backdrop-filter: var(--dsw-menu-backdrop-filter, none);
  box-shadow: var(--dsw-elevation-panel, none);
  color: var(--dsw-alias-label-secondary, GrayText);
  font-size: 12px;
  line-height: 18px;
  overflow-wrap: anywhere;
  animation: deja-card-in 160ms ease-out;
}
@keyframes deja-card-in { from { opacity: 0; transform: translateY(4px); } to { opacity: 1; transform: none; } }
@media (prefers-reduced-motion: reduce) { .deja-card { animation: none; } }
.deja-head { display: flex; align-items: center; gap: 8px; height: 20px; }
.deja-name { flex: 1 1 auto; color: var(--dsw-alias-label-caption, GrayText); font-size: 11px; font-weight: 500; letter-spacing: .02em; }
.deja-close {
  flex: none; display: grid; place-items: center; width: 20px; height: 20px; margin-right: -6px;
  padding: 0; border: 0; border-radius: 999px; background: transparent; cursor: pointer;
  color: var(--dsw-alias-label-tertiary, GrayText); font: inherit; font-size: 14px; line-height: 1;
}
.deja-close:hover { background: var(--dsw-alias-interactive-bg-hover, transparent); }
.deja-close:focus-visible, .deja-more:focus-visible, .deja-card a:focus-visible {
  outline: var(--dsw-focus-ring-width, 2px) solid var(--dsw-focus-ring-color, var(--dsw-alias-state-business-primary, Highlight));
  outline-offset: 1px;
}
.deja-receipt { margin-top: 2px; font-size: 13px; line-height: 20px; }
.deja-topic { color: var(--dsw-alias-label-primary, CanvasText); }
.deja-lead { color: var(--dsw-alias-label-tertiary, GrayText); }
.deja-meta { color: var(--dsw-alias-label-tertiary, GrayText); font-size: 12px; margin-left: 8px; white-space: nowrap; }
.deja-note { color: var(--dsw-alias-label-secondary, GrayText); margin-top: 2px; }
.deja-detail { color: var(--dsw-alias-label-caption, GrayText); }
.deja-foot { display: flex; flex-wrap: wrap; align-items: baseline; gap: 2px 12px; margin-top: 4px; font-size: 12px; }
.deja-card a { color: var(--dsw-alias-link, var(--dsw-alias-state-business-primary, LinkText)); text-decoration: none; }
.deja-card a:hover { text-decoration: underline; }
.deja-linked a { color: var(--dsw-alias-label-tertiary, GrayText); }
.deja-more {
  padding: 0; border: 0; background: transparent; cursor: pointer; font: inherit;
  color: var(--dsw-alias-label-tertiary, GrayText); text-decoration: underline; text-underline-offset: 2px;
}
`;

    function installStyles() {
      const style = document.createElement("style");
      style.dataset.plugin = "dsh-deja";
      style.textContent = STYLES;
      document.head.appendChild(style);
      return () => style.remove();
    }

    function link(href, label, key) {
      return h("a", { key, href, target: "_blank", rel: "noopener noreferrer" }, label);
    }

    // A note reads as its claim and then its detail: the part after the first
    // " — " or " · " is the detail ("today: memory arrived 3 times"), drawn quieter.
    function noteRow(text, key) {
      const cut = /\s+(—|·)\s+/.exec(text);
      if (!cut) return h("div", { key, className: "deja-note" }, text);
      return h(
        "div",
        { key, className: "deja-note" },
        text.slice(0, cut.index),
        h("span", { className: "deja-detail" }, " " + text.slice(cut.index).trim()),
      );
    }

    function Card({ lines, onClose }) {
      const [open, setOpen] = React.useState(false);
      const parsed = parseNotes(lines);
      const body = [];
      if (parsed.receipt) {
        const r = parsed.receipt;
        body.push(
          h(
            "div",
            { key: "receipt", className: "deja-receipt" },
            h("span", { className: "deja-lead" }, r.lead + " "),
            h("span", { className: "deja-topic" }, "“" + r.topic + "”"),
            h("span", { className: "deja-meta" }, r.meta),
          ),
        );
      }
      parsed.notes.forEach((n, i) => body.push(noteRow(n, "n" + i)));
      // Links stay in view: the docs, the tracker and the one-time sentence
      // share one small footer row rather than queueing behind "more".
      const footer = [
        ...parsed.links.map((l, i) => link(l.href, l.label, "a" + i)),
        ...parsed.linked.map((l, i) => h("span", { key: "s" + i, className: "deja-linked" }, link(l.href, l.text))),
      ];
      if (body.length === 0 && footer.length === 0) return null;
      const hidden = open ? 0 : Math.max(0, body.length - VISIBLE);
      if (hidden > 0) {
        footer.unshift(h("button", { key: "more", type: "button", className: "deja-more", onClick: () => setOpen(true) }, hidden + " more"));
      }
      return h(
        "div",
        { className: "deja-card", "data-deja-notes": "", role: "status" },
        h(
          "div",
          { className: "deja-head" },
          h("a", { className: "deja-name", href: "https://github.com/vshulcz/deja-vu", target: "_blank", rel: "noopener noreferrer" }, "deja-vu"),
          h("button", { type: "button", className: "deja-close", title: "Dismiss", "aria-label": "Dismiss deja-vu notes", onClick: onClose }, "×"),
        ),
        hidden > 0 ? body.slice(0, VISIBLE) : body,
        footer.length > 0 ? h("div", { className: "deja-foot" }, footer) : null,
      );
    }

    function DejaNotes(props) {
      const sessionId = props && props.sessionId;
      const useSession = props && typeof props.useSession === "function" ? props.useSession : null;
      const running = useSession ? useSession((s) => Boolean(s && s.running)) : null;
      const [lines, setLines] = React.useState([]);

      // A new session or a new turn starts clean: a receipt answers the prompt
      // it came with, and left standing under the next one it would name the
      // wrong question. The session check stands on its own, because dsh may
      // keep this component and hand it another idle session.
      React.useEffect(() => {
        setLines([]);
      }, [sessionId]);
      React.useEffect(() => {
        if (running === true) setLines([]);
      }, [running]);

      React.useEffect(() => {
        if (!sessionId) return undefined;
        let alive = true;
        const ask = () =>
          pull(sessionId).then((notes) => {
            if (alive && notes.length > 0) setLines((held) => [...held, ...notes].slice(-8));
          });
        ask();
        // The recall runs while the turn's context is assembled, which may be
        // a moment after the page sees the turn start; the end of the turn
        // asks again. A host without useSession gets a slow poll instead.
        const timers = [];
        if (running === true) timers.push(setTimeout(ask, 800), setTimeout(ask, 2500));
        const poll = running === null ? setInterval(ask, 4000) : null;
        return () => {
          alive = false;
          timers.forEach(clearTimeout);
          if (poll) clearInterval(poll);
        };
      }, [sessionId, running]);

      if (lines.length === 0) return null;
      return h(Card, { lines, onClose: () => setLines([]) });
    }

    // Preferences belong to this browser/profile, like DSH's keybindings.
    const SEARCH_PREFS = "dsh.deja.search.v1";
    function preferences(storage) {
      let value = { conversation: true, sidebar: true, opensDeja: true };
      try {
        const saved = JSON.parse(storage.getItem(SEARCH_PREFS));
        for (const key of Object.keys(value)) if (typeof saved?.[key] === "boolean") value[key] = saved[key];
      } catch {}
      const listeners = new Set();
      // Ctrl+Alt+K opens DSH's sidebar search and switches it to Deja's
      // full-text mode via requestOpen(); native Ctrl+K is untouched.
      let pendingOpen = false;
      const openListeners = new Set();
      return {
        requestOpen() {
          if (!value.sidebar || !value.opensDeja) return;
          pendingOpen = true;
          for (const fn of openListeners) fn();
        },
        onOpen(fn) {
          openListeners.add(fn);
          if (pendingOpen) fn();
          return () => openListeners.delete(fn);
        },
        getSnapshot: () => value,
        subscribe(fn) { listeners.add(fn); return () => listeners.delete(fn); },
        set(key, enabled) {
          if (!(key in value)) return;
          value = { ...value, [key]: Boolean(enabled) };
          try { storage.setItem(SEARCH_PREFS, JSON.stringify(value)); } catch {}
          for (const fn of listeners) fn();
        },
        // When Deja owns the Ctrl+Alt+K gesture, the search it opens becomes
        // Deja's. The magnifier click and re-open interactions reset the
        // flag, so the native view stays reachable.
        claimOpen() {
          if (!value.opensDeja || !value.sidebar) return false;
          pendingOpen = true;
          return true;
        },
        releaseOpen() {
          const wasPending = pendingOpen;
          pendingOpen = false;
          return wasPending;
        },
      };
    }

    function usePreferences(prefs) {
      const [value, setValue] = React.useState(prefs.getSnapshot);
      React.useEffect(() => prefs.subscribe(() => setValue(prefs.getSnapshot())), [prefs]);
      return value;
    }

    // The sidebar mode and its shortcut query the host route the package's
    // index.js installs. `deja install dsh-auto` loads this same file as the
    // deja-notes card without that route; a 404 there means the search would
    // only ever fail, so the whole feature stands down instead of showing a
    // dead entry. The empty query is the route's own launch-nothing path, so
    // the probe never starts a deja search.
    const routeKey = Symbol.for("dsh-deja.search-route");
    function searchRoute() {
      if (window[routeKey]) return window[routeKey];
      let status = "unknown", started = false, decided = false, version = "";
      const listeners = new Set();
      const publish = (next, nextVersion) => {
        if (typeof nextVersion === "string") version = nextVersion;
        if (status === next) return;
        status = next;
        for (const fn of [...listeners]) fn();
      };
      const store = window[routeKey] = {
        getSnapshot: () => status,
        getVersion: () => version,
        subscribe(fn) { listeners.add(fn); return () => listeners.delete(fn); },
        // A host that knows its own state (a test, or a mount that shipped
        // with the route) can declare it; declaring stops the probe from
        // second-guessing it later.
        set(next, nextVersion) { decided = true; publish(next, nextVersion); return store; },
        probe(send = typeof fetch === "function" ? fetch : null) {
          if (started || decided) return store;
          started = true;
          if (!send) { publish("absent"); return store; }
          Promise.resolve()
            .then(() => send("api/deja-search?probe=1", {
              credentials: "same-origin", cache: "no-store", headers: { accept: "application/json" },
            }))
            .then(async (response) => {
              if (decided) return;
              // Fetch Response has `ok`; minimal test doubles only carry
              // `status`. Accept either, but never a 4xx/5xx or a failed
              // request as evidence of a working route.
              const ok = response && (response.ok === true ||
                (response.ok === undefined && typeof response.status === "number" &&
                  response.status >= 200 && response.status < 300));
              if (!ok) { publish("absent"); return; }
              // Only the route's own probe payload proves a working endpoint.
              // A 500, an SPA HTML fallback, or a proxy error must not enable
              // a sidebar toggle that has nothing behind it.
              try {
                const body = response.json ? await response.json() : null;
                if (decided) return;
                if (body && body.ok === true && body.plugin === "dsh-deja") {
                  publish("present", String(body.version || ""));
                } else {
                  publish("absent");
                }
              } catch { if (!decided) publish("absent"); }
            }, () => { if (!decided) publish("absent"); });
          return store;
        },
      };
      return store;
    }

    function useSearchRoute(route) {
      const [status, setStatus] = React.useState(route.getSnapshot);
      React.useEffect(() => {
        const update = () => setStatus(route.getSnapshot());
        const off = route.subscribe(update);
        // Read once on subscribe: a store handed in later may already answer.
        update();
        return off;
      }, [route]);
      return status;
    }

    // Ctrl+F writes a real `/deja` command, so the search runs when the
    // person sends it — a plain "deja search" sentence would only be a message
    // to the model. The draft keeps its reference chips by shifting every
    // occurrence past the prefix; the shape is DSH's own draft snapshot.
    const SESSION_COMMAND = "/deja";
    function bareSession(id) {
      const bare = String(id || "").replace(/^session-/, "");
      return /^[a-zA-Z0-9_-]+$/.test(bare) ? bare : "";
    }
    function sessionDraft(id, projection) {
      const bare = bareSession(id);
      if (!bare) return null;
      const prefix = SESSION_COMMAND + " --session " + bare + " -- ";
      const text = typeof projection?.clipboardText === "string" ? projection.clipboardText : "";
      const command = { token: SESSION_COMMAND };
      const occurrences = Array.isArray(projection?.occurrences) ? projection.occurrences : [];
      // A different command chip leads this draft; rewriting it would replace
      // the command the person typed, so leave their draft as it is.
      if (projection?.command && !text.startsWith(prefix)) return null;
      // Repeated Ctrl+F focuses the same draft instead of nesting prefixes.
      const shift = text.startsWith(prefix) ? 0 : prefix.length;
      return {
        text: shift ? prefix + text : text,
        command,
        references: occurrences.map((row) => ({
          source: String(row.source || ""),
          ref: String(row.ref || ""),
          label: String(row.label || ""),
          offset: Number(row.offset) + shift,
          length: Number(row.length),
          clipboardText: String(row.clipboardText || ""),
          ...(row.appearance ? { appearance: row.appearance } : {}),
          ...(row.invalid ? { invalid: true } : {}),
        })),
      };
    }

    function installFind(ctx, prefs) {
      return ctx.shortcuts.register({
        id: "deja.search-session",
        label: () => "Deja: search this conversation",
        aliases: ["deja", "find in conversation"],
        defaults: Object.fromEntries([
          ...["windows", "macos", "linux"].map((os) => ["desktop:" + os, { code: "KeyF", modifiers: ["primary"] }]),
          ...["windows", "macos", "linux"].map((os) => ["web:" + os, { code: "KeyF", modifiers: ["primary", "alt"] }]),
        ]),
        regions: ["page", "editable"],
        modals: [],
        resolve(context) {
          if (!prefs.getSnapshot().conversation || context.modal != null || context.region === "terminal") return { status: "pass" };
          const id = ctx.uiSession.current.getSnapshot().key;
          const binding = id && ctx.sessions.binding(id);
          if (!binding || !bareSession(id)) return { status: "pass" };
          return { status: "handled", run() {
            // Resolve again: navigation can change between key resolution and execution.
            const current = ctx.uiSession.current.getSnapshot().key;
            const active = current && ctx.sessions.binding(current);
            if (!active) return;
            const input = ctx.conversation.input.for(active.ctx);
            const draft = sessionDraft(current, input.projection);
            if (!draft) return;
            input.actions.setDraft(draft);
            input.actions.persistDraft();
            input.focus();
          } };
        },
      });
    }

    // All edits go through DSH so native menu bindings, conflict checks and
    // per-platform persistence stay in sync with the built-in shortcut editor.
    function bindingEditor(shortcuts, id, changed) {
      let alive = true, recording = false, busy = false, starting = false, candidate = null, revision;
      let generation = 0;
      let protection = Promise.resolve();
      const emit = (message = "") => { if (alive) changed({ recording, busy, starting, message }); };
      const stop = () => {
        generation++;
        recording = false;
        candidate = null;
        // Serialize stop after start, including disposal during bridge startup.
        protection = protection.catch(() => {}).then(() => shortcuts.recording(false));
        protection.catch(() => {});
      };
      const save = async (operation) => {
        if (!alive || busy) return;
        busy = true;
        stop();
        emit();
        try {
          await protection;
          if (!alive) return;
          const result = await shortcuts.edit(operation, revision);
          emit(result.status === "saved" ? "Saved" : "Save failed: " + result.status + ". Record again and retry.");
        } catch (error) { emit("Save failed: " + error.message); }
        finally { busy = false; if (alive) changed({ recording, busy, message: null }); }
      };
      return {
        async start() {
          if (!alive || busy || recording) return;
          if (shortcuts.config.getSnapshot().status !== "ready") { emit("Shortcut settings are not ready yet."); return; }
          revision = shortcuts.config.getSnapshot().revision;
          busy = true; starting = true;
          const attempt = ++generation;
          emit();
          protection = protection.catch(() => {}).then(() => shortcuts.recording(true));
          try { await protection; if (alive && attempt === generation) { recording = true; emit("Press a combination, then release to save; Esc cancels."); } }
          catch { stop(); emit("Could not enable recording protection. Try again."); }
          finally { busy = false; starting = false; if (alive) changed({ recording, busy, starting, message: null }); }
        },
        keydown(event) {
          if (!recording) return;
          event.preventDefault(); event.stopPropagation();
          if (event.code === "Escape") { stop(); emit("Cancelled"); return; }
          if (event.isComposing || event.key === "Dead" || event.getModifierState?.("AltGraph")) {
            stop(); emit("Cannot record during input-method composition or AltGraph. Try again."); return;
          }
          if (event.repeat || /^(Control|Alt|Shift|Meta)(Left|Right)$/.test(event.code)) return;
          if (event.code === "Tab" && !event.ctrlKey && !event.altKey && !event.metaKey) { stop(); emit("Cancelled"); return; }
          if (candidate) { candidate = null; stop(); emit("Record one combination at a time."); return; }
          try {
            const binding = { code: event.code, modifiers: [
              event.ctrlKey && "control", event.altKey && "alt", event.shiftKey && "shift", event.metaKey && "meta",
            ].filter(Boolean) };
            const description = shortcuts.describeBinding(binding);
            const conflicts = description.conflicts.filter((other) => other !== id);
            if (description.issue || conflicts.length) {
              const catalog = shortcuts.catalog.getSnapshot();
              const labels = conflicts.map((other) => catalog.find((row) => row.id === other)?.label || other);
              stop(); emit(conflicts.length ? "Shortcut conflict: " + labels.join(", ") : "Combination unavailable: " + description.issue);
              return;
            }
            candidate = description.binding;
            emit(description.keys.join(" ") + " — release to save");
          } catch { stop(); emit("This combination is not supported."); }
        },
        keyup(event) {
          if (!recording) return;
          event.preventDefault(); event.stopPropagation();
          const modifier = /^(Control|Alt|Shift|Meta)(Left|Right)$/.exec(event.code)?.[1]?.toLowerCase();
          if (candidate && (event.code === candidate.code || candidate.modifiers.includes(modifier))) {
            return save({ type: "set", id, binding: candidate });
          }
        },
        cancel() { if (recording || starting) { stop(); emit("Cancelled"); } },
        reset() {
          revision = shortcuts.config.getSnapshot().revision;
          return save({ type: "reset", id });
        },
        clear() {
          revision = shortcuts.config.getSnapshot().revision;
          return save({ type: "set", id, binding: null });
        },
        activate() { alive = true; },
        dispose() { alive = false; if (recording || busy) stop(); },
      };
    }

    function useShortcutStore(store) {
      const [value, setValue] = React.useState(() => store.getSnapshot());
      React.useEffect(() => {
        const update = () => setValue(store.getSnapshot());
        const dispose = store.subscribe(update);
        update();
        return dispose;
      }, [store]);
      return value;
    }

    function ShortcutBinding({ shortcuts, id, title }) {
      const catalog = useShortcutStore(shortcuts.catalog);
      const config = useShortcutStore(shortcuts.config);
      const [state, setState] = React.useState({ recording: false, busy: false, message: "" });
      const editor = React.useMemo(() => bindingEditor(shortcuts, id, (next) => setState((old) => ({
        ...next, message: next.message === null ? old.message : next.message,
      }))), [shortcuts, id]);
      React.useEffect(() => {
        editor.activate();
        const cancel = () => editor.cancel();
        window.addEventListener("blur", cancel);
        document.addEventListener("compositionstart", cancel, true);
        return () => {
          window.removeEventListener("blur", cancel);
          document.removeEventListener("compositionstart", cancel, true);
          editor.dispose();
        };
      }, [editor]);
      const recorder = React.useRef(null);
      React.useEffect(() => {
        if (!state.recording) return;
        recorder.current?.focus();
        const down = (event) => { if (document.activeElement === recorder.current) editor.keydown(event); };
        const up = (event) => { if (document.activeElement === recorder.current) editor.keyup(event); };
        document.addEventListener("keydown", down, true);
        document.addEventListener("keyup", up, true);
        return () => {
          document.removeEventListener("keydown", down, true);
          document.removeEventListener("keyup", up, true);
        };
      }, [editor, state.recording]);
      const row = catalog.find((item) => item.id === id);
      const disabled = !row || config.status !== "ready" || state.busy;
      return h("div", { className: "deja-setting deja-keybinding" },
        h("div", null, h("strong", null, title),
          h("div", { className: "deja-setting-hint" }, row?.keys?.join(" ") || "Not bound")),
        h("button", { type: "button", ref: recorder, disabled: !row || config.status !== "ready" || (state.busy && !state.starting), "aria-label": "Change the " + title + " shortcut",
          onClick: () => editor.start(), onBlur: () => editor.cancel(),
        }, state.recording ? "Press a combination…" : "Change shortcut"),
        h("button", { type: "button", disabled: disabled || state.recording, onClick: () => editor.reset() }, "Restore default"),
        h("button", { type: "button", disabled: disabled || state.recording, onClick: () => editor.clear() }, "Remove"),
        h("span", { role: "status", className: "deja-setting-hint" }, state.message),
      );
    }

    // What an install needs to say, and how the page offers it: the sidebar
    // mode cannot work without the package's route, so the settings page names
    // the endpoint and its version, and hands over the command when it is
    // missing instead of leaving a dead switch.
    const INSTALL_COMMAND = "dsh plugin --profile web add dsh-deja";

    async function copyText(text) {
      try {
        if (navigator?.clipboard?.writeText) { await navigator.clipboard.writeText(text); return true; }
      } catch {}
      try {
        const area = document.createElement("textarea");
        area.value = text;
        area.setAttribute("readonly", "");
        area.style.position = "fixed";
        area.style.opacity = "0";
        document.body.appendChild(area);
        area.select();
        const copied = document.execCommand("copy");
        area.remove();
        return copied;
      } catch { return false; }
    }

    function useRouteVersion(route) {
      const [version, setVersion] = React.useState(() => route.getVersion ? route.getVersion() : "");
      React.useEffect(() => {
        const update = () => setVersion(route.getVersion ? route.getVersion() : "");
        const off = route.subscribe(update);
        update();
        return off;
      }, [route]);
      return version;
    }

    function EndpointStatus({ route }) {
      const status = useSearchRoute(route);
      const version = useRouteVersion(route);
      const [copied, setCopied] = React.useState(false);
      return h("div", null,
        h("h3", null, "Search endpoint"),
        h("p", { className: "deja-endpoint", role: "status" },
          status === "unknown"
            ? "Checking /api/deja-search …"
            : status === "present"
              ? "dsh-deja " + (version || "(unknown version)") + " is serving the full-text search endpoint."
              : "No /api/deja-search endpoint was found. Sidebar full-text search and the cross-session shortcut need the dsh-deja package."),
        status === "absent"
          ? h("div", { className: "deja-setting deja-keybinding" },
            h("code", { className: "deja-install-command" }, INSTALL_COMMAND),
            h("button", { type: "button", onClick: async () => setCopied(await copyText(INSTALL_COMMAND)) },
              copied ? "Copied" : "Copy install command"),
            h("span", { className: "deja-setting-hint" }, "Replace web with your profile name (for example desktop), then reload this page after installing."),
          )
          : null,
      );
    }

    function Settings({ prefs, shortcuts, route }) {
      const value = usePreferences(prefs);
      // Only a confirmed, working route may offer sidebar features. "unknown"
      // is the probe in flight, so it is not usable either — this matches
      // Sidebar's `status === "present"` gate and avoids showing switches that
      // do nothing for a moment. The composer draft needs no route and stays.
      const status = useSearchRoute(route || searchRoute());
      const sidebarUsable = status === "present";
      return h("section", { className: "deja-settings", "aria-label": "Deja" },
        h("h2", null, "Deja"),
        h("p", null, "Search your conversation history with the local Deja index."),
        ...[
          ["conversation", "Search this conversation", "Fill the message input with a search command for the current session. Existing draft text becomes the query; nothing is sent automatically. Customize the key below."],
          ...(sidebarUsable ? [
            ["sidebar", "Deja search in the sidebar", "Open sidebar search, then click its magnifying glass again to search DSH conversation text with Deja. Click again to return to the regular results."],
            ["opensDeja", "Deja cross-session shortcut", "Enable the shortcut that opens Deja full-text search for DSH conversations. Customize its key below. The magnifying glass returns the native search."],
          ] : []),
        ].map(([key, title, hint]) => h("label", { key, className: "deja-setting" },
          h("input", { type: "checkbox", role: "switch", checked: value[key], onChange: (event) => prefs.set(key, event.target.checked) }),
          h("span", null, h("strong", null, title), h("span", { className: "deja-setting-hint" }, hint)),
        )),
        h(EndpointStatus, { route: route || searchRoute() }),
        h("h3", null, "Customize shortcuts"),
        h("p", { className: "deja-setting-hint" }, "Click Change shortcut, press the combination you want, then release to save. These stay in sync with DSH's built-in shortcut editor."),
        shortcuts?.edit && shortcuts?.recording && shortcuts?.catalog && shortcuts?.config
          ? h(React.Fragment, null,
            h(ShortcutBinding, { shortcuts, id: "deja.search-session", title: "Search this conversation" }),
            ...(sidebarUsable ? [
              h(ShortcutBinding, { key: "deja.search-sessions", shortcuts, id: "deja.search-sessions", title: "Deja cross-session search" }),
              h(ShortcutBinding, { key: "session.search", shortcuts, id: "session.search", title: "Open sidebar search" }),
            ] : []),
          )
          : h("p", null, "This DSH version has no shortcut editing service. Upgrade DSH to change these bindings."),
      );
    }

    // DSH exposes an addressable slot anchor but no sidebar-search provider
    // extension. Keep this adapter inside that anchor, use semantic controls,
    // and stand down if its shape is unavailable. Never replace slot owners or
    // reach into React internals. Removing the adapter restores native content.
    function attachSidebar(anchor, prefs, changed) {
      const input = anchor.querySelector('input[type="text"]');
      const button = input?.parentElement?.querySelector('button[aria-expanded]');
      const list = anchor.querySelector('[class*="_listArea"]');
      if (!input || !button || !list) return null;
      const target = document.createElement("div");
      target.className = "deja-sidebar-results";
      target.dataset.dejaResults = "";
      target.hidden = true;
      list.parentElement.appendChild(target);
      const old = { hidden: list.hidden, title: button.getAttribute("title"), pressed: button.getAttribute("aria-pressed") };
      let active = false;
      const publish = () => changed({ active, query: input.value, target });
      const setActive = (next) => {
        active = next;
        list.hidden = active || old.hidden;
        target.hidden = !active;
        button.setAttribute("aria-pressed", String(active));
        button.setAttribute("title", active ? "Deja search — click for regular search" : "Search conversation text with Deja");
        publish();
      };
      // The native search is reachable whenever its list is the one showing.
      publish();
      const click = (event) => {
        if (button.getAttribute("aria-expanded") !== "true") return;
        event.preventDefault();
        event.stopPropagation();
        // The magnifier is the escape hatch back to DSH's title search.
        prefs.releaseOpen();
        setActive(!active);
        input.focus();
      };
      const update = () => { if (active) publish(); };
      const sync = () => { if (active && button.getAttribute("aria-expanded") !== "true") setActive(false); };
      // A search opened by the Ctrl+Alt+K gesture becomes Deja's when the
      // switch is on: that expansion lands in full-text mode. Native Ctrl+K
      // still opens DSH's regular title search. The sidebar's own re-render
      // races the shortcut, so poll briefly and give up cleanly — an
      // unclaimed opening keeps the native search.
      const claimOpen = () => {
        if (!prefs.claimOpen()) return false;
        let attempts = 0;
        const settle = () => {
          if (stopped) { prefs.releaseOpen(); return false; }
          if (button.getAttribute("aria-expanded") === "true") {
            prefs.releaseOpen();
            setActive(true);
            input.focus();
            return true;
          }
          if (++attempts >= 40) { prefs.releaseOpen(); return false; }
          settleTimer = setTimeout(settle, 10);
          return null;
        };
        return settle();
      };
      const observer = new MutationObserver(sync);
      let stopped = false;
      let settleTimer;
      const removeOpen = prefs.onOpen(() => {
        if (button.getAttribute("aria-expanded") !== "true") button.click();
        claimOpen();
      });
      button.addEventListener("click", click, true);
      input.addEventListener("input", update);
      observer.observe(button, { attributes: true, attributeFilter: ["aria-expanded"] });
      return {
        input, button, list, target, claimOpen,
        dispose() {
          stopped = true;
          clearTimeout(settleTimer);
          removeOpen();
          observer.disconnect();
          button.removeEventListener("click", click, true);
          input.removeEventListener("input", update);
          list.hidden = old.hidden;
          for (const [key, value] of [["title", old.title], ["aria-pressed", old.pressed]]) {
            if (value === null) button.removeAttribute(key); else button.setAttribute(key, value);
          }
          target.remove();
        },
      };
    }

    function observeSidebar(prefs, changed, root = document) {
      let attachment = null;
      let stopped = false;
      const scan = () => {
        if (stopped) return;
        const anchor = root.querySelector('[data-slot="sidebar.workspaces"]');
        if (attachment && (!anchor?.contains(attachment.input) || !anchor.contains(attachment.button) || !anchor.contains(attachment.list))) {
          attachment.dispose();
          attachment = null;
          changed(null);
        }
        if (!attachment && anchor) attachment = attachSidebar(anchor, prefs, changed);
      };
      const observer = new MutationObserver(scan);
      observer.observe(root.body || root, { childList: true, subtree: true });
      scan();
      return () => {
        stopped = true;
        observer.disconnect();
        attachment?.dispose();
        changed(null);
      };
    }

    async function searchHistory(query, signal) {
      const response = await fetch("api/deja-search?q=" + encodeURIComponent(query), {
        credentials: "same-origin", cache: "no-store", headers: { accept: "application/json" }, signal,
      });
      let body;
      try { body = await response.json(); } catch { throw new Error("Deja search is unavailable. Update the dsh-deja host plugin."); }
      if (!response.ok || body.error) throw new Error(body.error?.message || "Deja search is unavailable.");
      if (!Array.isArray(body.hits)) throw new Error("Deja returned invalid search data.");
      return body;
    }

    function Results({ query, open }) {
      const [result, setResult] = React.useState({ query: "", status: "idle", hits: [] });
      const [openError, setOpenError] = React.useState("");
      React.useEffect(() => {
        setOpenError("");
        if (!query.trim()) { setResult({ query, status: "idle", hits: [] }); return undefined; }
        const controller = new AbortController();
        let alive = true;
        setResult({ query, status: "loading", hits: [] });
        const timer = setTimeout(() => {
          searchHistory(query, controller.signal).then(
            (body) => { if (alive) setResult({ ...body, query, status: "ready" }); },
            (error) => { if (alive) setResult({ query, status: "error", hits: [], message: error.message }); },
          );
        }, 250);
        return () => { alive = false; clearTimeout(timer); controller.abort(); };
      }, [query]);
      // Do not show the old query's hits while the next effect is being committed.
      const current = result.query === query ? result : { status: "loading", hits: [] };
      return h("section", { "aria-label": "Deja search results", className: "deja-results" },
        h("div", { className: "deja-search-label" }, "Deja · DSH conversations"),
        current.status === "idle" ? h("p", { role: "status" }, "Enter a keyword to search conversation text.") : null,
        current.status === "loading" ? h("p", { role: "status" }, "Searching Deja…") : null,
        current.status === "error" ? h("p", { role: "alert" }, current.message) : null,
        current.status === "ready" && current.hits.length === 0 ? h("p", { role: "status" }, "No matching conversations.") : null,
        openError ? h("p", { role: "alert" }, openError) : null,
        h("ul", null, ...current.hits.map((hit) => h("li", { key: hit.sessionId },
          h("button", { type: "button", className: "deja-hit", onClick: async () => {
            try { setOpenError(""); await open(hit.sessionId); }
            catch { setOpenError("This conversation could not be opened. It may have been archived or removed from DSH."); }
          } },
            h("strong", null, hit.title || hit.sessionId),
            ...(hit.snippets || []).map((snippet, i) => h("span", { key: i }, snippet)),
          ),
        ))),
        current.hasMore ? h("p", { role: "status" }, "Showing the first 20 matches. Narrow your query for more precise results.") : null,
      );
    }

    function Sidebar({ prefs, open, portal, route = searchRoute() }) {
      const settings = usePreferences(prefs);
      const status = useSearchRoute(route);
      const [view, setView] = React.useState(null);
      const usable = status === "present";
      React.useEffect(() => settings.sidebar && usable ? observeSidebar(prefs, setView) : undefined, [settings.sidebar, usable]);
      return settings.sidebar && usable && view?.active ? portal(h(Results, { query: view.query, open }), view.target) : null;
    }

    function installSearch(ctx) {
      // `deja install dsh-auto` can load this same browser file as deja-notes
      // alongside the npm package. One owner prevents two click listeners from
      // toggling on and straight back off, and duplicate shortcut registrations.
      const ownerKey = Symbol.for("dsh-deja.search-owner");
      if (typeof ctx.inject === "function") {
        if (window[ownerKey]) return;
        const owner = {};
        window[ownerKey] = owner;
        if (typeof ctx.effect === "function") ctx.effect(() => () => {
          if (window[ownerKey] === owner) delete window[ownerKey];
        }, "dsh-deja: search owner");
      }
      let storage;
      try { storage = window.localStorage; } catch {}
      const prefs = preferences(storage);
      const route = searchRoute();
      route.probe();
      let shortcutService;
      if (typeof ctx.inject === "function") ctx.inject(["shortcuts"], (c) => { shortcutService = c.shortcuts; });
      try {
        ctx.slots.inject("settings.section", () => ctx.slots.register({
          name: "settings.section", id: "deja", order: 650, label: () => "Deja",
        }, () => h(Settings, { prefs, shortcuts: shortcutService, route })));
      } catch {}
      if (typeof ctx.inject !== "function") return;
      // Ctrl+Alt+K is Deja's own gesture: it opens the sidebar search and
      // switches that expansion to Deja full-text mode. DSH's native Ctrl+K
      // (session.search) keeps its title search. The magnifier click is the
      // way back to the title search for a Deja-owned opening.
      ctx.inject(["shortcuts", "uiSession", "sessions", "conversation"], (c) => {
        try { c.effect(() => installFind(c, prefs), "dsh-deja: find in conversation"); } catch {}
      });
      ctx.inject(["uiWorkspace"], (c) => {
        let portal;
        try { portal = require("react-dom").createPortal; } catch { return; }
        if (typeof portal !== "function") return;
        c.slots.inject("shell.overlay", () => c.slots.register({
          name: "shell.overlay", id: "deja-search", order: 950,
        }, () => h(Sidebar, { prefs, portal, route, open: (id) => c.uiWorkspace.openSession(id) })));
      });
      // The direct binding is Ctrl+Alt+K (with Shift in a browser). It only
      // fires when both switches are on, and claims the sidebar search it
      // opens. An unproven route is a pass: the key stays DSH's until the
      // probe confirms the search this shortcut opens can actually answer.
      const takeoverEnabled = () => route.getSnapshot() === "present"
        && prefs.getSnapshot().sidebar && prefs.getSnapshot().opensDeja;
      ctx.inject(["shortcuts"], (c) => {
        try {
          c.effect(() => c.shortcuts.register({
            id: "deja.search-sessions",
            label: () => "Deja: search DSH conversations",
            aliases: ["deja search", "search conversations"],
            // DSH's native session.search owns primary+K, so Deja uses its
            // own primary+alt+K binding (plus Shift in a browser). The native
            // Ctrl+K title search is left alone.
            defaults: Object.fromEntries([
              ...["windows", "macos", "linux"].map((os) => ["desktop:" + os, { code: "KeyK", modifiers: ["primary", "alt"] }]),
              ...["windows", "macos", "linux"].map((os) => ["web:" + os, { code: "KeyK", modifiers: ["primary", "alt", "shift"] }]),
            ]),
            regions: ["page"],
            modals: [],
            resolve(context) {
              if (context.modal != null || !takeoverEnabled()) return { status: "pass" };
              return { status: "handled", run() {
                prefs.requestOpen();
              } };
            },
          }), "dsh-deja: open sidebar search");
        } catch {}
      });
    }

    function apply(ctx) {
      installSearch(ctx);
      try {
        if (typeof ctx.effect === "function") ctx.effect(installStyles, "dsh-deja: styles");
        else installStyles();
      } catch {}
      try {
        ctx.slots.inject(SLOT, () => {
          try {
            return ctx.slots.register({ name: SLOT, id: "deja-notes", order: 900 }, DejaNotes);
          } catch {
            return () => {};
          }
        });
      } catch {}
    }

    module.exports.inject = ["slots"];
    module.exports.apply = apply;
    // For the tests; the host reads only inject and apply.
    module.exports.parseNotes = parseNotes;
    module.exports.stripName = stripName;
    module.exports.safeHref = safeHref;
    module.exports.preferences = preferences;
    module.exports.sessionDraft = sessionDraft;
    module.exports.bareSession = bareSession;
    module.exports.installFind = installFind;
    module.exports.installSearch = installSearch;
    module.exports.attachSidebar = attachSidebar;
    module.exports.observeSidebar = observeSidebar;
    module.exports.searchHistory = searchHistory;
    module.exports.searchRoute = searchRoute;
    module.exports.bindingEditor = bindingEditor;
    module.exports.ShortcutBinding = ShortcutBinding;
    module.exports.Settings = Settings;
    module.exports.Sidebar = Sidebar;
    module.exports.Results = Results;
    return module.exports;
  },
});
