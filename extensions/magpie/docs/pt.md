# `@vshulcz/magpie-deja` — Hook de gateway Magpie para o deja-vu

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | Português | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=social)](https://github.com/vshulcz/deja-vu)

Parte do **[★ vshulcz/deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** no LongMemEval-S) · [Documentação](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Demo ao vivo](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![Demonstração do deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

> Injeta automaticamente contexto das sessões de outros 52 agentes de código da sua máquina (Claude Code, Codex, Cursor, Gemini CLI, OpenClaw, Hermes etc.) nas requisições roteadas pelo [Magpie](https://github.com/yetone/magpie).

Compatível com os protocolos OpenAI (`chat`, `responses`), Anthropic (`anthropic`) e Gemini (`gemini`) usando o binário único Go `deja` com índice BM25 local, sem LLMs ou embeddings (`DEJA_OFFLINE=1` desativa a verificação diária de versão).

## Instalação

```sh
magpie plugin add @vshulcz/magpie-deja
```

Requer o binário `deja` (`@vshulcz/deja-vu`, `brew install deja-vu` ou `install.sh`) e índice gerado com `deja index`. Para configurar também `recall`, `fix`, `blame`, `how`, `orient`, `context`, `remember` e `handoff` em todos os 53 agentes suportados:

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh && deja install --auto
```

Se a mensagem já incluir `<deja-memory>` ou `<deja-recall>`, o hook ignora a inserção para evitar duplicidade.

## Funcionamento e fronteira de dados

1. **`onRequest` executa `deja hook-prompt --plain`**: lê `~/.cache/deja/index.db` (`DEJA_INDEX_DIR` opcional) em até `800ms` e insere `<deja-memory>`. **Aviso de tráfego externo**: os trechos recuperados são enviados na requisição ao provedor de LLM configurado no Magpie.
2. **Escopo estrito por `ctx.cwd`**: com `allowUnscoped: false`, nunca usa `process.cwd()` do gateway para consultar `deja hook-prompt`.
3. **Filtro de origem local**: `recallRemote` inicia como `false`, restringindo o uso a `ctx.origin === "local"` ou `ctx.origin === "magpie-key"`.
4. **Sanitização em duas camadas**: redige `sk-*`, `ghp_*`, `github_pat_*`, `AKIA*`, `Bearer`, `PRIVATE KEY`, senhas em URL e `api_key=...` na indexação e antes do envio via `maskSecrets` (igual ao `deja.hooks.mjs`).
5. **Prevenção de loops e cache de prefixo**: ignora prompts contendo `<deja-memory`, `<deja-recall>` ou `[deja —` e mantém o cache de prefixo intacto.

## Opções

| Opção | Padrão | Descrição |
|---|---|---|
| `requestTimeoutMs` | `800` | Tempo limite do processo `deja` (`50`–`5000` ms) |
| `maxBytes` | `4000` | Limite em bytes do bloco injetado (`256`–`32768`) |
| `recallRemote` | `false` | Habilita busca para requisições remotas |
| `allowUnscoped` | `false` | Permite consulta sem `ctx.cwd` |
| `cwd` | `""` | Caminho de projeto forçado |
| `skipAgents` | `[]` | Lista de agentes ignorados (ex.: `["claude-code"]`) |
| `bin` | `""` | Caminho explícito para o binário `deja` |

## Benchmarks

| Recall | Tokens | Indexação e busca |
|---|---|---|
| **97.2% R@5** no LongMemEval-S (88.9% hit@1 em 470 perguntas) | **58% menos tokens** (em tarefas repetidas) | **13.6 s** para indexar 19,195 sessões / **0.7 ms** de busca entre 53 agentes de código |

## Memória do repositório e handoff entre agentes

Compartilhe decisões validadas e caminhos descartados via git em `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). As entradas passam por redação de segredos ao gravar, ficam ordenadas por hash com `merge=union` para mesclar branches paralelos sem conflito e carregam para qualquer pessoa que clonar o repositório (`{"activations":{"auto":{"repo":false}}}` na política de confiança desativa a injeção via hooks). [Saiba mais](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Trocando de ferramenta no meio da tarefa? Execute `deja handoff --to <agent>` (ou chame `handoff` via MCP) para transferir objetivo, decisões, arquivos alterados, testes que passaram e trabalho em aberto para outro agente.

## Licença

Parte do [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
