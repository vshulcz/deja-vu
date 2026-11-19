# opencode-deja

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | Türkçe | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

**[deja-vu](https://github.com/vshulcz/deja-vu)** eklentisi (LongMemEval-S'te **%97.2 R@5** · 19,195 oturumu **13.6 sn**'de indeksleme · agentmemory'nin **yarısı kadar token**) · [Belgeler](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Canlı demo](https://vshulcz.github.io/deja-vu/view/) · [Ölçümler](https://vshulcz.github.io/deja-vu/guide/benchmarks.html) · **[★ GitHub'da yıldız ver](https://github.com/vshulcz/deja-vu)**

![deja-vu demosu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

[opencode](https://opencode.ai) için makinenizdeki diğer 48 kodlama ajanının (Claude Code, Codex, Cursor, Gemini, Hermes, Zed vb.) geçmişinden beslenen yerel bellek eklentisi. Tek Go ikilisi `deja` ve yerel BM25 ile LLM olmadan çalışır (`DEJA_OFFLINE=1` günlük sürüm kontrolünü kapatır).

## Kurulum

`opencode.ai/config.json` dosyasına ekleyin:

```json
{ "plugin": ["opencode-deja"] }
```

(opencode 2.x için `"plugins": ["opencode-deja"]` veya `opencode plugin add opencode-deja` kullanın.) İsterseniz `deja install --auto` ile `opencode.json` ve `plugins/deja.js` kurulumunu otomatik yapabilirsiniz.

## Özellikler

### Altı isteğe bağlı araç

| Araç | Amaç |
|---|---|
| `deja_recall` | Geçmiş oturumlarda hata, konu veya sembol arama |
| `deja_session` | Eşleşen oturum etrafındaki tam diyaloğu okuma |
| `deja_blame` | Bir dosya veya `path:line` yazan oturumu bulma |
| `deja_fix` | Aynı hatayı daha önce çözen komutu getirme |
| `deja_how` | Projede kullanılan kesin komut bayraklarını hatırlama |
| `deja_remember` | Verilmiş bir kararı kaydetme |

### Dört otomatik bellek davranışı

- **Başlangıç uyarısı**: 1.x sürümünde kısa toast, 2.x'te sistem mesajı (`deja-vu — from Codex CLI, 4 months ago: "…"`).
- **20 dakika bekleme ve alt-ajan atlama**: aynı projede tekrarı önler, alt-ajanların bağlamını şişirmez.
- **Hatalı komutlar ve dosya notları**: hata sonrası geçmiş çözümü, dosya düzenlemesinde eski kararı ekler.
- **Sıkıştırma öncesi**: özetlemeden önce görevi, kararları ve açık komutları korur.

## Seçenekler

```json
{
  "plugin": [["opencode-deja", { "autoRecall": false, "tools": true }]]
}
```

(2.x sürümünde `"plugins"` anahtarı kullanılır.)

- `autoRecall`: otomatik eklemeyi açıp kapatır (varsayılan `true`).
- `tools`: altı aracı sunar (varsayılan `true`).
- `bin`: `deja` ikili yolu.

49 aracın tamamında `recall`, `fix`, `blame`, `how`, `orient`, `context`, `remember` ve `handoff` olmak üzere 8 MCP kipi için `deja install --auto` çalıştırın ([bağlam kaybetmeden ajan geçişi](https://vshulcz.github.io/deja-vu/guide/switch-agents-without-losing-context.html)).

## İkili dosya sırası

`bin` -> `DEJA_BIN` -> `PATH` -> `~/.local/bin`, `/usr/local/bin`, `/opt/homebrew/bin` -> npm paketi içindeki kopya.

## Gizlilik

Dizin `~/.cache/deja/index.db` konumundadır (`DEJA_INDEX_DIR` ile değişir); `sk-*`, `ghp_*`, `AKIA*` ve `Bearer` dizinlenirken maskelenir. Açıkça `deja embed` çalıştırılmadıkça dışarıya veri gönderilmez.

## Ölçümler

| İsabet (Recall) | Token | İndeksleme ve sorgu |
|---|---|---|
| LongMemEval-S'te **%97.2 R@5** (470 soruda %88.9 hit@1; MemPalace %96.6, agentmemory %95.2 yayımlar) | **%58 daha az token** (tekrarlanan depo işlerinde agentmemory'nin **yarısı kadar token**) | 19,195 oturumu **13.6 sn**'de indeksler / 49 kodlama ajanı genelinde **0.7 ms** sorgu süresi (7 araçtan sonraki: 72 sn) |

## Depo belleği ve ajanlar arası devir (`handoff`)

Onaylanmış proje kararlarını ve çıkmaz yolları git üzerinden `.deja/memory.jsonl` dosyasında paylaşın (`deja repo candidates`, `deja repo add <id>`). Kayıtlar yazılırken gizli anahtarlardan arındırılır, paralel dalların temiz birleşmesi için `merge=union` ile içerik özeti sırasıyla tutulur ve depoyu klonlayan her ekipte yüklenir (güven politikasında `{"activations":{"auto":{"repo":false}}}` kanca enjeksiyonunu kapatır). [Ayrıntılar](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Görevin ortasında araç mı değiştiriyorsunuz? `deja handoff --to <agent>` çalıştırın (veya MCP üzerinden `handoff` çağırın); etkin oturumun hedefi, kararları, dokunulan dosyaları, geçen testleri ve açık işleri başka kodlama ajanına aktarılır.

## ## Lisans

MIT

İşinize yaradı mı? [GitHub'da deja-vu'ya yıldız verin](https://github.com/vshulcz/deja-vu).
