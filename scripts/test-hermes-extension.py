#!/usr/bin/env python3
"""Contract and live round-trip tests for extensions/hermes/__init__.py."""

import importlib.util
import json
import os
import shutil
import subprocess
import sys
import tempfile
import types
import unittest
from datetime import datetime, timedelta, timezone
from pathlib import Path
from unittest import mock

REPO_ROOT = Path(__file__).resolve().parent.parent
HERMES_INIT = REPO_ROOT / "extensions" / "hermes" / "__init__.py"


def load_hermes_module():
    agent_mod = types.ModuleType("agent")
    mp_mod = types.ModuleType("agent.memory_provider")

    class MemoryProvider:
        pass

    class RecallStatus:
        def __init__(self, provider_label: str = "", count: int = 0, **_kwargs):
            self.provider_label = provider_label
            self.count = count

    mp_mod.MemoryProvider = MemoryProvider
    mp_mod.RecallStatus = RecallStatus
    agent_mod.memory_provider = mp_mod
    sys.modules["agent"] = agent_mod
    sys.modules["agent.memory_provider"] = mp_mod

    spec = importlib.util.spec_from_file_location("deja_hermes_test_mod", HERMES_INIT)
    mod = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(mod)
    return mod


class _FakeAgent:
    def __init__(self, suppress_status_output: bool = False):
        self.suppress_status_output = suppress_status_output

    def _emit_status(self, *_args, **_kwargs):
        return None


