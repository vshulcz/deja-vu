"""What a memory tool costs in the agent's context before it answers anything.

A tool's definitions are sent with every request for as long as it is wired in,
so they are paid once per turn whether or not the agent calls them — the one
number on this page that a user pays on day zero without asking a question.

Run it against each server's own stdio command:

    SERVER='["deja","mcp"]' python3 scripts/day0compare/toolcost.py
    SERVER='["npx","-y","@agentmemory/agentmemory@0.9.29","mcp"]' python3 scripts/day0compare/toolcost.py
    SERVER='["npx","-y","claude-mem@13.25.3","mcp"]' python3 scripts/day0compare/toolcost.py
    SERVER='["ctx","mcp","serve"]' python3 scripts/day0compare/toolcost.py
    SERVER='["mempalace-mcp"]' python3 scripts/day0compare/toolcost.py

ENV='HOME=/tmp/probe-home;…' pins what the child sees, so a probe never reads
the machine's real store. Counted with tiktoken o200k_base over the JSON of the
tools array, which is what a client puts in the prompt.
"""

import json
import os
import subprocess
import sys

import tiktoken

enc = tiktoken.get_encoding("o200k_base")


def main() -> int:
    cmd = json.loads(os.environ.get("SERVER", '["deja","mcp"]'))
    env = dict(os.environ)
    for kv in os.environ.get("ENV", "").split(";"):
        if "=" in kv:
            k, v = kv.split("=", 1)
            env[k] = v
    p = subprocess.Popen(
        cmd, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL, text=True, env=env,
    )

    def call(method, params, i):
        p.stdin.write(json.dumps({"jsonrpc": "2.0", "id": i, "method": method, "params": params}) + "\n")
        p.stdin.flush()
        while True:
            line = p.stdout.readline()
            if not line:
                return None
            try:
                d = json.loads(line)
            except ValueError:
                continue
            if d.get("id") == i:
                return d

    try:
        call("initialize", {"protocolVersion": "2024-11-05", "capabilities": {},
                            "clientInfo": {"name": "toolcost", "version": "0"}}, 1)
        p.stdin.write(json.dumps({"jsonrpc": "2.0", "method": "notifications/initialized", "params": {}}) + "\n")
        p.stdin.flush()
        listed = call("tools/list", {}, 2)
    except BrokenPipeError:
        print(f"{cmd[0]}: the server exited before it listed its tools")
        return 1
    if not listed or "result" not in listed:
        print(f"{cmd[0]}: no tools/list answer")
        return 1
    tools = listed["result"]["tools"]
    block = json.dumps(tools)
    print(f"tools advertised: {len(tools)} ({', '.join(t['name'] for t in tools)})")
    print(f"in context every turn: {len(enc.encode(block))} tokens ({len(block)} bytes)")
    p.kill()
    return 0


if __name__ == "__main__":
    sys.exit(main())
