# VS Code, Cursor, Windsurf ve Antigravity için deja-vu

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | Türkçe | [हिन्दी](hi.md)

**Kodlama ajanlarınız zaten çözdüğünüz hataları baştan ayıklamayı bırakır.**

[deja-vu](https://github.com/vshulcz/deja-vu), Claude Code, Cursor, Codex, Windsurf, Copilot Chat, OpenCode, Cline, Roo Code ve 41 diğer kodlama ajanının diske yazdığı oturum dökümlerini yerel olarak indeksler — tek Go ikilisiyle, LLM ve arka plan servisi olmadan.

**[Belgeler](https://vshulcz.github.io/deja-vu/guide/getting-started.html)** · **[Ölçümler](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)**

![deja-vu demosu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Bu eklenti ne yapar

IDE durum çubuğunda canlı `deja statusline` bilgilerini gösterir (ajanlar genelinde indekslenen oturumlar, bugün sunulan hatırlamalar ve çalışma alanı geçmişi). `deja` henüz kurulu değilse durum çubuğuna tıklamak terminalde `npx -y @vshulcz/deja-vu install --auto` çalıştırıp algılanan tüm ajanları bağlar.

## Hızlı kurulum

```sh
brew install deja-vu && deja install --auto
# veya Homebrew olmadan:
npx -y @vshulcz/deja-vu install --auto
```

Durum çubuğu eklentisini CLI'dan kurun:

```sh
code --install-extension vshulcz.deja-status
```

## Ölçülen etki

| Ölçüt | Sonuç |
|---|---|
| LongMemEval-S erişim doğruluğu (470 temizlenmiş soru) | **%97.2 R@5**, **%88.9 hit@1** |
| Tekrarlanan depo görevlerinde token maliyeti | **%58 daha az token**|
| Süreç içi sözlüksel sorgu gecikmesi | 50 kodlama ajanı genelinde ortanca **0.7–0.8 ms** |
| 19,195 gerçek oturumun ilk indeksleme süresi | **13.6 sn**, API anahtarsız |

## Depo belleği ve ajanlar arası devir (`handoff`)

Onaylanmış proje kararlarını ve çıkmaz yolları git üzerinden `.deja/memory.jsonl` dosyasında paylaşın (`deja repo candidates`, `deja repo add <id>`). Kayıtlar yazılırken gizli anahtarlardan arındırılır, paralel dalların temiz birleşmesi için `merge=union` ile içerik özeti sırasıyla tutulur ve depoyu klonlayan her ekipte yüklenir (güven politikasında `{"activations":{"auto":{"repo":false}}}` kanca enjeksiyonunu kapatır). [Ayrıntılar](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Görevin ortasında araç mı değiştiriyorsunuz? `deja handoff --to <agent>` çalıştırın (veya MCP üzerinden `handoff` çağırın); etkin oturumun hedefi, kararları, dokunulan dosyaları, geçen testleri ve açık işleri başka kodlama ajanına aktarılır.

## Lisans

MIT — [github.com/vshulcz/deja-vu](https://github.com/vshulcz/deja-vu)

[deja-vu](https://github.com/vshulcz/deja-vu) parçasıdır. MIT.
