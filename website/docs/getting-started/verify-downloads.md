# Verifying downloads

Every release ships a `checksums.txt` signed with the release GPG key, a detached signature for each
file, and [SLSA](https://slsa.dev) build provenance (`nyxd.intoto.jsonl`) that records which commit, tag
and workflow run built each file.

```bash
FPR=<fingerprint published in the README>
curl -fsSLO https://github.com/zrougamed/nyxd/releases/download/v0.10.0/nyxd-release-key.asc
gpg --import nyxd-release-key.asc
gpg --fingerprint "$FPR"                       # must match the published fingerprint

gpg --verify checksums.txt.sig checksums.txt   # "Good signature from nyxd release signing"
sha256sum --ignore-missing -c checksums.txt    # macOS: shasum -a 256 --ignore-missing -c checksums.txt
```

Build provenance, with [slsa-verifier](https://github.com/slsa-framework/slsa-verifier#installation)
(no key needed; releases after v1.2.0):

```bash
TAG=<release tag, e.g. v1.3.0>
curl -fsSLO "https://github.com/zrougamed/nyxd/releases/download/$TAG/nyxd.intoto.jsonl"
slsa-verifier verify-artifact nyxd_${TAG#v}_amd64.deb \
  --provenance-path nyxd.intoto.jsonl \
  --source-uri github.com/zrougamed/nyxd \
  --source-tag "$TAG"                          # "PASSED: SLSA verification passed"
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
