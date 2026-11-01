# Magpie Gateway Hook for deja-vu

English | [简体中文](https://github.com/vshulcz/deja-vu/blob/main/extensions/magpie/docs/zh.md)

[![GitHub Repo stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat-square&logo=github&label=deja-vu)](https://github.com/vshulcz/deja-vu)

> Part of [**vshulcz/deja-vu**](https://github.com/vshulcz/deja-vu) — local cross-agent session memory for Claude Code, Codex, Cursor, Gemini CLI, OpenClaw, Hermes, and 37+ more agents.

Brings historical session context from `deja` into [Magpie](https://github.com/yetone/magpie) API gateway requests across OpenAI-compatible and Anthropic-compatible routes (`POST /v1/chat/completions`, `POST /v1/responses`, `POST /v1/messages`).

## Install

Install the `deja` binary and build your local index once:

```bash
brew install deja-vu && deja index
# or: npm i -g @vshulcz/deja-vu && deja index
```

Then add the gateway hook in Magpie:

```bash
magpie plugin add github:vshulcz/deja-vu
```

## What It Does

When a local or authenticated gateway request arrives with a user prompt of 4 or more characters, `extensions/magpie/deja.hooks.mjs` runs `deja context` asynchronously with a 150ms ceiling and prepends matching prior session context into the user message inside an `<untrusted-historical-context>` reference fence. If `deja` is not installed or finds no relevant session, the request passes through unmodified.
