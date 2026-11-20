# @vshulcz/openclaw-deja

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | Türkçe | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

**[deja-vu](https://github.com/vshulcz/deja-vu)** eklentisi (LongMemEval-S'te **%97.2 R@5**) · [Belgeler](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Canlı demo](https://vshulcz.github.io/deja-vu/view/) · [Ölçümler](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![deja-vu demosu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

Makinenizdeki diğer 48 kodlama ajanının (Claude Code, Codex, Cursor, Gemini, Zed vb.) geçmişini [OpenClaw](https://openclaw.ai) içine getirir. Tek Go ikilisi `deja` ve yerel BM25 ile LLM olmadan çalışır (`DEJA_OFFLINE=1` günlük sürüm kontrolünü kapatır).

## Kurulum

```sh
openclaw plugins install clawhub:@vshulcz/openclaw-deja
```

Sistem ikilisini önceden kurmanız önerilir:

```sh
brew install deja-vu
```

`deja install openclaw-auto` veya `deja install --auto` zaten kuruluysa eklenti çift ekleme yapmamak için otomatik olarak çekilir.

## Özellikler

- **`agent:bootstrap` (`openclaw agent --local` dahil)**: oturum başında proje kararlarını bağlama ekler.
- **`before_prompt_build` (OpenClaw `2026.8.1` ve `hooks.allowConversationAccess: true` ile)**: her istem öncesi ilgili geçmişi ekler.
- **`before_compaction`**: sıkıştırma öncesinde aktif görev, sonuç ve açık komutları korur.
- **`session_end`**: kapanışta yerel dizini günceller.
- **Araç sonucu ara katmanı**: hata sonrası eski çözümü ve dosya üzerindeki eski kararları ekler.
- **`deja_recall` / `deja_fix` / `deja_blame` araçları**: ajan tarafından çağrılabilen arama, düzeltme komutu ve dosya geçmişi.
- **`/deja <query>`**: doğrudan mesaj kutusundan tüm araçlarda arama.

### DM kaynak bilgisi (Receipt)

Uygun doğrudan mesaj yanıtlarının altında `reply_payload_sending` aracılığıyla eğik bir kaynak satırı eklenir:

`_↺ from Codex CLI, 3 weeks ago: "how should the zorbit queue drain on deploy" · deja_`

Bu satır yalnızca dört koşulun tamamı sağlandığında eklenir:

1. Grup olmayan doğrudan mesajlaşma (`isGroup: false` / `direct`).
2. Gönderici `commands.ownerAllowFrom` / `allowFrom` içinde yetkili.
3. Blok akışı kapalı (`streaming.block.enabled` / `blockStreaming` / `channels.telegram.streaming.mode: "off"`).
4. `direct` kanalına giden son metin yanıtı.

`"receipt": false` ile kapatılabilir.

## Yapılandırma

```json
{
  "plugins": {
    "entries": {
      "deja-vu": {
        "enabled": true,
        "hooks": {
          "allowConversationAccess": true
        },
        "config": {
          "autoRecall": true,
          "tools": true,
          "receipt": true,
          "bin": "/opt/homebrew/bin/deja"
        }
      }
    }
  }
}
```

Veya CLI üzerinden:

```sh
openclaw config set plugins.entries.deja-vu.hooks.allowConversationAccess true
```

## İkili dosya sırası

`bin` -> `DEJA_BIN` -> `PATH` -> olağan konumlar -> `@vshulcz/deja-vu` paket kopyası.

## Ölçümler

| İsabet (Recall) | Token | İndeksleme ve sorgu |
|---|---|---|
| LongMemEval-S'te **%97.2 R@5** (470 soruda %88.9 hit@1) | **%58 daha az token** (tekrarlanan depo işlerinde) | 19,195 oturumu **13.6 sn**'de indeksler / 50 kodlama ajanı genelinde **0.7 ms** sorgu süresi |

## Depo belleği ve ajanlar arası devir (`handoff`)

Onaylanmış proje kararlarını ve çıkmaz yolları git üzerinden `.deja/memory.jsonl` dosyasında paylaşın (`deja repo candidates`, `deja repo add <id>`). Kayıtlar yazılırken gizli anahtarlardan arındırılır, paralel dalların temiz birleşmesi için `merge=union` ile içerik özeti sırasıyla tutulur ve depoyu klonlayan her ekipte yüklenir (güven politikasında `{"activations":{"auto":{"repo":false}}}` kanca enjeksiyonunu kapatır). [Ayrıntılar](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Görevin ortasında araç mı değiştiriyorsunuz? `deja handoff --to <agent>` çalıştırın (veya MCP üzerinden `handoff` çağırın); etkin oturumun hedefi, kararları, dokunulan dosyaları, geçen testleri ve açık işleri başka kodlama ajanına aktarılır.

## Gizlilik

Dizin `~/.cache/deja/index.db` yolundadır (`DEJA_INDEX_DIR`), `sk-*`, `ghp_*`, `AKIA*` ve `Bearer` dizinlemede gizlenir. `deja embed` kullanılmadığı sürece ağa veri gönderilmez. 50 kodlama ajanının tamamını tek adımda bağlamak için `deja install --auto` çalıştırın.

[deja-vu](https://github.com/vshulcz/deja-vu) parçasıdır. MIT.
