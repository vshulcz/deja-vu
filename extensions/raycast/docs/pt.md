# deja-vu para Raycast

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | Português | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

Parte do **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** no LongMemEval-S) · [Documentação](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Demo ao vivo](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

Pesquise e retome sessões de agentes de código entre Claude Code, Codex, Cursor, OpenCode, Gemini CLI e mais 44 ferramentas diretamente pelo Raycast.

![Demonstração do deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Pré-requisitos

Instale o [`deja-vu`](https://github.com/vshulcz/deja-vu):

```sh
brew install deja-vu && deja install --auto
```

## Comandos

- **Search Sessions**: busca textual completa em todas as sessões de agentes no seu Mac (`deja search --json --limit 25 -- <query>`), separando correspondências exatas dos resultados por proximidade.
- **Recent Sessions**: navegue pelas 100 sessões mais recentes de todos os agentes instalados (`deja last 100 --json`) com prévia de metadados (`Agent`, `Project`, `Started`, `Updated`, `Session ID`).

## Ações

- **Show Session**: leia a conversa com segredos redigidos dentro do Raycast (`deja show <id> --json --limit 200`).
- **Resume in Terminal**: execute o comando nativo de retomada do agente (`deja resume <id>`) no `Terminal.app`.
- **Copy Resume Command**, **Copy Session ID** e **Open Project Folder**.

## Benchmarks

| Recall | Tokens | Indexação e busca |
|---|---|---|
| **97.2% R@5** no LongMemEval-S (88.9% hit@1 em 470 perguntas) | **58% menos tokens** (em tarefas repetidas) | **13.6 s** para indexar 19,195 sessões / **0.7 ms** de busca entre 49 agentes de código |

## Memória do repositório e handoff entre agentes

Compartilhe decisões validadas e caminhos descartados via git em `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). As entradas passam por redação de segredos ao gravar, ficam ordenadas por hash com `merge=union` para mesclar branches paralelos sem conflito e carregam para qualquer pessoa que clonar o repositório (`{"activations":{"auto":{"repo":false}}}` na política de confiança desativa a injeção via hooks). [Saiba mais](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Trocando de ferramenta no meio da tarefa? Execute `deja handoff --to <agent>` (ou chame `handoff` via MCP) para transferir objetivo, decisões, arquivos alterados, testes que passaram e trabalho em aberto para outro agente.

## Desenvolvimento local

```sh
cd extensions/raycast && npm install && npx ray develop
```

Parte do [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
