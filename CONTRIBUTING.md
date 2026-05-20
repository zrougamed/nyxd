# Contributing to nyxd

Thank you for helping improve nyxd and `nyx`.

## Community

- Follow the **[Code of Conduct](CODE_OF_CONDUCT.md)** in all interactions (issues, PRs, discussions).
- **Bug reports** and **feature requests** use the [GitHub issue forms](.github/ISSUE_TEMPLATE/).
- **Pull requests** should use the [PR template](.github/pull_request_template.md) (GitHub loads it automatically).

## Development

- **Go:** match the version in `go.mod`. Run **`go test ./...`** before opening a PR.
- **Linux + `crun`:** most behavior is only exercised on Linux; mention your test environment in the PR.
- **Docs:** update `docs/USAGE.md`, `docs/openapi.yaml`, or `docs/ROADMAP.md` when user-visible behavior or the HTTP API changes.

## License

This project is released under the **PolyForm Noncommercial License 1.0.0** — see **[LICENSE](LICENSE)**.

- **Noncommercial** use, contribution, and distribution under that license are welcome.
- **Commercial** use (e.g. selling the software, offering it as part of a paid service outside the license’s permitted purposes) **requires a separate written license** from the copyright holder(s). Open a discussion or contact the maintainers if you need a commercial license.

Do not submit third-party code unless you have the right to license it under the same terms (and note the origin in the PR).
