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

    const STRIP = {
      boxSizing: "border-box",
      width: "100%",
      display: "flex",
      flexDirection: "column",
      gap: "6px",
      margin: "0 auto",
      padding: "6px 10px",
      borderRadius: "12px",
      border: "1px solid var(--dsw-alias-border-l1)",
      background: "var(--dsw-specific-tip)",
    };
    const ROW = { display: "flex", alignItems: "center", gap: "8px" };
    const LABEL = {
      flex: "none",
      fontSize: "13px",
      fontWeight: 500,
      lineHeight: "24px",
      color: "var(--dsw-alias-label-primary)",
    };
    const INPUT = {
      flex: 1,
      minWidth: 0,
      height: "26px",
      padding: "0 8px",
      fontSize: "13px",
      lineHeight: "20px",
      color: "var(--dsw-alias-label-primary)",
      background: "var(--dsw-alias-bg-base)",
      border: "1px solid var(--dsw-alias-border-l2)",
      borderRadius: "6px",
      outline: "none",
    };
    const BUTTON = {
      flex: "none",
      height: "26px",
      padding: "0 10px",
      fontSize: "13px",
      color: "var(--dsw-alias-label-secondary)",
      background: "transparent",
      border: "1px solid var(--dsw-alias-border-l2)",
      borderRadius: "6px",
      cursor: "pointer",
    };
    const NOTE = {
      fontSize: "12px",
      lineHeight: "18px",
      color: "var(--dsw-alias-label-tertiary)",
    };
    const OUTPUT = {
      maxHeight: "220px",
      overflow: "auto",
      margin: 0,
      padding: "8px",
      fontSize: "12px",
      lineHeight: "18px",
      whiteSpace: "pre-wrap",
      wordBreak: "break-word",
      color: "var(--dsw-alias-label-primary-dimmed)",
      background: "var(--dsw-alias-bg-base)",
      border: "1px solid var(--dsw-alias-border-l2)",
      borderRadius: "8px",
    };

    const ROWS = { display: "flex", flexDirection: "column", gap: "6px", maxHeight: "260px", overflow: "auto" };
    const ROW_BOX = {
      padding: "6px 8px",
      borderRadius: "8px",
      background: "var(--dsw-alias-bg-base)",
      border: "1px solid var(--dsw-alias-border-l2)",
      cursor: "pointer",
    };
    const HEAD = { display: "flex", alignItems: "center", gap: "8px", fontSize: "12px", lineHeight: "18px" };
    const BADGE = {
      flex: "none",
      padding: "1px 6px",
      borderRadius: "999px",
      fontSize: "11px",
      color: "var(--dsw-alias-label-secondary)",
      background: "var(--dsw-alias-interactive-bg-hover)",
    };
    const META = { color: "var(--dsw-alias-label-tertiary)" };
    const SNIPPET = {
      margin: "4px 0 0",
      fontSize: "12px",
      lineHeight: "17px",
      color: "var(--dsw-alias-label-primary-dimmed)",
      whiteSpace: "pre-wrap",
      wordBreak: "break-word",
    };

    // parse turns what deja printed into rows. A header line names the agent,
    // the project, the date and the match count; the lines indented under it
    // are that session's own text. Anything matching neither shape — the notes
    // deja writes about word forms and result caps — is dropped rather than
    // shown as if it were a result.
    function parse(text) {
      const rows = [];
      for (const line of String(text).split("\n")) {
        const header = /^\[([^\]]+)\]\s+(\S+)\s+·\s+([^·]+?)\s+·\s+\S+\s+—\s+(.+)$/.exec(line);
        if (header) {
          rows.push({
            agent: header[1].trim(),
            project: header[2].trim(),
            when: header[3].trim(),
            count: header[4].trim(),
            body: [],
          });
          continue;
        }
        if (rows.length > 0 && /^\s+\S/.test(line)) rows[rows.length - 1].body.push(line.trim());
      }
      return rows;
    }

    /** One past session. Collapsed it shows two lines; a click opens the rest. */
    function Row({ row }) {
      const [open, setOpen] = useState(false);
      const body = open ? row.body : row.body.slice(0, 2);
      return h(
        "div",
        { style: ROW_BOX, onClick: () => setOpen(!open) },
        h(
          "div",
          { style: HEAD },
          h("span", { style: BADGE }, row.agent),
          h("span", { style: META }, row.project + " · " + row.when + " · " + row.count),
        ),
        body.length > 0 ? h("p", { style: SNIPPET }, body.join("\n")) : null,
      );
    }

    /**
     * HistoryStrip: collapsed it is one button, because a search box nobody
     * asked for is noise above the composer. Open, it searches on Enter and
     * shows one row per past session it found.
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
          { style: { ...STRIP, padding: "4px 10px" } },
          h(
            "div",
            { style: ROW },
            h(
              "button",
              {
                type: "button",
                style: BUTTON,
                onClick: () => setOpen(true),
              },
              "Search your other agents' history",
            ),
          ),
        );
      }

      return h(
        "div",
        { style: STRIP },
        h(
          "div",
          { style: ROW },
          h("span", { style: LABEL }, "History"),
          h("input", {
            ref: inputRef,
            style: INPUT,
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
          h("button", { type: "button", style: BUTTON, onClick: () => void run() }, "Search"),
          h("button", { type: "button", style: BUTTON, onClick: () => setOpen(false) }, "Close"),
        ),
        state.kind === "searching" ? h("div", { style: NOTE }, "searching this machine's history…") : null,
        state.kind === "failed" ? h("div", { style: NOTE }, "search failed: " + state.text) : null,
        state.kind === "done"
          ? state.rows.length > 0
            ? h("div", { style: ROWS }, state.rows.map((row, i) => h(Row, { key: i, row })))
            : h("div", { style: NOTE }, state.text)
          : null,
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
