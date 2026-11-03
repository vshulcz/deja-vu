"use strict";
const vscode = require("vscode");
const { execFile } = require("child_process");
const fs = require("fs");
const os = require("os");
const path = require("path");

function resolveDejaBinary() {
  const home = os.homedir();
  const ext = process.platform === "win32" ? ".exe" : "";
  const candidates = [
    path.join(home, ".config", "deja", "bin", "deja-hook" + ext),
    path.join(home, ".local", "bin", "deja" + ext),
    "/opt/homebrew/bin/deja",
    "/usr/local/bin/deja",
    path.join(home, "go", "bin", "deja" + ext),
  ];
  for (const c of candidates) {
    try {
      if (fs.statSync(c).isFile()) return c;
    } catch {}
  }
  return "deja" + ext;
}

function compactStatusLine(raw) {
  const cleaned = String(raw || "")
    .split("\n")[0]
    .replace(/\x1b\[[0-9;]*m/g, "")
    .replace(/ · ~\d+×\b/g, "")
    .replace(/\s+\(~\d+×[^)]*\)/g, "")
    .trim();
  if (!cleaned) return { full: "", text: "" };
  const text = cleaned
    .split(" · ")
    .slice(0, 3)
    .map((p, i) => (i === 2 ? p.replace(/^(\d+\s+earlier)\b.*/, "$1") : p))
    .join(" · ");
  return { full: cleaned, text };
}

function activate(context) {
  const item = vscode.window.createStatusBarItem("deja.status", vscode.StatusBarAlignment.Right, 100);
  item.name = "deja";
  item.command = "deja.installOrStats";
  let busy = false;
  let hasIndex = false;
  let installed = true;

  const cmd = vscode.commands.registerCommand("deja.installOrStats", () => {
    const term = vscode.window.createTerminal("deja-vu");
    term.show();
    if (!installed) {
      term.sendText("npx -y @vshulcz/deja-vu install --auto");
    } else {
      term.sendText("deja stats");
    }
  });

  const refresh = () => {
    if (busy) return;
    busy = true;
    const exe = resolveDejaBinary();
    const folder = (vscode.workspace.workspaceFolders || [])[0];
    let child;
    try {
      child = execFile(exe, ["statusline"], { timeout: 5000, windowsHide: true, maxBuffer: 65536 }, (err, stdout) => {
        busy = false;
        if (err && err.code === "ENOENT") {
          installed = false;
          item.text = "deja: click to install";
          item.tooltip = "Install deja-vu local cross-agent memory (npx -y @vshulcz/deja-vu install --auto)";
          item.show();
          return;
        }
        installed = true;
        const { full, text } = compactStatusLine(stdout);
        if (err || !full) {
          item.hide();
          return;
        }
        if (/[░█]/.test(full)) {
          if (hasIndex) return;
        } else if (/\b[1-9][\d,]*\s+sessions?\b/.test(full)) {
          hasIndex = true;
        }
        item.tooltip = full;
        item.text = text;
        item.show();
      });
    } catch {
      busy = false;
      return;
    }
    if (child.stdin) {
      child.stdin.on("error", () => {});
      child.stdin.end(JSON.stringify({ cwd: folder ? folder.uri.fsPath : "" }));
    }
  };

  refresh();
  const timer = setInterval(refresh, 10000);
  context.subscriptions.push(item, cmd, { dispose: () => clearInterval(timer) });
}

module.exports = { activate, deactivate() {}, compactStatusLine, resolveDejaBinary };
