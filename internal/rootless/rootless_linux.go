//go:build linux

// Package rootless runs nyxd as an unprivileged user.
//
// The daemon re-executes itself inside a new user + mount namespace in which
// the invoking user is root. With newuidmap/newgidmap and a subordinate ID
// range in /etc/subuid and /etc/subgid, the namespace maps
//
//	0            → invoking user
//	1..count     → subordinate range
//
// so images with files owned by other users work. Without them, only the
// invoking user is mapped (to 0) and file ownership in images collapses to 0.
//
// Stages, selected by the _NYXD_USERNS environment variable:
//
//	""      host process: spawns "map", writes ID maps, forwards signals, waits
//	"map"   in the new namespace, unmapped: waits for the maps, re-execs as "ready"
//	"ready" mapped as root with full capabilities in the namespace: the daemon
//
// The extra "map" exec is needed because capabilities are recomputed at exec
// time, and an exec that happens before the maps exist runs as an unmapped
// user and loses them.
package rootless

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	envStage   = "_NYXD_USERNS"
	stageMap   = "map"
	stageReady = "ready"
	envMapping = "_NYXD_USERNS_MAPPING" // "subid" or "single"

	// EnvHostUID carries the invoking user's uid into the namespace.
	EnvHostUID = "_NYXD_HOST_UID"
)

// Info describes the namespace the daemon is running in.
type Info struct {
	// Rootless is true when nyxd runs inside its own user namespace.
	Rootless bool
	// SubIDs is true when a subordinate ID range is mapped; false means only
	// the invoking user is mapped (single-ID mode).
	SubIDs bool
}

// Wanted reports whether this process must go through Enter: it is not real
// root, or it is already one of the namespace stages. The uid alone is not
// enough: once the ID maps are written, the "map" stage also sees uid 0.
func Wanted() bool {
	return os.Geteuid() != 0 || os.Getenv(envStage) != ""
}

// Current reports whether this process is the rootless daemon stage.
func Current() Info {
	if os.Getenv(envStage) != stageReady {
		return Info{}
	}
	return Info{Rootless: true, SubIDs: os.Getenv(envMapping) == "subid"}
}

// Enter makes sure the daemon runs inside its user namespace. In the host
// stage it never returns: it runs the namespaced daemon and exits with its
// status. In the "ready" stage it prepares the mount namespace and returns.
func Enter() (Info, error) {
	switch os.Getenv(envStage) {
	case stageReady:
		// Keep our mounts (overlay, etc.) from propagating back to the host.
		if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
			return Info{}, fmt.Errorf("make mounts private: %w", err)
		}
		return Current(), nil
	case stageMap:
		return Info{}, waitAndReexec()
	}
	os.Exit(runHost())
	return Info{}, nil
}

