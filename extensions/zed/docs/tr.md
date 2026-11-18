# deja for Zed

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | Türkçe | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

**[deja-vu](https://github.com/vshulcz/deja-vu)** eklentisi (LongMemEval-S'te **%97.2 R@5** · 19,195 oturumu **13.6 sn**'de indeksleme · agentmemory'nin **yarısı kadar token**) · [Belgeler](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Canlı demo](https://vshulcz.github.io/deja-vu/view/) · [Ölçümler](https://vshulcz.github.io/deja-vu/guide/benchmarks.html) · **[★ GitHub'da yıldız ver](https://github.com/vshulcz/deja-vu)**

[deja](https://github.com/vshulcz/deja-vu), kodlama ajanlarının diske yazdığı oturum dosyalarını — Claude Code, Codex, Cursor, opencode ve 45 diğer aracı — indeksler ve bunlardan yanıt üretir. Bu eklenti aynı indeksi Zed'in ajan paneline bağlam sunucusu (context server) olarak sunar; böylece bir sohbet deja kurulmadan önceki işler de dahil geçmişi arayabilir.

![deja-vu demosu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Ajana sunulan araç

`mode` parametresiyle çağrılan tek bir `deja` aracı:

| Mode | Yanıtladığı bilgi |
|---|---|
| `recall` | Hata metni, işlev adı, dosya yolu veya bayrakla eşleşen oturumlar. |
| `context` | Gerekçenin önemli olduğu tek bir geçmiş oturumun tam özeti. |
| `blame` | Düzenlemeden veya silmeden önce o dosyayı ele alan oturumlar. |
| `fix` | Daha önce aynı hatadan sonra bu makinede çalıştırılan düzeltme. |
| `how` | Buradaki çalıştırmalardan çıkarılan gerçek derleme, test veya dağıtım komutu. |
| `orient` | Geçmiş oturumların bu projede çalıştırdığı komutlar ve dokunduğu dosyalar. |
| `remember` | Daha sonra hatırlamak üzere kalıcı bir kararı saklar. |
| `handoff` | Başka bir oturumun kaldığı yerden devam etmek için durum bilgisini getirir. |

## Kurulum

Eklentiyi Zed'in eklenti listesinden kurun. İlk kullanımda Zed'den `deja --version` çalıştırmasını ister; başarılı olursa Zed `PATH`'indeki `deja`'yı kullanır. Aksi halde kendi dizinine bir sürüm `deja` ikilisi indirir. Eklentiler korumalı alanda (sandbox) çalıştığından olağan kurulum yollarını tarayamaz; Zed başlatılırken `PATH` üzerinde olmayan bir `deja` açıkça belirtilmelidir:

```json
{
  "context_servers": {
    "deja-context-server": {
      "settings": {
        "binary": "/opt/homebrew/bin/deja"
      }
    }
  }
}
```

`deja install --auto` da Zed'i doğrudan `settings.json` içine yazar — CLI kuruluysa daha kısa yoldur. İkisi de aynı `deja-context-server` kimliğini kullanır ve Zed sunucuları kimliğe göre sakladığından hangi sırayla kurulursa kurulsun tek sunucu kalır. CLI'ın yazdığını kaldırmak için `deja uninstall zed` kullanın.

İndeksleme ve arama yereldir: ağ çağrısı yapılmaz ve indeks oluşturulurken kimlik bilgileri temizlenir.

## Ölçümler

| İsabet (Recall) | Token | İndeksleme ve sorgu |
|---|---|---|
| LongMemEval-S'te **%97.2 R@5** (470 soruda %88.9 hit@1; MemPalace %96.6, agentmemory %95.2 yayımlar) | **%58 daha az token** (tekrarlanan depo işlerinde agentmemory'nin **yarısı kadar token**) | 19,195 oturumu **13.6 sn**'de indeksler / 49 kodlama ajanı genelinde **0.7 ms** sorgu süresi (7 araçtan sonraki: 72 sn) |

## Depo belleği ve ajanlar arası devir (`handoff`)

Onaylanmış proje kararlarını ve çıkmaz yolları git üzerinden `.deja/memory.jsonl` dosyasında paylaşın (`deja repo candidates`, `deja repo add <id>`). Kayıtlar yazılırken gizli anahtarlardan arındırılır, paralel dalların temiz birleşmesi için `merge=union` ile içerik özeti sırasıyla tutulur ve depoyu klonlayan her ekipte yüklenir (güven politikasında `{"activations":{"auto":{"repo":false}}}` kanca enjeksiyonunu kapatır). [Ayrıntılar](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Görevin ortasında araç mı değiştiriyorsunuz? `deja handoff --to <agent>` çalıştırın (veya MCP üzerinden `handoff` çağırın); etkin oturumun hedefi, kararları, dokunulan dosyaları, geçen testleri ve açık işleri başka kodlama ajanına aktarılır.

MIT lisansı, deja ile aynı.

[deja-vu](https://github.com/vshulcz/deja-vu) parçasıdır. MIT. İşinize yaradı mı? [GitHub'da deja-vu'ya yıldız verin](https://github.com/vshulcz/deja-vu).
