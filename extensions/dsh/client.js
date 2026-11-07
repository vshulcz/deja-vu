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
      // Ctrl+K opens DSH's sidebar search. With the switch on, the Deja
      // adapter switches that search straight to full-text results, so the
      // official title search becomes the second magnifier click.
      let pendingOpen = false;
      return {
        getSnapshot: () => value,
        subscribe(fn) { listeners.add(fn); return () => listeners.delete(fn); },
        set(key, enabled) {
          if (!(key in value)) return;
          value = { ...value, [key]: Boolean(enabled) };
          try { storage.setItem(SEARCH_PREFS, JSON.stringify(value)); } catch {}
          for (const fn of listeners) fn();
        },
        // When Deja owns the open gesture, the first opened search becomes
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

    function sessionCommand(id, draft = "") {
      const bare = String(id || "").replace(/^session-/, "");
      if (!/^[a-zA-Z0-9_-]+$/.test(bare)) return null;
      const prefix = "deja search --session " + bare + " ";
      // Repeated Ctrl+F focuses the same draft, without nesting commands.
      return draft.startsWith(prefix) ? draft : prefix + draft;
    }

    function installFind(ctx, prefs) {
      return ctx.shortcuts.register({
        id: "deja.search-session",
        label: () => "Deja: search this conversation",
        aliases: ["deja", "find in conversation"],
        // group places the command in the built-in shortcut editor's
        // "application" section, where users can rebind or clear it.
        group: "application",
        defaults: Object.fromEntries([
          ...["windows", "macos", "linux"].map((os) => ["desktop:" + os, { code: "KeyF", modifiers: ["primary"] }]),
          ...["windows", "macos"].map((os) => ["web:" + os, { code: "KeyF", modifiers: ["primary", "alt"] }]),
        ]),
        regions: ["page", "editable"],
        modals: [],
        resolve(context) {
          if (!prefs.getSnapshot().conversation || context.modal != null || context.region === "terminal") return { status: "pass" };
          const id = ctx.uiSession.current.getSnapshot().key;
          const binding = id && ctx.sessions.binding(id);
          if (!binding || !sessionCommand(id)) return { status: "pass" };
          return { status: "handled", run() {
            // Resolve again: navigation can change between key resolution and execution.
            const current = ctx.uiSession.current.getSnapshot().key;
            const active = current && ctx.sessions.binding(current);
            if (!active) return;
            const input = ctx.conversation.input.for(active.ctx);
            const draft = sessionCommand(current, input.projection.clipboardText || "");
            if (!draft) return;
            input.actions.setDraft(draft);
            input.actions.persistDraft();
            input.focus();
          } };
        },
      });
    }

    function Settings({ prefs }) {
      const value = usePreferences(prefs);
      return h("section", { className: "deja-settings", "aria-label": "Deja" },
        h("h2", null, "Deja"),
        h("p", null, "Search your conversation history with the local Deja index."),
        ...[
          ["conversation", "Ctrl+F in conversations", "Fill the message input with a search command for the current session. In a browser, use Ctrl+Alt+F (Option+Command+F on macOS). Existing draft text becomes the query; nothing is sent automatically."],
          ["sidebar", "Deja search in the sidebar", "Open sidebar search, then click its magnifying glass again to search DSH conversation text with Deja. Click again to return to the regular results."],
          ["opensDeja", "Ctrl+K opens Deja full-text search", "Take over the Ctrl+K sidebar search: the opened search starts in Deja full-text mode instead of the title search. The magnifying glass returns the native search. Also binds Ctrl+Alt+K (Ctrl+Option+Shift+K in a browser) as a direct Deja search shortcut."],
        ].map(([key, title, hint]) => h("label", { key, className: "deja-setting" },
          h("input", { type: "checkbox", role: "switch", checked: value[key], onChange: (event) => prefs.set(key, event.target.checked) }),
          h("span", null, h("strong", null, title), h("span", { className: "deja-setting-hint" }, hint)),
        )),
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
      const update = (event) => {
        if (event && event.preventDefault) { event.preventDefault(); event.stopPropagation(); }
        if (active) publish();
      };
      const sync = () => { if (active && button.getAttribute("aria-expanded") !== "true") setActive(false); };
      // A search opened from the keyboard (Ctrl+K) becomes Deja's when the
      // switch is on: the opening expansion lands in full-text mode. The
      // sidebar's own re-render races the shortcut, so poll briefly and give
      // up cleanly — an unclaimed opening keeps the native search.
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
          setTimeout(settle, 10);
          return null;
        };
        return settle();
      };
      const observer = new MutationObserver(sync);
      let stopped = false;
      button.addEventListener("click", click, true);
      input.addEventListener("input", update);
      observer.observe(button, { attributes: true, attributeFilter: ["aria-expanded"] });
      return {
        input, button, list, target, claimOpen,
        dispose() {
          stopped = true;
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

    function Sidebar({ prefs, open, portal }) {
      const settings = usePreferences(prefs);
      const [view, setView] = React.useState(null);
      React.useEffect(() => settings.sidebar ? observeSidebar(prefs, setView) : undefined, [settings.sidebar]);
      return settings.sidebar && view?.active ? portal(h(Results, { query: view.query, open }), view.target) : null;
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
      try {
        ctx.slots.inject("settings.section", () => ctx.slots.register({
          name: "settings.section", id: "deja", order: 650, label: () => "Deja",
        }, () => h(Settings, { prefs })));
      } catch {}
      if (typeof ctx.inject !== "function") return;
      // Ctrl+K officially opens DSH's sidebar search. The shortcut registry
      // rejects a second default binding for the same key, so instead the
      // adapter takes over the search that gesture opens: the first expansion
      // lands in Deja full-text mode when the switch is on. The magnifier
      // click remains the way back to the title search.
      let claimOpen = null;
      ctx.inject(["shortcuts", "uiSession", "sessions", "conversation"], (c) => {
        try { c.effect(() => installFind(c, prefs), "dsh-deja: find in conversation"); } catch {}
      });
      ctx.inject(["uiWorkspace"], (c) => {
        let portal;
        try { portal = require("react-dom").createPortal; } catch { return; }
        if (typeof portal !== "function") return;
        // The sidebar's adapter claims an opened search; the shortcut runs the
        // same probe so Ctrl+K and the direct binding share one entry point.
        claimOpen = (probe) => { try { return probe(); } catch { return false; } };
        c.slots.inject("shell.overlay", () => c.slots.register({
          name: "shell.overlay", id: "deja-search", order: 950,
        }, () => h(Sidebar, { prefs, portal, open: (id) => c.uiWorkspace.openSession(id) })));
      });
      // The direct binding mirrors the takeover gesture: it only fires when
      // both switches are on, and always claims the next sidebar opening.
      const takeoverEnabled = () => prefs.getSnapshot().sidebar && prefs.getSnapshot().opensDeja;
      ctx.inject(["shortcuts"], (c) => {
        try {
          c.effect(() => c.shortcuts.register({
            id: "deja.search-sessions",
            label: () => "Deja: search DSH conversations",
            aliases: ["deja search", "search conversations"],
            // Editable in the built-in shortcut editor's application section.
            group: "application",
            // The native session.search owns primary+K; registering the same
            // default would throw. Deja keeps its own discoverable binding.
            defaults: Object.fromEntries([
              ...["windows", "macos", "linux"].map((os) => ["desktop:" + os, { code: "KeyK", modifiers: ["primary", "alt"] }]),
              ...["windows", "macos"].map((os) => ["web:" + os, { code: "KeyK", modifiers: ["primary", "alt", "shift"] }]),
            ]),
            regions: ["page"],
            modals: [],
            resolve(context) {
              if (context.modal != null || !takeoverEnabled()) return { status: "pass" };
              return { status: "handled", run() {
                // The probe defers to the live sidebar; the shared prefs flag
                // above keeps an opening claimed even before it mounts.
                if (!claimOpen?.(() => false)) prefs.claimOpen();
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
    module.exports.sessionCommand = sessionCommand;
    module.exports.installFind = installFind;
    module.exports.installSearch = installSearch;
    module.exports.attachSidebar = attachSidebar;
    module.exports.observeSidebar = observeSidebar;
    module.exports.searchHistory = searchHistory;
    module.exports.Settings = Settings;
    module.exports.Sidebar = Sidebar;
    module.exports.Results = Results;
    return module.exports;
  },
});
