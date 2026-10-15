// nyxd - minimal OCI container orchestrator for Linux (x86_64, arm64, …).
// No Docker, no Podman, no containerd. Just crun + networking + Go.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/zrougamed/nyxd/internal/build"
	"github.com/zrougamed/nyxd/internal/cgroup"
	"github.com/zrougamed/nyxd/internal/control"
	"github.com/zrougamed/nyxd/internal/daemonlock"
	"github.com/zrougamed/nyxd/internal/image"
	"github.com/zrougamed/nyxd/internal/logs"
	"github.com/zrougamed/nyxd/internal/netdns"
	"github.com/zrougamed/nyxd/internal/network"
	"github.com/zrougamed/nyxd/internal/network/native"
	"github.com/zrougamed/nyxd/internal/overlay"
	"github.com/zrougamed/nyxd/internal/rootless"
	"github.com/zrougamed/nyxd/internal/runtime"
	"github.com/zrougamed/nyxd/internal/supervisor"
)

// Build-time variables injected via -ldflags.
var (
	version   = "dev"
	gitCommit = "unknown"
	buildDate = "unknown"
)

// Config holds daemon configuration.
type Config struct {
	BaseDir     string
	CrunBin     string
	NetDriver   string // "native" (default) or "cni"
	CNIBinDir   string
	CNIConfDir  string
	NetworkName string
	LogLevel    string
	Version      bool
	Socket       string // Unix socket for HTTP control API; empty disables
	SocketGroup  string // optional POSIX group for socket (0660); lets non-root users in that group run nyx
	PullPlatform string // OCI platform for multi-arch indexes, e.g. linux/arm64; empty uses GOOS/GOARCH
	DNS          string // auto (default), embedded, cni, off — see docs/networking.md
	LogMaxSize   string // container log size that triggers rotation, e.g. 10m; 0 = never rotate
	LogMaxFiles  int    // container log files kept, the current one included

	Rootless    rootless.Info // set when running in nyxd's own user namespace
	UsernsRemap string        // "START:COUNT" or "default": root runs the daemon in a remapped user namespace
}

