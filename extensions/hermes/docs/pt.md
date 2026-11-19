# deja-memory for Hermes

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | Português | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

Parte do **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** no LongMemEval-S) · [Documentação](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Demo ao vivo](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

Um provedor de memória para o Hermes que responde a partir das sessões de código já presentes nesta máquina: Claude Code, Codex, Cursor, o próprio Hermes e os demais agentes lidos pelo deja, incluindo os meses anteriores a qualquer instalação. Sem modelo adicional, sem chave de API e sem daemon: todo o histórico é indexado localmente pelo [deja](https://github.com/vshulcz/deja-vu), um único binário em Go.

![Demonstração do deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Instalação

O `deja` precisa estar no `PATH` primeiro:

```bash
brew install deja-vu        # ou: curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto         # conecta o Hermes e todos os agentes suportados neste computador
hermes plugins install deja-memory   # ou via GitHub: hermes plugins install vshulcz/deja-vu/extensions/hermes
hermes config set memory.provider deja-memory
```

ou escolha `deja-memory` em `hermes memory setup`, que verifica o binário antes de ativar o provedor.

## O que ele faz

- **Antes do primeiro turno da sessão**: entrega ao Hermes o mesmo resumo de projeto que os hooks do deja enviam ao Claude Code e ao Codex — trabalho recente neste projeto e decisões consolidadas. Depois disso, só atua quando a pergunta atual combina com algo anterior; na maior parte dos turnos não acrescenta nada.
- **Transparência do que foi trazido**: as linhas mostradas pelo deja no Claude Code (o recibo com o agente de origem, o contexto de início de sessão e as notas semanais ou de indexação) seguem na linha `🧠 deja · …` do Hermes na CLI interativa e em `hermes chat -q`. O texto do modelo permanece intacto. O Hermes 0.17 não tem linha de recall, `hermes -z` e `hermes chat -Q` não imprimem nada, a TUI apenas pisca na barra de status e um chat de gateway pode ser um grupo compartilhado, então nesses casos o deja guarda as notas para uma sessão que possa exibi-las.
- **Sem recall automático em cron**: quando o Hermes informa plataforma `cron`, o provedor não injeta contexto sozinho, mas as ferramentas do deja continuam respondendo quando chamadas.
- **Três ferramentas**: `deja_recall` busca sessões antigas, `deja_fix` mostra o que rodou após um erro da última vez e `deja_blame` lista as sessões que editaram um arquivo.
- **Notas com prioridade**: entradas adicionadas pelo Hermes em `MEMORY.md` ou `USER.md` também viram notas do deja e ranqueiam acima das sessões vizinhas.
- **Compactação preservada**: preserva no turno seguinte o ponto em que a sessão estava após uma compactação e remove o selo ativo ao encerrar a sessão.

Uma coisa um provedor de memória não consegue fazer sozinho: colocar a correção anterior ao lado de um comando que acabou de falhar. Isso depende de `transform_tool_result`, um hook de plugin cujo `register_hook` no contexto do provedor de memória do Hermes não faz nada (`plugins/memory/__init__.py`, 0.17.0). `deja install hermes-auto` instala o plugin de hooks responsável por isso.

Ele nunca regrava turnos do Hermes em outro lugar: o Hermes já os armazena e o deja lê diretamente na próxima atualização.

## Benchmarks

| Recall | Tokens | Indexação e busca |
|---|---|---|
| **97.2% R@5** no LongMemEval-S (88.9% hit@1 em 470 perguntas) | **58% menos tokens** (em tarefas repetidas) | **13.6 s** para indexar 19,195 sessões / **0.7 ms** de busca entre 49 agentes de código |

## Memória do repositório e handoff entre agentes

Compartilhe decisões validadas e caminhos descartados via git em `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). As entradas passam por redação de segredos ao gravar, ficam ordenadas por hash com `merge=union` para mesclar branches paralelos sem conflito e carregam para qualquer pessoa que clonar o repositório (`{"activations":{"auto":{"repo":false}}}` na política de confiança desativa a injeção via hooks). [Saiba mais](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Trocando de ferramenta no meio da tarefa? Execute `deja handoff --to <agent>` (ou chame `handoff` via MCP) para transferir objetivo, decisões, arquivos alterados, testes que passaram e trabalho em aberto para outro agente.

## Mesmo código do `deja install`

`deja install hermes-auto` grava este provedor em `~/.hermes/plugins/deja-memory` junto com o servidor MCP e um plugin de hook `pre_llm_call`. Os arquivos aqui são gerados a partir da mesma fonte (`cmd/deja/install_hermes_memory.go`); a única diferença é que esta cópia procura o `deja` no `PATH` em vez de fixar o caminho. `TestHermesExtensionIsTheProviderInstallWrites` falha se os dois divergirem.

Parte do [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
