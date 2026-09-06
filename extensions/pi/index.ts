// deja-vu for pi: recall from the coding sessions already on this machine at
// session start and on every prompt, and /deja to search them by hand.
import { execFileSync } from "node:child_process";
import { accessSync, constants, existsSync } from "node:fs";
import { createRequire } from "node:module";
import { homedir } from "node:os";
import { join } from "node:path";

import { argv, contextText, installerExtensionPath, sessionKey } from "./lib.mjs";

const require = createRequire(import.meta.url);

const WINDOWS = process.platform === "win32";
const PLATFORM = WINDOWS ? "windows" : process.platform;
const ARCH = process.arch === "x64" ? "amd64" : process.arch;
const EXE = WINDOWS ? "deja.exe" : "deja";

// wellKnown lists the places a user's own install lands, for a pi started
// from a launcher whose PATH never sourced a shell profile.
function wellKnown(): string[] {
  const home = homedir();
  if (WINDOWS) {
    const local = process.env.LOCALAPPDATA || join(home, "AppData", "Local");
    return [join(local, "deja", "bin", EXE), join(home, ".local", "bin", EXE)];
  }
  return [join(home, ".local", "bin", EXE), "/usr/local/bin/deja", "/opt/homebrew/bin/deja", "/usr/bin/deja"];
}

// resolveDeja: what the user pointed at, then the deja they installed and keep
// current, and only last the copy npm brought with this package — pinning
// them to our bundled copy would freeze their memory at whatever version this
// package was released against.
function resolveDeja(): string {
  const candidates: string[] = [process.env.DEJA_BIN || "", EXE, ...wellKnown()];
  try {
    candidates.push(require.resolve(`@vshulcz/deja-vu-${PLATFORM}-${ARCH}/bin/${EXE}`));
  } catch {}
  for (const candidate of candidates) {
    if (!candidate) continue;
    if (candidate.includes("/") || candidate.includes("\\")) {
      try {
        accessSync(candidate, constants.X_OK);
      } catch {
        continue;
      }
    }
    return candidate;
  }
  return EXE;
}

const DEJA = resolveDeja();

// deja is asked for text, never for control flow: memory is optional
// everywhere, and a throw here would end someone's turn.
function run(args: string[], input: string, timeout = 10000): string {
  try {
    return execFileSync(DEJA, args, {
      input,
      encoding: "utf8",
      timeout,
      maxBuffer: 4 * 1024 * 1024,
      stdio: ["pipe", "pipe", "ignore"],
    }).trim();
  } catch {
    return "";
  }
}

export default function (pi: any) {
  // `deja install pi-auto` writes an extension of its own; when it is there,
  // it already does everything below, and two copies would inject the
  // recall twice and register /deja twice.
  try {
    if (existsSync(installerExtensionPath(homedir()))) return;
  } catch {}

  let injected = false;
  let toldBuilding = false;

  // pi keeps a footer status line: while the first index builds, that is
  // where the user can see it happening instead of wondering why recall is
  // quiet. Cleared as soon as the build finishes.
  const showBuild = (ctx: any) => {
    const status = run(["warmup-status"], "");
    if (status) {
      ctx.ui.setStatus("deja", status);
      return true;
    }
    ctx.ui.setStatus("deja", "");
    return false;
  };

  pi.on("session_start", async (_event: any, ctx: any) => {
    try {
      showBuild(ctx);
    } catch {
      // memory is optional: never break the session over it
    }
  });

  // pi surfaces registered commands in the prompt box, which is how someone
  // who never read the docs finds this at all.
  pi.registerCommand("deja", {
    description: "Search your own past coding sessions",
    handler: async (args: string, ctx: any) => {
      const query = (args || "").trim();
      if (!query) {
        ctx.ui.notify("Usage: /deja <what you are looking for>", "info");
        return;
      }
      // The user is waiting on this one, so it may outlive the hook budget:
      // a first search can rebuild the index.
      const found = run(argv("search", [], query), "", 120000);
      ctx.ui.notify(found || "Nothing in your history matches " + query, "info");
    },
  });

  pi.on("before_agent_start", async (event: any, ctx: any) => {
    try {
      if (!injected) {
        const { context: digest, receipt } = contextText(run(["hook-context"], ""));
        if (digest) {
          injected = true;
          ctx.ui.setStatus("deja", "");
          // The receipt is what tells the user memory arrived; without it the
          // recall is invisible and reads as the model guessing.
          if (receipt) ctx.ui.notify(receipt, "info");
          // pi keeps the footer until it is cleared, so the session carries a
          // quiet reminder that memory is on rather than a one-off toast.
          const stats = run(["statusline"], "");
          if (stats) ctx.ui.setStatus("deja", stats);
          return { message: { customType: "deja-recall", content: digest, display: false } };
        }
        // Nothing to recall: either there is no history yet, or the first
        // index is still building. Only the second is worth saying, once.
        if (!toldBuilding && showBuild(ctx)) {
          toldBuilding = true;
          ctx.ui.notify(run(["warmup-status"], ""), "info");
        }
        return;
      }
      const key = sessionKey(event, ctx);
      const raw = run(["hook-prompt"], JSON.stringify({ prompt: event.prompt || "", session_id: key }));
      if (!raw) return;
      const resp = JSON.parse(raw);
      if (resp && resp.systemMessage) ctx.ui.notify(resp.systemMessage, "info");
      const extra = resp && resp.hookSpecificOutput && resp.hookSpecificOutput.additionalContext;
      if (!extra) return;
      return { message: { customType: "deja-recall", content: extra, display: false } };
    } catch {
      // memory is optional: never break the session over it
    }
  });
}
