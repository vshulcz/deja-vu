# deja-vu for JetBrains IDEs

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)
[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | Türkçe | [हिन्दी](hi.md)

IntelliJ IDEA, GoLand, PyCharm, WebStorm, RustRover, CLion, PhpStorm ve Rider (`2024.2+`) için yerel ajanlar arası oturum arama, AI satır geçmişi (Blame) ve iş devri eklentisi.

[deja-vu](https://github.com/vshulcz/deja-vu); Claude Code, Codex, OpenCode, Cursor, Gemini CLI, Junie, GitHub Copilot, Aider, Windsurf ve 39 diğer kodlama ajanının diske yazdığı oturum kayıtlarını SQLite FTS5 içinde yerel olarak indeksler, çıktı vermeden önce gizli anahtarları temizler ve bulut servisi kullanmaz.

**[Belgeler](https://vshulcz.github.io/deja-vu/guide/getting-started.html)** · **[Ölçümler](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)**

![deja-vu demosu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Özellikler

- **Durum çubuğu bileşeni**: Etkin proje için canlı `deja statusline` istatistiklerini IDE durum çubuğunun sağında gösterir. Tıklandığında `deja-vu` eylem penceresini açar.
- **AI oturumlarında arama (`Tools → deja-vu → Search AI Sessions...`)**: Desteklenen 50 kodlama ajanının tümünde yerel oturumları sorgular (`deja search --json`) ve seçilen kaydı salt okunur Markdown sekmesinde açar.
- **Son AI oturumları (`Tools → deja-vu → Recent AI Sessions`)**: Makinedeki en güncel 50 oturumu (`deja last 50 --json`) hızlı arama filtresiyle listeler.
- **Geçerli satır için AI Blame (Düzenleyici sağ tık menüsü / `Tools → deja-vu → AI Blame Current Line`)**: `deja blame <file>:<line> --json` komutunu (satır eşleşmezse `<file>` yedeğiyle) çalıştırır ve imlecin altındaki satırı yazan veya düzenleyen AI oturumunu açar.
- **Ajanlar arası devir özeti kopyala (`Tools → deja-vu → Copy Cross-Agent Handoff`)**: Proje kökünde `deja handoff` çalıştırır, özeti panoya kopyalar ve salt okunur Markdown sekmesinde açar.

## Kurulum

1. `deja` CLI aracını kurun ve yerel kodlama ajanlarınızı bağlayın:

```sh
brew install deja-vu && deja install --auto
# veya Homebrew olmadan:
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

2. JetBrains eklentisini ([JetBrains Marketplace'te `com.github.vshulcz.deja-vu`](https://plugins.jetbrains.com/plugin/35998-deja-vu-cross-agent-ai-session-memory)) kurun:

- **JetBrains Marketplace üzerinden**: [plugins.jetbrains.com adresinden doğrudan kurun](https://plugins.jetbrains.com/plugin/35998-deja-vu-cross-agent-ai-session-memory) ya da **Settings → Plugins → Marketplace** bölümünden `deja-vu` arayın.
- **Diskten**: [GitHub Releases](https://github.com/vshulcz/deja-vu/releases/latest) üzerinden `deja-jetbrains.zip` dosyasını indirip **Settings → Plugins → ⚙️ → Install Plugin from Disk...** ile kurun.
- **CLI ile**:

```sh
idea installPlugins com.github.vshulcz.deja-vu
```

*(Kullandığınız IDE'ye göre `idea` yerine `goland`, `pycharm`, `webstorm`, `rustrover` veya `clion` yazın.)*

## Kaynaktan derleme

```sh
cd extensions/jetbrains
./gradlew buildPlugin
```

Kurulabilir arşiv `build/distributions/` dizinine yazılır.

## Ölçümler

| İsabet (Recall) | Token | İndeksleme ve sorgu |
|---|---|---|
| LongMemEval-S'te **%97.2 R@5** (470 soruda %88.9 hit@1) | **%58 daha az token** (tekrarlanan depo işlerinde) | 19,195 oturumu **13.6 sn**'de indeksler / 50 kodlama ajanı genelinde **0.7 ms** sorgu süresi |

## Depo belleği ve ajanlar arası devir (`handoff`)

Onaylanmış proje kararlarını ve çıkmaz yolları git üzerinden `.deja/memory.jsonl` dosyasında paylaşın (`deja repo candidates`, `deja repo add <id>`). Kayıtlar yazılırken gizli anahtarlardan arındırılır, paralel dalların temiz birleşmesi için `merge=union` ile içerik özeti sırasıyla tutulur ve depoyu klonlayan her ekipte yüklenir (güven politikasında `{"activations":{"auto":{"repo":false}}}` kanca enjeksiyonunu kapatır). [Ayrıntılar](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Görevin ortasında araç mı değiştiriyorsunuz? `deja handoff --to <agent>` çalıştırın (veya MCP üzerinden `handoff` çağırın); etkin oturumun hedefi, kararları, dokunulan dosyaları, geçen testleri ve açık işleri başka kodlama ajanına aktarılır.

## Lisans

MIT — [github.com/vshulcz/deja-vu](https://github.com/vshulcz/deja-vu)
