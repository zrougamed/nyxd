// nyxd - minimal OCI container orchestrator for Linux (x86_64, arm64, …).
// No Docker, no Podman, no containerd. Just crun + networking + Go.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/zrougamed/nyxd/internal/cgroup"
	"github.com/zrougamed/nyxd/internal/control"
	"github.com/zrougamed/nyxd/internal/daemonlock"
	"github.com/zrougamed/nyxd/internal/image"
	"github.com/zrougamed/nyxd/internal/logs"
	"github.com/zrougamed/nyxd/internal/netdns"
	"github.com/zrougamed/nyxd/internal/network"
	"github.com/zrougamed/nyxd/internal/network/native"
	"github.com/zrougamed/nyxd/internal/overlay"
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
}

func main() {
	cfg := parseFlags()

	if cfg.Version {
		fmt.Printf("nyxd %s (commit=%s built=%s)\n", version, gitCommit, buildDate)
		os.Exit(0)
	}

	log := newLogger(cfg.LogLevel)
	log.Info("nyxd starting", "version", version, "baseDir", cfg.BaseDir)

	if os.Geteuid() != 0 {
		log.Error("nyxd must run as root (overlayfs + namespace setup requires it)")
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg, log); err != nil {
		log.Error("daemon error", "err", err)
		os.Exit(1)
	}
	log.Info("nyxd stopped cleanly")
}

func run(ctx context.Context, cfg Config, logger *slog.Logger) error {
	dlock, err := daemonlock.Acquire(cfg.BaseDir)
	if err != nil {
		return err
	}
	defer dlock.Close()

	if err := cgroup.CheckUnifiedV2(); err != nil {
		return fmt.Errorf("cgroup: %w", err)
	}

	imgStore, err := image.NewStore(cfg.BaseDir + "/images")
	if err != nil {
		return fmt.Errorf("image store: %w", err)
	}
	imgStore.SetPlatform(cfg.PullPlatform)

	ovl, err := overlay.NewManager(cfg.BaseDir + "/overlay")
	if err != nil {
		return fmt.Errorf("overlay: %w", err)
	}

	var net network.Backend
	driver := strings.ToLower(strings.TrimSpace(cfg.NetDriver))
	switch driver {
	case "cni":
		net = network.NewManager(cfg.NetworkName, cfg.CNIConfDir, cfg.CNIBinDir)
	case "", "native":
		driver = "native"
		net = native.NewManager(logger)
	default:
		return fmt.Errorf("unknown -net-driver %q (use native or cni)", cfg.NetDriver)
	}
	logger.Info("network backend", "driver", driver)
	if err := net.EnsureNetwork(); err != nil {
		return fmt.Errorf("network setup: %w", err)
	}

	rt, err := runtime.New(cfg.CrunBin, cfg.BaseDir+"/run/crun")
	if err != nil {
		return fmt.Errorf("runtime: %w", err)
	}

	logColl, err := logs.NewCollector(cfg.BaseDir+"/logs", logger)
	if err != nil {
		return fmt.Errorf("log collector: %w", err)
	}

	gw := netdns.DefaultGateway()
	dns := netdns.NewBackend(cfg.DNS, driver, logger, gw)
	sup := supervisor.New(rt, ovl, net, cfg.BaseDir, logger, logColl, imgStore, dns, driver, cfg.DNS, gw.String())

	ctl := control.New(logger, rt, imgStore, sup, ctx, nil, version, gitCommit, buildDate, cfg.BaseDir, cfg.Socket, cfg.SocketGroup, driver, cfg.DNS)
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
	flag.StringVar(&cfg.NetDriver, "net-driver", "native", "Container networking: native (in-process, no CNI plugins) or cni (exec plugins under -cni-bin-dir)")
	flag.StringVar(&cfg.CNIBinDir, "cni-bin-dir", "/opt/cni/bin", "CNI plugin binaries directory (only for -net-driver=cni)")
	flag.StringVar(&cfg.CNIConfDir, "cni-conf-dir", "/etc/cni/net.d", "CNI config directory (only for -net-driver=cni)")
	flag.StringVar(&cfg.NetworkName, "network", "nyx", "CNI network name (only for -net-driver=cni)")
	flag.StringVar(&cfg.LogLevel, "log-level", "info", "Log level: debug|info|warn|error")
	flag.StringVar(&cfg.Socket, "socket", "/run/nyxd/nyxd.sock", "Unix socket for HTTP control API (nyx client); set to \"\" to disable")
	flag.StringVar(&cfg.SocketGroup, "socket-group", "", "POSIX group name for the socket (mode 0660, chown root:group); add users to this group so nyx works without sudo")
	flag.StringVar(&cfg.PullPlatform, "pull-platform", "", "OCI platform for multi-arch manifests (e.g. linux/arm64); default uses this binary's GOOS/GOARCH")
	flag.StringVar(&cfg.DNS, "dns", "auto", "DNS: auto (embedded; on CNI only for compose internal networks), embedded, cni (no embedded), off")
	flag.BoolVar(&cfg.Version, "version", false, "Print version and exit")
	flag.Parse()
	return cfg
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
