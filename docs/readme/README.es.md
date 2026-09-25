<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/logo-dark.svg">
    <img src="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/logo.svg" width="330" alt="deja-vu">
  </picture>
</p>

<p align="center"><b>Una sola memoria para todos tus agentes de código, construida con el historial que ya tienes en disco.</b></p>

<p align="center">Tu agente está a punto de volver a depurar algo que arreglaste en marzo, en otro agente.
deja indexa las sesiones que Claude Code, Codex, Cursor y el resto de los agentes de esta máquina ya escriben
en disco, y devuelve la que corresponde a cualquiera de ellos que pregunte.</p>

<p align="center"><img src="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif" width="720" alt="la misma pregunta al mismo agente dos veces: sin memoria no recuerda nada, con deja responde con una conclusión de hace ocho meses"></p>

<p align="center"><sub><em>Nadie buscó nada: el agente llamó a deja por su cuenta. Dos ejecuciones reales, modelo real, llamadas a herramientas reales, sobre un corpus sintético: no se publica el historial de nadie.</em></sub></p>

<p align="center"><b>deja está llena desde el primer minuto: el historial que 34 agentes ya escribieron, indexado en segundos, sin modelo y sin un paso aparte de captura.</b></p>

<p align="center">
<b>58% menos tokens</b> en una tarea que esta máquina ya había resuelto &middot; <b>88.1% hit@1</b> en LongMemEval-S (conjunto depurado de 470 preguntas) &middot; <b>70.5%</b> en LoCoMo &middot; consultas en <b>milisegundos</b> sobre gigabytes de historial<br>
<sub>Once ejecuciones por brazo: 53,558 tokens frente a 126,222 sin nada conectado, y 52,815 frente a 103,443 en una versión posterior, otra vez once ejecuciones &middot;
<a href="https://vshulcz.github.io/deja-vu/guide/day-zero.html">lo que cuesta terminar una tarea</a> &middot;
los dos harnesses de recuperación están en este repositorio y corren sobre datasets públicos en minutos &middot;
<a href="https://vshulcz.github.io/deja-vu/guide/benchmarks.html">comprueba los números tú mismo</a></sub>
</p>

<p align="center"><a href="../../README.md">English</a> | <a href="README.zh.md">简体中文</a> | <a href="README.zh-TW.md">繁體中文</a> | <a href="README.ja.md">日本語</a> | <a href="README.ko.md">한국어</a> | Español | <a href="README.pt.md">Português</a> | <a href="README.fr.md">Français</a> | <a href="README.de.md">Deutsch</a> | <a href="README.ru.md">Русский</a> | <a href="README.tr.md">Türkçe</a> | <a href="README.hi.md">हिन्दी</a></p>

<p align="center"><a href="https://vshulcz.github.io/deja-vu/">Documentación</a> &middot; <a href="https://vshulcz.github.io/deja-vu/guide/benchmarks.html">Benchmarks</a> &middot; <a href="https://vshulcz.github.io/deja-vu/guide/compare.html">Comparación</a></p>
<p align="center"><sub>Si te resulta útil, dale una estrella a deja-vu en <a href="https://github.com/vshulcz/deja-vu">GitHub</a>.</sub></p>

## Instalación

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

Si `raw.githubusercontent.com` no está accesible, hay un espejo en npm con la misma versión:

```sh
npm i -g @vshulcz/deja-vu --registry=https://registry.npmmirror.com
deja install --auto
```

Diez segundos para instalar, unos diez para indexar. El segundo comando conecta el recall por MCP a cada
agente que encuentra, activa el recall al inicio de sesión donde el agente lo soporta y construye el primer
índice, para que la siguiente sesión no espere nada.

Abre una sesión nueva y pregunta por algo de hace meses:

> ¿ya nos peleamos antes con jwt refresh rotation? busca en tu memoria

Tampoco hace falta preguntar: con el recall automático activado, el agente ya sabe al abrir la sesión qué se
resolvió en este proyecto.

opencode, DeepSeek Harness, Zed, Kimi Code, Codex CLI, Grok Build, OpenClaw y pi tienen además su propio
paquete, para quien está acostumbrado a instalar extensiones desde ahí:

