# deja for Kimi Code

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | Türkçe | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

**[deja-vu](https://github.com/vshulcz/deja-vu)** eklentisi (LongMemEval-S'te **%97.2 R@5**) · [Belgeler](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Canlı demo](https://vshulcz.github.io/deja-vu/view/) · [Ölçümler](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

[deja](https://github.com/vshulcz/deja-vu), kodlama ajanlarının diske yazdığı oturum dosyalarını — Claude Code, Codex, Cursor, opencode ve 45 diğer aracı — indeksler ve bunlardan yanıt verir. Bu eklenti o indeksi Kimi Code içine getirir: hatırlatmalar istemle birlikte gelir ve ajan geçmişi kendisi arayabilir.

![deja-vu demosu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Kurulum

```
/plugins install https://github.com/vshulcz/deja-vu
/reload
```

Bu biçim en son sürümü sabitler ve Kimi'nin güncelleme denetiminin okuduğu kaynağı kaydeder. Tüm depo yerine yalnızca ~10 KB boyutunda `kimi-deja.zip` sürüm paketi de sunulur.

Henüz kurulu değilse önce `deja` ikilisini kurun (ve isteğe bağlı olarak makinedeki diğer ajanları da bağlayın):

```
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

Eklentiler yönetilen kopyadan yüklendiği için kurulumdan sonra `/reload` veya `/new` çalıştırın.

## Güncellemeler

Kimi yalnızca kendi pazar yerinden kurulan eklentiler için güncelleme bildirimi verir. Depo kurulumunda `/plugins install` komutunu yeniden çalıştırmak güncel sürümü çeker; elinizdeki kopya bu `deja` sürümünün gerisindeyse `deja doctor` uyarır:

```
kimi  plugin  ~/.kimi-code/config.toml  (v0.21.2 installed, v0.22.0 ships with this deja — reinstall it in Kimi to update)
```

## Neler elde edersiniz

- **`deja install kimi-auto` tarafından yazılan kancalar**: Oturumun ilk isteminde bu projedeki kararlar eklenir (`deja hook-context --once`). Her istemde sorulan konuya uygun kayıtlar tura dahil edilir (`deja hook-prompt`); eşleşme yoksa hiçbir şey eklenmez. Sıkıştırma öncesinde özetlenecek turlar okunur ki sonraki istem yarım kalan işi sürdürebilsin (`deja hook-precompact`). `SessionEnd` anında oturumun canlı damgası kaldırılır (`deja hook-session-end`).
- **Araçlar**: Eklenti `deja mcp` sunucusunu MCP olarak tanımlar; `recall`, `context`, `blame`, `fix`, `how`, `orient`, `remember` veya `handoff` modlarına sahip tek bir `deja` aracı sunar.
- **`/deja:recall <query>`**: Geçmişi doğrudan sorgulamak istediğiniz anlar için.
- **`deja-history` becerisi**: Oturum başında yüklenir ve ajanın bir hatayı yeniden ayıklamadan önce geçmişe bakmasını sağlar.

## Hangi ikili kullanılır

Sırayla: `DEJA_BIN`, kendi kurduğunuz `deja` (`~/.local/bin`, `/usr/local/bin`, `/opt/homebrew/bin`, `/usr/bin`) ve ardından `PATH` üzerindeki `deja`. Kendi `deja update` veya `brew upgrade deja-vu` güncellemeniz her zaman önceliklidir.

## `deja install kimi` de çalıştırdıysanız

CLI aynı MCP sunucusunu `~/.kimi-code/mcp.json` dosyasına ve aynı kancayı `config.toml` içine yazar. Kimi eklenti sunucusunu kendi ad alanında (`plugin-deja:deja`) çalıştırdığı için her ikisi de etkin olursa araçlar iki kez listelenir ve hatırlatma iki kez eklenir.

Bu eklenti kanca tetiklendiğinde ve sunucu başladığında CLI kurulumunu kontrol eder; bulduğunda geri çekilir çünkü `deja install` ile kurulan kopyayı güncel tutan araç kendisidir. `deja uninstall kimi` komutu yeniden kurulum gerekmeden yönetimi eklentiye geri verir.

Yalnızca eklenti devredeyken `deja doctor`, kimi için `plugin` bilgisini gösterir ve kurulumsuzmuş gibi hata vermez.

Göremeyeceği iki durum vardır: `deja` dışında bir adla elle eklenmiş MCP girişi ve kurucunun işaret yorumu olmadan `deja` çağıran özel kancalar.

## Ölçümler

| İsabet (Recall) | Token | İndeksleme ve sorgu |
|---|---|---|
| LongMemEval-S'te **%97.2 R@5** (470 soruda %88.9 hit@1) | **%58 daha az token** (tekrarlanan depo işlerinde) | 19,195 oturumu **13.6 sn**'de indeksler / 49 kodlama ajanı genelinde **0.7 ms** sorgu süresi |

## Depo belleği ve ajanlar arası devir (`handoff`)

Onaylanmış proje kararlarını ve çıkmaz yolları git üzerinden `.deja/memory.jsonl` dosyasında paylaşın (`deja repo candidates`, `deja repo add <id>`). Kayıtlar yazılırken gizli anahtarlardan arındırılır, paralel dalların temiz birleşmesi için `merge=union` ile içerik özeti sırasıyla tutulur ve depoyu klonlayan her ekipte yüklenir (güven politikasında `{"activations":{"auto":{"repo":false}}}` kanca enjeksiyonunu kapatır). [Ayrıntılar](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Görevin ortasında araç mı değiştiriyorsunuz? `deja handoff --to <agent>` çalıştırın (veya MCP üzerinden `handoff` çağırın); etkin oturumun hedefi, kararları, dokunulan dosyaları, geçen testleri ve açık işleri başka kodlama ajanına aktarılır.

## Lisans

MIT

[deja-vu](https://github.com/vshulcz/deja-vu) parçasıdır. MIT.
