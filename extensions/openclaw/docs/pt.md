# @vshulcz/openclaw-deja

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | Português | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

Parte do **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** no LongMemEval-S) · [Documentação](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Demo ao vivo](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![Demonstração do deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

Leva o histórico em disco de outros 51 agentes de programação (Claude Code, Codex, Cursor, Gemini, Zed e mais) para o [OpenClaw](https://openclaw.ai). Baseado no binário Go único `deja` com índice BM25 local e sem LLM (`DEJA_OFFLINE=1` desativa a checagem diária).

## Instalação

```sh
openclaw plugins install clawhub:@vshulcz/openclaw-deja
```

Instale também o binário no sistema caso ainda não o tenha:

```sh
brew install deja-vu
```

Se `deja install openclaw-auto` ou `deja install --auto` já estiver configurado, o plugin se afasta automaticamente para evitar duplicidade.

## O que ele faz

- **`agent:bootstrap` (com `openclaw agent --local`)**: carrega decisões anteriores do projeto ao iniciar a sessão.
- **`before_prompt_build` (com OpenClaw `2026.8.1` e `hooks.allowConversationAccess: true`)**: busca contexto útil antes de cada turno.
- **`before_compaction`**: preserva tarefa em andamento, conclusões e comandos executados durante a compactação.
- **`session_end`**: atualiza o índice local ao finalizar.
- **Middleware de ferramentas**: ao falhar um comando ou editar arquivos sensíveis, acrescenta correções ou decisões antigas.
- **Ferramentas `deja_recall` / `deja_fix` / `deja_blame`**: acesso direto para buscar histórico, correções e autoria por arquivo.
- **`/deja <query>`**: busca rápida em todos os agentes indexados.

### Recibo em mensagens diretas

Em mensagens diretas qualificadas, `reply_payload_sending` anexa uma linha discreta em itálico indicando a origem do contexto:

`_↺ from Codex CLI, 3 weeks ago: "how should the zorbit queue drain on deploy" · deja_`

O recibo só aparece quando todas as quatro condições são atendidas:

1. Chat direto, não em grupo (`isGroup: false` / `direct`).
2. Remetente autorizado por `commands.ownerAllowFrom` / `allowFrom`.
3. Streaming em blocos desligado (`streaming.block.enabled` / `blockStreaming` / `channels.telegram.streaming.mode: "off"`).
4. Resposta final em texto via canal `direct`.

Defina `"receipt": false` para desativar esse rodapé.

## Configuração

```json
{
  "plugins": {
    "entries": {
      "deja-vu": {
        "enabled": true,
        "hooks": {
          "allowConversationAccess": true
        },
        "config": {
          "autoRecall": true,
          "tools": true,
          "receipt": true,
          "bin": "/opt/homebrew/bin/deja"
        }
      }
    }
  }
}
```

Ou via CLI:

```sh
openclaw config set plugins.entries.deja-vu.hooks.allowConversationAccess true
```

## Qual binário é usado

`bin` -> `DEJA_BIN` -> `PATH` -> locais padrão -> cópia embutida em `@vshulcz/deja-vu`.

## Benchmarks

| Recall | Tokens | Indexação e busca |
|---|---|---|
| **97.2% R@5** no LongMemEval-S (88.9% hit@1 em 470 perguntas) | **58% menos tokens** (em tarefas repetidas) | **13.6 s** para indexar 19,195 sessões / **0.7 ms** de busca entre 52 agentes de código |

## Memória do repositório e handoff entre agentes

Compartilhe decisões validadas e caminhos descartados via git em `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). As entradas passam por redação de segredos ao gravar, ficam ordenadas por hash com `merge=union` para mesclar branches paralelos sem conflito e carregam para qualquer pessoa que clonar o repositório (`{"activations":{"auto":{"repo":false}}}` na política de confiança desativa a injeção via hooks). [Saiba mais](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Trocando de ferramenta no meio da tarefa? Execute `deja handoff --to <agent>` (ou chame `handoff` via MCP) para transferir objetivo, decisões, arquivos alterados, testes que passaram e trabalho em aberto para outro agente.

## Privacidade

Índice guardado em `~/.cache/deja/index.db` (`DEJA_INDEX_DIR`), removendo `sk-*`, `ghp_*`, `AKIA*` e `Bearer` durante a indexação. Nada sai do dispositivo sem `deja embed`. Use `deja install --auto` para configurar todos os 52 agentes.

Parte do [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
