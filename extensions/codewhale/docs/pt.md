# deja-vu para CodeWhale

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | Português | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

Pacote de plugin compatível com Claude para CodeWhale — expõe o servidor MCP `deja` (`recall`, `fix`, `blame`, `how`, `orient`, `context`, `remember`, `handoff`), a skill `deja-history` e `/deja` sobre sessões de 53 ferramentas de codificação com IA.

![Demonstração do deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Instalação

Requisitos: [`deja`](https://github.com/vshulcz/deja-vu#install) instalado no `PATH`.

Instale este pacote no CodeWhale a partir do subdiretório do repositório (`extensions/codewhale`) usando `/plugin install`.

Como o carregador de compatibilidade de plugins Claude do CodeWhale importa servidores MCP, skills e comandos slash mas ignora `hooks` e `lspServers`, execute `deja install codewhale-auto` (ou `deja install --auto`) se também quiser os hooks nativos de ciclo de vida do CodeWhale (`message_submit`, `tool_call_before`, `tool_call_after`, `session_end`) gravados em `~/.codewhale/config.toml` (`deja install codewhale` configura apenas MCP e `tools.always_load`):

```sh
deja install codewhale-auto
```

## Conteúdo do pacote

- `.claude-plugin/plugin.json`: metadados do plugin (`name: deja-vu`)
- `.mcp.json`: entrada do servidor stdio `deja mcp`
- `skills/deja-history/SKILL.md`: instruções da skill para os modos do `deja`
- `commands/deja.md`: comando slash `/deja`

## Benchmarks

| Recall | Tokens | Indexação e busca |
|---|---|---|
| **97.2% R@5** no LongMemEval-S (88.9% hit@1 em 470 perguntas) | **58% menos tokens** (em tarefas repetidas) | **13.6 s** para indexar 19,195 sessões / **0.7 ms** de busca entre 53 agentes de código |

## Memória do repositório e handoff entre agentes

Compartilhe decisões validadas e caminhos descartados via git em `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). As entradas passam por redação de segredos ao gravar, ficam ordenadas por hash com `merge=union` para mesclar branches paralelos sem conflito e carregam para qualquer pessoa que clonar o repositório (`{"activations":{"auto":{"repo":false}}}` na política de confiança desativa a injeção via hooks). [Saiba mais](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Trocando de ferramenta no meio da tarefa? Execute `deja handoff --to <agent>` (ou chame `handoff` via MCP) para transferir objetivo, decisões, arquivos alterados, testes que passaram e trabalho em aberto para outro agente.

## Licença

[MIT](../LICENSE)
