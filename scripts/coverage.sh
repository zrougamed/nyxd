#!/bin/sh
# Statement coverage of the whole module: unit tests plus, with E2E=1, the
# end-to-end scripts run against -cover builds of nyxd and nyx. Every run
# writes raw coverage data; they are merged per statement, so a line counts as
# covered if any test or script executed it.
#
#   scripts/coverage.sh                  unit tests only
#   E2E=1 scripts/coverage.sh            + rootless, build, transfer and WSL smoke tests
#   COVER_MIN=95 scripts/coverage.sh     fail below 95% total
#
# Output: $COVER_DIR (default ./coverage): coverage.out (go tool cover format),
# packages.txt (per package), html with `go tool cover -html=coverage/coverage.out`.
set -eu

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"
OUT=${COVER_DIR:-$ROOT/coverage}
MIN=${COVER_MIN:-0}

# Test helpers (harness programs, internal/testutil) are not product code.
PKGS=$(go list ./... | grep -v -e '/scripts/' -e '/internal/testutil/' | paste -sd, -)

rm -rf "$OUT"
mkdir -p "$OUT/unit" "$OUT/e2e" "$OUT/bin"
# Processes in user namespaces (rootless, remapped, WSL distro) write their
# counters as subordinate IDs.
chmod 0777 "$OUT/e2e"

echo "== unit tests"
go test -count=1 -coverpkg="$PKGS" ./... -args -test.gocoverdir="$OUT/unit" >"$OUT/unit.log" 2>&1 ||
	{ cat "$OUT/unit.log"; exit 1; }

if [ "${E2E:-0}" = 1 ]; then
	echo "== -cover builds"
	for b in nyxd nyx; do
		CGO_ENABLED=0 go build -cover -coverpkg="$PKGS" -o "$OUT/bin/$b" "./cmd/$b"
	done
	export GOCOVERDIR="$OUT/e2e"
	for s in rootless-smoke build-smoke transfer-smoke; do
		echo "== $s"
		BIN="$OUT/bin" "scripts/$s.sh"
	done
	echo "== wsl-smoke"
	ARCH=$(go env GOARCH)
	NYXD_BIN="$OUT/bin/nyxd" scripts/build-wsl-rootfs.sh "$ARCH" "$OUT/bin" cover >/dev/null
	BIN="$OUT/bin" TARBALL="$OUT/bin/nyx-wsl-$ARCH.tar.gz" scripts/wsl-smoke.sh
	unset GOCOVERDIR
fi

go tool covdata textfmt -i="$OUT/unit,$OUT/e2e" -o "$OUT/coverage.out"
# Per package: statements and covered statements (blocks merged by position).
awk -F'[: ,]' 'NR > 1 {
	key = $1 ":" $2 ":" $3
	n[key] = $4
	if ($5 > 0) hit[key] = 1
} END {
	for (k in n) {
		split(k, f, ":"); pkg = f[1]; sub(/\/[^\/]*$/, "", pkg)
		stmts[pkg] += n[k]; total += n[k]
		if (k in hit) { cov[pkg] += n[k]; covered += n[k] }
	}
	for (p in stmts) printf "%6.1f%%  %5d/%-5d  %s\n", 100 * cov[p] / stmts[p], cov[p], stmts[p], p
	printf "%6.1f%%  %5d/%-5d  total\n", 100 * covered / total, covered, total > "/dev/stderr"
}' "$OUT/coverage.out" 2>"$OUT/total.txt" | sort -n >"$OUT/packages.txt"
cat "$OUT/packages.txt" "$OUT/total.txt"

pct=$(awk '{ sub(/%/, "", $1); print $1 }' "$OUT/total.txt")
if awk -v p="$pct" -v m="$MIN" 'BEGIN { exit !(p < m) }'; then
	echo "FAIL: coverage ${pct}% is below ${MIN}%" >&2
	exit 1
fi
