# Security

This document is for operators evaluating **nyxd as software to install and run**.
It maps public controls to common assessment criteria and states what is **not**
claimed (no substitute for an independent penetration test).

## Quick posture

| Area | Status |
|------|--------|
| Container hardening (nnp, caps, seccomp, masked paths, netns) | Implemented by default |
| Rootless daemon | Supported (automatic when started as non-root); rootful still common as a system service |
| Image blob digests | Verified on pull |
| Image signatures (cosign / Notary) | **Not implemented** (see ROADMAP) |
| Control API | Local Unix socket only; group membership is root-equivalent |
| Independent pen-test / public audit | **Not claimed** |
| Vulnerability process | [SECURITY.md](../SECURITY.md) + GitHub private advisories |

## Privilege separation

- **Prefer rootless:** run `nyxd` as a normal user ([rootless.md](rootless.md)). Container “root” maps to your uid (or subordinate IDs).
- **Rootful systemd:** use `-userns-remap=default` when possible so container UIDs are remapped.
- The daemon logs a **warning** when started as root without remapping, and when `-net-driver=host`.

## Container isolation

Defaults (unless overridden in compose / OCI):

- `noNewPrivileges`
- Reduced Linux capabilities; `privileged: true` / `cap_add` weaken this
- Default seccomp profile (`seccomp_profile: unconfined` disables it)
- Optional `apparmor_profile` when AppArmor is available
- Separate network namespace with the **native** or **cni** driver

`nyx run --network host`, compose `network_mode: host`, and privileged services print
**CLI warnings**. Host networking removes network namespace isolation between the
workload and the host.

## Network security

- Default: bridge / native networking with published ports only as configured.
- Internal compose networks (`internal: true`) restrict egress on the native driver.
- Rootless defaults to host networking (shared loopback); treat that as a lab/dev
  trade-off — see [rootless.md](rootless.md#networking).

## Image supply chain

- Pulls verify **SHA-256 digests** of blobs against the manifest.
- Use private registries and short-lived credentials; prefer digests over mutable tags in production compose files.
- **Signed images are not verified yet.** Dependency scanning of the nyxd Go module
  tree is recommended in CI (e.g. `make scan` / govulncheck) for operators who build from source.

## Secrets

- Do not bake secrets into images or compose files checked into git.
- Registry passwords may appear in process environment / auth files; protect
  `~/.config/nyx/auth.json` and docker/podman auth stores.
- The control socket must not be exposed over TCP.

## API security

- Default listen: Unix socket (`/run/nyxd/nyxd.sock` or `$XDG_RUNTIME_DIR/nyxd/nyxd.sock`).
- `-socket-group=…` makes the socket `0660` for that group — **membership is
  root-equivalent for container ops**. Restrict like the Docker group.
- There is no remote unauthenticated HTTP admin API in the default configuration.

## Vulnerability management

- Report privately via [SECURITY.md](../SECURITY.md).
- Security-relevant fixes are called out under **Security** in [release-notes.md](release-notes.md)
  (example: Go certificate fix in 1.1.7).

## What would raise assurance further

Aligned with a typical independent assessment (not yet all shipped):

1. External penetration testing of create/run, mounts, networking, and privilege boundaries  
2. Broader rootless-by-default packaging with loud warnings for rootful / host net / privileged  
3. Cosign/sigstore verification before run  
4. Published support window and advisories for every CVE-class fix  
5. Reproducible builds / signed release artifacts verification docs  

Until those exist, treat nyxd as **suitable for labs, development, and carefully
configured servers**, not as “independently proven enterprise-assured” solely from
this documentation.

## Related docs

- [rootless.md](rootless.md) — user namespaces  
- [INSTALL.md](INSTALL.md) — socket group warnings  
- [networking.md](networking.md) — drivers and DNS  
- [ROADMAP.md](ROADMAP.md) — signature verification and other gaps  
