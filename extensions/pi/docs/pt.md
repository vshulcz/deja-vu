# @vshulcz/pi-deja

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | Português | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

Parte do **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** no LongMemEval-S · **13.6 s** para indexar 19,195 sessões · **metade dos tokens** do agentmemory) · [Documentação](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Demo ao vivo](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html) · **[★ Dar estrela no GitHub](https://github.com/vshulcz/deja-vu)**

![Demonstração do deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

Memória local para o [pi](https://github.com/badlogic/pi-mono) alimentada pelo histórico de sessões dos outros 48 agentes de programação na sua máquina (Claude Code, Codex, Cursor, Gemini, OpenClaw, Hermes e mais). Roda sobre um único binário Go (`deja`) com índice BM25 local, sem LLM e sem embeddings; a rede só é usada em comandos explícitos ou na checagem diária de release, que `DEJA_OFFLINE=1` desativa.

## Instalação

```sh
pi install npm:@vshulcz/pi-deja
```

Caso você ainda não tenha o `deja` instalado no sistema, o pacote usa a cópia embarcada conforme a ordem em [Qual binário é usado](#qual-binário-é-usado). Para instalar o binário do sistema manualmente:

```sh
brew install deja-vu
```

Se uma extensão standalone instalada via `deja install pi-auto` ou `deja install --auto` já estiver presente, este pacote se desativa para evitar registro duplo ou memória duplicada. O mesmo fluxo funciona no `omp` usando `pi.extensions` e `before_agent_start`.

## O que ela faz

- **Início da sessão (status no rodapé)**: exibe a contagem de sessões indexadas e recuperações no projeto atual.
- **Em cada prompt (`before_agent_start`)**: busca decisões e investigações anteriores do projeto antes da resposta do modelo.
- **Após comando com falha (`tool_result` de `bash` / `powershell`)**: mostra qual comando corrigiu esse mesmo erro anteriormente.
- **Em leituras e edições (`tool_result` de read/edit/write)**: traz anotações de sessões passadas sobre o arquivo em questão.
- **Após compactação (`session_compact`)**: devolve objetivo atual, conclusões e comandos pendentes logo depois do resumo.
- **Fim da sessão (`session_shutdown`)**: atualiza o índice com a sessão recém-encerrada.
- **`/deja <query>`**: pesquisa direta pelo histórico completo direto da conversa.

## Qual binário é usado

A resolução do binário `deja` segue esta ordem:

1. Variável de ambiente `DEJA_BIN`
2. `deja` encontrado no `PATH`
3. Caminhos padrão de instalação (`~/.local/bin`, `/opt/homebrew/bin`, `/usr/local/bin` etc.)
4. Binário empacotado em `@vshulcz/deja-vu`

## Benchmarks

| Recall | Tokens | Indexação e busca |
|---|---|---|
| **97.2% R@5** no LongMemEval-S (88.9% hit@1 em 470 perguntas; MemPalace publica 96.6%, agentmemory 95.2%) | **58% menos tokens** (**metade dos tokens** do agentmemory em tarefas repetidas) | **13.6 s** para indexar 19,195 sessões / **0.7 ms** de busca entre 49 agentes de código (próximo de 7: 72 s) |

## Memória do repositório e handoff entre agentes

Compartilhe decisões validadas e caminhos descartados via git em `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). As entradas passam por redação de segredos ao gravar, ficam ordenadas por hash com `merge=union` para mesclar branches paralelos sem conflito e carregam para qualquer pessoa que clonar o repositório (`{"activations":{"auto":{"repo":false}}}` na política de confiança desativa a injeção via hooks). [Saiba mais](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Trocando de ferramenta no meio da tarefa? Execute `deja handoff --to <agent>` (ou chame `handoff` via MCP) para transferir objetivo, decisões, arquivos alterados, testes que passaram e trabalho em aberto para outro agente.

## Privacidade

O índice fica em `~/.cache/deja/index.db` (configurável por `DEJA_INDEX_DIR`) e remove credenciais como `sk-*`, `ghp_*`, `AKIA*` e `Bearer` antes de salvar. Nada sai da máquina, exceto se você rodar `deja embed` manualmente. Configure `recall`, `fix`, `blame` e `handoff` em todos os 49 agentes com `deja install --auto`.

## Licença

Parte do [deja-vu](https://github.com/vshulcz/deja-vu). MIT. Achou útil? [Dê uma estrela ao deja-vu no GitHub](https://github.com/vshulcz/deja-vu).
