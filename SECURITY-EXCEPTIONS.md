# Security exceptions

Recorded per `/home/user/dev/standards/SUPPLY-CHAIN.md` §5. Review dates are
no more than 90 days out.

| Advisory | Package | Why acceptable | Owner | Review by |
|----------|---------|----------------|-------|-----------|
| [GHSA-vfj7-8cjw-p6xm](https://osv.dev/GHSA-vfj7-8cjw-p6xm) (CVSS 8.7) | `braces@3.0.3` via `website/bun.lock` (chokidar / micromatch → Docusaurus build) | No fixed release on npm (`last_affected` 3.0.3; upstream PRs open, not published). Reachable only if a build-time tool expands attacker-controlled brace patterns; the docs site build does not take untrusted micromatch input. Not in the nyxd/nyx Go runtime. | mdarguechi | 2027-01-16 |

When `braces` publishes a patch (or micromatch/chokidar moves off 3.0.3), remove this row and bump via `website/package.json` overrides + `bun install`.
