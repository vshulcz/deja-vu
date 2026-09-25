<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/logo-dark.svg">
    <img src="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/logo.svg" width="330" alt="deja-vu">
  </picture>
</p>

<p align="center"><b>Uma só memória para todos os seus agentes de código, construída com o histórico que já está no seu disco.</b></p>

<p align="center">Seu agente está prestes a depurar de novo algo que você resolveu em março, naquela vez em outro
agente. O deja indexa as sessões que Claude Code, Codex, Cursor e os demais agentes desta máquina já escrevem
em disco, e devolve a certa para qualquer um deles que perguntar.</p>

<p align="center"><img src="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif" width="720" alt="a mesma pergunta ao mesmo agente duas vezes: sem memória ele não lembra de nada, com o deja responde com uma conclusão de oito meses atrás"></p>

<p align="center"><sub><em>Ninguém pesquisou nada: o agente chamou o deja por conta própria. Duas execuções reais, modelo real, chamadas de ferramenta reais, sobre um corpus sintético: o histórico de ninguém é publicado.</em></sub></p>

<p align="center"><b>O deja já chega cheio: o histórico que 34 agentes escreveram, indexado em segundos, sem modelo e sem uma etapa separada de captura.</b></p>

<p align="center">
<b>58% menos tokens</b> numa tarefa que esta máquina já havia resolvido &middot; <b>88.1% hit@1</b> no LongMemEval-S (conjunto limpo de 470 perguntas) &middot; <b>70.5%</b> no LoCoMo &middot; consultas em <b>milissegundos</b> sobre gigabytes de histórico<br>
<sub>Onze execuções por braço: 53,558 tokens contra 126,222 sem nada conectado, e 52,815 contra 103,443 numa versão posterior, de novo onze execuções &middot;
<a href="https://vshulcz.github.io/deja-vu/guide/day-zero.html">quanto custa terminar uma tarefa</a> &middot;
os dois harnesses de recuperação estão neste repositório e rodam sobre datasets públicos em minutos &middot;
<a href="https://vshulcz.github.io/deja-vu/guide/benchmarks.html">confira os números você mesmo</a></sub>
</p>

<p align="center"><a href="../../README.md">English</a> | <a href="README.zh.md">简体中文</a> | <a href="README.zh-TW.md">繁體中文</a> | <a href="README.ja.md">日本語</a> | <a href="README.ko.md">한국어</a> | <a href="README.es.md">Español</a> | Português | <a href="README.fr.md">Français</a> | <a href="README.de.md">Deutsch</a> | <a href="README.ru.md">Русский</a> | <a href="README.tr.md">Türkçe</a> | <a href="README.hi.md">हिन्दी</a></p>

<p align="center"><a href="https://vshulcz.github.io/deja-vu/">Documentação</a> &middot; <a href="https://vshulcz.github.io/deja-vu/guide/benchmarks.html">Benchmarks</a> &middot; <a href="https://vshulcz.github.io/deja-vu/guide/compare.html">Comparação</a></p>
<p align="center"><sub>Se for útil, deixe uma estrela no deja-vu no <a href="https://github.com/vshulcz/deja-vu">GitHub</a>.</sub></p>

## Instalação

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

Se o `raw.githubusercontent.com` não estiver acessível, existe um espelho no npm com a mesma versão:

```sh
npm i -g @vshulcz/deja-vu --registry=https://registry.npmmirror.com
deja install --auto
```

Dez segundos para instalar, uns dez para indexar. O segundo comando conecta o recall via MCP em cada agente que
encontrar, liga o recall no início da sessão onde o agente suporta isso, e monta o primeiro índice, para que a
sessão seguinte não espere nada.

Abra uma sessão nova e pergunte sobre algo de meses atrás:

> a gente já mexeu com jwt refresh rotation antes? procura na sua memória

Nem é preciso perguntar: com o recall automático ligado, o agente já sabe na abertura da sessão o que foi
resolvido neste projeto.

opencode, DeepSeek Harness, Zed, Kimi Code, Codex CLI, Grok Build, OpenClaw e pi também têm pacote no próprio
ecossistema, para quem está acostumado a instalar extensões por lá:

