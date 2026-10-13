// Package build builds OCI images from Dockerfiles without Docker or BuildKit.
//
// Every filesystem step runs on an overlayfs mount of the current layers:
// RUN executes through crun in the same sandbox as containers (user namespace
// when rootless, seccomp, reduced capabilities, noNewPrivileges, host
// network), COPY/ADD write into the merged tree from Go. The overlay upper
// directory is then committed as a new layer with internal/layer.
package build

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zrougamed/nyxd/internal/build/dockerfile"
	"github.com/zrougamed/nyxd/internal/image"
	"github.com/zrougamed/nyxd/internal/layer"
	"github.com/zrougamed/nyxd/internal/overlay"
	"github.com/zrougamed/nyxd/internal/rootfs"
	"github.com/zrougamed/nyxd/internal/runtime"
	"github.com/zrougamed/nyxd/pkg/oci"
)

// Options describe one build.
type Options struct {
	ContextDir string            // build context on disk
	Dockerfile string            // path inside ContextDir (default "Dockerfile")
	Tags       []string          // image references to write
	BuildArgs  map[string]string // --build-arg values
	Target     string            // stop after this stage (default: last)
	NoCache    bool
	Out        io.Writer     // human-readable progress and RUN output
	Auth       image.AuthMap // credentials by registry host for FROM pulls
}

// Result is a finished build.
type Result struct {
	ImageID string   // config digest
	Tags    []string // references written
}

// Config wires a Builder to the daemon.
type Config struct {
	Store    *image.Store
	Overlay  *overlay.Manager
	Runtime  *runtime.Runtime
	BaseDir  string // daemon base dir; build state lives in BaseDir/build
	Rootless bool
	SingleID bool // rootless with only one mapped ID: chown is not possible
	Log      *slog.Logger
}

// Builder builds images. It is safe for concurrent builds.
type Builder struct {
	cfg   Config
	dir   string
	cache *cache
}

// New returns a Builder.
func New(cfg Config) (*Builder, error) {
	dir := filepath.Join(cfg.BaseDir, "build")
	for _, d := range []string{dir, filepath.Join(dir, "bundles"), filepath.Join(dir, "contexts")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, err
		}
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Builder{cfg: cfg, dir: dir, cache: &cache{dir: filepath.Join(dir, "cache")}}, nil
}

// ContextDir creates an empty directory for an incoming build context.
func (b *Builder) ContextDir() (string, error) {
	return os.MkdirTemp(filepath.Join(b.dir, "contexts"), "ctx-")
}

// stageState is the image being assembled by one stage.
type stageState struct {
	name   string
	layers []oci.Descriptor
	cfg    oci.ImageConfig
	args   map[string]string // ARG values declared in this stage
	key    string            // cache chain key
	cmdSet bool              // CMD set in this stage (ENTRYPOINT then keeps it)
}

func (st *stageState) env() map[string]string {
	m := map[string]string{}
	for _, e := range st.cfg.Config.Env {
		k, v, _ := strings.Cut(e, "=")
		m[k] = v
	}
	return m
}

// lookup resolves variables for expansion: ENV wins over ARG.
func (st *stageState) lookup(name string) (string, bool) {
	if v, ok := st.env()[name]; ok {
		return v, true
	}
	v, ok := st.args[name]
	return v, ok
}

// runEnv is the environment of RUN: image ENV plus stage ARGs not shadowed.
func (st *stageState) runEnv() []string {
	env := append([]string(nil), st.cfg.Config.Env...)
	have := st.env()
	var extra []string
	for k, v := range st.args {
		if _, ok := have[k]; !ok {
			extra = append(extra, k+"="+v)
		}
	}
	sort.Strings(extra)
	if !slices.ContainsFunc(env, func(e string) bool { return strings.HasPrefix(e, "PATH=") }) {
		env = append(env, "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin")
	}
	return append(env, extra...)
}

