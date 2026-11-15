import assert from "node:assert/strict"
import { existsSync, readFileSync } from "node:fs"
import test from "node:test"

const root = new URL("..", import.meta.url)

test("codewhale plugin.json obeys Claude-compatible bundle constraints", () => {
  const manifest = JSON.parse(readFileSync(new URL(".claude-plugin/plugin.json", root), "utf8"))
  assert.match(manifest.name, /^[a-z0-9-]+$/)
  assert.equal(manifest.version, "0.24.2")
  assert.equal(manifest.license, "MIT")
  assert.equal(manifest.hooks, undefined)
  assert.equal(manifest.lspServers, undefined)
  assert.ok(Array.isArray(manifest.keywords) && manifest.keywords.length > 0)
  for (const kw of manifest.keywords) {
    assert.match(kw, /^[a-z0-9-]+$/)
  }

  const raw = JSON.stringify(manifest)
  assert.equal(raw.includes("CLAUDE_PLUGIN_ROOT"), false)
})

test("codewhale .mcp.json invokes deja mcp without custom root paths", () => {
  const mcpText = readFileSync(new URL(".mcp.json", root), "utf8")
  assert.equal(mcpText.includes("CLAUDE_PLUGIN_ROOT"), false)
  const mcp = JSON.parse(mcpText)
  assert.deepEqual(mcp, {
    mcpServers: {
      deja: {
        command: "deja",
        args: ["mcp"],
      },
    },
  })
})

test("codewhale skill, command, and LICENSE exist with expected metadata", () => {
  const skill = readFileSync(new URL("skills/deja-history/SKILL.md", root), "utf8")
  assert.match(skill, /^---\n[\s\S]*name:\s*deja-history\b/m)
  const cmd = readFileSync(new URL("commands/deja.md", root), "utf8")
  assert.ok(cmd.includes("orient"))
  assert.ok(existsSync(new URL("LICENSE", root)))
})