```sh
opencode plugin opencode-deja
dsh plugin --profile web add dsh-deja
# Zed: busca deja en el panel de extensiones
# Kimi Code: /plugins install https://github.com/vshulcz/deja-vu
# Codex CLI: codex plugin marketplace add https://github.com/vshulcz/deja-vu && codex plugin add deja-vu@deja-vu
# Grok Build: grok plugin marketplace add xai-org/plugin-marketplace && grok plugin install deja
openclaw plugins install clawhub:@vshulcz/openclaw-deja
pi install npm:@vshulcz/pi-deja
```

`deja install --auto` ya conecta todo lo anterior, así que cualquiera de los dos caminos basta. Los dos juntos
tampoco estorban: cada paquete lee lo que escribió `deja install` y solo completa lo que falta, sin registrar
herramientas por duplicado ni hacer el recall dos veces. Los detalles están en [`extensions/`](../../extensions).

La misma búsqueda existe como skill, instalable por cualquier agente que lea `SKILL.md`:

```sh
npx skills add https://github.com/vshulcz/deja-vu --skill deja-search   # skills CLI: Claude Code, Cursor, Goose, Copilot…
openclaw skills install @vshulcz/deja-search                            # ClawHub
hermes skills install vshulcz/deja-vu/skills/deja-search                # Hermes
```

El skill llama al binario `deja` que ya tienes instalado; no trae uno propio.

