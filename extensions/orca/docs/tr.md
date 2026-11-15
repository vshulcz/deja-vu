# Orca için deja-vu

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | Türkçe | [हिन्दी](hi.md)

`deja` için Orca v2 `trusted-worker` eklentisi — `deja` kurulmadan önce kaydedilmiş olanlar dahil 48 yapay zeka kodlama aracından (Claude Code, Codex, Cursor, opencode, Gemini CLI, Orca ve diğerleri) gelen oturum belleğini Orca içine taşır.

![deja-vu demosu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Kurulum

Gereksinim: [`deja`](https://github.com/vshulcz/deja-vu#install) ikilisinin `PATH` üzerinde (veya `~/.local/bin`, `/opt/homebrew/bin`, `/usr/local/bin`, `~/bin`, `%LOCALAPPDATA%\Programs\deja` altında) bulunması.

Orca (`>=1.4.248`) içinde `extensions/orca` eklenti dizinini kurun.

Orca içinde yerel MCP, beceri dosyaları, `SessionStart`, `PreCompact`, `SessionEnd` ve başarısız komut sonrası düzeltme önerileri için şunu da çalıştırın:

```sh
deja install orca-auto
```

(`deja install orca` yaşam döngüsü kancaları olmadan yalnızca MCP, beceri ve `/deja` kurar; `deja install orca-auto` veya `deja install --auto` tam kanca setini ekler.)

`deja install orca-auto` Orca'nın `settings.json` dosyasına yerel `UserPromptSubmit` kancasını zaten yazmışsa bu worker `<deja-recall>` bloklarının çift eklenmemesi için kendiliğinden devreden çıkar.

## Bu eklentinin kattıkları

- **Kancalar**
  - `agent.prompt.before`: etkin çalışma alanı yolunu çözer ve Orca'nın 2 sn kanca bütçesi içinde (`1.3s` zaman aşımı, hata durumunda engellemeden geçer) ilgili `<deja-recall>` bağlamını isteme ekler.
  - `agent.tool.before`: `Agent` veya `Task` alt ajanlarını başlatırken `<deja-recall>` bağlamını yalnızca `input.prompt` alanına ekler (`Bash`, `Read`, `Edit` ve diğer araç girdilerini değiştirmez).
  - `agent.tool.after`: Orca konak API'sinde yalnızca gözlem amaçlıdır (`next` yok sayılır ve Orca beklemez); bu nedenle başarısız komut `fix` çıktısı `deja install orca-auto` üzerinden gelir.
- **Komutlar**
  - `deja.orient` (`deja: Orient on workspace`): etkin çalışma alanı için `deja hook-context --plain` çalıştırır ve özet bildirim gösterir.
  - `deja.recall` (`deja: Recall past sessions`): `deja search --limit 5 -- <query>` (sorgu verilmezse `deja last -n 5`) çalıştırır ve sonucu gösterir.

## Ölçümler

| İsabet (Recall) | Token | İndeksleme ve sorgu |
|---|---|---|
| LongMemEval-S'te **%97.2 R@5** (470 soruda %88.9 hit@1; MemPalace %96.6, agentmemory %95.2 yayımlar) | **%58 daha az token** (tekrarlanan depo işlerinde agentmemory'nin **yarısı kadar token**) | 19,195 oturumu **13.6 sn**'de indeksler / 48 kodlama ajanı genelinde **0.7 ms** sorgu süresi (7 araçtan sonraki: 72 sn) |

## Depo belleği ve ajanlar arası devir (`handoff`)

Onaylanmış proje kararlarını ve çıkmaz yolları git üzerinden `.deja/memory.jsonl` dosyasında paylaşın (`deja repo candidates`, `deja repo add <id>`). Kayıtlar yazılırken gizli anahtarlardan arındırılır, paralel dalların temiz birleşmesi için `merge=union` ile içerik özeti sırasıyla tutulur ve depoyu klonlayan her ekipte yüklenir (güven politikasında `{"activations":{"auto":{"repo":false}}}` kanca enjeksiyonunu kapatır). [Ayrıntılar](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Görevin ortasında araç mı değiştiriyorsunuz? `deja handoff --to <agent>` çalıştırın (veya MCP üzerinden `handoff` çağırın); etkin oturumun hedefi, kararları, dokunulan dosyaları, geçen testleri ve açık işleri başka kodlama ajanına aktarılır.

## Lisans

[MIT](../LICENSE)

İşinize yaradı mı? [GitHub'da deja-vu'ya yıldız verin](https://github.com/vshulcz/deja-vu).