// waitAndReexec blocks until the host stage has written the ID maps (it closes
// the sync pipe on fd 3), then re-execs so the daemon starts with capabilities.
func waitAndReexec() error {
	sync := os.NewFile(3, "nyxd-userns-sync")
	buf := make([]byte, 1)
	n, _ := sync.Read(buf)
	sync.Close()
	if n != 1 || buf[0] != '1' {
		return errors.New("rootless: user namespace setup failed in parent")
	}
	if err := unix.Setresgid(0, 0, 0); err != nil {
		return fmt.Errorf("setresgid: %w", err)
	}
	if err := unix.Setresuid(0, 0, 0); err != nil {
		return fmt.Errorf("setresuid: %w", err)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	env := setEnv(os.Environ(), envStage, stageReady)
	return syscall.Exec(exe, os.Args, env)
}

func runHost() int {
	uid, gid := os.Getuid(), os.Getgid()
	subUID, errU := lookupSubID("/etc/subuid", uid)
	subGID, errG := lookupSubID("/etc/subgid", gid)
	newuidmap, errNU := exec.LookPath("newuidmap")
	newgidmap, errNG := exec.LookPath("newgidmap")
	useSubIDs := errU == nil && errG == nil && errNU == nil && errNG == nil
	// Inside the namespace getuid() is 0; keep the real uid for per-user paths.
	os.Setenv(EnvHostUID, strconv.Itoa(uid))

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "nyxd: rootless:", err)
		return 1
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS,
		Pdeathsig:  syscall.SIGKILL,
	}

	var syncW *os.File
	if useSubIDs {
		r, w, err := os.Pipe()
		if err != nil {
			fmt.Fprintln(os.Stderr, "nyxd: rootless:", err)
			return 1
		}
		defer r.Close()
		syncW = w
		cmd.ExtraFiles = []*os.File{r}
		cmd.Env = setEnv(setEnv(os.Environ(), envStage, stageMap), envMapping, "subid")
	} else {
		fmt.Fprintf(os.Stderr, "nyxd: rootless: no subordinate IDs for uid %d (%v); mapping only this user — "+
			"images with files owned by other users lose that ownership. See docs/rootless.md.\n",
			uid, firstErr(errU, errG, errNU, errNG))
		// Go writes a single-ID map before exec, so the child starts as root.
		cmd.SysProcAttr.UidMappings = []syscall.SysProcIDMap{{ContainerID: 0, HostID: uid, Size: 1}}
		cmd.SysProcAttr.GidMappings = []syscall.SysProcIDMap{{ContainerID: 0, HostID: gid, Size: 1}}
		cmd.SysProcAttr.GidMappingsEnableSetgroups = false
		cmd.Env = setEnv(setEnv(os.Environ(), envStage, stageReady), envMapping, "single")
	}

	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "nyxd: rootless: start user namespace:", err)
		if strings.Contains(err.Error(), "permission denied") || strings.Contains(err.Error(), "operation not permitted") {
			fmt.Fprintln(os.Stderr, "nyxd: rootless: unprivileged user namespaces look disabled "+
				"(kernel.unprivileged_userns_clone / kernel.apparmor_restrict_unprivileged_userns). See docs/rootless.md.")
		}
		return 1
	}

	if syncW != nil {
		pid := strconv.Itoa(cmd.Process.Pid)
		mapErr := idmap(newuidmap, pid, uid, subUID)
		if mapErr == nil {
			mapErr = idmap(newgidmap, pid, gid, subGID)
		}
		if mapErr != nil {
			fmt.Fprintln(os.Stderr, "nyxd: rootless:", mapErr)
			syncW.Write([]byte{'0'})
		} else {
			syncW.Write([]byte{'1'})
		}
		syncW.Close()
	}

	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	go func() {
		for s := range sigs {
			_ = cmd.Process.Signal(s)
		}
	}()

	err = cmd.Wait()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "nyxd: rootless:", err)
		return 1
	}
	return 0
}

type subRange struct{ start, count int }

func idmap(tool, pid string, id int, r subRange) error {
	out, err := exec.Command(tool, pid,
		"0", strconv.Itoa(id), "1",
		"1", strconv.Itoa(r.start), strconv.Itoa(r.count),
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %v: %s", tool, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// lookupSubID finds the first subordinate range for id (by name or number).
func lookupSubID(file string, id int) (subRange, error) {
	f, err := os.Open(file)
	if err != nil {
		return subRange{}, err
	}
	defer f.Close()
	keys := map[string]bool{strconv.Itoa(id): true}
	if u, err := user.LookupId(strconv.Itoa(id)); err == nil {
		keys[u.Username] = true
	}
	return parseSubID(bufio.NewScanner(f), keys, file)
}

func parseSubID(sc *bufio.Scanner, keys map[string]bool, file string) (subRange, error) {
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) != 3 || !keys[parts[0]] {
			continue
		}
		start, err1 := strconv.Atoi(parts[1])
		count, err2 := strconv.Atoi(parts[2])
		if err1 != nil || err2 != nil || count < 1 {
			continue
		}
		return subRange{start, count}, nil
	}
	if err := sc.Err(); err != nil {
		return subRange{}, err
	}
	return subRange{}, fmt.Errorf("no entry in %s", file)
}

func setEnv(env []string, key, val string) []string {
	out := env[:0:0]
	for _, e := range env {
		if !strings.HasPrefix(e, key+"=") {
			out = append(out, e)
		}
	}
	return append(out, key+"="+val)
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}