func (st *stageState) setEnv(k, v string) {
	for i, e := range st.cfg.Config.Env {
		if strings.HasPrefix(e, k+"=") {
			st.cfg.Config.Env[i] = k + "=" + v
			return
		}
	}
	st.cfg.Config.Env = append(st.cfg.Config.Env, k+"="+v)
}

func (st *stageState) shell() []string {
	if len(st.cfg.Config.Shell) > 0 {
		return st.cfg.Config.Shell
	}
	return []string{"/bin/sh", "-c"}
}

func (st *stageState) clone() *stageState {
	c := *st
	c.layers = slices.Clone(st.layers)
	c.cfg.Config.Env = slices.Clone(st.cfg.Config.Env)
	c.cfg.Config.Cmd = slices.Clone(st.cfg.Config.Cmd)
	c.cfg.Config.Entrypoint = slices.Clone(st.cfg.Config.Entrypoint)
	c.cfg.Config.Shell = slices.Clone(st.cfg.Config.Shell)
	c.cfg.Config.OnBuild = nil
	c.cfg.Config.Labels = cloneMap(st.cfg.Config.Labels)
	c.cfg.Config.ExposedPorts = cloneMap(st.cfg.Config.ExposedPorts)
	c.cfg.Config.Volumes = cloneMap(st.cfg.Config.Volumes)
	c.cfg.RootFS.DiffIDs = slices.Clone(st.cfg.RootFS.DiffIDs)
	c.cfg.History = slices.Clone(st.cfg.History)
	c.args = map[string]string{}
	c.cmdSet = false
	return &c
}

