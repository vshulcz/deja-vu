# opencode-deja

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | Português | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

Parte do **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** no LongMemEval-S) · [Documentação](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Demo ao vivo](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![Demonstração do deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

Memória local para o [opencode](https://opencode.ai) compartilhando o histórico de outros 51 agentes de programação no mesmo computador (Claude Code, Codex, Cursor, Gemini, Hermes, Zed e mais). Usa o binário Go único `deja` com índice BM25 local, sem LLMs (`DEJA_OFFLINE=1` desativa a checagem diária).

## Instalação

Adicione ao `opencode.ai/config.json`:

```json
{ "plugin": ["opencode-deja"] }
```

(No opencode 2.x use `"plugins": ["opencode-deja"]` ou `opencode plugin add opencode-deja`.) Também é possível rodar `deja install --auto` para gravar `opencode.json` e `plugins/deja.js`.

## O que você ganha

### Seis ferramentas para o agente

| Ferramenta | Função |
|---|---|
| `deja_recall` | Buscar sessões anteriores por erro ou tema |
| `deja_session` | Ler turnos ao redor de um resultado |
| `deja_blame` | Descobrir qual sessão originou um arquivo ou `path:line` |
| `deja_fix` | Ver o comando que corrigiu um erro igual antes |
| `deja_how` | Relembrar flags e comandos reais do repositório |
| `deja_remember` | Fixar uma decisão tomada |

### Quatro recursos automáticos

- **Resumo na abertura**: toast no opencode 1.x e sistema em 2.x (`deja-vu — from Codex CLI, 4 months ago: "…"`).
- **Espera de 20 minutos e filtro de subagentes**: não repete lembretes no mesmo diretório e pula subagentes.
- **Erros e arquivos**: anexa correções antigas quando um comando falha e decisões ao mexer em arquivos.
- **Pré-compactação**: preserva tarefa atual e conclusões antes do resumo automático.

## Opções

```json
{
  "plugin": [["opencode-deja", { "autoRecall": false, "tools": true }]]
}
```

(No 2.x, use `"plugins"`.)

- `autoRecall`: liga/desliga injeção automática (padrão `true`).
- `tools`: registra as 6 ferramentas (padrão `true`).
- `bin`: caminho customizado para `deja`.

Para configurar os 8 modos MCP (`recall`, `fix`, `blame`, `how`, `orient`, `context`, `remember`, `handoff`) em todas as 52 ferramentas, execute `deja install --auto` (saiba mais em [trocar de agente sem perder contexto](https://vshulcz.github.io/deja-vu/guide/switch-agents-without-losing-context.html)).

## Qual binário é usado

`bin` -> `DEJA_BIN` -> `PATH` -> `~/.local/bin`, `/usr/local/bin`, `/opt/homebrew/bin` -> pacote npm.

## Privacidade

Índice em `~/.cache/deja/index.db` (ajustável com `DEJA_INDEX_DIR`) com remoção automática de `sk-*`, `ghp_*`, `AKIA*` e `Bearer`. Todo o processamento é offline, exceto ao configurar manualmente `deja embed`.

## Benchmarks

| Recall | Tokens | Indexação e busca |
|---|---|---|
| **97.2% R@5** no LongMemEval-S (88.9% hit@1 em 470 perguntas) | **58% menos tokens** (em tarefas repetidas) | **13.6 s** para indexar 19,195 sessões / **0.7 ms** de busca entre 52 agentes de código |

## Memória do repositório e handoff entre agentes

Compartilhe decisões validadas e caminhos descartados via git em `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). As entradas passam por redação de segredos ao gravar, ficam ordenadas por hash com `merge=union` para mesclar branches paralelos sem conflito e carregam para qualquer pessoa que clonar o repositório (`{"activations":{"auto":{"repo":false}}}` na política de confiança desativa a injeção via hooks). [Saiba mais](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Trocando de ferramenta no meio da tarefa? Execute `deja handoff --to <agent>` (ou chame `handoff` via MCP) para transferir objetivo, decisões, arquivos alterados, testes que passaram e trabalho em aberto para outro agente.

## ## Licença

MIT
