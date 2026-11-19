# `@vshulcz/magpie-deja` — deja-vu için Magpie Ağ Geçidi Kancası

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | Türkçe | [हिन्दी](hi.md)

[![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=social)](https://github.com/vshulcz/deja-vu)

**[★ vshulcz/deja-vu](https://github.com/vshulcz/deja-vu)** eklentisi (LongMemEval-S'te **%97.2 R@5**) · [Belgeler](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Canlı demo](https://vshulcz.github.io/deja-vu/view/) · [Ölçümler](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![deja-vu demosu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

> [Magpie](https://github.com/yetone/magpie) üzerinden geçen her sohbet isteğine makinenizdeki diğer 48 kodlama ajanının (Claude Code, Codex, Cursor, Gemini CLI, OpenClaw, Hermes vb.) oturum belleğini otomatik ekler.

OpenAI (`chat`, `responses`), Anthropic (`anthropic`) ve Gemini (`gemini`) protokollerini tek Go ikilisi `deja` ve yerel BM25 dizini üzerinden ek LLM gerektirmeden destekler (`DEJA_OFFLINE=1` günlük sürüm denetimini kapatır).

## Kurulum

```sh
magpie plugin add @vshulcz/magpie-deja
```

Sistemde `deja` (`@vshulcz/deja-vu`, `brew install deja-vu` veya `install.sh`) kurulu olmalı ve `deja index` ile dizin oluşturulmalıdır. 49 kodlama ajanının tamamında `recall`, `fix`, `blame`, `how`, `orient`, `context`, `remember` ve `handoff` araçlarını bağlamak için:

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh && deja install --auto
```

İstek zaten `<deja-memory>` veya `<deja-recall>` içeriyorsa çift enjeksiyonu önlemek için eklenti devre dışı kalır.

## Çalışma Prensibi ve Veri Sınırı

1. **`onRequest`, `deja hook-prompt --plain` çalıştırır**: `~/.cache/deja/index.db` (`DEJA_INDEX_DIR` ile değişir) üzerinde varsayılan `800ms` süre sınırıyla arama yapıp `<deja-memory>` ekler. **Giden istek uyarısı**: eklenen geçmiş özeti ağ geçidi üzerinden üst LLM sağlayıcısına gider.
2. **`ctx.cwd` kapsam koruması**: `allowUnscoped: false` varsayılanı ile `deja hook-prompt` çağrısında asla ağ geçidinin `process.cwd()` yoluna düşülmez.
3. **Yalnızca yerel kaynak**: `recallRemote: false` yalnızca `ctx.origin === "local"` veya `ctx.origin === "magpie-key"` olduğunda çalışır.
4. **İki katmanlı gizli bilgi maskeleme**: dizinleme sırasındaki temizliğe ek olarak `deja.hooks.mjs` ile ortak `maskSecrets` (`sk-*`, `ghp_*`, `github_pat_*`, `AKIA*`, `Bearer`, `PRIVATE KEY`, URL kimlik bilgileri, `api_key=...`) uygulanır.
5. **Döngü ve önek önbelleği koruması**: `<deja-memory`, `<deja-recall>` veya `[deja —` varsa atlanır ve istem önek önbelleği korunur.

## Seçenekler

| Seçenek | Varsayılan | Açıklama |
|---|---|---|
| `requestTimeoutMs` | `800` | `deja` alt işlem zaman aşımı (`50`–`5000` ms) |
| `maxBytes` | `4000` | Eklenen bağlam sınırı (`256`–`32768` bayt) |
| `recallRemote` | `false` | Uzak kaynaklı isteklerde çağrıyı açar |
| `allowUnscoped` | `false` | `ctx.cwd` olmadan genel aramaya izin verir |
| `cwd` | `""` | Çalışma dizinini elle belirtir |
| `skipAgents` | `[]` | Atlanacak ajanlar (ör. `["claude-code"]`) |
| `bin` | `""` | `deja` çalıştırılabilir dosya yolu |

## Ölçümler

| İsabet (Recall) | Token | İndeksleme ve sorgu |
|---|---|---|
| LongMemEval-S'te **%97.2 R@5** (470 soruda %88.9 hit@1) | **%58 daha az token** (tekrarlanan depo işlerinde) | 19,195 oturumu **13.6 sn**'de indeksler / 49 kodlama ajanı genelinde **0.7 ms** sorgu süresi |

## Depo belleği ve ajanlar arası devir (`handoff`)

Onaylanmış proje kararlarını ve çıkmaz yolları git üzerinden `.deja/memory.jsonl` dosyasında paylaşın (`deja repo candidates`, `deja repo add <id>`). Kayıtlar yazılırken gizli anahtarlardan arındırılır, paralel dalların temiz birleşmesi için `merge=union` ile içerik özeti sırasıyla tutulur ve depoyu klonlayan her ekipte yüklenir (güven politikasında `{"activations":{"auto":{"repo":false}}}` kanca enjeksiyonunu kapatır). [Ayrıntılar](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Görevin ortasında araç mı değiştiriyorsunuz? `deja handoff --to <agent>` çalıştırın (veya MCP üzerinden `handoff` çağırın); etkin oturumun hedefi, kararları, dokunulan dosyaları, geçen testleri ve açık işleri başka kodlama ajanına aktarılır.

## Lisans

[deja-vu](https://github.com/vshulcz/deja-vu) parçasıdır. MIT.
