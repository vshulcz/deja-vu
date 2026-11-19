# Raycast için deja-vu

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | Türkçe | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

**[deja-vu](https://github.com/vshulcz/deja-vu)** eklentisi (LongMemEval-S'te **%97.2 R@5** · 19,195 oturumu **13.6 sn**'de indeksleme · agentmemory'nin **yarısı kadar token**) · [Belgeler](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Canlı demo](https://vshulcz.github.io/deja-vu/view/) · [Ölçümler](https://vshulcz.github.io/deja-vu/guide/benchmarks.html) · **[★ GitHub'da yıldız ver](https://github.com/vshulcz/deja-vu)**

Claude Code, Codex, Cursor, OpenCode, Gemini CLI ve 44 diğer araçtaki kodlama ajanı oturumlarını doğrudan Raycast içinden arayın ve sürdürün.

![deja-vu demosu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Ön koşullar

[`deja-vu`](https://github.com/vshulcz/deja-vu) aracını kurun:

```sh
brew install deja-vu && deja install --auto
```

## Komutlar

- **Search Sessions**: Mac'inizdeki tüm kodlama ajanı oturumlarında tam metin araması (`deja search --json --limit 25 -- <query>`), bire bir eşleşmeleri yakın ilgi puanlı sonuçlardan ayırır.
- **Recent Sessions**: Kurulu tüm ajanlardaki en son 100 oturumu üst veri önizlemesiyle (`Agent`, `Project`, `Started`, `Updated`, `Session ID`) tarar (`deja last 100 --json`).

## Eylemler

- **Show Session**: Gizli anahtarlardan arındırılmış konuşmayı Raycast içinde okur (`deja show <id> --json --limit 200`).
- **Resume in Terminal**: Ajanın kendi sürdürme komutunu (`deja resume <id>`) `Terminal.app` içinde çalıştırır.
- **Copy Resume Command**, **Copy Session ID** ve **Open Project Folder**.

## Ölçümler

| İsabet (Recall) | Token | İndeksleme ve sorgu |
|---|---|---|
| LongMemEval-S'te **%97.2 R@5** (470 soruda %88.9 hit@1; MemPalace %96.6, agentmemory %95.2 yayımlar) | **%58 daha az token** (tekrarlanan depo işlerinde agentmemory'nin **yarısı kadar token**) | 19,195 oturumu **13.6 sn**'de indeksler / 49 kodlama ajanı genelinde **0.7 ms** sorgu süresi (7 araçtan sonraki: 72 sn) |

## Depo belleği ve ajanlar arası devir (`handoff`)

Onaylanmış proje kararlarını ve çıkmaz yolları git üzerinden `.deja/memory.jsonl` dosyasında paylaşın (`deja repo candidates`, `deja repo add <id>`). Kayıtlar yazılırken gizli anahtarlardan arındırılır, paralel dalların temiz birleşmesi için `merge=union` ile içerik özeti sırasıyla tutulur ve depoyu klonlayan her ekipte yüklenir (güven politikasında `{"activations":{"auto":{"repo":false}}}` kanca enjeksiyonunu kapatır). [Ayrıntılar](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Görevin ortasında araç mı değiştiriyorsunuz? `deja handoff --to <agent>` çalıştırın (veya MCP üzerinden `handoff` çağırın); etkin oturumun hedefi, kararları, dokunulan dosyaları, geçen testleri ve açık işleri başka kodlama ajanına aktarılır.

## Yerel geliştirme

```sh
cd extensions/raycast && npm install && npx ray develop
```

[deja-vu](https://github.com/vshulcz/deja-vu) parçasıdır. MIT. İşinize yaradı mı? [GitHub'da deja-vu'ya yıldız verin](https://github.com/vshulcz/deja-vu).
