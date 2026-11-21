import { execFile } from "node:child_process"
import { promisify } from "node:util"
import {
  appendRecall,
  formatNotificationBody,
  installerOwns,
  isSubagentTool,
  parseRecallArgs,
  resolveDeja,
  rewriteSubagentInput,
} from "./lib.mjs"

const execFileAsync = promisify(execFile)
const HOOK_TIMEOUT_MS = 1300
const COMMAND_TIMEOUT_MS = 5000

async function runDeja(args, { stdin, timeoutMs = HOOK_TIMEOUT_MS, signal } = {}) {
  const bin = resolveDeja()
  const controller = new AbortController()
  const onAbort = () => controller.abort()
  if (signal) {
    if (signal.aborted) return { ok: false, stdout: "" }
    signal.addEventListener("abort", onAbort, { once: true })
  }
  try {
    const res = await execFileAsync(bin, args, {
      encoding: "utf8",
      input: stdin,
      timeout: timeoutMs,
      maxBuffer: 1024 * 1024,
      signal: controller.signal,
      windowsHide: true,
    })
    return { ok: true, stdout: res.stdout || "" }
  } catch {
    return { ok: false, stdout: "" }
  } finally {
    if (signal) signal.removeEventListener("abort", onAbort)
  }
}

export function createWorker({ run = runDeja, owns = installerOwns } = {}) {
  const workspacePaths = new Map()
  let workspacesLoaded = false

  async function refreshWorkspaces(orca, signal) {
    try {
      const res = await orca.host.call("workspace.list", {}, { signal })
      const list = Array.isArray(res?.workspaces) ? res.workspaces : []
      workspacePaths.clear()
      for (const ws of list) {
        if (ws && typeof ws.id === "string" && typeof ws.path === "string" && ws.path) {
          workspacePaths.set(ws.id, ws.path)
        }
      }
      workspacesLoaded = true
    } catch {
      // Fail open when workspace listing is unavailable.
    }
  }

  async function resolveCwd(orca, { workspaceId, signal } = {}) {
    let targetId = typeof workspaceId === "string" ? workspaceId : ""
    if (!targetId) {
      try {
        const ctx = await orca.host.call("workspace.readContext", {}, { signal })
        if (ctx && typeof ctx.workspaceId === "string") targetId = ctx.workspaceId
      } catch {
        return ""
      }
    }
    if (!targetId) return ""
    if (!workspacesLoaded || !workspacePaths.has(targetId)) {
      await refreshWorkspaces(orca, signal)
    }
    return workspacePaths.get(targetId) || ""
  }

  async function notify(orca, title, body, signal) {
    const trimmed = formatNotificationBody(body)
    if (!trimmed) return
    try {
      await orca.host.call("notifications.show", { title, body: trimmed }, { signal })
    } catch {
      // Notifications are best-effort.
    }
  }

  return async function activate(orca) {
    orca.hooks.on("agent.prompt.before", async (payload, next) => {
      if (!payload || typeof payload.prompt !== "string" || owns("hook-prompt")) {
        return next(payload)
      }
      const cwd = await resolveCwd(orca, { workspaceId: payload.workspaceId })
      const stdin = JSON.stringify({
        cwd,
        hook_event_name: "UserPromptSubmit",
        session_id: payload.sessionId || payload.terminalId || payload.workspaceId || "",
        prompt: payload.prompt,
      })
      const { ok, stdout } = await run(["hook-prompt", "--plain"], {
        stdin,
        timeoutMs: HOOK_TIMEOUT_MS,
      })
      if (!ok || !stdout.trim()) return next(payload)
      return next({
        ...payload,
        prompt: appendRecall(payload.prompt, stdout),
      })
    })

    orca.hooks.on("agent.tool.before", async (payload, next) => {
      if (!payload || !isSubagentTool(payload.tool) || owns("hook-prompt")) {
        return next(payload)
      }
      const prompt = payload.input?.prompt
      if (typeof prompt !== "string" || !prompt.trim()) {
        return next(payload)
      }
      const cwd = await resolveCwd(orca, { workspaceId: payload.workspaceId })
      const stdin = JSON.stringify({
        cwd,
        hook_event_name: "UserPromptSubmit",
        session_id: payload.sessionId || payload.terminalId || payload.workspaceId || "",
        prompt,
      })
      const { ok, stdout } = await run(["hook-prompt", "--plain"], {
        stdin,
        timeoutMs: HOOK_TIMEOUT_MS,
      })
      if (!ok || !stdout.trim()) return next(payload)
      const nextInput = rewriteSubagentInput(payload.tool, payload.input, stdout)
      if (!nextInput) return next(payload)
      return next({ ...payload, input: nextInput })
    })

    // agent.tool.after is observe-only in Orca; pass payload through without blocking.
    orca.hooks.on("agent.tool.after", async (payload, next) => next(payload))

    orca.commands.register("deja.orient", async (_args, { signal } = {}) => {
      const cwd = await resolveCwd(orca, { signal })
      const stdin = JSON.stringify({
        cwd,
        hook_event_name: "SessionStart",
      })
      const { ok, stdout } = await run(["hook-context", "--plain"], {
        stdin,
        timeoutMs: COMMAND_TIMEOUT_MS,
        signal,
      })
      const text = stdout.trim()
      if (!ok || !text) {
        await notify(orca, "deja", "No prior session digest found for this workspace.", signal)
        return { text: "" }
      }
      await notify(orca, "deja orient", text, signal)
      return { text }
    })

    orca.commands.register("deja.recall", async (args, { signal } = {}) => {
      const query = parseRecallArgs(args)
      const cliArgs = query ? ["search", "--limit", "5", "--", query] : ["last", "-n", "5"]
      const { ok, stdout } = await run(cliArgs, {
        timeoutMs: COMMAND_TIMEOUT_MS,
        signal,
      })
      const text = stdout.trim()
      if (!ok || !text) {
        await notify(orca, "deja recall", "No matching sessions found.", signal)
        return { text: "" }
      }
      await notify(orca, "deja recall", text, signal)
      return { text }
    })
  }
}

export default createWorker()

export async function deactivate() {}