class HermesExtensionContractTest(unittest.TestCase):
    def setUp(self):
        self.mod = load_hermes_module()
        self.mod._HOOK_EXT = None
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.workspace = Path(self.tmp.name) / "work"
        self.workspace.mkdir(parents=True, exist_ok=True)

    def test_register_and_metadata(self):
        registered = []

        class Ctx:
            def register_memory_provider(self, provider):
                registered.append(provider)

        self.mod.register(Ctx())
        self.assertEqual(len(registered), 1)
        prov = registered[0]
        self.assertEqual(prov.name, "deja")
        self.assertIn("deja_recall", prov.system_prompt_block())
        names = [s["name"] for s in prov.get_tool_schemas()]
        self.assertEqual(names, ["deja_recall", "deja_fix", "deja_blame"])
        self.assertEqual(prov.get_config_schema(), [])

    def test_prefetch_status_compression_memory_and_tools_via_hook_ext(self):
        calls = []

        def fake_run(args, payload="", timeout=10, cwd=None):
            calls.append((list(args), payload, cwd))
            if args[:4] == ["hook-ext", "--host", "hermes", "--op"]:
                op = args[4]
                req = json.loads(payload or "{}")
                if op == "context":
                    out = {"ok": True, "kind": "hook", "context": "- prior worker fix", "note": "deja: from claude"}
                elif op == "prompt":
                    out = {"ok": True, "kind": "hook", "context": "- keep --no-verify", "note": ""}
                elif op == "settle":
                    out = {
                        "ok": True,
                        "note": "deja: from claude",
                        "held_notes": [],
                        "indicator_label": "deja · from claude",
                    }
                elif op == "tool":
                    out = {
                        "ok": True,
                        "kind": "success",
                        "output": f"tool:{req.get('tool')}:{req.get('query')}:{req.get('offset', '')}",
                    }
                else:
                    out = {"ok": True}
                return subprocess.CompletedProcess([self.mod.DEJA, *args], 0, json.dumps(out) + "\n", "")
            return subprocess.CompletedProcess([self.mod.DEJA, *args], 0, "", "")

        prov = self.mod.DejaMemoryProvider()
        agent = _FakeAgent(suppress_status_output=False)
        prov.initialize("hermes-s1", platform="cli", status_callback=agent._emit_status, cwd=str(self.workspace))

        with mock.patch.object(self.mod, "_run_deja", side_effect=fake_run):
            first = prov.prefetch("check worker.go", session_id="hermes-s1")
            self.assertIn("- prior worker fix", first)
            self.assertIn("- keep --no-verify", first)
            status = prov.recall_status()
            self.assertIsNotNone(status)
            self.assertEqual(status.provider_label, "deja · from claude")
            self.assertEqual(status.count, 2)

            # Second turn skips context digest.
            second = prov.prefetch("another question")
            self.assertEqual(second, "- keep --no-verify")

            # Cron skips automatic prefetch but leaves provider intact.
            prov.initialize("cron-1", platform="cron", cwd=str(self.workspace))
            self.assertEqual(prov.prefetch("ignored cron query"), "")
            prov.initialize("hermes-s1", platform="cli", status_callback=agent._emit_status, cwd=str(self.workspace))

            # Pre-compression buffered until compression session switch commits.
            self.assertEqual(prov.on_pre_compress([{"role": "user", "content": "x" * 5000}]), "")
            prov.on_session_switch("hermes-s2", reason="compression", cwd=str(self.workspace))

            # Non-compression reset clears notes and re-arms first turn digest.
            prov.on_session_switch("hermes-s3", reset=True, cwd=str(self.workspace))
            self.assertTrue(prov._first_turn)

            prov.on_memory_write("add", "USER", "  - prefer go test -race  ")
            prov.on_session_end([])

            recall_res = json.loads(prov.handle_tool_call("deja_recall", {"query": "worker.go", "offset": "not-an-int"}))
            self.assertEqual(recall_res["result"], "tool:deja_recall:worker.go:0")
            paged_res = json.loads(prov.handle_tool_call("deja_recall", {"query": "worker.go", "offset": 10**12}))
            self.assertEqual(paged_res["result"], "tool:deja_recall:worker.go:1000000000")
            fix_res = json.loads(prov.handle_tool_call("deja_fix", {"error": "undefined: foo"}))
            self.assertEqual(fix_res["result"], "tool:deja_fix:undefined: foo:")
            blame_res = json.loads(prov.handle_tool_call("deja_blame", {"path": "worker.go"}))
            self.assertEqual(blame_res["result"], "tool:deja_blame:worker.go:")
            bad_tool = json.loads(prov.handle_tool_call("deja_unknown", {}))
            self.assertIn("unknown tool", bad_tool["error"])

        ops_called = [c[0][4] for c in calls if len(c[0]) >= 5 and c[0][0] == "hook-ext"]
        self.assertIn("context", ops_called)
        self.assertIn("prompt", ops_called)
        self.assertIn("settle", ops_called)
        self.assertIn("precompact", ops_called)
        self.assertIn("remember", ops_called)
        self.assertIn("session-end", ops_called)
        self.assertIn("tool", ops_called)
        for args, payload, _ in calls:
            if args[:5] == ["hook-ext", "--host", "hermes", "--op", "precompact"]:
                pc = json.loads(payload)
                self.assertEqual(len(pc["messages"][0]["content"]), 4000)

    def test_legacy_and_missing_binary_fallbacks(self):
        calls = []

        def fake_legacy(args, payload="", timeout=10, cwd=None):
            calls.append(list(args))
            if args[0] == "hook-ext":
                return subprocess.CompletedProcess([self.mod.DEJA, *args], 2, "", "unknown command hook-ext\n")
            if args[0] == "hook-context":
                return subprocess.CompletedProcess([self.mod.DEJA, *args], 0, "- legacy context\n", "")
            if args[0] == "hook-prompt":
                return subprocess.CompletedProcess([self.mod.DEJA, *args], 0, "- legacy prompt\n", "")
            if args[0] == "recall":
                return subprocess.CompletedProcess([self.mod.DEJA, *args], 0, "LEGACY-RECALL\n", "")
            return subprocess.CompletedProcess([self.mod.DEJA, *args], 0, "", "")

        prov = self.mod.DejaMemoryProvider()
        prov.initialize("hermes-leg", platform="desktop", cwd=str(self.workspace))
        with mock.patch.object(self.mod, "_run_deja", side_effect=fake_legacy):
            text = prov.prefetch("worker")
            self.assertIn("- legacy context", text)
            self.assertIn("- legacy prompt", text)
            res = json.loads(prov.handle_tool_call("deja_recall", {"query": "-json", "offset": 5}))
            self.assertEqual(res["result"], "LEGACY-RECALL")

        self.assertFalse(self.mod._HOOK_EXT)
        hook_ext_attempts = [c for c in calls if c[0] == "hook-ext"]
        self.assertEqual(len(hook_ext_attempts), 1)
        self.assertIn(["recall", "--offset", "5", "--", "-json"], calls)

        # Missing binary never raises across any lifecycle or tool method.
        self.mod.DEJA = os.path.join(self.tmp.name, "nonexistent-deja-bin")
        self.mod._HOOK_EXT = None
        self.assertFalse(prov.is_available())
        self.assertIn("brew install deja-vu", prov.unavailable_reason())
        prov.initialize("hermes-miss", platform="cli", cwd=str(self.workspace))
        self.assertEqual(prov.prefetch("worker"), "")
        self.assertEqual(prov.on_pre_compress([{"role": "user", "content": "hi"}]), "")
        prov.on_session_switch("hermes-miss-2", reason="compression", cwd=str(self.workspace))
        prov.on_memory_write("add", "memory", "note")
        prov.on_session_end([])
        miss_res = json.loads(prov.handle_tool_call("deja_recall", {"query": "worker"}))
        self.assertIn("result", miss_res)

    def test_live_binary_round_trip(self):
        explicit = os.environ.get("DEJA_BIN", "").strip()
        deja_bin = explicit or shutil.which("deja") or ""
        if explicit and not os.path.isfile(explicit):
            self.fail(f"DEJA_BIN points to missing binary: {explicit}")
        if not deja_bin:
            self.skipTest("set DEJA_BIN to run live Hermes -> Go binary round-trip")

        base = Path(self.tmp.name).resolve()
        home = base / "home"
        workspace = base / "work" / "app"
        workspace.mkdir(parents=True, exist_ok=True)
        sess_dir = home / ".claude" / "projects" / "app"
        sess_dir.mkdir(parents=True, exist_ok=True)
        cache_dir = home / ".cache"
        data_dir = home / ".local" / "share"
        appdata_roaming = home / "AppData" / "Roaming"
        appdata_local = home / "AppData" / "Local"
        idx_dir = base / "index.db"
        cache_dir.mkdir(parents=True, exist_ok=True)
        data_dir.mkdir(parents=True, exist_ok=True)
        appdata_roaming.mkdir(parents=True, exist_ok=True)
        appdata_local.mkdir(parents=True, exist_ok=True)

        worker_path = workspace / "worker.go"
        worker_path.write_text("package app\n", encoding="utf-8")
        target_file = str(worker_path)
        now = datetime.now(timezone.utc)

        def write_fix_session(sid: str, offset_min: int) -> None:
            def ts(delta: int) -> str:
                return (now - timedelta(minutes=offset_min - delta)).strftime("%Y-%m-%dT%H:%M:%SZ")

            def rec(role: str, content, delta: int) -> str:
                return json.dumps({
                    "type": role,
                    "sessionId": sid,
                    "cwd": str(workspace),
                    "timestamp": ts(delta),
                    "message": {"role": role, "content": content},
                })

            recs = [
                rec("user", "fix failing step in app", 0),
                rec("assistant", [{"type": "tool_use", "id": "u1", "name": "Bash", "input": {"command": "quibtool_9821 check ./..."}}], 1),
                rec("user", [{"type": "tool_result", "tool_use_id": "u1", "is_error": True, "content": "zsh:1: command not found: quibtool_9821"}], 2),
                rec("assistant", [{"type": "tool_use", "id": "u2", "name": "Bash", "input": {"command": "brew install quibtool_9821-integration"}}], 3),
                rec("user", [{"type": "tool_result", "tool_use_id": "u2", "is_error": False, "content": "PASS ok"}], 4),
            ]
            (sess_dir / f"{sid}.jsonl").write_text("\n".join(recs) + "\n", encoding="utf-8")

        write_fix_session("11111111-2222-4333-8444-555555555501", 30)
        write_fix_session("11111111-2222-4333-8444-555555555503", 45)

        sid2 = "11111111-2222-4333-8444-555555555502"
        recs2 = [
            json.dumps({
                "type": "user",
                "sessionId": sid2,
                "cwd": str(workspace),
                "timestamp": (now - timedelta(minutes=20)).strftime("%Y-%m-%dT%H:%M:%SZ"),
                "message": {"role": "user", "content": "Investigate --no-verify flag and worker.go race condition {{template_var}}"},
            }),
            json.dumps({
                "type": "assistant",
                "sessionId": sid2,
                "cwd": str(workspace),
                "timestamp": (now - timedelta(minutes=19)).strftime("%Y-%m-%dT%H:%M:%SZ"),
                "message": {
                    "role": "assistant",
                    "content": [
                        {"type": "text", "text": "Checking worker.go and running build."},
                        {"type": "tool_use", "id": "e1", "name": "Edit", "input": {"file_path": target_file, "old_string": "a", "new_string": "b"}},
                        {"type": "tool_use", "id": "e2", "name": "Bash", "input": {"command": "pytest --no-verify -k smoke"}},
                    ],
                },
            }),
            json.dumps({
                "type": "user",
                "sessionId": sid2,
                "cwd": str(workspace),
                "timestamp": (now - timedelta(minutes=18)).strftime("%Y-%m-%dT%H:%M:%SZ"),
                "message": {
                    "role": "user",
                    "content": [
                        {"type": "tool_result", "tool_use_id": "e1", "is_error": False, "content": "ok"},
                        {"type": "tool_result", "tool_use_id": "e2", "is_error": False, "content": "2 passed"},
                    ],
                },
            }),
            json.dumps({
                "type": "assistant",
                "sessionId": sid2,
                "cwd": str(workspace),
                "timestamp": (now - timedelta(minutes=17)).strftime("%Y-%m-%dT%H:%M:%SZ"),
                "message": {"role": "assistant", "content": "We decided to keep --no-verify disabled by default in worker.go and pass {{template_var}} intact."},
            }),
        ]
        (sess_dir / f"{sid2}.jsonl").write_text("\n".join(recs2) + "\n", encoding="utf-8")

        env_overrides = {
            "HOME": str(home),
            "USERPROFILE": str(home),
            "APPDATA": str(appdata_roaming),
            "LOCALAPPDATA": str(appdata_local),
            "XDG_CONFIG_HOME": "",
            "XDG_CACHE_HOME": str(cache_dir),
            "XDG_DATA_HOME": "",
            "CLAUDE_PROJECT_DIR": "",
            "CLAUDE_CONFIG_DIR": "",
            "HERMES_HOME": "",
            "DEJA_HERMES_HOME": "",
            "DEJA_CLAUDE_ROOT": str(sess_dir.parent),
            "DEJA_CODEX_ROOT": str(base / "codex"),
            "DEJA_OPENCODE_DB": str(base / "opencode.db"),
            "DEJA_INDEX_DIR": str(idx_dir),
            "DEJA_NOTES_FILE": str(base / "notes.jsonl"),
            "DEJA_WARMUP_SENTINEL": str(base / "warmup-guard"),
            "DEJA_HOOK_REFRESH": "1",
            "DEJA_POLICY_FILE": str(base / "missing-policy.json"),
            "DEJA_EMBED_OFF": "1",
            "DEJA_NO_AUTO_INDEX": "",
            "NO_COLOR": "1",
        }
        with mock.patch.dict(os.environ, env_overrides):
            indexed = subprocess.run([deja_bin, "index"], capture_output=True, text=True, check=False)
            self.assertEqual(indexed.returncode, 0, indexed.stderr or indexed.stdout)

            self.mod.DEJA = deja_bin
            self.mod._HOOK_EXT = None
            prov = self.mod.DejaMemoryProvider()
            self.assertTrue(prov.is_available())
            agent = _FakeAgent(suppress_status_output=False)
            prov.initialize("live-hermes-1", platform="cli", status_callback=agent._emit_status, cwd=str(workspace))

            prefetched = prov.prefetch("quibtool_9821 worker.go")
            self.assertTrue(prefetched, "expected live prefetch from indexed session")
            self.assertTrue(self.mod._HOOK_EXT)

            prov.on_memory_write("add", "memory", "Always run quibtool_9821 after editing worker.go")
            recall_out = json.loads(prov.handle_tool_call("deja_recall", {"query": "quibtool_9821"}))
            self.assertIn("quibtool_9821", recall_out.get("result", ""))

            blame_out = json.loads(prov.handle_tool_call("deja_blame", {"path": target_file}))
            self.assertIn("worker.go", blame_out.get("result", ""))

            fix_out = json.loads(prov.handle_tool_call("deja_fix", {"error": "zsh:1: command not found: quibtool_9821"}))
            self.assertIn("quibtool_9821-integration", fix_out.get("result", ""))

            prov.on_pre_compress([{"role": "user", "content": "Checking worker.go"}])
            prov.on_session_switch("live-hermes-2", reason="compression", cwd=str(workspace))
            prov.on_session_end([])


if __name__ == "__main__":
    unittest.main(verbosity=2)