```sh
opencode plugin opencode-deja
dsh plugin --profile web add dsh-deja
# Zed: procure deja no painel de extensões
# Kimi Code: /plugins install https://github.com/vshulcz/deja-vu
# Codex CLI: codex plugin marketplace add https://github.com/vshulcz/deja-vu && codex plugin add deja-vu@deja-vu
# Grok Build: grok plugin marketplace add xai-org/plugin-marketplace && grok plugin install deja
openclaw plugins install clawhub:@vshulcz/openclaw-deja
pi install npm:@vshulcz/pi-deja
```

O `deja install --auto` já conecta tudo isso, então qualquer um dos dois caminhos basta. Os dois juntos também
não incomodam: cada pacote lê o que o `deja install` escreveu e completa só o que falta, sem registrar
ferramentas em duplicidade nem fazer o recall duas vezes. Detalhes em [`extensions/`](../../extensions).

A mesma busca existe como skill, instalável por qualquer agente que leia `SKILL.md`:

```sh
npx skills add https://github.com/vshulcz/deja-vu --skill deja-search   # skills CLI: Claude Code, Cursor, Goose, Copilot…
openclaw skills install @vshulcz/deja-search                            # ClawHub
hermes skills install vshulcz/deja-vu/skills/deja-search                # Hermes
```

O skill chama o binário `deja` que você já instalou; não traz um próprio.

