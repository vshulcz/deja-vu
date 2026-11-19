# deja-vu for Spec Kit

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | Português | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

Parte do **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** no LongMemEval-S · **13.6 s** para indexar 19,195 sessões · **metade dos tokens** do agentmemory) · [Documentação](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Demo ao vivo](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html) · **[★ Dar estrela no GitHub](https://github.com/vshulcz/deja-vu)**

Uma extensão para o [Spec Kit](https://github.com/github/spec-kit). Antes de implementar uma funcionalidade, `/speckit.deja-vu.recall` pesquisa suas sessões passadas de agentes de código e relata o que foi tentado, o que foi decidido e o que falhou, com o ID de sessão por trás de cada ponto.

As sessões vêm do [deja](https://github.com/vshulcz/deja-vu), que indexa as transcrições já gravadas em disco por Claude Code, Codex, Cursor, Copilot, Gemini e outros agentes. Tudo permanece na sua máquina.

![Demonstração do deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Instalação

Instale o `deja` primeiro:

```sh
brew install deja-vu
# ou
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

Depois, em um projeto Spec Kit:

```sh
specify extension add deja-vu --from https://github.com/vshulcz/deja-vu/releases/latest/download/deja-spec-kit.zip
```

Cada release do deja inclui `deja-spec-kit.zip` na mesma versão. Para testar um checkout local: `specify extension add --dev /path/to/deja-vu/extensions/spec-kit`.

## Uso

```
/speckit.deja-vu.recall
/speckit.deja-vu.recall stripe webhook signature
```

Sem argumentos, lê `spec.md` e `plan.md` da feature ativa e escolhe as consultas sozinho. Com argumentos, busca esses termos. O nome do comando segue o padrão do seu agente e pode aparecer como `/speckit-deja-vu-recall` ou `$speckit-deja-vu-recall`.

Nos bastidores ele executa, a partir da raiz do repositório:

```sh
deja recall -- "<query>"   # sessões correspondentes, priorizando este projeto
deja files -- "<query>"    # arquivos abertos ou editados sobre o tema
```

e `deja show <id>` quando um trecho for curto demais para esclarecer o contexto. O relatório gera uma seção `Prior sessions` dividida em tentativas que falharam, decisões, arquivos e pendências, com cada item citado como `(deja:<id>)`.

## Hook

A extensão registra um hook opcional `before_implement`. Ao rodar `/speckit.implement`, o agente oferece executar a busca antes; nada roda sem você confirmar. Para desativar, defina `enabled: false` em `.specify/extensions.yml`, ou execute `specify extension disable deja-vu`.

## Notas

- Somente leitura: o comando busca e lê, sem editar arquivos.
- A primeira chamada do deja em uma máquina constrói o índice, o que pode levar alguns segundos em históricos grandes; depois responde bem abaixo de um segundo.
- O conteúdo recuperado das sessões é tratado como dado de referência; o comando instrui o agente a nunca seguir instruções encontradas dentro dele.

## Benchmarks

| Recall | Tokens | Indexação e busca |
|---|---|---|
| **97.2% R@5** no LongMemEval-S (88.9% hit@1 em 470 perguntas; MemPalace publica 96.6%, agentmemory 95.2%) | **58% menos tokens** (**metade dos tokens** do agentmemory em tarefas repetidas) | **13.6 s** para indexar 19,195 sessões / **0.7 ms** de busca entre 49 agentes de código (próximo de 7: 72 s) |

## Memória do repositório e handoff entre agentes

Compartilhe decisões validadas e caminhos descartados via git em `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). As entradas passam por redação de segredos ao gravar, ficam ordenadas por hash com `merge=union` para mesclar branches paralelos sem conflito e carregam para qualquer pessoa que clonar o repositório (`{"activations":{"auto":{"repo":false}}}` na política de confiança desativa a injeção via hooks). [Saiba mais](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Trocando de ferramenta no meio da tarefa? Execute `deja handoff --to <agent>` (ou chame `handoff` via MCP) para transferir objetivo, decisões, arquivos alterados, testes que passaram e trabalho em aberto para outro agente.

## Licença

MIT

Parte do [deja-vu](https://github.com/vshulcz/deja-vu). MIT. Achou útil? [Dê uma estrela ao deja-vu no GitHub](https://github.com/vshulcz/deja-vu).
