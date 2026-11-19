# CodeWhale için deja-vu

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | Türkçe | [हिन्दी](hi.md)

CodeWhale için Claude uyumlu eklenti paketi — 49 yapay zeka kodlama aracından gelen oturumlar üzerinde `deja` MCP sunucusunu (`recall`, `fix`, `blame`, `how`, `orient`, `context`, `remember`, `handoff`), `deja-history` becerisini ve `/deja` komutunu sunar.

![deja-vu demosu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Kurulum

Gereksinim: [`deja`](https://github.com/vshulcz/deja-vu#install) ikilisinin `PATH` üzerinde kurulu olması.

Bu paketi CodeWhale içinde `/plugin install` ile depo alt dizininden (`extensions/codewhale`) kurun.

CodeWhale'in Claude eklenti uyumluluk yükleyicisi MCP sunucularını, becerileri ve eğik çizgi komutlarını içe aktarıp `hooks` ile `lspServers` alanlarını yok saydığı için `~/.codewhale/config.toml` içine yerel CodeWhale yaşam döngüsü kancalarını (`message_submit`, `tool_call_before`, `tool_call_after`, `session_end`) da bağlamak isterseniz `deja install codewhale-auto` (veya `deja install --auto`) çalıştırın (`deja install codewhale` yalnızca MCP ve `tools.always_load` kurar):

```sh
deja install codewhale-auto
```

## Paket içeriği

- `.claude-plugin/plugin.json`: eklenti üst verisi (`name: deja-vu`)
- `.mcp.json`: `deja mcp` stdio sunucu kaydı
- `skills/deja-history/SKILL.md`: `deja` kipleri için beceri yönergeleri
- `commands/deja.md`: `/deja` eğik çizgi komutu

## Ölçümler

| İsabet (Recall) | Token | İndeksleme ve sorgu |
|---|---|---|
| LongMemEval-S'te **%97.2 R@5** (470 soruda %88.9 hit@1) | **%58 daha az token** (tekrarlanan depo işlerinde) | 19,195 oturumu **13.6 sn**'de indeksler / 49 kodlama ajanı genelinde **0.7 ms** sorgu süresi |

## Depo belleği ve ajanlar arası devir (`handoff`)

Onaylanmış proje kararlarını ve çıkmaz yolları git üzerinden `.deja/memory.jsonl` dosyasında paylaşın (`deja repo candidates`, `deja repo add <id>`). Kayıtlar yazılırken gizli anahtarlardan arındırılır, paralel dalların temiz birleşmesi için `merge=union` ile içerik özeti sırasıyla tutulur ve depoyu klonlayan her ekipte yüklenir (güven politikasında `{"activations":{"auto":{"repo":false}}}` kanca enjeksiyonunu kapatır). [Ayrıntılar](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Görevin ortasında araç mı değiştiriyorsunuz? `deja handoff --to <agent>` çalıştırın (veya MCP üzerinden `handoff` çağırın); etkin oturumun hedefi, kararları, dokunulan dosyaları, geçen testleri ve açık işleri başka kodlama ajanına aktarılır.

## Lisans

[MIT](../LICENSE)
