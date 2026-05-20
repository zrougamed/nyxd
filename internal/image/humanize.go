package image

import (
	"context"
	"errors"
	"net"
	"strings"
)

// HumanizePullError turns registry / transport errors into short, user-facing text
// for HTTP bodies and NDJSON pull streams. The original error is still logged server-side.
func HumanizePullError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "image download timed out (slow network, large layer, or registry limits). Try `nyx pull <ref>` again, or check connectivity."
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "network timeout while contacting the registry."
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "context deadline exceeded"):
		return "image download timed out while reading from the registry."
	case strings.Contains(s, "Client.Timeout"):
		return "image download timed out (HTTP read timed out)."
	case strings.Contains(s, "connection refused"):
		return "could not connect to the registry: " + trimErrLine(s, 140)
	case strings.Contains(s, "digest mismatch"):
		return "downloaded data did not match the expected digest (corruption or interception?)."
	case strings.Contains(s, "401") || strings.Contains(s, "403"):
		return "registry denied access (private image or missing credentials?)."
	case strings.Contains(s, "404") || strings.Contains(s, "manifest unknown") || strings.Contains(s, "not found"):
		return "image or tag was not found on the registry."
	case strings.Contains(s, "HTML/XML") || strings.Contains(s, "non-JSON response") || strings.Contains(s, "HTML instead of"):
		// Emitted by the puller when the registry path returns a web page.
		return "registry returned a web page instead of image metadata (proxy, captive portal, TLS inspection, or DNS pointing at the wrong host). Check network/VPN and try: nyx pull <image>"
	case strings.Contains(s, "invalid character") && strings.Contains(s, "looking for beginning of value"):
		// Typical when the body starts with '<' (HTML) but was still parsed as JSON.
		return "registry returned non-JSON (often HTML) instead of a manifest — usually a proxy, captive portal, or broken path to the registry. Check connectivity/DNS and try: nyx pull <image>"
	}
	return trimErrLine(s, 420)
}

// HumanizeComposeBuildError maps errors from compose.BuildContainerSpecs (pull, parse, …)
// to short CLI/API text. Pull-related chains use [HumanizePullError]; others use [TrimUserMessage].
func HumanizeComposeBuildError(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	// compose.BuildContainerSpecs wraps pulls as: service "name" pull "ref": …
	if strings.Contains(s, ` pull "`) || strings.Contains(s, "pull manifest") ||
		strings.Contains(s, "pull auth") || strings.Contains(s, "pull config") ||
		strings.Contains(s, "pull layers") || strings.Contains(s, "decode manifest") ||
		strings.Contains(s, "registry manifest") {
		return HumanizePullError(err)
	}
	return TrimUserMessage(err)
}

// TrimUserMessage collapses whitespace and caps length for errors shown to API clients
// when no specialized humanization applies (e.g. container start failures).
func TrimUserMessage(err error) string {
	if err == nil {
		return ""
	}
	return trimErrLine(err.Error(), 420)
}

func trimErrLine(s string, max int) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
