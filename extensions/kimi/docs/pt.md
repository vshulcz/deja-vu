# deja for Kimi Code

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | Português | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

Parte do **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** no LongMemEval-S) · [Documentação](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Demo ao vivo](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

O [deja](https://github.com/vshulcz/deja-vu) indexa os arquivos de sessão que os agentes de código já gravam em disco — Claude Code, Codex, Cursor, opencode e mais 48 — e responde a partir deles. Este plugin traz esse índice para o Kimi Code: o histórico relevante chega junto com o prompt, e o agente pode pesquisar por conta própria.

![Demonstração do deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Instalação

```
/plugins install https://github.com/vshulcz/deja-vu
/reload
```

Essa sintaxe fixa a release mais recente e registra a origem, permitindo que o verificador de atualizações do Kimi a acompanhe. Também existe o ativo `kimi-deja.zip` (~10 KB em vez do repositório inteiro) para listagens de marketplace ou instalações enxutas.

Instale o binário caso ainda não o tenha (e opcionalmente conecte os outros agentes desta máquina):

```
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

Os plugins carregam da cópia gerenciada, então execute `/reload` ou `/new` depois de instalar.

## Atualizações

O Kimi só avisa sobre atualizações para plugins do marketplace próprio. Em instalações via repositório, rodar `/plugins install` novamente puxa a versão atual, e `deja doctor` aponta quando sua cópia está atrás da embarcada neste deja:

```
kimi  plugin  ~/.kimi-code/config.toml  (v0.21.2 installed, v0.22.0 ships with this deja — reinstall it in Kimi to update)
```

## O que você recebe

- **Os hooks gravados por `deja install kimi-auto`**: no primeiro prompt da sessão, o que o projeto definiu (`deja hook-context --once`). Em cada prompt, recall relevante ao pedido (`deja hook-prompt`); o Kimi anexa ao turno e não injeta nada quando não há correspondência. Antes da compactação lê os turnos prestes a serem resumidos para manter o trabalho em andamento (`deja hook-precompact`). Em `SessionEnd`, limpa a marca ativa (`deja hook-session-end`).
- **Ferramentas**: declara `deja mcp` como servidor MCP com a ferramenta única `deja` em modos `recall`, `context`, `blame`, `fix`, `how`, `orient`, `remember` ou `handoff`.
- **`/deja:recall <query>`** para consultar o histórico diretamente.
- **A skill `deja-history`**, carregada no início para o agente consultar o histórico antes de depurar tudo do zero.

## Qual binário é usado

Em ordem: `DEJA_BIN`, o binário instalado por você (`~/.local/bin`, `/usr/local/bin`, `/opt/homebrew/bin`, `/usr/bin`) e então `deja` no `PATH`. Seu próprio `deja update` ou `brew upgrade deja-vu` prevalece sobre qualquer versão travada pelo plugin.

## Se você também executou `deja install kimi`

A CLI grava o mesmo servidor MCP em `~/.kimi-code/mcp.json` e o mesmo hook em `config.toml`. O Kimi executa o servidor de um plugin em namespace próprio (`plugin-deja:deja`), então ambos rodariam em dobro.

Este plugin verifica a presença da configuração do instalador quando o hook dispara e quando o servidor inicia, recuando automaticamente — pois a cópia mantida por `deja install` é a que permanece atualizada. `deja uninstall kimi` devolve o controle ao plugin sem precisar reinstalar.

`deja doctor` mostra `plugin` para o kimi quando é o plugin que provê a integração, evitando falsos alertas.

Duas coisas que ele não consegue enxergar: uma entrada MCP adicionada à mão com nome diferente de `deja` (que rodaria ao lado desta) e um hook escrito manualmente que invoque o deja sem o comentário identificador do instalador.

## Benchmarks

| Recall | Tokens | Indexação e busca |
|---|---|---|
| **97.2% R@5** no LongMemEval-S (88.9% hit@1 em 470 perguntas) | **58% menos tokens** (em tarefas repetidas) | **13.6 s** para indexar 19,195 sessões / **0.7 ms** de busca entre 52 agentes de código |

## Memória do repositório e handoff entre agentes

Compartilhe decisões validadas e caminhos descartados via git em `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). As entradas passam por redação de segredos ao gravar, ficam ordenadas por hash com `merge=union` para mesclar branches paralelos sem conflito e carregam para qualquer pessoa que clonar o repositório (`{"activations":{"auto":{"repo":false}}}` na política de confiança desativa a injeção via hooks). [Saiba mais](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Trocando de ferramenta no meio da tarefa? Execute `deja handoff --to <agent>` (ou chame `handoff` via MCP) para transferir objetivo, decisões, arquivos alterados, testes que passaram e trabalho em aberto para outro agente.

## Licença

MIT

Parte do [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
