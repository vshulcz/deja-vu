# deja for Zed

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | Português | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

Parte do **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** no LongMemEval-S) · [Documentação](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Demo ao vivo](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

O [deja](https://github.com/vshulcz/deja-vu) indexa os arquivos de sessão que os agentes de código já gravam em disco — Claude Code, Codex, Cursor, opencode e mais 45 — e responde com base neles. Esta extensão entrega esse índice ao painel de agente do Zed como context server, permitindo que uma thread busque trabalho anterior, inclusive de antes da instalação do deja.

![Demonstração do deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## O que o agente recebe

Uma única ferramenta, `deja`, chamada com um `mode`:

| Mode | Resposta |
|---|---|
| `recall` | Sessões compatíveis com uma mensagem de erro, função, caminho ou flag. |
| `context` | O resumo completo de uma sessão antiga quando o raciocínio importa. |
| `blame` | As sessões que discutiram um arquivo antes de editá-lo ou excluí-lo. |
| `fix` | O que esta máquina executou após esse mesmo erro antes. |
| `how` | O comando real de build, teste ou deploy usado neste ambiente. |
| `orient` | Comandos rodados e arquivos manipulados em sessões passadas do projeto. |
| `remember` | Salva uma decisão durável para consulta futura. |
| `handoff` | O estado de outra sessão para continuar do ponto em que parou. |

## Instalação

Instale a extensão pela lista de extensões do Zed. No primeiro uso, ela pede ao Zed para rodar `deja --version`; se funcionar, utiliza o `deja` do `PATH` do Zed. Caso contrário, baixa o binário de release no seu próprio diretório. Como extensões rodam em sandbox e não enxergam diretórios comuns, um `deja` fora do `PATH` inicial do Zed precisa ser configurado explicitamente:

```json
{
  "context_servers": {
    "deja-context-server": {
      "settings": {
        "binary": "/opt/homebrew/bin/deja"
      }
    }
  }
}
```

`deja install --auto` também configura o Zed e grava o servidor em `settings.json` diretamente — caminho mais curto quando você já usa a CLI. Ambos usam o mesmo ID, `deja-context-server`, e o Zed indexa servidores por ID, mantendo apenas uma entrada independentemente da ordem. `deja uninstall zed` remove o que a CLI adicionou.

Indexação e busca são locais: sem chamadas de rede, e credenciais são redigidas durante a indexação.

## Benchmarks

| Recall | Tokens | Indexação e busca |
|---|---|---|
| **97.2% R@5** no LongMemEval-S (88.9% hit@1 em 470 perguntas) | **58% menos tokens** (em tarefas repetidas) | **13.6 s** para indexar 19,195 sessões / **0.7 ms** de busca entre 50 agentes de código |

## Memória do repositório e handoff entre agentes

Compartilhe decisões validadas e caminhos descartados via git em `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). As entradas passam por redação de segredos ao gravar, ficam ordenadas por hash com `merge=union` para mesclar branches paralelos sem conflito e carregam para qualquer pessoa que clonar o repositório (`{"activations":{"auto":{"repo":false}}}` na política de confiança desativa a injeção via hooks). [Saiba mais](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Trocando de ferramenta no meio da tarefa? Execute `deja handoff --to <agent>` (ou chame `handoff` via MCP) para transferir objetivo, decisões, arquivos alterados, testes que passaram e trabalho em aberto para outro agente.

MIT, a mesma licença do deja.

Parte do [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
