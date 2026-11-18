package image

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/zrougamed/nyxd/pkg/oci"
)

// Image signature policy (nyxd -image-policy): key-based cosign signatures,
// verified with the standard library only. Keyless (Fulcio / Rekor) is out
// of scope.

// Policy modes for images no registries: entry covers.
const (
	PolicyAllow  = "allow"
	PolicyWarn   = "warn"
	PolicyReject = "reject"
)

// cosignSignatureAnnotation holds a signature layer's base64 signature.
const cosignSignatureAnnotation = "dev.cosignproject.cosign/signature"

// verifiedFile marks, next to an image's metadata, the digest whose
// signature was verified at pull time.
const verifiedFile = "signature-verified"

// Policy is the parsed policy file.
type Policy struct {
	Default string // allow, warn or reject
	rules   []policyRule
}

type policyRule struct {
	pattern string // registry/repository glob, Docker Hub as registry-1.docker.io
	signed  bool
	keys    []*ecdsa.PublicKey
}

type policyFile struct {
	Default    string `yaml:"default"`
	Registries map[string]struct {
		Require string   `yaml:"require"`
		Keys    []string `yaml:"keys"`
	} `yaml:"registries"`
}

// LoadPolicy reads a policy file:
//
//	default: warn            # allow | warn | reject, for images no entry covers
//	registries:
//	  ghcr.io/myorg/*:
//	    require: signed
//	    keys: [/etc/nyxd/keys/myorg.pub]
func LoadPolicy(file string) (*Policy, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var pf policyFile
	if err := yaml.Unmarshal(data, &pf); err != nil {
		return nil, fmt.Errorf("image policy %s: %w", file, err)
	}
	p := &Policy{Default: strings.ToLower(strings.TrimSpace(pf.Default))}
	switch p.Default {
	case "":
		p.Default = PolicyAllow
	case PolicyAllow, PolicyWarn, PolicyReject:
	default:
		return nil, fmt.Errorf("image policy %s: default %q: want allow, warn or reject", file, pf.Default)
	}
	for pattern, r := range pf.Registries {
		rule := policyRule{pattern: normalizePattern(pattern)}
		if _, err := path.Match(rule.pattern, "x"); err != nil {
			return nil, fmt.Errorf("image policy %s: pattern %q: %w", file, pattern, err)
		}
		switch strings.ToLower(strings.TrimSpace(r.Require)) {
		case "signed":
			rule.signed = true
		case "", "none":
		default:
			return nil, fmt.Errorf("image policy %s: %s: require %q: want signed or none", file, pattern, r.Require)
		}
		for _, k := range r.Keys {
			kf := k
			if !filepath.IsAbs(kf) {
				kf = filepath.Join(filepath.Dir(file), kf)
			}
			pub, err := loadPublicKey(kf)
			if err != nil {
				return nil, fmt.Errorf("image policy %s: %s: %w", file, pattern, err)
			}
			rule.keys = append(rule.keys, pub)
		}
		if rule.signed && len(rule.keys) == 0 {
			return nil, fmt.Errorf("image policy %s: %s: require signed needs keys", file, pattern)
		}
		p.rules = append(p.rules, rule)
	}
	// The most specific pattern wins.
	sort.Slice(p.rules, func(i, j int) bool { return len(p.rules[i].pattern) > len(p.rules[j].pattern) })
	return p, nil
}

func normalizePattern(p string) string {
	p = strings.TrimSpace(p)
	host, rest, ok := strings.Cut(p, "/")
	if !ok {
		return p
	}
	return normalizeDockerHubRegistry(host) + "/" + rest
}

func loadPublicKey(file string) (*ecdsa.PublicKey, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("key %s: not PEM", file)
	}
	k, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("key %s: %w", file, err)
	}
	pub, ok := k.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("key %s: not an ECDSA public key (cosign generate-key-pair makes P-256 keys)", file)
	}
	return pub, nil
}

// rule returns the rule covering ref, or nil.
func (p *Policy) rule(ref string) *policyRule {
	if p == nil {
		return nil
	}
	reg, repo, _ := parseRef(ref)
	name := reg + "/" + repo
	for i := range p.rules {
		if ok, _ := path.Match(p.rules[i].pattern, name); ok {
			return &p.rules[i]
		}
	}
	return nil
}

// SetPolicy sets the signature policy (nil: none).
func (s *Store) SetPolicy(p *Policy) { s.policy = p }

// ErrPolicy is the error kind of an image the policy rejects.
var ErrPolicy = errors.New("rejected by the image policy")