func cloneMap[K comparable, V any](m map[K]V) map[K]V {
	if m == nil {
		return nil
	}
	out := make(map[K]V, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func chainKey(parent string, parts ...string) string {
	h := sha256.New()
	h.Write([]byte(parent))
	for _, p := range parts {
		h.Write([]byte{0})
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}

type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// build is the state of one Build call.
type build struct {
	b       *Builder
	opts    Options
	df      *dockerfile.Dockerfile
	out     io.Writer
	meta    map[string]string // values of ARGs declared before FROM
	stages  []*stageState
	byName  map[string]*stageState
	usedArg map[string]bool
	step    int
	steps   int
}

// Build runs a build.
func (b *Builder) Build(ctx context.Context, opts Options) (*Result, error) {
	if opts.Out == nil {
		opts.Out = io.Discard
	}
	out := &syncWriter{w: opts.Out}
	if opts.Dockerfile == "" {
		opts.Dockerfile = "Dockerfile"
	}
	dfPath, err := rootfs.ScopedJoin(opts.ContextDir, opts.Dockerfile)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(dfPath)
	if err != nil {
		return nil, fmt.Errorf("read Dockerfile: %w", err)
	}
	df, err := dockerfile.Parse(f)
	f.Close()
	if err != nil {
		return nil, fmt.Errorf("Dockerfile: %w", err)
	}

	bd := &build{b: b, opts: opts, df: df, out: out, meta: map[string]string{},
		byName: map[string]*stageState{}, usedArg: map[string]bool{}}
	if err := bd.metaArgs(); err != nil {
		return nil, err
	}

	last := len(df.Stages) - 1
	if opts.Target != "" {
		last = -1
		for i, s := range df.Stages {
			if s.Name == strings.ToLower(opts.Target) {
				last = i
				break
			}
		}
		if last < 0 {
			return nil, fmt.Errorf("target stage %q not found", opts.Target)
		}
	}
	for i := 0; i <= last; i++ {
		bd.steps += 1 + len(df.Stages[i].Instructions)
	}

	var final *stageState
	for i := 0; i <= last; i++ {
		st, err := bd.runStage(ctx, df.Stages[i])
		if err != nil {
			return nil, err
		}
		bd.stages = append(bd.stages, st)
		if st.name != "" {
			bd.byName[st.name] = st
		}
		final = st
	}
	for k := range opts.BuildArgs {
		if !bd.usedArg[k] {
			fmt.Fprintf(out, "[warning] build-arg %q was not consumed by any ARG\n", k)
		}
	}
	return bd.commitImage(final)
}

func (bd *build) metaArgs() error {
	lookup := func(k string) (string, bool) { v, ok := bd.meta[k]; return v, ok }
	for _, in := range bd.df.MetaArgs {
		kv, err := argDecl(in.Raw, bd.df.Escape, lookup)
		if err != nil {
			return fmt.Errorf("line %d: %w", in.Line, err)
		}
		for _, d := range kv {
			v, given := bd.opts.BuildArgs[d.name]
			if given {
				bd.usedArg[d.name] = true
			} else {
				v = d.def
			}
			bd.meta[d.name] = v
		}
	}
	return nil
}

type argDef struct {
	name, def string
	hasDef    bool
}

func argDecl(raw string, escape rune, lookup dockerfile.Lookup) ([]argDef, error) {
	words, err := dockerfile.Words(raw, escape, lookup)
	if err != nil {
		return nil, err
	}
	if len(words) == 0 {
		return nil, errors.New("ARG requires a name")
	}
	out := make([]argDef, 0, len(words))
	for _, w := range words {
		k, v, ok := strings.Cut(w, "=")
		if k == "" {
			return nil, fmt.Errorf("ARG %q: empty name", w)
		}
		out = append(out, argDef{name: k, def: v, hasDef: ok})
	}
	return out, nil
}

func (bd *build) printStep(in dockerfile.Instruction) {
	bd.step++
	fmt.Fprintf(bd.out, "STEP %d/%d: %s\n", bd.step, bd.steps, in.Original)
}

func (bd *build) runStage(ctx context.Context, s dockerfile.Stage) (*stageState, error) {
	bd.printStep(s.From)
	if v, ok := s.From.Flag("platform"); ok {
		p, err := dockerfile.Expand(v, bd.df.Escape, func(k string) (string, bool) { v, ok := bd.meta[k]; return v, ok })
		if err != nil {
			return nil, err
		}
		if want := "linux/" + goruntime.GOARCH; p != "" && p != want && p != "$BUILDPLATFORM" {
			return nil, fmt.Errorf("line %d: --platform=%s: only %s is supported (no cross-architecture builds yet)", s.From.Line, p, want)
		}
	}
	baseRef, err := dockerfile.Expand(strings.Fields(s.From.Raw)[0], bd.df.Escape,
		func(k string) (string, bool) { v, ok := bd.meta[k]; return v, ok })
	if err != nil {
		return nil, fmt.Errorf("line %d: %w", s.From.Line, err)
	}
	st, err := bd.baseState(ctx, baseRef)
	if err != nil {
		return nil, fmt.Errorf("line %d: FROM %s: %w", s.From.Line, baseRef, err)
	}
	st.name = s.Name

	for _, in := range s.Instructions {
		bd.printStep(in)
		if err := bd.exec(ctx, st, in); err != nil {
			return nil, fmt.Errorf("line %d: %s: %w", in.Line, strings.ToUpper(in.Cmd), err)
		}
	}
	return st, nil
}

func (bd *build) baseState(ctx context.Context, ref string) (*stageState, error) {
	if strings.EqualFold(ref, "scratch") {
		return &stageState{
			cfg:  oci.ImageConfig{OS: "linux", Architecture: goruntime.GOARCH, RootFS: oci.RootFS{Type: "layers"}},
			args: map[string]string{},
			key:  chainKey("", "scratch"),
		}, nil
	}
	if prev, ok := bd.byName[strings.ToLower(ref)]; ok {
		return prev.clone(), nil
	}
	if n, err := strconv.Atoi(ref); err == nil && n >= 0 && n < len(bd.stages) {
		return bd.stages[n].clone(), nil
	}

	store := bd.b.cfg.Store
	m, cfg, err := store.LoadImageMeta(ref)
	if err != nil {
		fmt.Fprintf(bd.out, "pulling %s\n", ref)
		if _, err := store.PullWithProgress(ctx, ref, bd.opts.Auth.For(ref), nil); err != nil {
			return nil, fmt.Errorf("pull: %s", image.HumanizePullError(err))
		}
		if m, cfg, err = store.LoadImageMeta(ref); err != nil {
			return nil, err
		}
	}
	if len(cfg.Config.OnBuild) > 0 {
		fmt.Fprintf(bd.out, "[warning] %s has ONBUILD triggers; they are not executed\n", ref)
	}
	st := &stageState{cfg: *cfg, args: map[string]string{}, key: chainKey("", "image", m.Config.Digest)}
	st.layers = slices.Clone(m.Layers)
	return st.clone(), nil
}

// exec applies one instruction to st.
func (bd *build) exec(ctx context.Context, st *stageState, in dockerfile.Instruction) error {
	esc := bd.df.Escape
	words := func() ([]string, error) {
		if in.JSON {
			return in.Args, nil
		}
		return dockerfile.Words(in.Raw, esc, st.lookup)
	}
	emptyStep := func(desc string) {
		st.key = chainKey(st.key, in.Cmd, desc)
		st.cfg.History = append(st.cfg.History, oci.HistoryEntry{
			Created: now(), CreatedBy: strings.ToUpper(in.Cmd) + " " + desc, EmptyLayer: true,
		})
	}

	switch in.Cmd {
	case "arg":
		defs, err := argDecl(in.Raw, esc, st.lookup)
		if err != nil {
			return err
		}
		for _, d := range defs {
			v, given := bd.opts.BuildArgs[d.name]
			switch {
			case given:
				bd.usedArg[d.name] = true
			case d.hasDef:
				v = d.def
			default:
				mv, ok := bd.meta[d.name]
				if !ok {
					continue // declared without value: unset
				}
				v = mv
			}
			st.args[d.name] = v
			// ARG values change the RUN environment, so they are part of the key.
			st.key = chainKey(st.key, "arg", d.name, v)
		}
		return nil

	case "env":
		kv, err := dockerfile.KeyValues(in.Raw, esc, st.lookup, true)
		if err != nil {
			return err
		}
		var parts []string
		for _, p := range kv {
			st.setEnv(p[0], p[1])
			parts = append(parts, p[0]+"="+p[1])
		}
		emptyStep(strings.Join(parts, " "))

	case "label":
		kv, err := dockerfile.KeyValues(in.Raw, esc, st.lookup, true)
		if err != nil {
			return err
		}
		if st.cfg.Config.Labels == nil {
			st.cfg.Config.Labels = map[string]string{}
		}
		var parts []string
		for _, p := range kv {
			st.cfg.Config.Labels[p[0]] = p[1]
			parts = append(parts, p[0]+"="+p[1])
		}
		emptyStep(strings.Join(parts, " "))

	case "workdir":
		w, err := dockerfile.Expand(in.Raw, esc, st.lookup)
		if err != nil {
			return err
		}
		if !path.IsAbs(w) {
			w = path.Join("/", st.cfg.Config.WorkingDir, w)
		}
		st.cfg.Config.WorkingDir = path.Clean(w)
		emptyStep(st.cfg.Config.WorkingDir)

	case "user":
		u, err := dockerfile.Expand(in.Raw, esc, st.lookup)
		if err != nil {
			return err
		}
		st.cfg.Config.User = strings.TrimSpace(u)
		emptyStep(st.cfg.Config.User)

	case "expose":
		ws, err := words()
		if err != nil {
			return err
		}
		if st.cfg.Config.ExposedPorts == nil {
			st.cfg.Config.ExposedPorts = map[string]struct{}{}
		}
		for _, p := range ws {
			if !strings.Contains(p, "/") {
				p += "/tcp"
			}
			st.cfg.Config.ExposedPorts[strings.ToLower(p)] = struct{}{}
		}
		emptyStep(strings.Join(ws, " "))

	case "volume":
		ws, err := words()
		if err != nil {
			return err
		}
		if st.cfg.Config.Volumes == nil {
			st.cfg.Config.Volumes = map[string]struct{}{}
		}
		for _, v := range ws {
			st.cfg.Config.Volumes[v] = struct{}{}
		}
		emptyStep(strings.Join(ws, " "))

	case "stopsignal":
		s, err := dockerfile.Expand(in.Raw, esc, st.lookup)
		if err != nil {
			return err
		}
		st.cfg.Config.StopSignal = strings.TrimSpace(s)
		emptyStep(st.cfg.Config.StopSignal)

	case "cmd", "entrypoint":
		argv := in.Args
		if !in.JSON {
			argv = append(slices.Clone(st.shell()), in.Raw)
		}
		desc, _ := json.Marshal(argv)
		if in.Cmd == "cmd" {
			st.cfg.Config.Cmd = argv
			st.cmdSet = true
		} else {
			st.cfg.Config.Entrypoint = argv
			if !st.cmdSet {
				st.cfg.Config.Cmd = nil // a new ENTRYPOINT drops the inherited CMD
			}
		}
		emptyStep(string(desc))

	case "shell":
		if !in.JSON || len(in.Args) == 0 {
			return errors.New("SHELL requires the JSON form, e.g. SHELL [\"/bin/bash\", \"-c\"]")
		}
		st.cfg.Config.Shell = in.Args
		desc, _ := json.Marshal(in.Args)
		emptyStep(string(desc))

	case "healthcheck":
		hc, err := parseHealthcheck(in, st)
		if err != nil {
			return err
		}
		st.cfg.Config.Healthcheck = hc
		desc, _ := json.Marshal(hc)
		emptyStep(string(desc))

	case "onbuild":
		st.cfg.Config.OnBuild = append(st.cfg.Config.OnBuild, in.Raw)
		emptyStep(in.Raw)

	case "run":
		return bd.run(ctx, st, in)

	case "copy", "add":
		return bd.copy(ctx, st, in)

	default:
		return fmt.Errorf("unsupported instruction")
	}
	return nil
}

func parseHealthcheck(in dockerfile.Instruction, st *stageState) (*oci.HealthConfig, error) {
	raw := strings.TrimSpace(in.Raw)
	if strings.EqualFold(raw, "none") {
		return &oci.HealthConfig{Test: []string{"NONE"}}, nil
	}
	kw, rest, _ := strings.Cut(raw, " ")
	if !strings.EqualFold(kw, "cmd") {
		return nil, errors.New("expected HEALTHCHECK [OPTIONS] CMD command, or HEALTHCHECK NONE")
	}
	rest = strings.TrimSpace(rest)
	hc := &oci.HealthConfig{}
	var arr []string
	if strings.HasPrefix(rest, "[") && json.Unmarshal([]byte(rest), &arr) == nil {
		hc.Test = append([]string{"CMD"}, arr...)
	} else {
		hc.Test = []string{"CMD-SHELL", rest}
	}
	durs := map[string]*int64{"interval": &hc.Interval, "timeout": &hc.Timeout, "start-period": &hc.StartPeriod}
	for name, dst := range durs {
		if v, ok := in.Flag(name); ok {
			d, err := time.ParseDuration(v)
			if err != nil {
				return nil, fmt.Errorf("--%s: %w", name, err)
			}
			*dst = int64(d)
		}
	}
	if v, ok := in.Flag("retries"); ok {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("--retries: invalid value %q", v)
		}
		hc.Retries = n
	}
	return hc, nil
}

func now() *time.Time {
	t := time.Now().UTC().Truncate(time.Second)
	return &t
}

func randomID(prefix string) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return prefix + hex.EncodeToString(b[:])
}

func (b *Builder) blobPaths(layers []oci.Descriptor) []string {
	out := make([]string, len(layers))
	for i, l := range layers {
		out[i] = b.cfg.Store.BlobPath(l.Digest)
	}
	return out
}

// commitLayer turns the upper dir of overlay id into a layer blob. It returns
// nil for an empty upper dir.
func (b *Builder) commitLayer(id string) (*oci.Descriptor, string, error) {
	upper := b.cfg.Overlay.UpperDir(id)
	ents, err := os.ReadDir(upper)
	if err != nil {
		return nil, "", err
	}
	if len(ents) == 0 {
		return nil, "", nil
	}
	tmp, err := b.cfg.Store.TempDir()
	if err != nil {
		return nil, "", err
	}
	dst := filepath.Join(tmp, "layer-"+id)
	desc, err := layer.WriteBlob(upper, dst, layer.DiffOptions{XattrPrefix: b.cfg.Overlay.XattrPrefix()})
	if err != nil {
		os.Remove(dst)
		return nil, "", fmt.Errorf("commit layer: %w", err)
	}
	if err := b.cfg.Store.AdoptBlob(dst, desc.Digest); err != nil {
		return nil, "", err
	}
	return &oci.Descriptor{MediaType: oci.MediaTypeImageLayerGzip, Digest: desc.Digest, Size: desc.Size}, desc.DiffID, nil
}

// addLayer records the result of a filesystem step (from cache or fresh).
func (st *stageState) addLayer(e cacheEntry, createdBy string) {
	h := oci.HistoryEntry{Created: now(), CreatedBy: createdBy}
	if e.Layer == nil {
		h.EmptyLayer = true
	} else {
		st.layers = append(st.layers, *e.Layer)
		st.cfg.RootFS.DiffIDs = append(st.cfg.RootFS.DiffIDs, e.DiffID)
	}
	st.cfg.History = append(st.cfg.History, h)
}

func (bd *build) commitImage(st *stageState) (*Result, error) {
	store := bd.b.cfg.Store
	cfg := st.cfg
	cfg.Created = now()
	cfg.RootFS.Type = "layers"
	if cfg.OS == "" {
		cfg.OS = "linux"
	}
	if cfg.Architecture == "" {
		cfg.Architecture = goruntime.GOARCH
	}
	layers := slices.Clone(st.layers)
	for i := range layers {
		// Same bytes; use the OCI media type throughout an OCI manifest.
		if layers[i].MediaType == oci.MediaTypeDockerLayerGzip || layers[i].MediaType == "" {
			layers[i].MediaType = oci.MediaTypeImageLayerGzip
		}
	}
	id, data, err := image.ConfigDigest(&cfg)
	if err != nil {
		return nil, err
	}
	if _, err := store.PutBlob(data); err != nil {
		return nil, err
	}
	m := &oci.Manifest{
		SchemaVersion: 2,
		MediaType:     oci.MediaTypeImageManifest,
		Config:        oci.Descriptor{MediaType: oci.MediaTypeImageConfig, Digest: id, Size: int64(len(data))},
		Layers:        layers,
	}
	tags := bd.opts.Tags
	if len(tags) == 0 {
		tags = []string{"nyx-build:" + strings.TrimPrefix(id, "sha256:")[:12]}
	}
	for _, t := range tags {
		if err := store.SaveImage(t, m, &cfg); err != nil {
			return nil, fmt.Errorf("tag %s: %w", t, err)
		}
	}
	fmt.Fprintf(bd.out, "COMMIT %s\n", strings.Join(tags, ", "))
	fmt.Fprintf(bd.out, "--> %s\n", strings.TrimPrefix(id, "sha256:")[:12])
	return &Result{ImageID: id, Tags: tags}, nil
}