Outros caminhos: `brew install deja-vu`,
`go install github.com/vshulcz/deja-vu/cmd/deja@latest`, ou `npx @vshulcz/deja-vu "consulta"` para
experimentar sem instalar nada. No Windows o script de instalação termina com `unsupported OS` porque é um
script de shell; pegue `deja-vu_<version>_windows_amd64.zip` da
[última release](https://github.com/vshulcz/deja-vu/releases/latest) e coloque `deja.exe` no `PATH`.

Só o binário já é uma instalação completa: indexar, buscar, `show`, `ctx`, `blame`, `--json` e a limpeza de
credenciais não precisam de mais nada. O que o `deja install` faz é plugar o MCP nos seus agentes e ligar o
recall no início da sessão — vale a pena, mas é opcional.

## O que você ganha

**Resolvido no Codex, lembrado pelo Claude.** Trinta e quatro agentes de código escrevem cada conversa em
arquivos locais, e o deja transforma esses arquivos numa camada de memória que todos eles conseguem ler.

| | |
| --- | --- |
| **Busca para trás no tempo** | `deja "connection pool exhausted"` varre gigabytes, incluindo tudo o que veio antes de você instalar o deja. Uma pergunta em linguagem natural cai no modo por relevância. Tempo é uma dica, não um filtro. |
| **Recall entre agentes** | A ferramenta MCP `deja` no modo `recall` responde «isso a gente corrigiu três semanas atrás» de dentro de qualquer agente, não importa quem corrigiu na época. |
| **Sobrevive à compactação** | Medido em 43 compactações: o resumo preservou 77% das decisões e 0.2% dos comandos que você rodou. Os 99.8% restantes o deja devolve. No Claude Code e no Codex, o deja anota a tarefa, os arquivos e os comandos no instante em que a compactação começa, e devolve tudo de uma vez na sessão seguinte. |
| **Recall na hora de agir** | Antes de o agente editar um arquivo ou rodar um comando, o hook `PreToolUse` diz o que já foi decidido sobre aquele arquivo, qual forma daquele comando funciona aqui, ou que o programa simplesmente não existe nesta máquina. Quando um comando falha, o hook `PostToolUse` mostra o que foi rodado depois do mesmo erro nesta máquina — exatamente o par que o agente não vai perguntar. |
| **Indexa o trabalho, não só a conversa** | Cada arquivo aberto em cada turno, cada comando executado com seu código de saída, e o trecho exato que uma edição substituiu. Justamente o que todo resumo perde. |

Além disso: `deja promote <id> --state rejected` marca uma decisão revertida, e daí em diante todo resultado
mostra que aquilo foi tentado e descartado; um resultado avisa que «4 arquivos desta sessão mudaram desde
então» e fica calado quando não tem como saber; `deja sync ssh laptop` move a memória entre máquinas só
acrescentando, sem nuvem no meio; `deja handoff --to codex` empacota o contexto atual para continuar em outro
agente; chaves, tokens, JWT e blocos de chave privada de formatos conhecidos são removidos na indexação — mas
casar padrão não é detectar segredo, e um formato desconhecido pode passar.

### Desenhe o seu próprio trabalho

`deja stats --card` desenha direto no terminal; passe um nome de arquivo e ele escreve um SVG para o README do
seu perfil. Para publicar em outro lugar,
[converta para PNG](https://vshulcz.github.io/deja-vu/card/) — essa página converte no seu próprio navegador.

<p align="center"><img src="https://raw.githubusercontent.com/vshulcz/deja-vu/main/docs/assets/stats-card-demo.svg" width="760" alt="card de estatísticas do deja: um ano de sessões em mapa de calor, de quais agentes vieram e qual foi a mais longa"></p>

A referência completa está no [site da documentação](https://vshulcz.github.io/deja-vu/).

## Privacidade

Indexar e buscar são operações locais. Só usam a rede `deja update`, `deja sync ssh`, a checagem de versão
dentro do `deja doctor`, e o `deja embed`, que vai ao endpoint que você configurar.

Credenciais são limpas na indexação: chaves AWS, atribuições `api_key=` e `token=`, tokens bearer e JWT nus,
blocos PEM de chave privada, tokens de vários provedores, URLs no formato `scheme://user:pass@host`, valores de
alta entropia que nenhuma regra cobre, e senhas escritas em prosa — «the admin password is …», onde não há
separador nenhum em que se apoiar. O valor vira `[redacted:<kind>]` e o texto ao redor continua pesquisável.
`deja share` e `deja sync export` limpam de novo na exportação. Casar padrão não é detectar segredo: um formato
que as regras não conhecem pode passar do jeito que está — veja o modelo de segurança.

`deja forget` tira sessões do índice reconstruído e deixa uma lápide, então o `deja index` seguinte não
consegue trazê-las de volta do histórico original.
O [modelo de segurança](../../docs/SECURITY-MODEL.md) documenta os fluxos de dados, os limites da limpeza, as
premissas de confiança e a verificação das releases.

## Linha de comando

```text
$ deja "jwt refresh token"
[claude] api        · Jul 8 · 8f31c0a9 — 2 matches
  login started failing after refresh token rotation; jwt kid mismatch in tests
  fixed by reloading jwks cache after rotateKey and adding a clock-skew test
[codex]  web        · Jul 1 · b77d91e2 — 1 match
  refresh token cookie needed SameSite=Lax in local callback flow
```

| Comando | O que faz |
| --- | --- |
| `deja <consulta>` | Busca em todo o histórico. Várias palavras são AND, aspas exigem texto contíguo; sem correspondência exata ele tenta formas da palavra e grafias próximas. |
| `deja wip` | O que a última sessão neste diretório estava fazendo: a tarefa, o que ficou decidido, os arquivos em mão, o último comando e se ele falhou. Tudo deduzido dos registros, sem depender de alguém ter anotado. |
| `deja blame <caminho>[:linha]` | Quais sessões discutiram este arquivo, o que foi decidido e por quê. Com número de linha: o commit que a alterou por último e a sessão que escreveu o texto que esse commit apagou. |
| `deja files <assunto>` | Na direção contrária: quais arquivos o trabalho sobre um assunto realmente mexeu. |
| `deja how <ferramenta>` | Como isso é executado de verdade nesta máquina, com argumentos reais, tirados de comandos que os agentes já rodaram. |
| `deja fix <erro>` | O que foi executado depois do mesmo erro nesta máquina, e depois do quê o erro não voltou. |
| `deja friction` | Erros que aparecem em mais de três sessões diferentes, dizendo de quais ferramentas vieram. |
| `deja ctx <consulta>` | Resumo em Markdown dos melhores resultados, pronto para entrar num prompt. |
| `deja resume <id>` | Reabre a sessão encontrada na ferramenta a que ela pertence. |
| `deja view` | Exporta toda a memória para um único arquivo HTML local. Sem servidor, nada sai da máquina. |
| `deja doctor [--deep]` | Autodiagnóstico; com `--deep` verifica o índice contra os arquivos de origem. |
| `deja mcp` | O servidor MCP por stdio, o mesmo que o `deja install` conecta. |

A referência completa está na [documentação dos comandos](https://vshulcz.github.io/deja-vu/guide/commands.html).

### Ferramentas MCP

O servidor expõe uma única ferramenta, `deja`, e o parâmetro `mode` escolhe a capacidade: `recall`, `context`,
`blame`, `fix`, `how`, `remember`. O `deja install` conecta sozinho, então isso só importa se você configurar
um agente à mão. Os seis nomes antigos continuam funcionando nos clientes já conectados.

Uma ferramenta em vez de sete é questão de custo, não de estilo. Um servidor MCP conectado manda as definições
das suas ferramentas em cada requisição, então elas são pagas a cada turno, tenha o agente chamado algo ou não:
477 tokens aqui, contra 8,283 do maior dos oito servidores medidos. O do próprio deja era 828 até o schema ser
reduzido a uma ferramenta com modos.

## Ferramentas suportadas

Com o recall automático ligado, Claude Code e Codex entregam ao deja o registro atual quando a compactação
começa, e o deja guarda o que o resumo está a ponto de perder: a tarefa, as conclusões, os arquivos, no que
cada comando deu, e o que ficou pendente. O hook seguinte da mesma sessão e do mesmo diretório devolve isso de
uma vez, sem passar de 4 KB, com uma linha sobre se o repositório mudou desde então. O `deja stats` conta as
chamadas de ferramenta entre a compactação e a primeira edição, que foi com o que isso se mediu.
Detalhes em [recuperação depois da compactação](../../docs/compaction.md).

Claude Code · Cline · Codex CLI · opencode · aider · Gemini CLI · Cursor · Antigravity ·
Grok Build · Hermes · Goose · Qwen Code · Kimi Code · pi · omp (Oh My Pi) · OpenClaw ·
Copilot CLI · VS Code Copilot Chat · Amp · prime-agent (PrimeIntellect) · Roo Code ·
Continue · Crush · DeepSeek Harness · Cherry Studio · Senpi · gajae-code · Kimchi Coding ·
Command Code · ZCode · CodeWhale · Kiro · Kilo Code · Zed.

O que cada um suporta — recall por MCP, recall automático, skills, comandos, resume, handoff — está na
[matriz de recursos do README em inglês](../../README.md#supported-harnesses). Locais de armazenamento customizados
se indicam com variáveis `DEJA_*_ROOT`, e as variáveis de migração de cada ferramenta também são respeitadas.

### Agentes com pacote próprio

O `deja install --auto` conecta estes como qualquer outro, e esse é sempre o caminho mais curto. Além disso
cada um tem um pacote no próprio ecossistema, para quem instala extensões por lá:

| Agente | Pacote | Instalação |
| --- | --- | --- |
| opencode | npm `opencode-deja` | `opencode plugin opencode-deja` |
| DeepSeek Harness | npm `dsh-deja` | `dsh plugin --profile web add dsh-deja` |
| Zed | `deja-context-server` | Zed → Extensions → deja |
| Kimi Code | plugin `deja` | `/plugins install https://github.com/vshulcz/deja-vu` |
| Codex CLI | plugin `deja-vu` | `codex plugin marketplace add https://github.com/vshulcz/deja-vu`, depois `codex plugin add deja-vu@deja-vu` |
| Grok Build | plugin `deja` | `grok plugin marketplace add xai-org/plugin-marketplace`, depois `grok plugin install deja` |
| OpenClaw | ClawHub e npm `@vshulcz/openclaw-deja` | `openclaw plugins install clawhub:@vshulcz/openclaw-deja` |
| pi (e omp) | npm `@vshulcz/pi-deja` | `pi install npm:@vshulcz/pi-deja` |

Qualquer um dos dois caminhos basta e os dois juntos não quebram nada: cada pacote lê primeiro o que o
`deja install` deixou escrito. opencode, dsh e OpenClaw só completam o que falta; Kimi, Grok, Codex e pi saem
de cena se o instalador já conectou; no Zed os dois lados usam o mesmo id de servidor. Então a ordem de
instalação não importa.

Todos usam o deja que você já instalou; a cópia dentro do pacote é só o plano B.

## Recall semântico, opcional

Aponte o `deja embed` para um Ollama local, LM Studio ou qualquer endpoint compatível com OpenAI via
`DEJA_EMBED_URL` e perguntar com outras palavras também acerta. Sem runtime disponível, a busca léxica e o
recall por MCP funcionam como sempre.

## Evidência

```sh
deja bench recall     # piso de regressão do ranking: 100 consultas, metade em russo, CI falha se o recall cair
deja bench context    # 30 cadeias de tarefas com semente mais cinco controles negativos
deja bench block      # se a resposta continua no trecho de texto entregue
deja bench prompt     # quando o hook por prompt fala e quando fala errado
deja bench ingest     # custo de uma atualização do índice: sem mudança, um turno acrescentado, um arquivo novo, reescrita completa
```

O experimento de contexto compara o recall do deja com o histórico completo, um grep ingênuo e a partida a
frio. Com a semente padrão:

| Abordagem | Tokens (mediana) | Cobertura (mediana) | Tokens do controle negativo |
| --- | ---: | ---: | ---: |
| deja-recall | 1,096 | 1.00 | 0 |
| full-history | 80,547 | 1.00 | 78,145 |
| naive-grep | 273,238 | 1.00 | 0 |
| cold | 0 | 0.00 | 0 |

A mesma cobertura de fatos que dar grep nos logs crus com cerca de 250 vezes menos tokens; umas 70 vezes menos
que as sessões encontradas reproduzindo tudo; e nada injetado nas cadeias sem histórico relevante. O gerador do
corpus e a rotulagem de relevância são código Go comum e auditável. Antes de acreditar em qualquer número, veja
como ele define «relevante» — inclusive os nossos.

Medido sobre um repositório real: 2,419 sessões, 179k mensagens, 1.9 GB de registros.

| Métrica | Resultado |
| --- | --- |
| Consulta dentro do processo | mediana **0.7–0.8 ms**, cerca de 19 ms nos palheiros do LongMemEval-S |
| `deja <consulta>` ponta a ponta | mediana de uns 0.2 s nesse repositório: partida do processo, checagem de frescor de todos os stores, ranking, impressão |
| Só a checagem de frescor | cerca de 50 ms quando nada mudou |
| Tamanho do índice | 200 MB, uns 10% do corpus |

O índice é incremental. Quando um arquivo de sessão cresce, só aquele arquivo é lido de novo.

## Como funciona

Um índice invertido local em `~/.cache/deja`: faz o parse de stores JSONL e SQLite, limpa credenciais, escreve
`records.bin` e buckets de palavras, e guarda o estado de cada arquivo em `manifest.gob`, de modo que uma
segunda execução ingere só o que mudou. O servidor MCP, as estatísticas, o share e o sync leem esse mesmo
índice. Detalhes em [docs/ARCHITECTURE.md](../../docs/ARCHITECTURE.md).

## Perguntas frequentes

**Alguma coisa sai da minha máquina?** Não, a não ser que você peça. Veja
[fluxos de dados](../../docs/SECURITY-MODEL.md#data-flows).

**E os segredos que já estão nos logs?** Ficam nos arquivos da ferramenta original, que são dados do seu
agente. Não entram no índice do deja, nem nos resumos, nem no share, nem no export do sync.

**Vai deixar meu agente mais lento?** Um recall é uma consulta léxica a um índice local: mediana de 0.7–0.8 ms
e nada esperando um modelo. O hook acrescenta a partida do processo e a checagem de frescor dos stores —
dezenas de milissegundos num repositório de vários gigabytes.

**Preciso mudar como eu trabalho?** Não. O recall é o próprio agente que chama; com o recall automático
ligado, ele já sabe na abertura da sessão o que foi decidido antes neste projeto.

**Qual a diferença em relação a outras ferramentas de memória?**

| | deja | Plataformas de memória<br>(Mem0, Letta, memU) | Busca de sessões<br>(cass) |
| --- | :-: | :-: | :-: |
| Conhece o trabalho anterior à instalação | sim | não | sim |
| Precisa de uma etapa de captura | não, a transcrição é a memória | o agente ou seu código escrevem fatos | não |
| Precisa de chave de LLM ou de embeddings | não | sim | opcional |
| Faz recall sem ser perguntado | no início da sessão e antes de executar ferramentas | não | não |

A [comparação completa](https://vshulcz.github.io/deja-vu/guide/compare.html) cobre onze delas.

**Onde fica o histórico de sessões do Claude Code e dá para pesquisar?** Em `~/.claude/projects`, um arquivo
JSONL por sessão; Codex em `~/.codex/sessions`, Cursor no SQLite `state.vscdb`. O `deja search` lê tudo onde
está, o `deja last` lista a sessão mais recente de cada agente, e o `deja view` abre o histórico inteiro como
uma página local. Os caminhos por agente estão em
[onde as sessões ficam guardadas](https://vshulcz.github.io/deja-vu/guide/where-sessions-are-stored.html).

**Meu histórico do Claude Code desapareceu, eu perdi?** O Claude Code apaga registros com mais de 30 dias
(`cleanupPeriodDays` em `~/.claude/settings.json`), e o `claude --resume` lista só o que sobrou. As sessões que
o deja indexou antes da limpeza continuam pesquisáveis depois de o arquivo sumir. Detalhes em
[arquivos de sessão em disco](https://vshulcz.github.io/deja-vu/guide/session-files-on-disk.html).

**Como apagar tudo?**

```sh
deja uninstall --all
rm -rf ~/.cache/deja
```

## Guias

Escritos por situação, não por recurso:

- [Um agente de código lembra das conversas anteriores?](https://vshulcz.github.io/deja-vu/guide/does-my-agent-remember.html) — o que cada agente deixa entre sessões e o que perde
- [Arquivos de sessão em disco](https://vshulcz.github.io/deja-vu/guide/session-files-on-disk.html) — quanto cresce o `~/.claude/projects` e o que custa apagar
- [O agente perdeu o contexto](https://vshulcz.github.io/deja-vu/guide/lost-context.html) — depois de um crash, depois de um clear, ou quando a sessão volta vazia
- [A janela de contexto está cheia](https://vshulcz.github.io/deja-vu/guide/context-window-full.html) — o que a compactação realmente preserva, com medições, e o que fazer no lugar
- [Continuar a sessão de ontem](https://vshulcz.github.io/deja-vu/guide/resume-a-session.html) — achar entre todos os agentes e abrir naquele a que ela pertence
- [O agente repetiu um erro que você já corrigiu](https://vshulcz.github.io/deja-vu/guide/repeated-mistakes.html)
- [Achar a sessão em que isso foi resolvido](https://vshulcz.github.io/deja-vu/guide/find-a-session.html)
- [Por que agentes esquecem entre sessões](https://vshulcz.github.io/deja-vu/guide/forgetting.html) · [onde cada agente guarda o histórico](https://vshulcz.github.io/deja-vu/guide/where-sessions-are-stored.html)
- [O que a compactação perde](https://vshulcz.github.io/deja-vu/guide/after-compaction.html) · [trocar de agente](https://vshulcz.github.io/deja-vu/guide/switching-agents.html) · [auditar o que os agentes fizeram](https://vshulcz.github.io/deja-vu/guide/auditing-agents.html) · [exportar uma conversa](https://vshulcz.github.io/deja-vu/guide/export-conversations.html) · [entre máquinas](https://vshulcz.github.io/deja-vu/guide/sync-across-machines.html) · [quantos tokens a memória custa](https://vshulcz.github.io/deja-vu/guide/token-cost.html)

Por ferramenta: [opencode](https://vshulcz.github.io/deja-vu/guide/memory-for-opencode.html) · [Zed](https://vshulcz.github.io/deja-vu/guide/memory-for-zed.html) · [Grok Build](https://vshulcz.github.io/deja-vu/guide/memory-for-grok.html) · [Gemini CLI](https://vshulcz.github.io/deja-vu/guide/memory-for-gemini.html) · [OpenClaw](https://vshulcz.github.io/deja-vu/guide/memory-for-openclaw.html) · [Goose](https://vshulcz.github.io/deja-vu/guide/memory-for-goose.html) · [Cline](https://vshulcz.github.io/deja-vu/guide/memory-for-cline.html) · [pi and omp](https://vshulcz.github.io/deja-vu/guide/memory-for-pi.html) · [Hermes](https://vshulcz.github.io/deja-vu/guide/memory-for-hermes.html)

## Experimente no seu próprio histórico

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

Dez segundos para instalar, uns dez para indexar. Na próxima vez que um agente abrir uma sessão, ele já vai
saber o que você resolveu neste projeto — inclusive o que veio antes de você instalar o deja.

## Desenvolvimento

`make build test lint`, e depois [CONTRIBUTING.md](../../CONTRIBUTING.md).
Um agente novo começa pelo [registro de parsers](../../docs/ARCHITECTURE.md#source-parsers).
Prioridades e o que não vamos fazer estão no [ROADMAP.md](../../ROADMAP.md).

## Licença

MIT © [Vladislav Shulcz](https://github.com/vshulcz)
