//go:build linux

package native

import (
	"crypto/sha256"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

func portmapCommentPrefix(containerID string) string {
	h := sha256.Sum256([]byte(containerID))
	return fmt.Sprintf("nyxdpm-%x", h[:12])
}

func internalEgressCommentPrefix(containerID string) string {
	h := sha256.Sum256([]byte("internal:" + containerID))
	return fmt.Sprintf("nyxdint-%x", h[:12])
}

var handleSuffix = regexp.MustCompile(`#\s*handle\s+(\d+)\s*$`)

func nftListChainLines(table, chain string) ([]string, error) {
	out, err := exec.Command("/usr/sbin/nft", "-a", "list", "chain", "ip", table, chain).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("nft list chain %s: %w: %s", chain, err, strings.TrimSpace(string(out)))
	}
	var lines []string
	for _, ln := range strings.Split(string(out), "\n") {
		ln = strings.TrimSpace(ln)
		if ln != "" {
			lines = append(lines, ln)
		}
	}
	return lines, nil
}

func parseHandle(line string) (uint64, bool) {
	m := handleSuffix.FindStringSubmatch(line)
	if m == nil {
		return 0, false
	}
	h, err := strconv.ParseUint(m[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return h, true
}

func nftDeleteRulesWithCommentPrefix(table, chain, prefix string) error {
	for {
		lines, err := nftListChainLines(table, chain)
		if err != nil {
			return err
		}
		var handles []uint64
		for _, ln := range lines {
			if strings.Contains(ln, prefix) {
				if h, ok := parseHandle(ln); ok {
					handles = append(handles, h)
				}
			}
		}
		if len(handles) == 0 {
			return nil
		}
		sort.Slice(handles, func(i, j int) bool { return handles[i] > handles[j] })
		rule := fmt.Sprintf("nft delete rule ip %s %s handle %d", table, chain, handles[0])
		if err := runNft(rule); err != nil {
			return fmt.Errorf("nft delete %s handle %d: %w", chain, handles[0], err)
		}
	}
}
