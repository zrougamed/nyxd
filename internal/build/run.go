package build

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/zrougamed/nyxd/internal/build/dockerfile"
	"github.com/zrougamed/nyxd/internal/bundle"
	"github.com/zrougamed/nyxd/internal/rootfs"
	"github.com/zrougamed/nyxd/pkg/oci"
)

func (bd *build) run(ctx context.Context, st *stageState, in dockerfile.Instruction) error {
	for _, f := range in.Flags {
		name, val, _ := strings.Cut(strings.TrimPrefix(f, "--"), "=")
		switch {
		case name == "network" && (val == "host" || val == "default"):
		case name == "mount":
			return fmt.Errorf("--mount is not supported yet")
		default:
			return fmt.Errorf("unsupported flag %s", f)
		}
	}

	argv := in.Args
	if !in.JSON {
		argv = append(slices.Clone(st.shell()), in.Raw)
	}
	if len(argv) == 0 {
		return fmt.Errorf("empty command")
	}
	env := st.runEnv()
	argvJSON := jsonNoEscape(argv)
	createdBy := "RUN " + argvJSON
	if !in.JSON {
		createdBy = "RUN " + strings.Join(argv, " ")
	}

	st.key = chainKey(st.key, "run", argvJSON, strings.Join(env, "\n"),
		st.cfg.Config.User, st.cfg.Config.WorkingDir)
	b := bd.b
	if !bd.opts.NoCache {
		if e, ok := b.cache.get(st.key, b.cfg.Store.HasBlob); ok {
			fmt.Fprintln(bd.out, "--> Using cache")
			st.addLayer(e, createdBy)
			return nil
		}
	}

	id := randomID("nyxbuild-")
	merged, err := b.cfg.Overlay.Prepare(id, b.blobPaths(st.layers), nil)
	if err != nil {
		return err
	}
	defer b.cfg.Overlay.Remove(id) //nolint:errcheck

	cwd := st.cfg.Config.WorkingDir
	if cwd == "" {
		cwd = "/"
	}
	if p, err := rootfs.ScopedJoin(merged, cwd); err == nil {
		_ = os.MkdirAll(p, 0o755)
	}
	user, err := rootfs.ResolveUser(merged, st.cfg.Config.User)
	if err != nil {
		return err
	}

	bundleDir := filepath.Join(b.dir, "bundles", id)
	defer os.RemoveAll(bundleDir)
	resolv := ""
	if _, err := os.Stat("/etc/resolv.conf"); err == nil {
		resolv = "/etc/resolv.conf"
	}
	if _, err := bundle.Generate(bundleDir, bundle.Options{
		ContainerID:    id,
		RootFS:         merged,
		ImageConfig:    &oci.ImageConfig{},
		Args:           argv,
		Env:            env,
		WorkDir:        cwd,
		User:           &user,
		Hostname:       "nyx-build",
		ResolvConfPath: resolv,
		HostNetwork:    true,
		Rootless:       b.cfg.Rootless,
	}); err != nil {
		return err
	}

	runErr := b.cfg.Runtime.RunForeground(ctx, id, bundleDir, bd.out, bd.out)
	_ = b.cfg.Runtime.Delete(context.Background(), id, true)
	if runErr != nil {
		return fmt.Errorf("process %s did not complete successfully: %w", argvJSON, runErr)
	}
	if err := b.cfg.Overlay.Unmount(id); err != nil {
		return err
	}
	desc, diffID, err := b.commitLayer(id)
	if err != nil {
		return err
	}
	e := cacheEntry{Layer: desc, DiffID: diffID}
	if err := b.cache.put(st.key, e); err != nil {
		b.cfg.Log.Warn("build cache write", "err", err)
	}
	st.addLayer(e, createdBy)
	return nil
}

// jsonNoEscape renders v as JSON without HTML escaping ("&&" stays readable).
func jsonNoEscape(v any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return strings.TrimSpace(b.String())
}
