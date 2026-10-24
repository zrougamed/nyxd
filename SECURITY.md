# Security policy

## Supported versions

Security fixes are applied to the **latest released tag** on `main` and published
in [docs/release-notes.md](docs/release-notes.md). Older tags are not routinely
backported unless a release is still marked supported in that document.

## Reporting a vulnerability

**Do not open a public issue for security bugs.**

1. Use GitHub’s private advisory form:
   [Report a vulnerability](https://github.com/zrougamed/nyxd/security/advisories/new)
2. Include: affected version / commit, reproduction steps, impact, and any
   suggested fix.
3. We aim to acknowledge within **7 days** and to ship a fix or mitigation for
   confirmed issues as soon as practical.

You may also use the contact links under `.github/ISSUE_TEMPLATE/config.yml`
(“Security advisory”).

## Threat model (short)

- The control Unix socket is **root-equivalent for container operations**
  (privileged, host binds, host networking, capability changes). Restrict
  socket permissions and `-socket-group` membership like the `docker` group.
- Default containers use `noNewPrivileges`, a reduced capability set, seccomp,
  and (when not on host net) a network namespace. Digests of pulled blobs are
  verified; **image signatures (cosign/sigstore) are not verified yet**.
- See [docs/security.md](docs/security.md) for operator guidance and known gaps.

## Out of scope for private reports

- Issues that require an already-compromised host or socket-group membership
  to “escape” into further host control (document as hardening suggestions
  instead).
- Dependency CVEs already fixed by upgrading Go or modules in a released tag.
