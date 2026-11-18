package image

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/zrougamed/nyxd/pkg/oci"
)

// sourceDigestFile records, next to a pulled image's metadata, the digest
// of the manifest or index its tag pointed to at pull time.
const sourceDigestFile = "source-digest"

func (s *Store) writeSourceDigest(ref, digest string) {
	if digest == "" {
		return
	}
	_ = os.WriteFile(filepath.Join(s.imageDir(ref), sourceDigestFile), []byte(digest+"\n"), 0o600)
}

// SourceDigest is the registry digest ref's tag had when it was last
// pulled ("" for images that were built, loaded or pulled before nyxd
// recorded it).
func (s *Store) SourceDigest(ref string) string {
	b, err := os.ReadFile(filepath.Join(s.imageDir(ref), sourceDigestFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// RemoteDigest asks the registry (its mirrors first) which digest ref's
// tag points to now, with a HEAD request: cheap, and counted lightly
// against rate limits. It falls back to a GET when the registry does not
// send Docker-Content-Digest.
func (s *Store) RemoteDigest(ctx context.Context, ref string, auth *RegistryAuth) (string, error) {
	reg, repo, tag := parseRef(ref)
	var errs []string
	for _, m := range s.mirrorsFor(reg) {
		sess := newSession(newPullHTTPClient(), m.Host, joinRepo(m.Prefix, repo), MirrorAuthFrom(ctx).host(m.Host), "pull")
		sess.scheme = m.Scheme
		d, err := headManifest(ctx, sess, tag)
		if err == nil {
			return d, nil
		}
		errs = append(errs, fmt.Sprintf("%s: %v", m.URL, err))
	}
	if len(errs) > 0 && s.mirrorOnly {
		return "", fmt.Errorf("every mirror failed: %s", strings.Join(errs, "; "))
	}
	return headManifest(ctx, newSession(newPullHTTPClient(), reg, repo, auth, "pull"), tag)
}

func joinRepo(prefix, repo string) string {
	if prefix == "" {
		return repo
	}
	return prefix + "/" + repo
}

func headManifest(ctx context.Context, sess *session, tag string) (string, error) {
	accept := strings.Join([]string{oci.MediaTypeImageManifest, oci.MediaTypeImageIndex,
		oci.MediaTypeDockerManifestV2, oci.MediaTypeDockerManifestList}, ", ")
	for _, method := range []string{http.MethodHead, http.MethodGet} {
		req, err := http.NewRequestWithContext(ctx, method, sess.repoURL("manifests/"+tag), nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Accept", accept)
		resp, err := sess.do(req)
		if err != nil {
			return "", err
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("manifest %s: %s", tag, resp.Status)
		}
		if d := strings.TrimSpace(resp.Header.Get("Docker-Content-Digest")); strings.HasPrefix(d, "sha256:") {
			return d, nil
		}
		if method == http.MethodGet {
			return "sha256:" + fmt.Sprintf("%x", sha256Sum(body)), nil
		}
	}
	return "", fmt.Errorf("manifest %s: no digest", tag)
}
