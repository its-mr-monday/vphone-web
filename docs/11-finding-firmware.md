# 11. Finding firmware (iPhone IPSW + CloudOS/PCC)

Every VM build needs **two** firmwares (see [Your first VM](02-first-vm.md#two-firmware-sources-iphone--cloudos)):

- an **iPhone** IPSW — the iOS device firmware (`IPHONE_SOURCE`), and
- a **CloudOS** IPSW — Apple's **Private Cloud Compute (PCC)** research stack
  (`CLOUDOS_SOURCE`).

Which CloudOS pairs with which iOS is **not** 1:1 — newer iOS runs on an older
PCC stack. The authoritative pairing is the compatibility table in the
[vphone-cli README](../vphone-cli/README.md#tested-environments). Quick reference
for `iPhone17,3`:

| iOS (iPhone firmware) | CloudOS (PCC) to use |
|-----------------------|----------------------|
| 26.0 – 26.1 (`23A…`/`23B85`) | **26.1** (`23B85`) — this is vphone-cli's built-in default |
| 26.3 (`23D127`) | 26.1 (`23B85`) or 26.3 (`23D128`) |
| 26.4 – 26.6 (`23E246` … `23G71`) | **26.4** (`23E5207q`) |
| 27.0 beta (`24A5380h`/`24A5390f`) | **26.4** (`23E5207q`) |

> So **iOS 26.5 (`23F77`) needs CloudOS 26.4 (`23E5207q`)** — not 26.1.

---

## Finding the iPhone IPSW

These are normal Apple device restore images.

### Signed (GA) releases

Use the [`ipsw`](https://github.com/blacktop/ipsw) CLI (a vphone-cli brew dep):

```bash
# List every currently-signed IPSW URL for the device
ipsw download ipsw --device iPhone17,3 --urls

# Or download a specific version straight to disk
ipsw download ipsw --device iPhone17,3 --version 26.5
```

You can also grab the direct Apple CDN URL from that list and paste it into
**IPSW Library → Download from URL** (kind **iPhone**).

> Only *signed* versions appear. As of writing, `iPhone17,3` tops out at
> `26.5.2 (23F84)`; older versions Apple has stopped signing still restore fine
> for research but aren't in the list — [AppleDB](https://appledb.dev) /
> [ipsw.me](https://ipsw.me) mirror them.

### Betas (e.g. iOS 27)

Beta iPhone firmwares (like `iPhone17,3 27.0 24A5380h`) are **not** in the signed
list. Get them from the **Apple Developer program** (developer.apple.com/download)
or a beta IPSW mirror, then add via **Upload** or **Download from URL** (kind
**iPhone**).

---

## Finding the CloudOS (PCC) image

CloudOS images are Apple **Private Cloud Compute** OS releases, published for
security researchers. Three ways to get them:

### 1. The `ipsw` CLI — `ipsw download pcc`

```bash
ipsw download pcc --info                 # list PCC releases
ipsw download pcc --version 26.4 --info  # filter to the 26.4 stack
ipsw download pcc <INDEX>                # download by index
ipsw download pcc --build 5E290          # filter by cloudOS build prefix
```

> **Known issue (as of this writing):** the full listing can fail with
> `failed to parse pcc log leaf … : release leaf missing metadata` — a malformed
> leaf in Apple's PCC transparency log breaks iteration. Update `ipsw`
> (`brew upgrade ipsw`) and retry; the `--version` / `--build` filters sometimes
> get past it. If it stays broken, use the sources below.

### 2. AppleDB — the most reliable source

[AppleDB](https://appledb.dev) catalogs every CloudOS build under the PCC node's
device identifier, **`ComputeModule14,1`** ("Private Cloud Compute Node"):

<https://appledb.dev/device/identifier/ComputeModule14,1.html>

Each build has a **Download** link straight to Apple's CDN. This is the go-to
source while `ipsw download pcc` is broken. Example (the build iOS 26.4–27.0 need):

```
cloudOS 26.4 (23E5207q) →
https://updates.cdn-apple.com/private-cloud-compute/c0ecdb4b310cf5239ab2b248dd3098eec297dc5aa3bbe6ada27273262b0b8b64
```

Paste that URL into **IPSW Library → Download from URL** (kind **CloudOS**), or
download it and **Upload** it.

### 3. Apple's PCC Virtual Research Environment

<https://security.apple.com/private-cloud-compute/> — Apple's PCC research portal
lists released CloudOS builds and their images (the "PCC VRE").

### 4. Direct CDN (what vphone-cli uses)

PCC images live under `https://updates.cdn-apple.com/private-cloud-compute/<hash>`.
vphone-cli's built-in default (used when you pick **Default** for CloudOS) is the
**26.1** stack — see `DEFAULT_CLOUDOS_SOURCE` in
[`scripts/fw_prepare.sh`](../vphone-cli/scripts/fw_prepare.sh). For a different
CloudOS you supply the URL/file yourself. The vphone-cli project/community is the
practical source for the exact 26.4 (`23E5207q`) URL.

Once you have the file, add it in **IPSW Library** with kind **CloudOS**.

---

## Putting it together (example: build iOS 26.5)

1. **iPhone IPSW** — download `iPhone17,3 26.5 (23F77)`:
   `ipsw download ipsw --device iPhone17,3 --version 26.5`, or paste the CDN URL
   into **Download from URL** (kind **iPhone**).
2. **CloudOS IPSW** — obtain `26.4 (23E5207q)` (via `ipsw download pcc` or the PCC
   portal), and add it (kind **CloudOS**).
3. **Create Device** → firmware step: pick the iPhone 26.5 **and** the CloudOS
   26.4, choose your variant, and build. (If you leave CloudOS on *Default*, the
   build uses the 26.1 stack — **wrong for 26.5**, so select the 26.4 entry.)

For iOS ≤ 26.1 you can leave CloudOS on **Default** and skip step 2 entirely.

See [Your first VM](02-first-vm.md) for the full create walkthrough.
