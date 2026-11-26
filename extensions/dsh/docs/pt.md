# dsh-deja

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=social)](https://github.com/vshulcz/deja-vu)

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | Português | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

Parte do **[★ vshulcz/deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** no LongMemEval-S) · [Documentação](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Demo ao vivo](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![Demonstração do deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

Plugin `session-query` para DeepSeek Harness (`dsh`) e DSH Desktop (`anywhere-labs/dsh-desktop`). Além das conversas do DSH, indexa localmente sessões de outros 52 agentes de programação na sua máquina (Claude Code, Codex, Cursor, Gemini, Hermes e mais) com o binário único Go `deja` e índice BM25 (`DEJA_OFFLINE=1` desativa a checagem diária).

## Instalação

Mantenha sempre `--profile` ao adicionar o plugin:

```sh
dsh plugin --profile web add dsh-deja
# ou direto do repositório:
dsh plugin --profile web add github:vshulcz/deja-vu#path:extensions/dsh
```

Caso ainda não tenha o `deja`, aponte `DEJA_BIN` ou configure os 53 agentes e `$DSH_HOME/plugins/deja/`:

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
brew install deja-vu && deja install --auto
npx @vshulcz/deja-vu sources
```

## O que adiciona

### Seis ferramentas

| Ferramenta | Função |
|---|---|
| `deja_recall` | Buscar sessões antigas por erro, tema ou flag |
| `deja_session` | Ler os turnos ao redor de uma ocorrência |
| `deja_blame` | Ver qual sessão editou um arquivo ou `path:line` |
| `deja_fix` | Consultar o comando que corrigiu o mesmo erro |
| `deja_how` | Relembrar comandos e flags reais usados no projeto |
| `deja_remember` | Registrar uma decisão fixa |

- **Comando `/deja <...>`**: pesquisa direta pelo campo de prompt.
- **Integrações**: funciona com `deja install dsh-auto` e `dsh web` (`tui`, `headless`, `sdk`), com `autoRecall: false` disponível na configuração YAML.

## Controles e atalhos de busca

Gerenciados em `Settings → Deja`:

- **Mod+F** (ou **Mod+Alt+F** / **Cmd+Option+F** no macOS): abre a pesquisa; filtre uma sessão usando `/deja --session <bare session UUID> -- <your draft>`.
- **Mod+Shift+K** (**Cmd+Shift+K** no macOS): busca global multiagente.
- **Mod+K**: abre o painel com alternância **DSH conversations only** (até 20 resultados).
- **Atalhos para navegadores Linux**: **`Mod+/`**, **`Mod+Shift+,`** e **`Mod+Shift+.`** (`shortcuts.open`).

## Personalização dos atalhos

Em `Settings → Deja`, use **Change shortcut**, **Restore default** ou **Remove** em **`session.search`**. O **dsh-deja package host plugin** expõe `/api/deja-search`; `/api/deja-search?probe=1` retorna `{"ok": true, "plugin": "dsh-deja"}` quando ligado ao `deja search`.

## Como funciona por baixo

- **Injeção de prompt**: envia `additionalContext` e `systemMessage` via `ctx.systemPrompt.context` e `agent/pre-step` (`dsh 0.1.1-rc.2`+).
- **Rotas e limites**: `/api/deja-notes` (`--plain`) abastece `client.js`; `GET /api/deja-search` roda `deja search --harness deepseek --json --limit 20 -- <query>` com limite de 20 segundos, 2 MiB de saída e 500 caracteres de consulta.
- **Barra lateral**: acopla em `data-slot="sidebar.workspaces"`, `aria-expanded` e `_listArea` (DSH `0.2.1-rc.5`).

## Testando a interface de busca

```sh
node --test extensions/dsh/test/*.test.js
```

Inclui também `extensions/dsh/test/search-browser.html` via React 18 CDN para validação rápida no navegador.

## Benchmarks

| Recall | Tokens | Indexação e busca |
|---|---|---|
| **97.2% R@5** no LongMemEval-S (88.9% hit@1 em 470 perguntas) | **58% menos tokens** (em tarefas repetidas) | **13.6 s** para indexar 19,195 sessões / **0.7 ms** de busca entre 53 agentes de código |

## Memória do repositório e handoff entre agentes

Compartilhe decisões validadas e caminhos descartados via git em `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). As entradas passam por redação de segredos ao gravar, ficam ordenadas por hash com `merge=union` para mesclar branches paralelos sem conflito e carregam para qualquer pessoa que clonar o repositório (`{"activations":{"auto":{"repo":false}}}` na política de confiança desativa a injeção via hooks). [Saiba mais](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Trocando de ferramenta no meio da tarefa? Execute `deja handoff --to <agent>` (ou chame `handoff` via MCP) para transferir objetivo, decisões, arquivos alterados, testes que passaram e trabalho em aberto para outro agente.

## O que não faz

Mantém o índice exclusivamente em `~/.cache/deja/index.db` (`DEJA_INDEX_DIR`), só utiliza modelos externos caso você invoque `deja embed`, remove credenciais `sk-*`, `ghp_*`, `AKIA*` e `Bearer` e nunca altera sessões originais.

## Licença

Parte do [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
