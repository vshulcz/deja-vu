# @vshulcz/pi-deja

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | Türkçe | [हिन्दी](hi.md)

**[deja-vu](https://github.com/vshulcz/deja-vu)** eklentisi (LongMemEval-S'te **%97.2 R@5** · 19,195 oturumu **13.6 sn**'de indeksleme · agentmemory'nin **yarısı kadar token**) · [Belgeler](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Canlı demo](https://vshulcz.github.io/deja-vu/view/) · [Ölçümler](https://vshulcz.github.io/deja-vu/guide/benchmarks.html) · **[★ GitHub'da yıldız ver](https://github.com/vshulcz/deja-vu)**

![deja-vu demosu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

[pi](https://github.com/badlogic/pi-mono) için makinenizdeki diğer 48 kodlama ajanının (Claude Code, Codex, Cursor, Gemini, OpenClaw, Hermes vb.) diskteki oturum geçmişini yerel belleğe dönüştüren eklenti. Tek Go ikilisi `deja` ve yerel BM25 dizini kullanır; LLM veya embedding gerektirmez ve açıkça çalıştırılan güncelleme/eşitleme komutları ile `DEJA_OFFLINE=1` ile kapatılabilen günlük sürüm denetimi dışında ağa çıkmaz.

## Kurulum

```sh
pi install npm:@vshulcz/pi-deja
```

Sisteminizde `deja` henüz yoksa paket [Hangi ikili dosya kullanılır](#hangi-ikili-dosya-kullanılır) sırasını izleyerek paket içi kopyayı kullanır. Dilerseniz ana ikiliyi önceden kurabilirsiniz:

```sh
brew install deja-vu
```

Makinede `deja install pi-auto` veya `deja install --auto` ile kurulmuş bağımsız eklenti varsa bu paket çift kayıt oluşmaması için otomatik olarak geri çekilir. Yapı `pi.extensions` ve `before_agent_start` üzerinden `omp` ile de uyumludur.

## Özellikler

- **Oturum başlangıcı (alt durum çubuğu)**: etkin proje için dizinlenen oturumları ve eşleşme bilgisini gösterir.
- **Her istemde (`before_agent_start`)**: mevcut proje kararlarını ve eski çözümleri model yanıtından önce ekler.
- **Hatalı komut sonrası (`bash` / `powershell` `tool_result`)**: aynı hatayı daha önce çözen komutu hemen gösterir.
- **Okuma/düzenleme anında (`read`/`edit`/`write` `tool_result`)**: ilgili dosya hakkındaki geçmiş notları hatırlatır.
- **Sıkıştırma sonrasında (`session_compact`)**: özet sonrası kaybolabilen görev, karar ve açık komutları yeniden sunar.
- **Oturum kapanışı (`session_shutdown`)**: oturumu yerel dizine işler.
- **`/deja <query>`**: tüm ajan geçmişini sohbet içinden arar.

## Hangi ikili dosya kullanılır

`deja` çalıştırılabilir dosyası şu sırayla aranır:

1. `DEJA_BIN` ortam değişkeni
2. `PATH` içindeki `deja`
3. Bilinen kurulum yolları (`~/.local/bin`, `/opt/homebrew/bin`, `/usr/local/bin` vb.)
4. `@vshulcz/deja-vu` paketindeki kopya

## Ölçümler

| İsabet (Recall) | Token | İndeksleme ve sorgu |
|---|---|---|
| LongMemEval-S'te **%97.2 R@5** (470 soruda %88.9 hit@1; MemPalace %96.6, agentmemory %95.2 yayımlar) | **%58 daha az token** (tekrarlanan depo işlerinde agentmemory'nin **yarısı kadar token**) | 19,195 oturumu **13.6 sn**'de indeksler / 49 kodlama ajanı genelinde **0.7 ms** sorgu süresi (7 araçtan sonraki: 72 sn) |

## Depo belleği ve ajanlar arası devir (`handoff`)

Onaylanmış proje kararlarını ve çıkmaz yolları git üzerinden `.deja/memory.jsonl` dosyasında paylaşın (`deja repo candidates`, `deja repo add <id>`). Kayıtlar yazılırken gizli anahtarlardan arındırılır, paralel dalların temiz birleşmesi için `merge=union` ile içerik özeti sırasıyla tutulur ve depoyu klonlayan her ekipte yüklenir (güven politikasında `{"activations":{"auto":{"repo":false}}}` kanca enjeksiyonunu kapatır). [Ayrıntılar](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Görevin ortasında araç mı değiştiriyorsunuz? `deja handoff --to <agent>` çalıştırın (veya MCP üzerinden `handoff` çağırın); etkin oturumun hedefi, kararları, dokunulan dosyaları, geçen testleri ve açık işleri başka kodlama ajanına aktarılır.

## Gizlilik

Dizin `~/.cache/deja/index.db` konumunda saklanır (`DEJA_INDEX_DIR` ile değiştirilebilir); `sk-*`, `ghp_*`, `AKIA*` ve `Bearer` anahtarları dizinleme sırasında temizlenir. Açıkça `deja embed` çalıştırılmadıkça veriler cihazdan çıkmaz. 49 kodlama ajanının tamamında `recall`, `fix`, `blame` ve `handoff` kurulumu için `deja install --auto` komutunu kullanabilirsiniz.

## Lisans

[deja-vu](https://github.com/vshulcz/deja-vu) parçasıdır. MIT. İşinize yaradı mı? [GitHub'da deja-vu'ya yıldız verin](https://github.com/vshulcz/deja-vu).
