# deja-vu for JetBrains IDEs

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)
[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | Português | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

Busca local de sessões entre agentes, AI Line Blame e handoff para IntelliJ IDEA, GoLand, PyCharm, WebStorm, RustRover, CLion, PhpStorm e Rider (`2024.2+`).

[deja-vu](https://github.com/vshulcz/deja-vu) indexa em SQLite FTS5 local as transcrições de sessão gravadas em disco por Claude Code, Codex, OpenCode, Cursor, Gemini CLI, Junie, GitHub Copilot, Aider, Windsurf e outros 39 agentes de código, com redação de segredos antes da saída e sem nenhum serviço na nuvem.

**[★ Dê estrela ao deja-vu no GitHub](https://github.com/vshulcz/deja-vu)** · **[Documentação](https://vshulcz.github.io/deja-vu/guide/getting-started.html)** · **[Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)**

![Demonstração do deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Recursos

- **Widget na barra de status**: exibe estatísticas ao vivo de `deja statusline` do projeto ativo no lado direito da barra de status da IDE. Clicar abre o menu de ações do `deja-vu`.
- **Pesquisar sessões de IA (`Tools → deja-vu → Search AI Sessions...`)**: consulta sessões locais em todos os 49 agentes suportados (`deja search --json`) e abre a transcrição selecionada em uma aba Markdown somente leitura.
- **Sessões recentes (`Tools → deja-vu → Recent AI Sessions`)**: lista as 50 sessões mais recentes na máquina (`deja last 50 --json`) com filtro de busca rápida.
- **AI Blame na linha atual (menu de contexto / `Tools → deja-vu → AI Blame Current Line`)**: executa `deja blame <file>:<line> --json` (com fallback para `<file>`) para abrir a sessão de IA que escreveu ou editou a linha sob o cursor.
- **Copiar handoff entre agentes (`Tools → deja-vu → Copy Cross-Agent Handoff`)**: executa `deja handoff` na raiz do projeto, copia o resumo para a área de transferência e o abre em uma aba Markdown somente leitura.

## Instalação

1. Instale a CLI `deja` e conecte seus agentes locais:

```sh
brew install deja-vu && deja install --auto
# ou sem Homebrew:
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

2. Instale o plugin da JetBrains ([`com.github.vshulcz.deja-vu` no JetBrains Marketplace](https://plugins.jetbrains.com/plugin/35998-deja-vu-cross-agent-ai-session-memory)):

- **Pelo JetBrains Marketplace**: [instale direto de plugins.jetbrains.com](https://plugins.jetbrains.com/plugin/35998-deja-vu-cross-agent-ai-session-memory) ou abra **Settings → Plugins → Marketplace** e pesquise `deja-vu`.
- **Pelo disco**: use **Settings → Plugins → ⚙️ → Install Plugin from Disk...** com `deja-jetbrains.zip` obtido em [GitHub Releases](https://github.com/vshulcz/deja-vu/releases/latest).
- **Pela CLI**:

```sh
idea installPlugins com.github.vshulcz.deja-vu
```

*(Substitua `idea` por `goland`, `pycharm`, `webstorm`, `rustrover` ou `clion` conforme a sua IDE.)*

## Compilar do código-fonte

```sh
cd extensions/jetbrains
./gradlew buildPlugin
```

O arquivo instalável é gravado em `build/distributions/`.

## Benchmarks

| Recall | Tokens | Indexação e busca |
|---|---|---|
| **97.2% R@5** no LongMemEval-S (88.9% hit@1 em 470 perguntas; MemPalace publica 96.6%, agentmemory 95.2%) | **58% menos tokens** (**metade dos tokens** do agentmemory em tarefas repetidas) | **13.6 s** para indexar 19,195 sessões / **0.7 ms** de busca entre 49 agentes de código (próximo de 7: 72 s) |

## Memória do repositório e handoff entre agentes

Compartilhe decisões validadas e caminhos descartados via git em `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). As entradas passam por redação de segredos ao gravar, ficam ordenadas por hash com `merge=union` para mesclar branches paralelos sem conflito e carregam para qualquer pessoa que clonar o repositório (`{"activations":{"auto":{"repo":false}}}` na política de confiança desativa a injeção via hooks). [Saiba mais](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Trocando de ferramenta no meio da tarefa? Execute `deja handoff --to <agent>` (ou chame `handoff` via MCP) para transferir objetivo, decisões, arquivos alterados, testes que passaram e trabalho em aberto para outro agente.

## Licença

MIT — [github.com/vshulcz/deja-vu](https://github.com/vshulcz/deja-vu)

Achou útil? [Dê uma estrela ao deja-vu no GitHub](https://github.com/vshulcz/deja-vu).