Otras vías: `brew install deja-vu`,
`go install github.com/vshulcz/deja-vu/cmd/deja@latest`, o `npx @vshulcz/deja-vu "consulta"` para probarlo sin
instalar nada. En Windows el script de instalación termina con `unsupported OS` porque es un script de shell;
coge `deja-vu_<version>_windows_amd64.zip` de la
[última release](https://github.com/vshulcz/deja-vu/releases/latest) y pon `deja.exe` en el `PATH`.

Solo el binario ya es una instalación completa: indexar, buscar, `show`, `ctx`, `blame`, `--json` y el borrado
de credenciales no necesitan nada más. Lo que hace `deja install` es enchufar MCP a tus agentes y activar el
recall al inicio de sesión: vale la pena, pero es opcional.

## Qué obtienes

**Resuelto en Codex, recordado por Claude.** Treinta y cuatro agentes de código escriben cada conversación en
archivos locales y deja convierte esos archivos en una capa de memoria que todos pueden leer.

| | |
| --- | --- |
| **Búsqueda hacia atrás** | `deja "connection pool exhausted"` recorre gigabytes, incluido todo lo anterior a instalar deja. Una pregunta en lenguaje natural cae en el modo por relevancia. El tiempo es una pista, no un filtro. |
| **Recall entre agentes** | La herramienta MCP `deja` en modo `recall` responde «esto lo arreglamos hace tres semanas» desde cualquier agente, sin importar quién lo arreglara entonces. |
| **Sobrevive a la compactación** | Medido en 43 compactaciones: el resumen conservó el 77% de las decisiones y el 0.2% de los comandos que ejecutaste. El 99.8% restante lo devuelve deja. En Claude Code y Codex, deja anota la tarea, los archivos y los comandos en el momento en que empieza la compactación, y los devuelve de una vez en la sesión siguiente. |
| **Recall en el momento de actuar** | Antes de que el agente edite un archivo o ejecute un comando, el hook `PreToolUse` dice qué se decidió antes sobre ese archivo, cuál es la forma de ese comando que funciona aquí, o que el programa simplemente no existe en esta máquina. Cuando un comando falla, el hook `PostToolUse` muestra qué se ejecutó después del mismo error en esta máquina: justo el par que el agente no va a preguntar. |
| **Indexa el trabajo, no solo las palabras** | Cada archivo abierto en cada turno, cada comando ejecutado con su código de salida, y el fragmento exacto que reemplazó una edición. Precisamente lo que pierde cualquier resumen. |

Además: `deja promote <id> --state rejected` marca una decisión revertida, y desde entonces cada resultado
muestra que se intentó y se descartó; un resultado avisa de que «4 archivos de esta sesión han cambiado desde
entonces» y calla cuando no puede saberlo; `deja sync ssh laptop` mueve la memoria entre máquinas solo
añadiendo, sin nube en medio; `deja handoff --to codex` empaqueta el contexto actual para seguir en otro
agente; las claves, tokens, JWT y bloques de clave privada de formas conocidas se quitan al indexar, aunque
emparejar patrones no es detectar secretos y una forma desconocida puede pasar.

### Dibuja tu propio trabajo

`deja stats --card` dibuja directamente en la terminal; dale un nombre de archivo y escribe un SVG para el
README de tu perfil. Para publicarlo en otro sitio,
[conviértelo a PNG](https://vshulcz.github.io/deja-vu/card/): esa página convierte en tu propio navegador.

<p align="center"><img src="https://raw.githubusercontent.com/vshulcz/deja-vu/main/docs/assets/stats-card-demo.svg" width="760" alt="tarjeta de estadísticas de deja: un año de sesiones como mapa de calor, de qué agentes vinieron y cuál fue la más larga"></p>

La referencia completa está en el [sitio de documentación](https://vshulcz.github.io/deja-vu/).

## Privacidad

Indexar y buscar son operaciones locales. Solo usan la red `deja update`, `deja sync ssh`, la comprobación de
versión dentro de `deja doctor` y `deja embed`, que va al endpoint que tú configures.

Las credenciales se limpian al indexar: claves de AWS, asignaciones `api_key=` y `token=`, tokens bearer y JWT
desnudos, bloques PEM de clave privada, tokens de distintos proveedores, URLs con forma
`scheme://user:pass@host`, valores de alta entropía que ninguna regla cubre, y contraseñas escritas en prosa
—«the admin password is …», donde no hay ningún separador en el que apoyarse. El valor pasa a ser
`[redacted:<kind>]` y el texto alrededor sigue siendo buscable. `deja share` y `deja sync export` limpian otra
vez al exportar. Emparejar patrones no es detectar secretos: una forma que las reglas no conocen puede pasar
tal cual, ver el modelo de seguridad.

`deja forget` saca sesiones del índice reconstruido y deja una lápida, así que el siguiente `deja index` no
puede recuperarlas del historial original.
El [modelo de seguridad](../../docs/SECURITY-MODEL.md) documenta los flujos de datos, los límites del borrado, los
supuestos de confianza y la verificación de releases.

## Línea de comandos

```text
$ deja "jwt refresh token"
[claude] api        · Jul 8 · 8f31c0a9 — 2 matches
  login started failing after refresh token rotation; jwt kid mismatch in tests
  fixed by reloading jwks cache after rotateKey and adding a clock-skew test
[codex]  web        · Jul 1 · b77d91e2 — 1 match
  refresh token cookie needed SameSite=Lax in local callback flow
```

| Comando | Qué hace |
| --- | --- |
| `deja <consulta>` | Busca en todo el historial. Varias palabras son AND, las comillas exigen texto contiguo; si no hay coincidencia exacta prueba formas de la palabra y grafías cercanas. |
| `deja wip` | Qué estaba haciendo la última sesión en este directorio: la tarea, a qué se llegó, los archivos abiertos, el último comando y si falló. Todo deducido de los registros, sin depender de que alguien tomara notas. |
| `deja blame <ruta>[:línea]` | Qué sesiones discutieron este archivo, qué se decidió y por qué. Con número de línea: el commit que la cambió por última vez y la sesión que escribió el texto que ese commit borró. |
| `deja files <tema>` | Al revés: qué archivos tocó realmente el trabajo sobre un tema. |
| `deja how <herramienta>` | Cómo se ejecuta esto de verdad en esta máquina, con argumentos reales, sacados de comandos que los agentes ya corrieron. |
| `deja fix <error>` | Qué se ejecutó después del mismo error en esta máquina, y tras lo cual el error no volvió a aparecer. |
| `deja friction` | Errores que aparecen en más de tres sesiones distintas, indicando de qué herramientas vienen. |
| `deja ctx <consulta>` | Resumen en Markdown de los mejores resultados, listo para meter en un prompt. |
| `deja resume <id>` | Vuelve a abrir la sesión encontrada en la herramienta a la que pertenece. |
| `deja view` | Exporta toda la memoria a un único archivo HTML local. Sin servidor, nada sale de la máquina. |
| `deja doctor [--deep]` | Autodiagnóstico; con `--deep` verifica el índice contra los archivos de origen. |
| `deja mcp` | El servidor MCP por stdio, el mismo que conecta `deja install`. |

La referencia completa está en la [documentación de comandos](https://vshulcz.github.io/deja-vu/guide/commands.html).

### Herramientas MCP

El servidor expone una sola herramienta, `deja`, y el parámetro `mode` elige la capacidad: `recall`,
`context`, `blame`, `fix`, `how`, `remember`. `deja install` la conecta sola, así que esto solo importa si
configuras un agente a mano. Los seis nombres antiguos siguen funcionando en los clientes ya conectados.

Una herramienta en lugar de siete es una cuestión de coste, no de estilo. Un servidor MCP conectado envía las
definiciones de sus herramientas con cada petición, así que se pagan en cada turno haya llamado el agente algo
o no: 477 tokens aquí, frente a 8,283 del mayor de los ocho servidores medidos. El de deja era 828 hasta que
el esquema se redujo a una herramienta con modos.

## Herramientas soportadas

Con el recall automático activado, Claude Code y Codex entregan a deja el registro actual cuando empieza la
compactación, y deja se queda con lo que el resumen está a punto de perder: la tarea, las conclusiones, los
archivos, en qué terminó cada comando y qué queda sin hacer. El siguiente hook de la misma sesión y el mismo
directorio lo devuelve de una vez, sin pasar de 4 KB, con una línea sobre si el repositorio cambió desde
entonces. `deja stats` cuenta las llamadas a herramientas entre la compactación y la primera edición, que es
con lo que se midió esto.
Los detalles están en [recuperación tras la compactación](../../docs/compaction.md).

Claude Code · Cline · Codex CLI · opencode · aider · Gemini CLI · Cursor · Antigravity ·
Grok Build · Hermes · Goose · Qwen Code · Kimi Code · pi · omp (Oh My Pi) · OpenClaw ·
Copilot CLI · VS Code Copilot Chat · Amp · prime-agent (PrimeIntellect) · Roo Code ·
Continue · Crush · DeepSeek Harness · Cherry Studio · Senpi · gajae-code · Kimchi Coding ·
Command Code · ZCode · CodeWhale · Kiro · Kilo Code · Zed.

Qué soporta cada uno —recall por MCP, recall automático, skills, comandos, resume, handoff— está en la
[matriz de capacidades del README en inglés](../../README.md#supported-harnesses). Las rutas de almacenamiento
personalizadas se indican con variables `DEJA_*_ROOT`, y también se respetan las variables de migración de
cada herramienta.

### Agentes con su propio paquete

`deja install --auto` conecta estos igual que el resto, y ese siempre es el camino más corto. Además cada uno
tiene un paquete en su propio ecosistema, para quien instala extensiones desde ahí:

| Agente | Paquete | Instalación |
| --- | --- | --- |
| opencode | npm `opencode-deja` | `opencode plugin opencode-deja` |
| DeepSeek Harness | npm `dsh-deja` | `dsh plugin --profile web add dsh-deja` |
| Zed | `deja-context-server` | Zed → Extensions → deja |
| Kimi Code | plugin `deja` | `/plugins install https://github.com/vshulcz/deja-vu` |
| Codex CLI | plugin `deja-vu` | `codex plugin marketplace add https://github.com/vshulcz/deja-vu`, luego `codex plugin add deja-vu@deja-vu` |
| Grok Build | plugin `deja` | `grok plugin marketplace add xai-org/plugin-marketplace`, luego `grok plugin install deja` |
| OpenClaw | ClawHub y npm `@vshulcz/openclaw-deja` | `openclaw plugins install clawhub:@vshulcz/openclaw-deja` |
| pi (y omp) | npm `@vshulcz/pi-deja` | `pi install npm:@vshulcz/pi-deja` |

Cualquiera de los dos caminos basta y los dos juntos no rompen nada: cada paquete lee primero lo que dejó
escrito `deja install`. opencode, dsh y OpenClaw solo completan lo que falta; Kimi, Grok, Codex y pi se
apartan si el instalador ya lo conectó; en Zed ambos lados usan el mismo id de servidor. Así que el orden de
instalación no importa.

Todos usan el deja que ya tienes instalado; la copia dentro del paquete es solo el respaldo.

## Recall semántico, opcional

Apunta `deja embed` a un Ollama local, LM Studio o cualquier endpoint compatible con OpenAI mediante
`DEJA_EMBED_URL` y preguntar con otras palabras también acierta. Sin un runtime disponible, la búsqueda léxica
y el recall por MCP funcionan como siempre.

## Evidencia

```sh
deja bench recall     # cota inferior de regresión del ranking: 100 consultas, la mitad en ruso, CI falla si el recall baja
deja bench context    # 30 cadenas de tareas con semilla más cinco controles negativos
deja bench block      # si la respuesta sigue estando en el fragmento de texto entregado
deja bench prompt     # cuándo habla el hook por prompt y cuándo habla de más
deja bench ingest     # coste de una actualización del índice: sin cambios, un turno añadido, un archivo nuevo, reescritura completa
```

El experimento de contexto compara el recall de deja con el historial completo, un grep ingenuo y el arranque
en frío. Con la semilla por defecto:

| Enfoque | Tokens (mediana) | Cobertura (mediana) | Tokens del control negativo |
| --- | ---: | ---: | ---: |
| deja-recall | 1,096 | 1.00 | 0 |
| full-history | 80,547 | 1.00 | 78,145 |
| naive-grep | 273,238 | 1.00 | 0 |
| cold | 0 | 0.00 | 0 |

La misma cobertura de hechos que hacer grep sobre los logs en crudo con unas 250 veces menos tokens; unas 70
veces menos que las sesiones encontradas reproduciéndolo todo; y nada inyectado en las cadenas sin historial
relevante. El generador del corpus y el etiquetado de relevancia son código Go normal y revisable. Antes de
creer cualquier número, mira cómo define «relevante», incluidos los nuestros.

Medido sobre un repositorio real: 2,419 sesiones, 179k mensajes, 1.9 GB de registros.

| Métrica | Resultado |
| --- | --- |
| Consulta dentro del proceso | mediana **0.7–0.8 ms**, unos 19 ms en los pajares de LongMemEval-S |
| `deja <consulta>` de extremo a extremo | mediana de unos 0.2 s en ese repositorio: arranque del proceso, comprobación de frescura de todos los almacenes, ranking, impresión |
| Solo la comprobación de frescura | unos 50 ms cuando nada ha cambiado |
| Tamaño del índice | 200 MB, alrededor del 10% del corpus |

El índice es incremental. Cuando un archivo de sesión crece, se vuelve a leer solo ese archivo.

## Cómo funciona

Un índice invertido local en `~/.cache/deja`: parsea almacenes JSONL y SQLite, limpia credenciales, escribe
`records.bin` y cubos de palabras, y guarda el estado de cada archivo en `manifest.gob`, de modo que una
segunda ejecución solo ingiere lo que cambió. El servidor MCP, las estadísticas, share y sync leen ese mismo
índice. Los detalles están en [docs/ARCHITECTURE.md](../../docs/ARCHITECTURE.md).

## Preguntas frecuentes

**¿Sale algo de mi máquina?** No, a menos que lo pidas. Ver
[flujos de datos](../../docs/SECURITY-MODEL.md#data-flows).

**¿Y los secretos que ya están en los logs?** Se quedan en los archivos de la herramienta original, que son
datos de tu agente. No entran en el índice de deja, ni en los resúmenes, ni en share, ni en el export de sync.

**¿Va a ralentizar a mi agente?** Un recall es una consulta léxica a un índice local: mediana de 0.7–0.8 ms y
nada esperando a un modelo. El hook añade el arranque del proceso y la comprobación de frescura de los
almacenes: decenas de milisegundos en un repositorio de varios gigabytes.

**¿Tengo que cambiar cómo trabajo?** No. El recall lo llama el propio agente; con el recall automático
activado, ya sabe al abrir la sesión qué se decidió antes en este proyecto.

**¿En qué se diferencia de otras herramientas de memoria?**

| | deja | Plataformas de memoria<br>(Mem0, Letta, memU) | Búsqueda de sesiones<br>(cass) |
| --- | :-: | :-: | :-: |
| Conoce el trabajo anterior a su instalación | sí | no | sí |
| Necesita un paso de captura | no, la transcripción es la memoria | el agente o tu código escriben hechos | no |
| Necesita clave de LLM o de embeddings | no | sí | opcional |
| Recuerda sin que se lo pidan | al inicio de sesión y antes de ejecutar herramientas | no | no |

La [comparación completa](https://vshulcz.github.io/deja-vu/guide/compare.html) cubre once de ellas.

**¿Dónde está el historial de sesiones de Claude Code y se puede buscar?** En `~/.claude/projects`, un archivo
JSONL por sesión; Codex en `~/.codex/sessions`, Cursor en el SQLite `state.vscdb`. `deja search` los lee donde
están, `deja last` lista la sesión más reciente de cada agente y `deja view` abre todo el historial como una
página local. Las rutas por agente están en
[dónde se guardan las sesiones](https://vshulcz.github.io/deja-vu/guide/where-sessions-are-stored.html).

**Ha desaparecido mi historial de Claude Code, ¿lo he perdido?** Claude Code borra los registros de más de 30
días (`cleanupPeriodDays` en `~/.claude/settings.json`) y `claude --resume` solo lista lo que queda. Las
sesiones que deja indexó antes de la limpieza siguen siendo buscables después de que el archivo desaparezca.
Los detalles están en
[archivos de sesión en disco](https://vshulcz.github.io/deja-vu/guide/session-files-on-disk.html).

**¿Cómo borro todo?**

```sh
deja uninstall --all
rm -rf ~/.cache/deja
```

## Guías

Escritas por situación, no por función:

- [¿Recuerda un agente de código las conversaciones anteriores?](https://vshulcz.github.io/deja-vu/guide/does-my-agent-remember.html) — qué deja cada agente entre sesiones y qué pierde
- [Archivos de sesión en disco](https://vshulcz.github.io/deja-vu/guide/session-files-on-disk.html) — cuánto crece `~/.claude/projects` y qué cuesta borrarlo
- [El agente ha perdido el contexto](https://vshulcz.github.io/deja-vu/guide/lost-context.html) — tras un fallo, tras un clear, o cuando la sesión vuelve vacía
- [La ventana de contexto está llena](https://vshulcz.github.io/deja-vu/guide/context-window-full.html) — qué conserva realmente la compactación, con medidas, y qué se puede hacer en su lugar
- [Seguir la sesión de ayer](https://vshulcz.github.io/deja-vu/guide/resume-a-session.html) — encontrarla entre todos los agentes y abrirla en el que le corresponde
- [El agente ha vuelto a cometer un error que ya arreglaste](https://vshulcz.github.io/deja-vu/guide/repeated-mistakes.html)
- [Encontrar la sesión donde se resolvió](https://vshulcz.github.io/deja-vu/guide/find-a-session.html)
- [Por qué los agentes olvidan entre sesiones](https://vshulcz.github.io/deja-vu/guide/forgetting.html) · [dónde guarda el historial cada agente](https://vshulcz.github.io/deja-vu/guide/where-sessions-are-stored.html)
- [Qué pierde la compactación](https://vshulcz.github.io/deja-vu/guide/after-compaction.html) · [cambiar de agente](https://vshulcz.github.io/deja-vu/guide/switching-agents.html) · [auditar lo que hicieron los agentes](https://vshulcz.github.io/deja-vu/guide/auditing-agents.html) · [exportar una conversación](https://vshulcz.github.io/deja-vu/guide/export-conversations.html) · [entre máquinas](https://vshulcz.github.io/deja-vu/guide/sync-across-machines.html) · [cuántos tokens cuesta la memoria](https://vshulcz.github.io/deja-vu/guide/token-cost.html)

Por herramienta: [opencode](https://vshulcz.github.io/deja-vu/guide/memory-for-opencode.html) · [Zed](https://vshulcz.github.io/deja-vu/guide/memory-for-zed.html) · [Grok Build](https://vshulcz.github.io/deja-vu/guide/memory-for-grok.html) · [Gemini CLI](https://vshulcz.github.io/deja-vu/guide/memory-for-gemini.html) · [OpenClaw](https://vshulcz.github.io/deja-vu/guide/memory-for-openclaw.html) · [Goose](https://vshulcz.github.io/deja-vu/guide/memory-for-goose.html) · [Cline](https://vshulcz.github.io/deja-vu/guide/memory-for-cline.html) · [pi and omp](https://vshulcz.github.io/deja-vu/guide/memory-for-pi.html) · [Hermes](https://vshulcz.github.io/deja-vu/guide/memory-for-hermes.html)

## Pruébalo con tu propio historial

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

Diez segundos para instalar, unos diez para indexar. La próxima vez que un agente abra una sesión ya sabrá qué
has resuelto en este proyecto, incluido lo de antes de instalar deja.

## Desarrollo

`make build test lint`, y luego [CONTRIBUTING.md](../../CONTRIBUTING.md).
Un agente nuevo empieza por el [registro de parsers](../../docs/ARCHITECTURE.md#source-parsers).
Las prioridades y lo que no vamos a hacer están en [ROADMAP.md](../../ROADMAP.md).

## Licencia

MIT © [Vladislav Shulcz](https://github.com/vshulcz)
