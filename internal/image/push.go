package image

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/zrougamed/nyxd/pkg/oci"
)

const pushTimeout = 60 * time.Minute

// Push uploads the local image ref to its registry (OCI distribution API):
// blobs the registry already has are skipped (HEAD), missing ones are
// uploaded monolithically (POST + PUT ?digest=), then the manifest is put
// under the tag. It returns the manifest digest. Events use PullEvent with
// phases begin, blob (Cached=true when skipped), blob_done, manifest, done.
func (s *Store) Push(ctx context.Context, ref string, auth *RegistryAuth, on func(PullEvent)) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, pushTimeout)
	defer cancel()
	emit := func(ev PullEvent) {
		if on != nil {
			on(ev)
		}
	}

	if strings.Contains(ref, "@") {
		return "", fmt.Errorf("push %s: push needs a tag, not a digest", ref)
	}
	m, _, err := s.LoadImageMeta(ref)
	if err != nil {
		return "", fmt.Errorf("push %s: %w", ref, err)
	}
	reg, repo, tag := parseRef(ref)
	sess := newSession(&http.Client{}, reg, repo, auth, "pull,push")
	emit(PullEvent{Phase: "begin", Ref: ref})

	blobs := append([]oci.Descriptor{m.Config}, m.Layers...)
	for i, d := range blobs {
		if !s.HasBlob(d.Digest) {
			return "", fmt.Errorf("push %s: blob %s missing from the local store", ref, d.Digest)
		}
		exists, err := blobExists(ctx, sess, d.Digest)
		if err != nil {
			return "", fmt.Errorf("push %s: %w", ref, err)
		}
		emit(PullEvent{Phase: "blob", Index: i + 1, Count: len(blobs), Digest: d.Digest, Size: d.Size, Cached: exists})
		if exists {
			continue
		}
		if err := s.uploadBlob(ctx, sess, d); err != nil {
			return "", fmt.Errorf("push %s: blob %s: %w", ref, d.Digest, err)
		}
		emit(PullEvent{Phase: "blob_done", Digest: d.Digest})
	}

	body, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	mediaType := m.MediaType
	if mediaType == "" {
		mediaType = oci.MediaTypeImageManifest
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, sess.repoURL("manifests/"+tag), strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(string(body))), nil }
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", mediaType)
	resp, err := sess.do(req)
	if err != nil {
		return "", fmt.Errorf("push %s: manifest: %w", ref, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("push %s: manifest: %s: %s", ref, resp.Status, readSnippet(resp.Body))
	}
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	emit(PullEvent{Phase: "manifest", Ref: ref, Digest: digest, MediaType: mediaType})
	return digest, nil
}

func blobExists(ctx context.Context, sess *session, digest string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, sess.repoURL("blobs/"+digest), nil)
	if err != nil {
		return false, err
	}
	resp, err := sess.do(req)
	if err != nil {
		return false, err
	}
	resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("check blob %s: %s", digest, resp.Status)
	}
}

func (s *Store) uploadBlob(ctx context.Context, sess *session, d oci.Descriptor) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sess.repoURL("blobs/uploads/"), http.NoBody)
	if err != nil {
		return err
	}
	resp, err := sess.do(req)
	if err != nil {
		return err
	}
	loc := resp.Header.Get("Location")
	snippet := readSnippet(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted || loc == "" {
		return fmt.Errorf("start upload: %s: %s", resp.Status, snippet)
	}
	base, _ := url.Parse(sess.baseURL() + "/")
	u, err := base.Parse(loc) // Location may be relative
	if err != nil {
		return fmt.Errorf("upload location %q: %w", loc, err)
	}
	q := u.Query()
	q.Set("digest", d.Digest)
	u.RawQuery = q.Encode()

	path := s.BlobPath(d.Digest)
	open := func() (io.ReadCloser, error) { return os.Open(path) }
	f, err := open()
	if err != nil {
		return err
	}
	fi, err := os.Stat(path)
	if err != nil {
		f.Close()
		return err
	}
	put, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), f)
	if err != nil {
		f.Close()
		return err
	}
	put.GetBody = open
	put.ContentLength = fi.Size()
	put.Header.Set("Content-Type", "application/octet-stream")
	presp, err := sess.do(put)
	if err != nil {
		return err
	}
	defer presp.Body.Close()
	if presp.StatusCode != http.StatusCreated {
		return fmt.Errorf("upload: %s: %s", presp.Status, readSnippet(presp.Body))
	}
	return nil
}

func readSnippet(r io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(r, 512))
	return strings.TrimSpace(string(b))
}
