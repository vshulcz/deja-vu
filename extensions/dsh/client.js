// dsh-deja, browser half: the lines deja writes for the person.
//
// The host half hands the model its recall and keeps deja's own line for the
// person — the "you have been here" receipt, the build and week notes — on a
// board behind /api/deja-notes. This draws them just above the input, the way
// Claude Code prints a hook's systemMessage. Plain JS in dsh's module wrapper,
// so the package needs no build step: React comes from the shell's module
// table, and everything else here is the platform.
//
// Nothing here may break the page. A host without the route answers 404, a
// page without a session draws nothing, and any failure leaves the dock empty.
window.__ModuleLoader__.load({
  id: "dsh-deja",
  factory: (require) => {
    var module = { exports: {} };
    const React = require("react");
    const h = React.createElement;

    // The panels above the input — dsh's queue and todo docks sit here.
    const SLOT = "conversation.input.dock";

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
      // Drawn as dsh draws its own todo panel in the same seat: a card just
      // above the input, as wide as it.
      const inset = "var(--dsh-composer-dock-inset, 8px)";
      const clearance = "var(--dsh-composer-side-clearance, 16px)";
      return h(
        "div",
        {
          "data-deja-notes": "",
          style: {
            display: "flex",
            alignItems: "flex-start",
            gap: "8px",
            boxSizing: "border-box",
            width: `calc(100% - 2 * ${clearance} - 4 * ${inset})`,
            maxWidth: `calc(var(--dsh-composer-card-max-width, 780px) - 4 * ${inset})`,
            margin: "0 auto",
            padding: "6px 12px",
            borderRadius: "var(--dsw-radius-lg, 10px)",
            background: "var(--dsw-specific-menu, rgba(127,127,127,0.08))",
            boxShadow: "var(--dsw-elevation-panel, none)",
            fontSize: "12px",
            lineHeight: "18px",
            color: "var(--dsw-alias-label-secondary, #6b7079)",
          },
        },
        h(
          "div",
          { style: { flex: "1 1 auto", minWidth: 0, overflowWrap: "anywhere" } },
          lines.map((line, i) => h("div", { key: i }, line)),
        ),
        h(
          "button",
          {
            type: "button",
            title: "Dismiss",
            "aria-label": "Dismiss deja's notes",
            onClick: () => setLines([]),
            style: {
              flex: "0 0 auto",
              border: 0,
              background: "transparent",
              color: "inherit",
              cursor: "pointer",
              padding: "0 2px",
              font: "inherit",
            },
          },
          "×",
        ),
      );
    }

    function apply(ctx) {
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
    return module.exports;
  },
});
