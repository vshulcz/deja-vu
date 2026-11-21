# deja para Grok Build

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | Português | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

Parte do **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** no LongMemEval-S) · [Documentação](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Demo ao vivo](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![Demonstração do deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

Memória persistente vinda do histórico local das suas sessões no Claude Code, Codex, Cursor, opencode, Zed e mais 44 agentes. Executa localmente via binário único `deja` em Go (`DEJA_OFFLINE=1` desativa a checagem diária).

## Instalação

Instale o binário `deja` primeiro (o pacote do plugin não inclui executáveis nativos):

```sh
brew install deja-vu
# ou:
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

Depois adicione o marketplace e instale o plugin ([xai-org/plugin-marketplace#332](https://github.com/xai-org/plugin-marketplace/pull/332)):

```sh
grok plugin marketplace add vshulcz/deja-vu
grok plugin install deja
```

Ou rode `deja install --auto` para configurar `[mcp_servers.deja]` em `~/.grok/config.toml` e `~/.grok/hooks/deja.json`.

## O que você ganha

- **Ferramenta MCP `deja`** (8 modos: `recall`, `fix`, `blame`, `how`, `orient`, `context`, `remember`, `handoff`)
- **Skill `deja-history`** e comando **`/deja:recall <query>`**
- **5 hooks (`hooks/recall.mjs`)**: `SessionStart`, `UserPromptSubmit`, `PreToolUse` (`updatedInput`), `PostToolUse` (para `Bash` no Grok 1.0.41), `PreCompact` e `SessionEnd`

## Convivência segura entre instalações

Se `~/.grok/config.toml` já possuir `[mcp_servers.deja]` ou `~/.grok/hooks/deja.json` já tiver hooks do deja, o plugin recua automaticamente para não duplicar chamadas (`deja uninstall grok` remove a configuração direta).

## Qual binário é usado

`DEJA_BIN` -> `~/.local/bin`, `/usr/local/bin`, `/opt/homebrew/bin`, `/usr/bin` -> `PATH`.

## Recuo silencioso sem quebrar o host

Configurado por `${GROK_PLUGIN_ROOT}` e `.mcp.json`. No Grok Build 1.0.5 sem o binário presente, `grok mcp doctor` e `grok inspect` registram `handshake failed: connection closed` até o `deja` ser instalado.

## Benchmarks

| Recall | Tokens | Indexação e busca |
|---|---|---|
| **97.2% R@5** no LongMemEval-S (88.9% hit@1 em 470 perguntas) | **58% menos tokens** (em tarefas repetidas) | **13.6 s** para indexar 19,195 sessões / **0.7 ms** de busca entre 50 agentes de código |

## Memória do repositório e handoff entre agentes

Compartilhe decisões validadas e caminhos descartados via git em `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). As entradas passam por redação de segredos ao gravar, ficam ordenadas por hash com `merge=union` para mesclar branches paralelos sem conflito e carregam para qualquer pessoa que clonar o repositório (`{"activations":{"auto":{"repo":false}}}` na política de confiança desativa a injeção via hooks). [Saiba mais](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Trocando de ferramenta no meio da tarefa? Execute `deja handoff --to <agent>` (ou chame `handoff` via MCP) para transferir objetivo, decisões, arquivos alterados, testes que passaram e trabalho em aberto para outro agente.

## ## Licença

MIT
