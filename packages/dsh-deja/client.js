// The browser half of dsh-deja: a strip above the composer that searches the
// history the other agents on this machine already wrote, and shows what it
// found without leaving the conversation.
//
// The web server serves exactly one file per plugin, so this is one CJS bundle
// inside the ModuleLoader handshake. React and every @deepseek-ai/* package
// come from the app's own module system through `require` — nothing is bundled
// here, which is why this file is plain JavaScript with no build step.
//
// Searching needs the index, which lives behind a binary the browser cannot
// run. Rather than mount a Remote namespace of its own, this calls the command
// the host half already registers: ctx.remote.commands.execute runs "/deja
// <query>" in the current session and hands back its text.
window.__ModuleLoader__.load({
  id: "dsh-deja",
  factory: (require) => {
    var module = { exports: {} };
    var exports = module.exports;

    const React = require("react");
    const { useCallback, useEffect, useRef, useState } = React;
    const h = React.createElement;

    // The composer publishes its own geometry as CSS variables, and the strip
    // has to sit on exactly that width — otherwise it reads as a foreign
    // element bolted above the card, which is what a hand-picked width looked
    // like. Hover and focus need real selectors, so this is a stylesheet
    // rather than inline styles.
    const CSS = `
.deja-dock {
  box-sizing: border-box;
  width: calc(100% - var(--dsh-composer-side-clearance) * 2 - var(--dsh-composer-dock-inset) * 4);
  max-width: calc(var(--dsh-composer-card-max-width) - 4 * var(--dsh-composer-dock-inset));
  margin: 0 auto;
}
.deja-trigger {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 28px;
  padding: 0 10px;
  border: none;
  border-radius: 999px;
  background: transparent;
  color: var(--dsw-alias-label-tertiary);
  font-size: 12px;
  line-height: 20px;
  cursor: pointer;
}
.deja-trigger:hover {
  background: var(--dsw-alias-interactive-bg-hover);
  color: var(--dsw-alias-label-secondary);
}
.deja-panel {
  box-sizing: border-box;
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 8px;
  border: 1px solid var(--dsw-alias-border-l1);
  border-radius: 12px;
  background: var(--dsw-specific-tip);
}
.deja-row {
  display: flex;
  align-items: center;
  gap: 6px;
}
.deja-input {
  flex: 1;
  min-width: 0;
  height: 30px;
  padding: 0 10px;
  border: 1px solid var(--dsw-alias-border-l2);
  border-radius: 8px;
  background: var(--dsw-alias-bg-base);
  color: var(--dsw-alias-label-primary);
  font-size: 13px;
  line-height: 20px;
  outline: none;
}
.deja-input:focus { border-color: var(--dsw-alias-state-business-primary); }
.deja-input::placeholder { color: var(--dsw-alias-label-caption); }
.deja-icon {
  flex: none;
  width: 28px;
  height: 28px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 0;
  border: none;
  border-radius: 999px;
  background: transparent;
  color: var(--dsw-alias-label-tertiary);
  font-size: 15px;
  line-height: 1;
  cursor: pointer;
}
.deja-icon:hover { background: var(--dsw-alias-interactive-bg-hover); color: var(--dsw-alias-label-secondary); }
.deja-note {
  padding: 0 2px;
  color: var(--dsw-alias-label-tertiary);
  font-size: 12px;
  line-height: 18px;
}
.deja-results {
  display: flex;
  flex-direction: column;
  gap: 2px;
  max-height: 244px;
  overflow-y: auto;
}
.deja-hit {
  padding: 7px 9px;
  border-radius: 8px;
  cursor: pointer;
}
.deja-hit:hover { background: var(--dsw-alias-bg-base); }
.deja-head {
  display: flex;
  align-items: baseline;
  gap: 8px;
  font-size: 12px;
  line-height: 18px;
}
.deja-agent {
  flex: none;
  font-weight: 500;
  color: var(--dsw-alias-label-primary);
}
.deja-meta {
  min-width: 0;
  color: var(--dsw-alias-label-caption);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.deja-snippet {
  margin: 3px 0 0;
  color: var(--dsw-alias-label-primary-dimmed);
  font-size: 12px;
  line-height: 17px;
  overflow: hidden;
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
}
.deja-snippet.deja-open {
  -webkit-line-clamp: unset;
  white-space: pre-wrap;
}
`;

    const TAG = "dsh-deja/strip.css";
    if (typeof document !== "undefined" && document.querySelector('style[data-plugin-css="' + TAG + '"]') === null) {
      const tag = document.createElement("style");
      tag.dataset.plugin = "dsh-deja";
      tag.dataset.pluginCss = TAG;
      tag.textContent = CSS;
      document.head.appendChild(tag);
    }

    /** The one glyph this needs: a magnifier at the app's own stroke weight. */
    function Glass() {
      return h(
        "svg",
        { width: 14, height: 14, viewBox: "0 0 16 16", fill: "none", "aria-hidden": "true" },
        h("circle", { cx: 7, cy: 7, r: 4.25, stroke: "currentColor", strokeWidth: 1.4 }),
        h("path", { d: "M10.5 10.5 L14 14", stroke: "currentColor", strokeWidth: 1.4, strokeLinecap: "round" }),
      );
    }

    // parse turns what deja printed into rows. A header line names the agent,
    // the project, the date and the match count; the lines indented under it
    // are that session's own text. Anything matching neither shape — the notes
    // deja writes about word forms and result caps — is dropped rather than
    // shown as if it were a result.
    function parse(text) {
      const rows = [];
      for (const line of String(text).split("\n")) {
        const header = /^\[([^\]]+)\]\s+(\S+)\s+·\s+([^·]+?)\s+·\s+(\S+)\s+—\s+(.+)$/.exec(line);
        if (header) {
          rows.push({
            agent: header[1].trim(),
            project: header[2].trim(),
            when: header[3].trim(),
            count: header[5].trim(),
            body: [],
          });
          continue;
        }
        if (rows.length > 0 && /^\s+\S/.test(line)) rows[rows.length - 1].body.push(line.trim());
      }
      return rows;
    }

    /** One past session: two lines of its own text, the rest on a click. */
    function Hit({ row }) {
      const [open, setOpen] = useState(false);
      return h(
        "div",
        { className: "deja-hit", onClick: () => setOpen(!open) },
        h(
          "div",
          { className: "deja-head" },
          h("span", { className: "deja-agent" }, row.agent),
          h("span", { className: "deja-meta" }, row.project + " · " + row.when + " · " + row.count),
        ),
        row.body.length > 0
          ? h("p", { className: open ? "deja-snippet deja-open" : "deja-snippet" }, row.body.join("\n"))
          : null,
      );
    }

    /**
     * HistoryStrip: collapsed it is one quiet button, because a search box
     * nobody asked for is noise above the composer. Open, it searches on Enter
     * and shows one row per past session it found.
     */
    function HistoryStrip({ onSearch }) {
      const [open, setOpen] = useState(false);
      const [query, setQuery] = useState("");
      const [state, setState] = useState({ kind: "idle" });
      const inputRef = useRef(null);
      const generation = useRef(0);

      useEffect(() => {
        if (open && inputRef.current) inputRef.current.focus();
      }, [open]);

      const run = useCallback(async () => {
        const text = query.trim();
        if (!text) return;
        const mine = ++generation.current;
        setState({ kind: "searching" });
        try {
          const answer = await onSearch(text);
          // A late answer to an abandoned query must not overwrite a newer one.
          if (mine !== generation.current) return;
          setState({ kind: "done", text: answer, rows: parse(answer) });
        } catch (error) {
          if (mine !== generation.current) return;
          setState({ kind: "failed", text: String((error && error.message) || error) });
        }
      }, [onSearch, query]);

      if (!open) {
        return h(
          "div",
          { className: "deja-dock" },
          h(
            "button",
            { type: "button", className: "deja-trigger", onClick: () => setOpen(true) },
            h(Glass),
            "Your history from the other agents",
          ),
        );
      }

      return h(
        "div",
        { className: "deja-dock" },
        h(
          "div",
          { className: "deja-panel" },
          h(
            "div",
            { className: "deja-row" },
            h("input", {
              ref: inputRef,
              className: "deja-input",
              value: query,
              placeholder: "an error string, a file path, a flag",
              onChange: (event) => setQuery(event.target.value),
              onKeyDown: (event) => {
                if (event.key === "Enter") {
                  event.preventDefault();
                  void run();
                }
                if (event.key === "Escape") setOpen(false);
              },
            }),
            h(
              "button",
              { type: "button", className: "deja-icon", title: "Search", onClick: () => void run() },
              h(Glass),
            ),
            h("button", { type: "button", className: "deja-icon", title: "Close", onClick: () => setOpen(false) }, "×"),
          ),
          state.kind === "searching"
            ? h("div", { className: "deja-note" }, "searching this machine's history…")
            : null,
          state.kind === "failed" ? h("div", { className: "deja-note" }, "search failed: " + state.text) : null,
          state.kind === "done"
            ? state.rows.length > 0
              ? h("div", { className: "deja-results" }, state.rows.map((row, i) => h(Hit, { key: i, row })))
              : h("div", { className: "deja-note" }, state.text)
            : null,
        ),
      );
    }

    // The dotted entry is not decoration: a plugin that injects only "remote"
    // is refused the namespace with 'cannot get property "remote.commands"
    // without inject' the first time it searches.
    const inject = ["slots", "remote", "remote.commands"];

    function apply(ctx) {
      ctx.slots.inject("conversation.input.dock", () =>
        ctx.slots.register(
          {
            name: "conversation.input.dock",
            id: "deja-history",
            order: 30,
            inject: (sessionId) => ({
              onSearch: async (query) => {
                const result = await ctx.remote.commands.execute(sessionId, "/deja " + query, []);
                if (!result || !result.ok) {
                  const error = result && result.error;
                  throw new Error(error ? error.code + ": " + error.message : "no answer from the host");
                }
                // The envelope is { value: { result: { kind, text } } }: the
                // command's own answer sits one level in, and reading `text`
                // off the outer object prints "[object Object]".
                const value = result.value;
                const answer = (value && value.result) || value;
                const text = answer && typeof answer.text === "string" ? answer.text.trim() : "";
                return text || "Nothing in this machine's history matches that.";
              },
            }),
          },
          HistoryStrip,
        ),
      );
    }

    exports.HistoryStrip = HistoryStrip;
    exports.apply = apply;
    exports.inject = inject;
    return module.exports;
  },
});
