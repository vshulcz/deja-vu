# dsh-deja

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=social)](https://github.com/vshulcz/deja-vu)

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | Türkçe | [हिन्दी](hi.md)

**[★ vshulcz/deja-vu](https://github.com/vshulcz/deja-vu)** eklentisi (LongMemEval-S'te **%97.2 R@5**) · [Belgeler](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Canlı demo](https://vshulcz.github.io/deja-vu/view/) · [Ölçümler](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![deja-vu demosu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

DeepSeek Harness (`dsh`) ve DSH Desktop (`anywhere-labs/dsh-desktop`) için `session-query` eklentisi. DSH oturumlarının yanında makinenizdeki diğer 51 kodlama ajanının (Claude Code, Codex, Cursor, Gemini, Hermes vb.) geçmişini tek Go ikilisi `deja` ve yerel BM25 dizini üzerinden arar (`DEJA_OFFLINE=1` günlük sürüm kontrolünü kapatır).

## Kurulum

Eklentiyi eklerken `--profile` bayrağını her zaman belirtin:

```sh
dsh plugin --profile web add dsh-deja
# veya doğrudan depodan:
dsh plugin --profile web add github:vshulcz/deja-vu#path:extensions/dsh
```

`deja` henüz kurulu değilse `DEJA_BIN` tanımlayın ya da 52 ajanın tamamını ve `$DSH_HOME/plugins/deja/` konumunu kurun:

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
brew install deja-vu && deja install --auto
npx @vshulcz/deja-vu sources
```

## Eklenen bileşenler

### Altı ajan aracı

| Araç | Görev |
|---|---|
| `deja_recall` | Hata, konu veya bayraklara göre geçmiş oturum arama |
| `deja_session` | Eşleşen satırın çevresindeki tüm diyaloğu okuma |
| `deja_blame` | Bir dosya veya `path:line` için kaynak oturumu bulma |
| `deja_fix` | Aynı hatayı çözen geçmiş komutu getirme |
| `deja_how` | Projede kullanılan komut bayraklarını görme |
| `deja_remember` | Verilen kararı kaydetme |

- **`/deja <...>` komutu**: istem kutusundan anlık arama.
- **Yüzeyler**: `deja install dsh-auto` ile `dsh web`, `tui`, `headless` ve `sdk` altında çalışır (YAML içinde `autoRecall: false` otomatik eklemeyi kapatır).

## Arama denetimleri ve kısayollar

`Settings → Deja` altından yönetilir:

- **Mod+F** (veya **Mod+Alt+F** / macOS'ta **Cmd+Option+F**): aramayı açar; tek oturuma kısıtlamak için `/deja --session <bare session UUID> -- <your draft>` kullanılır.
- **Mod+Shift+K** (macOS **Cmd+Shift+K**): tüm ajanlar arasında genel arama.
- **Mod+K**: **DSH conversations only** filtresiyle paneli açar (en fazla 20 sonuç).
- **Linux tarayıcı yedekleri**: çakışmalar için **`Mod+/`**, **`Mod+Shift+,`** ve **`Mod+Shift+.`** (`shortcuts.open`).

## Kısayolları özelleştirme

`Settings → Deja` bölümünde **`session.search`** yanında **Change shortcut**, **Restore default** ve **Remove** bulunur. **dsh-deja package host plugin**, `/api/deja-search` ucunu sunar ve `/api/deja-search?probe=1` isteği `{"ok": true, "plugin": "dsh-deja"}` döndürdüğünde yerel `deja search` erişimi doğrulanmış olur.

## Bağlantı mimarisi

- **İstem kancaları**: `ctx.systemPrompt.context` ve `agent/pre-step` (`dsh 0.1.1-rc.2`+) ile `additionalContext` ve `systemMessage` iletilir.
- **HTTP ve sınırlar**: `/api/deja-notes` (`--plain`) `client.js` için veri sağlar; `GET /api/deja-search`, `deja search --harness deepseek --json --limit 20 -- <query>` komutunu 20 saniye zaman aşımı, 2 MiB çıktı ve 500 karakter sorgu sınırıyla çalıştırır.
- **Kenar çubuğu yuvası**: `data-slot="sidebar.workspaces"`, `aria-expanded` ve `_listArea` (DSH `0.2.1-rc.5`).

## Arama denetimlerini test etme

```sh
node --test extensions/dsh/test/*.test.js
```

Derleme gerektirmeyen tarayıcı testi `extensions/dsh/test/search-browser.html` (React 18 CDN) dosyasında yer alır.

## Ölçümler

| İsabet (Recall) | Token | İndeksleme ve sorgu |
|---|---|---|
| LongMemEval-S'te **%97.2 R@5** (470 soruda %88.9 hit@1) | **%58 daha az token** (tekrarlanan depo işlerinde) | 19,195 oturumu **13.6 sn**'de indeksler / 52 kodlama ajanı genelinde **0.7 ms** sorgu süresi |

## Depo belleği ve ajanlar arası devir (`handoff`)

Onaylanmış proje kararlarını ve çıkmaz yolları git üzerinden `.deja/memory.jsonl` dosyasında paylaşın (`deja repo candidates`, `deja repo add <id>`). Kayıtlar yazılırken gizli anahtarlardan arındırılır, paralel dalların temiz birleşmesi için `merge=union` ile içerik özeti sırasıyla tutulur ve depoyu klonlayan her ekipte yüklenir (güven politikasında `{"activations":{"auto":{"repo":false}}}` kanca enjeksiyonunu kapatır). [Ayrıntılar](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Görevin ortasında araç mı değiştiriyorsunuz? `deja handoff --to <agent>` çalıştırın (veya MCP üzerinden `handoff` çağırın); etkin oturumun hedefi, kararları, dokunulan dosyaları, geçen testleri ve açık işleri başka kodlama ajanına aktarılır.

## Yapmadıkları

Dizin `~/.cache/deja/index.db` (`DEJA_INDEX_DIR`) dışına çıkmaz; `deja embed` olmadan ağ isteği yapmaz; `sk-*`, `ghp_*`, `AKIA*` ve `Bearer` dizgi temizliğini dizinlemede uygular ve kaynak oturum dosyalarını değiştirmez.

## Lisans

[deja-vu](https://github.com/vshulcz/deja-vu) parçasıdır. MIT.
