# deja-vu para Orca

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | Português | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

Plugin `trusted-worker` do Orca v2 para `deja` — traz memória de sessões entre 49 ferramentas de codificação com IA (Claude Code, Codex, Cursor, opencode, Gemini CLI, Orca e outras), incluindo sessões gravadas antes da instalação do `deja`.

![Demonstração do deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Instalação

Requisitos: [`deja`](https://github.com/vshulcz/deja-vu#install) no `PATH` (ou instalado em `~/.local/bin`, `/opt/homebrew/bin`, `/usr/local/bin`, `~/bin` ou `%LOCALAPPDATA%\Programs\deja`).

Instale o diretório do plugin `extensions/orca` no Orca (`>=1.4.248`).

Para habilitar MCP nativo, arquivos de skill, `SessionStart`, `PreCompact`, `SessionEnd` e captura de correções em comandos que falharam no Orca, execute também:

```sh
deja install orca-auto
```

(`deja install orca` instala MCP, skill e `/deja` sem hooks de ciclo de vida; `deja install orca-auto` ou `deja install --auto` adiciona o conjunto completo de hooks.)

Quando `deja install orca-auto` já gravou o hook nativo `UserPromptSubmit` no `settings.json` do Orca, este worker recua automaticamente para não injetar blocos `<deja-recall>` em dobro.

## O que este plugin adiciona

- **Hooks**
  - `agent.prompt.before`: resolve o caminho do workspace ativo e injeta contexto `<deja-recall>` relevante no prompt dentro do orçamento de 2 s do Orca (timeout de `1.3s`, fail-open).
  - `agent.tool.before`: ao iniciar subagentes `Agent` ou `Task`, anexa contexto `<deja-recall>` somente a `input.prompt` (nunca modifica entradas de `Bash`, `Read`, `Edit` ou outras ferramentas).
  - `agent.tool.after`: apenas observação na API host do Orca (`next` é ignorado e o Orca não aguarda), portanto a entrega de `fix` para comandos com falha vem via `deja install orca-auto`.
- **Comandos**
  - `deja.orient` (`deja: Orient on workspace`): executa `deja hook-context --plain` para o workspace ativo e exibe uma notificação resumida.
  - `deja.recall` (`deja: Recall past sessions`): executa `deja search --limit 5 -- <query>` (ou `deja last -n 5` sem consulta) e exibe o resultado.

## Benchmarks

| Recall | Tokens | Indexação e busca |
|---|---|---|
| **97.2% R@5** no LongMemEval-S (88.9% hit@1 em 470 perguntas; MemPalace publica 96.6%, agentmemory 95.2%) | **58% menos tokens** (**metade dos tokens** do agentmemory em tarefas repetidas) | **13.6 s** para indexar 19,195 sessões / **0.7 ms** de busca entre 49 agentes de código (próximo de 7: 72 s) |

## Memória do repositório e handoff entre agentes

Compartilhe decisões validadas e caminhos descartados via git em `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). As entradas passam por redação de segredos ao gravar, ficam ordenadas por hash com `merge=union` para mesclar branches paralelos sem conflito e carregam para qualquer pessoa que clonar o repositório (`{"activations":{"auto":{"repo":false}}}` na política de confiança desativa a injeção via hooks). [Saiba mais](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Trocando de ferramenta no meio da tarefa? Execute `deja handoff --to <agent>` (ou chame `handoff` via MCP) para transferir objetivo, decisões, arquivos alterados, testes que passaram e trabalho em aberto para outro agente.

## Licença

[MIT](../LICENSE)

Achou útil? [Dê uma estrela ao deja-vu no GitHub](https://github.com/vshulcz/deja-vu).
