# envmove — iki makineyi birbirine bağlama

Bugün için 3 adım var. Her adımda ne beklemen gerektiğini yazdım.

Şu anki durum:

```
bu bilgisayar   envmove v0.3.0 ✓    kendi anahtarı: yok
uzak deposu      şifreli state var, ama sadece 1 makine okuyabilir
bu bilgisayarda .env.local yok — çünkü daha hiç gelmedi
```

---

## Adım 1 — Bu bilgisayarı tanıt (BURADA, bu bilgisayarda)

```bash
cd ~/Desktop/Booking\ Layer
envmove setup
```

Sorulara şöyle cevap ver:

| soru | cevap |
|---|---|
| `set this repository up?` | `y` |
| `branch name ...` | boş bırak (Enter) |
| `carry them all / none` | `all` |
| `generate a recovery passphrase for you?` | `n` — **kendi şifreni yaz** |
| `recovery passphrase:` | **en az 12 karakter**, 1Password'a kaydet |
| `commit and push it now` | `y` |
| `I understand the risk, continue?` | `y` |

O son uyarı şunu söylüyor: şifreli dosyalar gidişe dönük olarak repoda kalıcı olur. Repo'n
privat olduğunu biliyorum, yine de onay vermen gerekiyor.

**Beklenen çıktı:**

```
+ this machine's public key added: age1...
✓ envmove.toml written (1 files)
✓ envmove.toml published
✓ git hooks installed (post-commit, pre-push)
```

Buraya kadar bırak ve bana dön. Çünkü bir sonraki adım **diğer bilgisayarda** yapılacak.

---

## Adım 2 — Diğer bilgisayarda (orada çalıştır)

```bash
git pull
envmove
```

`git pull` senin gönderdiğin `envmove.toml`'u alır ve iki alıcı olur. `envmove` bunu fark eder
ve **mevcut şifreli dosyayı iki anahtara birden şifreler.**

**Beklenen çıktı:**

```
✓ pushed to the "envmove" branch
```

Eğer `↓ .env.local` gibi bir şey görürsen normal, hâlâ eski anahtarla şifreli demektir; bir
kez daha `envmove` çalıştır.

---

## Adım 3 — Buraya dön, dosyaları çek

```bash
cd ~/Desktop/Booking\ Layer
envmove pull
ls -a | grep env
```

**Beklenen çıktı:**

```
↓ .env.local

.env.local
```

`.env.local` diğer bilgisayardan geldi. Artık bu bilgisayarda da var.

---

## Bundan sonra

Hiçbir şey yapmana gerek yok:

| ne yaparsın | ne olur |
|---|---|
| `.env.local`'ı düzenlersin | bir sonraki commit'te gider |
| `git commit` | context kodla birlikte gider |
| `git push` | context bırakıldıysa engellenir |
| agent oturumu açar | context çekilir, brifing basılır |

Bir şey ters giderse:

```bash
envmove doctor
```

---

## Bu adımlardan sonra yapmak isteyebileceklerin (isteğe bağlı)

**Diğer `.env` dosyalarını da taşımak.** v0.3.0 artık git'in ignore ettiği her şeyi taşıyor
(çöp listesi hariç). Booking Layer'ın `.gitignore`'ında `.env.development.local`,
`.env.test.local`, `.env.production.local` da var. Bunlar varsa, diğer bilgisayarda bir kez
`envmove setup` daha çalıştır — yeni dosyaları listeleyecek.

**Güvenlik.** Kullandığın kurtarma şifresini 1Password'a koy. O şifre tek geri dönüş yolun.