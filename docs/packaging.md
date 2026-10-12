# Release packaging and signing

Every `v*` tag builds, packages, signs and publishes a GitHub release with
[GoReleaser](https://goreleaser.com) (`.goreleaser.yaml`, run by
`.github/workflows/release.yml`).

## What a release contains

| Platform | Files | Contents |
|----------|-------|----------|
| Ubuntu / Debian | `nyxd_<ver>_amd64.deb`, `_arm64.deb` | `nyxd`, `nyx`, systemd system + user units |
| Fedora / RHEL / openSUSE | `nyxd-<ver>-1.x86_64.rpm`, `.aarch64.rpm` | same |
| Arch Linux | `nyxd-<ver>-1-x86_64.pkg.tar.zst`, `-aarch64` | same |
| Alpine | `nyxd_<ver>_x86_64.apk`, `_aarch64.apk` | `nyxd`, `nyx`, OpenRC script |
| Linux (any) | `nyxd_<ver>_linux_<arch>.tar.gz`, `nyxd-linux-<arch>`, `nyx-linux-<arch>` | archive, bare binaries |
| macOS (Apple silicon, Intel) | `nyxd_<ver>_darwin_arm64.tar.gz`, `_amd64`, `nyx-darwin-<arch>` | the `nyx` client |
| Windows (x64, ARM64) | `nyxd_<ver>_windows_amd64.zip`, `_arm64`, `nyx-windows-<arch>.exe` | `nyx.exe` |
| Windows WSL2 | `nyx-wsl-<arch>.tar.gz` (+ `.sha256`) | the distro `nyx machine init` imports |

`nyxd` is Linux-only; on macOS and Windows, `nyx` drives a `nyxd` running in
a Linux VM or in the nyx WSL2 distro. All Linux packages depend on `crun`.

Signatures:

- `checksums.txt`: sha256 of every file, with a detached GPG signature `checksums.txt.sig`.
- Every archive, package and binary has a detached GPG signature `<file>.sig`
  (pacman uses these for `.pkg.tar.zst` files).
- `.deb` files also carry an embedded signature (`_gpgorigin`, dpkg-sig style);
  `.rpm` files carry a native RPM signature (`rpm -K`).
- `.apk` files are signed natively only if an RSA key is configured (see below);
  their `.sig` covers them either way.
- `nyxd-release-key.asc`: the public key.

## One-time setup (maintainers)

### 1. Create the signing key

Use RSA 4096: older rpm (RHEL 8) and dpkg-sig do not verify Ed25519
signatures. Do this on a trusted machine, not on CI.

```bash
gpg --full-generate-key
#   kind: (1) RSA and RSA      size: 4096      expiry: 2y (renewable)
#   name: nyxd release signing    email: a project address
FPR=$(gpg --list-secret-keys --with-colons "nyxd release signing" | awk -F: '/^fpr/ {print $10; exit}')
echo "$FPR"
```

Keep a revocation certificate offline
(`~/.gnupg/openpgp-revocs.d/$FPR.rev` is created for you) and back up the
secret key (`gpg --armor --export-secret-keys "$FPR"`) somewhere offline.

### 2. Add the repository secrets

Settings → Secrets and variables → Actions → *New repository secret*:

| Secret | Value |
|--------|-------|
| `GPG_PRIVATE_KEY` | output of `gpg --armor --export-secret-keys "$FPR"` |
| `GPG_PASSPHRASE` | the key's passphrase |
| `APK_RSA_PRIVATE_KEY` (optional) | `openssl genrsa 4096` output; enables native apk signatures |

With the GitHub CLI:

```bash
gpg --armor --export-secret-keys "$FPR" | gh secret set GPG_PRIVATE_KEY
gh secret set GPG_PASSPHRASE            # prompts for the value
openssl genrsa 4096 | gh secret set APK_RSA_PRIVATE_KEY   # optional
```

Better still, put them on a `release` environment that only `v*` tags may
use, and add `environment: release` to the `publish` job.

### 3. Publish the public key

```bash
gpg --armor --export "$FPR" > nyxd-release-key.asc
gpg --keyserver hkps://keys.openpgp.org --send-keys "$FPR"   # then confirm by email
```

Put the fingerprint in the README and on the website so users can check the
key they download. Every release also ships `nyxd-release-key.asc`.

## Cutting a release

```bash
git tag -s v0.10.0 -m "v0.10.0"     # or -a for an unsigned tag
git push origin v0.10.0
```

The workflow runs CI, builds and signs the WSL tarballs, then GoReleaser
builds every binary, archive and package, signs them and creates the release.

Try packaging changes without tagging:

- **Locally:** `make snapshot` (needs [goreleaser v2](https://goreleaser.com/install/)); everything lands in `dist/`, unsigned.
- **On CI:** Actions → Release → *Run workflow*; the unsigned packages are attached to the run.

To test signing locally, export a throwaway key's fingerprint and passphrase:

```bash
GPG_FINGERPRINT=<fpr> GPG_PASSPHRASE=<pass> NFPM_KEY_FILE=key.asc NFPM_PASSPHRASE=<pass> \
  goreleaser release --snapshot --clean --skip=publish
```

## Verifying downloads (users)

```bash
FPR=<fingerprint published in the README>
curl -fsSLO https://github.com/zrougamed/nyxd/releases/download/v0.10.0/nyxd-release-key.asc
gpg --import nyxd-release-key.asc
gpg --fingerprint "$FPR"                       # must match the published fingerprint

gpg --verify checksums.txt.sig checksums.txt   # "Good signature from nyxd release signing"
sha256sum --ignore-missing -c checksums.txt    # macOS: shasum -a 256 --ignore-missing -c checksums.txt
```

Package managers:

```bash
# Debian / Ubuntu
sudo apt install ./nyxd_0.10.0_amd64.deb

# Fedora / RHEL
sudo rpm --import nyxd-release-key.asc
rpm -K nyxd-0.10.0-1.x86_64.rpm                # "digests signatures OK"
sudo dnf install ./nyxd-0.10.0-1.x86_64.rpm

# Arch Linux (keep the .sig next to the package)
sudo pacman-key --add nyxd-release-key.asc
sudo pacman-key --lsign-key "$FPR"
sudo pacman -U nyxd-0.10.0-1-x86_64.pkg.tar.zst

# Alpine (verify the .sig first; --allow-untrusted unless you installed the RSA key under /etc/apk/keys)
gpg --verify nyxd_0.10.0_x86_64.apk.sig nyxd_0.10.0_x86_64.apk
sudo apk add --allow-untrusted ./nyxd_0.10.0_x86_64.apk
```

Windows (PowerShell, with [Gpg4win](https://www.gpg4win.org/)):

```powershell
gpg --verify nyx-windows-amd64.exe.sig nyx-windows-amd64.exe
```

## Not covered

- **macOS notarization / Windows Authenticode.** GPG signatures prove the
  files come from the project; Gatekeeper and SmartScreen only trust Apple
  Developer ID and Authenticode signatures. Until those are added, macOS users
  run `xattr -d com.apple.quarantine nyx` after verifying, and Windows shows
  a SmartScreen prompt on first run.
- **Package repositories** (apt/dnf/pacman/apk repos, Homebrew tap,
  Scoop/winget manifests). Releases ship files only; a repository would let
  `apt upgrade` and friends pick up new versions.
