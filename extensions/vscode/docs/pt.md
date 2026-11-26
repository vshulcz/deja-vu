# deja-vu para VS Code, Cursor, Windsurf & Antigravity

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | Português | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

**Seus agentes param de depurar do zero o que você já resolveu.**

[deja-vu](https://github.com/vshulcz/deja-vu) indexa localmente os transcritos de sessão que Claude Code, Cursor, Codex, Windsurf, Copilot Chat, OpenCode, Cline, Roo Code e mais 41 agentes já gravam em disco — em um único binário Go, sem LLM e sem daemon em segundo plano.

**[Documentação](https://vshulcz.github.io/deja-vu/guide/getting-started.html)** · **[Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)**

![Demonstração do deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## O que esta extensão faz

Exibe o estado ao vivo de `deja statusline` na barra de status do IDE (sessões indexadas entre agentes, memórias servidas hoje e histórico do workspace). Se o `deja` ainda não estiver instalado, clicar no item executa `npx -y @vshulcz/deja-vu install --auto` no terminal para conectar todos os agentes detectados.

## Instalação rápida

```sh
brew install deja-vu && deja install --auto
# ou sem Homebrew:
npx -y @vshulcz/deja-vu install --auto
```

Instale a extensão da barra de status via CLI:

```sh
code --install-extension vshulcz.deja-status
```

## Impacto medido

| Benchmark | Resultado |
|---|---|
| Acurácia no LongMemEval-S (470 perguntas filtradas) | **97.2% R@5**, **88.9% hit@1** |
| Custo de tokens em trabalho repetido no repositório | **58% menos tokens**|
| Latência de consulta lexical em processo | Mediana de **0.7–0.8 ms** em 53 agentes de código |
| Indexação inicial de 19,195 sessões reais | **13.6 s**, sem chaves de API |

## Memória do repositório e handoff entre agentes

Compartilhe decisões validadas e caminhos descartados via git em `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). As entradas passam por redação de segredos ao gravar, ficam ordenadas por hash com `merge=union` para mesclar branches paralelos sem conflito e carregam para qualquer pessoa que clonar o repositório (`{"activations":{"auto":{"repo":false}}}` na política de confiança desativa a injeção via hooks). [Saiba mais](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Trocando de ferramenta no meio da tarefa? Execute `deja handoff --to <agent>` (ou chame `handoff` via MCP) para transferir objetivo, decisões, arquivos alterados, testes que passaram e trabalho em aberto para outro agente.

## Licença

MIT — [github.com/vshulcz/deja-vu](https://github.com/vshulcz/deja-vu)

Parte do [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