// checkSignature applies the policy to ref, whose tag resolved to digest,
// before anything is stored. It returns a warning to show for warn mode.
func (s *Store) checkSignature(ctx context.Context, sess *session, ref, digest string) (warning string, err error) {
	if s.policy == nil {
		return "", nil
	}
	r := s.policy.rule(ref)
	switch {
	case r == nil && s.policy.Default == PolicyReject:
		return "", fmt.Errorf("%w: %s: no policy entry covers this image (default: reject)", ErrPolicy, ref)
	case r == nil && s.policy.Default == PolicyWarn:
		return fmt.Sprintf("%s: not verified (no policy entry covers this image)", ref), nil
	case r == nil || !r.signed:
		return "", nil
	}
	if err := verifyCosign(ctx, sess, digest, r.keys); err != nil {
		return "", fmt.Errorf("%w: %s: %v", ErrPolicy, ref, err)
	}
	return "", nil
}

// simpleSigning is cosign's signed payload.
type simpleSigning struct {
	Critical struct {
		Image struct {
			Digest string `json:"docker-manifest-digest"`
		} `json:"image"`
		Type string `json:"type"`
	} `json:"critical"`
}

// verifyCosign checks that sha256-<digest>.sig in the image's repository
// holds a signature, by one of keys, over a payload naming digest.
func verifyCosign(ctx context.Context, sess *session, digest string, keys []*ecdsa.PublicKey) error {
	tag := strings.Replace(digest, ":", "-", 1) + ".sig"
	c := &registryClient{s: sess}
	m, err := c.manifest(ctx, tag)
	if err != nil {
		return fmt.Errorf("no signature found (%s): %v", tag, err)
	}
	var lastErr error = errors.New("no signature layers")
	for _, layer := range m.Layers {
		sigB64 := layer.Annotations[cosignSignatureAnnotation]
		if sigB64 == "" {
			continue
		}
		sig, err := base64.StdEncoding.DecodeString(sigB64)
		if err != nil {
			lastErr = fmt.Errorf("bad signature encoding: %v", err)
			continue
		}
		payload, err := fetchSmallBlob(ctx, c, layer)
		if err != nil {
			lastErr = err
			continue
		}
		sum := sha256.Sum256(payload)
		signedBy := false
		for _, k := range keys {
			if ecdsa.VerifyASN1(k, sum[:], sig) {
				signedBy = true
				break
			}
		}
		if !signedBy {
			lastErr = errors.New("signature does not match any policy key")
			continue
		}
		var ss simpleSigning
		if err := json.Unmarshal(payload, &ss); err != nil {
			lastErr = fmt.Errorf("signed payload: %v", err)
			continue
		}
		if ss.Critical.Image.Digest != digest {
			lastErr = fmt.Errorf("signature is for %s, not %s", ss.Critical.Image.Digest, digest)
			continue
		}
		return nil
	}
	return lastErr
}

// fetchSmallBlob downloads a signature payload (capped) and checks its digest.
func fetchSmallBlob(ctx context.Context, c *registryClient, d oci.Descriptor) ([]byte, error) {
	rc, err := c.blob(ctx, d.Digest, 0)
	if err != nil {
		return nil, fmt.Errorf("signature payload: %v", err)
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, 1<<20))
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(b)
	if got := "sha256:" + fmt.Sprintf("%x", sum[:]); got != d.Digest {
		return nil, fmt.Errorf("signature payload digest %s, want %s", got, d.Digest)
	}
	return b, nil
}

func (s *Store) markVerified(ref, digest string) {
	_ = os.WriteFile(filepath.Join(s.imageDir(ref), verifiedFile), []byte(digest+"\n"), 0o600)
}

// CheckRunPolicy refuses to run ref when the policy requires a signature
// and the image was not verified when it was pulled (nyx load, an image
// pulled before the policy, a local build). A nil error means it may run;
// a warning is returned for warn mode.
func (s *Store) CheckRunPolicy(ref string) (warning string, err error) {
	if s.policy == nil {
		return "", nil
	}
	r := s.policy.rule(ref)
	switch {
	case r == nil && s.policy.Default == PolicyReject:
		return "", fmt.Errorf("%w: %s: no policy entry covers this image (default: reject)", ErrPolicy, ref)
	case r == nil && s.policy.Default == PolicyWarn:
		return fmt.Sprintf("%s: not verified (no policy entry covers this image)", ref), nil
	case r == nil || !r.signed:
		return "", nil
	}
	b, err := os.ReadFile(filepath.Join(s.imageDir(ref), verifiedFile))
	if err != nil || strings.TrimSpace(string(b)) == "" || strings.TrimSpace(string(b)) != s.SourceDigest(ref) {
		return "", fmt.Errorf("%w: %s needs a verified signature, and this image was not verified when it came in (loaded, built, or pulled before the policy): pull it again", ErrPolicy, ref)
	}
	return "", nil
}
