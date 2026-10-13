// dsh-deja, browser half: the lines deja writes for the person.
//
// The host half hands the model its recall and keeps deja's own lines for the
// person — the "you have been here" receipt, the build and week notes — on a
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

    const RECEIPT = /^deja-vu\s+—\s+(you have been here|this was done on another machine):\s+"(.+)"\s+\((.+)\)$/;
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
          out.receipt = { lead: r[1].charAt(0).toUpperCase() + r[1].slice(1) + ":", topic: r[2], meta: r[3].replace(/\bvia:\s+/, "via ") };
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
          h("span", { className: "deja-name" }, "deja-vu"),
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

    function apply(ctx) {
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
    return module.exports;
  },
});