func main() {
	if isNologin(os.Args[0]) {
		os.Exit(nologin())
	}
	if len(os.Args) > 1 && os.Args[1] == "unshare" {
		os.Exit(unshareCmd(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "stdio-proxy" {
		os.Exit(stdioProxy(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "stop" {
		os.Exit(stopCmd(os.Args[2:]))
	}

	cfg := parseFlags()

	if cfg.Version {
		fmt.Printf("nyxd %s (commit=%s built=%s)\n", version, gitCommit, buildDate)
		os.Exit(0)
	}

	switch {
	case cfg.UsernsRemap != "":
		// Root, but the daemon and containers run in a user namespace mapped
		// to an unprivileged ID range (does not return in the host process).
		start, count, err := parseRemap(cfg.UsernsRemap)
		if err != nil {
			fmt.Fprintln(os.Stderr, "nyxd:", err)
			os.Exit(1)
		}
		info, err := rootless.EnterRemap(start, count, []string{cfg.BaseDir, filepath.Dir(cfg.Socket)})
		if err != nil {
			fmt.Fprintln(os.Stderr, "nyxd:", err)
			os.Exit(1)
		}
		cfg.Rootless = info
	case rootless.Wanted():
		// Not root: re-exec inside a user namespace (does not return in the
		// host process). See internal/rootless and docs/rootless.md.
		info, err := rootless.Enter()
		if err != nil {
			fmt.Fprintln(os.Stderr, "nyxd:", err)
			os.Exit(1)
		}
		cfg.Rootless = info
	}

	log := newLogger(cfg.LogLevel)
	log.Info("nyxd starting", "version", version, "baseDir", cfg.BaseDir,
		"rootless", cfg.Rootless.Rootless, "subids", cfg.Rootless.SubIDs)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg, log); err != nil {
		log.Error("daemon error", "err", err)
		os.Exit(1)
	}
	log.Info("nyxd stopped cleanly")
}

// unshareCmd runs a command inside the rootless user namespace (as root
// there), e.g. to delete files a rootless nyxd created with subordinate IDs:
//
//	nyxd unshare rm -rf ~/.local/share/nyxd
func unshareCmd(args []string) int {
	if !rootless.Wanted() {
		fmt.Fprintln(os.Stderr, "nyxd unshare: only meaningful for a non-root user")
		return 1
	}
	if _, err := rootless.Enter(); err != nil {
		fmt.Fprintln(os.Stderr, "nyxd unshare:", err)
		return 1
	}
	if len(args) == 0 {
		args = []string{"/bin/sh"}
		if sh := os.Getenv("SHELL"); sh != "" {
			args = []string{sh}
		}
	}
	bin, err := exec.LookPath(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "nyxd unshare:", err)
		return 127
	}
	err = syscall.Exec(bin, args, rootless.CleanEnv(os.Environ()))
	fmt.Fprintln(os.Stderr, "nyxd unshare:", err)
	return 1
}

func run(ctx context.Context, cfg Config, logger *slog.Logger) error {
	dlock, err := daemonlock.Acquire(cfg.BaseDir)
	if err != nil {
		return err
	}
	defer dlock.Close()

	// Rootless and -userns-remap run crun without a cgroup manager, so they
	// work on hosts with a hybrid v1/v2 layout (e.g. some WSL2 kernels).
	if !cfg.Rootless.Rootless {
		if err := cgroup.CheckUnifiedV2(); err != nil {
			return fmt.Errorf("cgroup: %w", err)
		}
	}

	imgStore, err := image.NewStore(cfg.BaseDir + "/images")
	if err != nil {
		return fmt.Errorf("image store: %w", err)
	}
	imgStore.SetPlatform(cfg.PullPlatform)

	ovl, err := overlay.NewManager(cfg.BaseDir+"/overlay", overlay.Options{
		Rootless: cfg.Rootless.Rootless,
		SingleID: cfg.Rootless.Rootless && !cfg.Rootless.SubIDs,
	})
	if err != nil {
		return fmt.Errorf("overlay: %w", err)
	}

	var net network.Backend
	driver := strings.ToLower(strings.TrimSpace(cfg.NetDriver))
	switch driver {
	case "cni":
		net = network.NewManager(cfg.NetworkName, cfg.CNIConfDir, cfg.CNIBinDir)
	case "", "native":
		if cfg.Rootless.Rootless {
			return fmt.Errorf("-net-driver=native needs root outside a user namespace; rootless and -userns-remap support -net-driver=host")
		}
		driver = "native"
		net = native.NewManager(logger)
	case "host":
		net = network.NewHostBackend()
	default:
		return fmt.Errorf("unknown -net-driver %q (use native, cni or host)", cfg.NetDriver)
	}
	logger.Info("network backend", "driver", driver)
	if err := net.EnsureNetwork(); err != nil {
		return fmt.Errorf("network setup: %w", err)
	}

	rt, err := runtime.New(cfg.CrunBin, cfg.BaseDir+"/run/crun")
	if err != nil {
		return fmt.Errorf("runtime: %w", err)
	}
	cgroups := !cfg.Rootless.Rootless
	if cfg.Rootless.Rootless {
		// The daemon's user namespace does not own any cgroup; without
		// delegation crun must not try to create one.
		rt.SetCgroupManager("disabled")
		logger.Warn("rootless: cgroups disabled, container resource limits are not applied")
	}

	logColl, err := logs.NewCollector(cfg.BaseDir+"/logs", logger)
	if err != nil {
		return fmt.Errorf("log collector: %w", err)
	}
	logMax, err := logs.ParseSize(cfg.LogMaxSize)
	if err != nil {
		return fmt.Errorf("-log-max-size: %w", err)
	}
	if cfg.LogMaxFiles < 1 {
		return fmt.Errorf("-log-max-files: must be at least 1")
	}
	logColl.SetRotation(logMax, cfg.LogMaxFiles)

	gw := netdns.DefaultGateway()
	var dns netdns.Backend = netdns.Noop{}
	if driver != "host" {
		dns = netdns.NewBackend(cfg.DNS, driver, logger, gw)
	}
	sup := supervisor.New(rt, ovl, net, cfg.BaseDir, logger, logColl, imgStore, dns, driver, cfg.DNS, gw.String())
	sup.SetRootless(cfg.Rootless.Rootless, cgroups)

	ctl := control.New(logger, rt, imgStore, sup, ctx, nil, version, gitCommit, buildDate, cfg.BaseDir, cfg.Socket, cfg.SocketGroup, driver, cfg.DNS)
	builder, err := build.New(build.Config{
		Store:    imgStore,
		Overlay:  ovl,
		Runtime:  rt,
		BaseDir:  cfg.BaseDir,
		Rootless: cfg.Rootless.Rootless,
		SingleID: cfg.Rootless.Rootless && !cfg.Rootless.SubIDs,
		Log:      logger,
	})
	if err != nil {
		return fmt.Errorf("builder: %w", err)
	}
	ctl.SetBuilder(builder)
	if err := ctl.Start(); err != nil {
		logger.Warn("control API not started", "err", err)
	} else {
		defer func() {
			sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := ctl.Shutdown(sctx); err != nil {
				logger.Warn("control API shutdown", "err", err)
			}
		}()
	}

	logger.Info("daemon ready - awaiting workload")

	<-ctx.Done()
	logger.Info("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sup.Shutdown(shutdownCtx)

	return nil
}

func parseFlags() Config {
	cfg := Config{}
	flag.StringVar(&cfg.BaseDir, "base-dir", "/var/lib/nyxd", "Base data directory")
	flag.StringVar(&cfg.CrunBin, "crun", "crun", "Path to crun binary")
	flag.StringVar(&cfg.NetDriver, "net-driver", "native", "Container networking: native (in-process, no CNI plugins), cni (exec plugins under -cni-bin-dir) or host (share host network; default when rootless)")
	flag.StringVar(&cfg.CNIBinDir, "cni-bin-dir", "/opt/cni/bin", "CNI plugin binaries directory (only for -net-driver=cni)")
	flag.StringVar(&cfg.CNIConfDir, "cni-conf-dir", "/etc/cni/net.d", "CNI config directory (only for -net-driver=cni)")
	flag.StringVar(&cfg.NetworkName, "network", "nyx", "CNI network name (only for -net-driver=cni)")
	flag.StringVar(&cfg.LogLevel, "log-level", "info", "Log level: debug|info|warn|error")
	flag.StringVar(&cfg.Socket, "socket", "/run/nyxd/nyxd.sock", "Unix socket for HTTP control API (nyx client); set to \"\" to disable")
	flag.StringVar(&cfg.SocketGroup, "socket-group", "", "POSIX group name for the socket (mode 0660, chown root:group); add users to this group so nyx works without sudo")
	flag.StringVar(&cfg.PullPlatform, "pull-platform", "", "OCI platform for multi-arch manifests (e.g. linux/arm64); default uses this binary's GOOS/GOARCH")
	flag.StringVar(&cfg.DNS, "dns", "auto", "DNS: auto (embedded; on CNI only for compose internal networks), embedded, cni (no embedded), off")
	flag.StringVar(&cfg.UsernsRemap, "userns-remap", "", "As root: run the daemon and containers in a user namespace mapped to host IDs START:COUNT (\"default\" = 100000:65536); no setuid helpers needed")
	flag.StringVar(&cfg.LogMaxSize, "log-max-size", "10m", "Rotate a container's log file when it reaches this size (k, m, g suffixes); 0 never rotates")
	flag.IntVar(&cfg.LogMaxFiles, "log-max-files", logs.DefaultMaxFiles, "Log files kept per container, the current one included")
	flag.BoolVar(&cfg.Version, "version", false, "Print version and exit")
	flag.Parse()

	switch {
	case cfg.UsernsRemap != "":
		set := map[string]bool{}
		flag.Visit(func(f *flag.Flag) { set[f.Name] = true })
		if !set["net-driver"] {
			cfg.NetDriver = "host"
		}
	case rootless.Wanted():
		applyRootlessDefaults(&cfg)
	}
	return cfg
}

// parseRemap parses -userns-remap: "default" or "START:COUNT".
func parseRemap(v string) (start, count int, err error) {
	if v == "default" {
		return 100000, 65536, nil
	}
	a, b, ok := strings.Cut(v, ":")
	if ok {
		_, err1 := fmt.Sscan(a, &start)
		_, err2 := fmt.Sscan(b, &count)
		if err1 == nil && err2 == nil && start > 0 && count > 0 {
			return start, count, nil
		}
	}
	return 0, 0, fmt.Errorf("-userns-remap=%q: expected \"default\" or START:COUNT, e.g. 100000:65536", v)
}

// applyRootlessDefaults moves defaults that need root to per-user locations,
// keeping any flag the user set explicitly.
func applyRootlessDefaults(cfg *Config) {
	set := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { set[f.Name] = true })

	if !set["base-dir"] {
		dataHome := os.Getenv("XDG_DATA_HOME")
		if dataHome == "" {
			if home, err := os.UserHomeDir(); err == nil {
				dataHome = filepath.Join(home, ".local", "share")
			}
		}
		cfg.BaseDir = filepath.Join(dataHome, "nyxd")
	}
	if !set["socket"] {
		cfg.Socket = filepath.Join(rootlessRuntimeDir(), "nyxd.sock")
	}
	if !set["net-driver"] {
		cfg.NetDriver = "host"
	}
}

// rootlessRuntimeDir is $XDG_RUNTIME_DIR/nyxd, or /tmp/nyxd-<uid> without it.
// The uid comes from the environment set before the user namespace (where
// getuid() reports 0).
func rootlessRuntimeDir() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return filepath.Join(d, "nyxd")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("nyxd-%d", hostUID()))
}

func hostUID() int {
	if v := os.Getenv(rootless.EnvHostUID); v != "" {
		var uid int
		if _, err := fmt.Sscan(v, &uid); err == nil {
			return uid
		}
	}
	return os.Getuid()
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}
