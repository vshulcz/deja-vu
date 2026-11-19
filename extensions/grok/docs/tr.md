# Grok Build için deja

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | Türkçe | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

**[deja-vu](https://github.com/vshulcz/deja-vu)** eklentisi (LongMemEval-S'te **%97.2 R@5**) · [Belgeler](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Canlı demo](https://vshulcz.github.io/deja-vu/view/) · [Ölçümler](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![deja-vu demosu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

Claude Code, Codex, Cursor, opencode, Zed ve diğer 44 kodlama ajanındaki yerel oturum geçmişinizden gelen kalıcı bellek. Tek Go ikilisi `deja` üzerinden tamamen yerel çalışır (`DEJA_OFFLINE=1` günlük sürüm kontrolünü kapatır).

## Kurulum

Önce `deja` ikilisini kurun (eklenti paketi yerel çalıştırılabilir dosya taşımaz):

```sh
brew install deja-vu
# veya:
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

Ardından mağazadan eklentiyi ekleyin ([xai-org/plugin-marketplace#332](https://github.com/xai-org/plugin-marketplace/pull/332)):

```sh
grok plugin marketplace add vshulcz/deja-vu
grok plugin install deja
```

İsterseniz `~/.grok/config.toml` (`[mcp_servers.deja]`) ve `~/.grok/hooks/deja.json` yapılandırmasını doğrudan `deja install --auto` ile yazabilirsiniz.

## Neler geliyor

- **`deja` MCP aracı** (8 kip: `recall`, `fix`, `blame`, `how`, `orient`, `context`, `remember`, `handoff`)
- **`deja-history` becerisi** ve **`/deja:recall <query>`** komutu
- **5 kanca (`hooks/recall.mjs`)**: `SessionStart`, `UserPromptSubmit`, `PreToolUse` (`updatedInput`), `PostToolUse` (Grok 1.0.41 üzerinde `Bash` için), `PreCompact` ve `SessionEnd`

## Çift kurulum çakışmaz

`~/.grok/config.toml` içinde `[mcp_servers.deja]` varsa veya `~/.grok/hooks/deja.json` zaten yapılandırılmışsa eklenti geri çekilir (`deja uninstall grok` CLI kaydını kaldırır).

## İkili dosya çözümü

`DEJA_BIN` -> `~/.local/bin`, `/usr/local/bin`, `/opt/homebrew/bin`, `/usr/bin` -> `PATH`.

## İkili yokken sorunsuz geri çekilme

`${GROK_PLUGIN_ROOT}` ve `.mcp.json` ile bağlanır. Grok Build 1.0.5 üzerinde `deja` kurulu değilken `grok mcp doctor` ve `grok inspect` çıktısı `handshake failed: connection closed` gösterebilir; `deja` kurulunca düzelir.

## Ölçümler

| İsabet (Recall) | Token | İndeksleme ve sorgu |
|---|---|---|
| LongMemEval-S'te **%97.2 R@5** (470 soruda %88.9 hit@1) | **%58 daha az token** (tekrarlanan depo işlerinde) | 19,195 oturumu **13.6 sn**'de indeksler / 49 kodlama ajanı genelinde **0.7 ms** sorgu süresi |

## Depo belleği ve ajanlar arası devir (`handoff`)

Onaylanmış proje kararlarını ve çıkmaz yolları git üzerinden `.deja/memory.jsonl` dosyasında paylaşın (`deja repo candidates`, `deja repo add <id>`). Kayıtlar yazılırken gizli anahtarlardan arındırılır, paralel dalların temiz birleşmesi için `merge=union` ile içerik özeti sırasıyla tutulur ve depoyu klonlayan her ekipte yüklenir (güven politikasında `{"activations":{"auto":{"repo":false}}}` kanca enjeksiyonunu kapatır). [Ayrıntılar](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Görevin ortasında araç mı değiştiriyorsunuz? `deja handoff --to <agent>` çalıştırın (veya MCP üzerinden `handoff` çağırın); etkin oturumun hedefi, kararları, dokunulan dosyaları, geçen testleri ve açık işleri başka kodlama ajanına aktarılır.

## ## Lisans

MIT
