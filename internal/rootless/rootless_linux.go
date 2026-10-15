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
	"path/filepath"
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
	// Remap is true when real root started nyxd with -userns-remap: the
	// daemon runs as root of a namespace mapped to an unprivileged ID range.
	Remap bool
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
	m := os.Getenv(envMapping)
	return Info{Rootless: true, SubIDs: m == "subid" || m == "remap", Remap: m == "remap"}
}

// EnterDaemon is Enter for the daemon with base directory baseDir: when
// containers started by an earlier daemon still run (it crashed or was
// killed), the new daemon joins their user namespace instead of creating
// one, so it can exec into and manage them again.
func EnterDaemon(baseDir string) (Info, error) {
	joinBaseDir = baseDir
	return Enter()
}

// joinBaseDir is the base directory whose running containers the host stage
// looks for (EnterDaemon); empty for other callers of Enter.
var joinBaseDir string

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

// EnterRemap is Enter for real root with -userns-remap: the daemon runs in a
// new user + mount namespace where 0..count-1 map to host IDs
// start..start+count-1. Root writes the maps itself (no newuidmap). The
// directories in own are chowned to the mapped root first so the daemon can
// use them. Like Enter, it never returns in the host process.
func EnterRemap(start, count int, own []string) (Info, error) {
	if os.Getenv(envStage) != "" {
		return Enter()
	}
	if os.Geteuid() != 0 {
		return Info{}, errors.New("-userns-remap needs root (run nyxd as a normal user for rootless mode)")
	}
	if start < 1 || count < 1 || start+count > 1<<32-2 {
		return Info{}, fmt.Errorf("-userns-remap: invalid range %d:%d", start, count)
	}
	for _, d := range own {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return Info{}, err
		}
		if err := os.Chown(d, start, start); err != nil {
			return Info{}, fmt.Errorf("-userns-remap: chown %s: %w", d, err)
		}
	}
	// /proc/self/exe: the remapped root may not be allowed to walk the path
	// to the binary (e.g. under a home directory), and it guarantees the
	// same binary is re-executed.
	cmd := exec.Command("/proc/self/exe", os.Args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags:                 syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS,
		Pdeathsig:                  syscall.SIGKILL,
		UidMappings:                []syscall.SysProcIDMap{{ContainerID: 0, HostID: start, Size: count}},
		GidMappings:                []syscall.SysProcIDMap{{ContainerID: 0, HostID: start, Size: count}},
		GidMappingsEnableSetgroups: true,
		// The caller's own uid (0) is not in the map, so the child would be
		// "nobody" and lose its capabilities at exec. Switch to uid/gid 0 of
		// the new namespace (host start) first, while it still has them.
		Credential: &syscall.Credential{Uid: 0, Gid: 0},
	}
	// Go writes the maps before exec, so the child starts as mapped root.
	cmd.Env = setEnv(setEnv(os.Environ(), envStage, stageReady), envMapping, "remap")
	if err := cmd.Start(); err != nil {
		return Info{}, fmt.Errorf("-userns-remap: start user namespace: %w", err)
	}
	os.Exit(forwardAndWait(cmd))
	return Info{}, nil
}

func runHost() int {
	uid, gid := os.Getuid(), os.Getgid()
	if joinBaseDir != "" {
		if pid := previousNamespace("/proc", filepath.Join(joinBaseDir, "run", "crun-pids"), uid); pid > 0 {
			if code, ok := joinNamespace(pid, uid); ok {
				return code
			}
		}
	}
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

	return forwardAndWait(cmd)
}

// forwardAndWait relays termination signals to the namespaced daemon and
// returns its exit status.
func forwardAndWait(cmd *exec.Cmd) int {
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	go func() {
		for s := range sigs {
			_ = cmd.Process.Signal(s)
		}
	}()

	err := cmd.Wait()
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

// CleanEnv drops nyxd's internal namespace-stage variables, for running
// another program (e.g. `nyxd unshare CMD`): a nyxd started from it must not
// believe it is already inside its own namespace.
func CleanEnv(env []string) []string {
	out := env[:0:0]
	for _, e := range env {
		if strings.HasPrefix(e, envStage+"=") || strings.HasPrefix(e, envMapping+"=") || strings.HasPrefix(e, EnvHostUID+"=") {
			continue
		}
		out = append(out, e)
	}
	return out
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

// previousNamespace returns the pid of a container process, listed in pidDir
// (crun's --pid-file directory), that runs as uid in a user namespace other
// than ours: the namespace of an earlier daemon. 0 when there is none.
func previousNamespace(proc, pidDir string, uid int) int {
	own, err := os.Readlink(filepath.Join(proc, "self", "ns", "user"))
	if err != nil {
		return 0
	}
	files, _ := filepath.Glob(filepath.Join(pidDir, "*.pid"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil || pid < 1 {
			continue
		}
		dir := filepath.Join(proc, strconv.Itoa(pid))
		ns, err := os.Readlink(filepath.Join(dir, "ns", "user"))
		if err != nil || ns == own {
			continue
		}
		if procUID(filepath.Join(dir, "status")) != uid {
			continue // the pid was reused by someone else's process
		}
		return pid
	}
	return 0
}

// procUID returns the real uid in a /proc/<pid>/status file, or -1.
func procUID(status string) int {
	b, err := os.ReadFile(status)
	if err != nil {
		return -1
	}
	for _, line := range strings.Split(string(b), "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "Uid:" {
			if n, err := strconv.Atoi(f[1]); err == nil {
				return n
			}
		}
	}
	return -1
}

// joinNamespace runs the daemon in the user namespace of pid, as its root,
// with a new mount namespace. Go cannot join a user namespace from a running
// (multithreaded) process, so util-linux nsenter and unshare do it. ok is
// false when they are missing or cannot start; the caller then creates a new
// namespace.
func joinNamespace(pid, uid int) (code int, ok bool) {
	nsenter, err1 := exec.LookPath("nsenter")
	unshare, err2 := exec.LookPath("unshare")
	exe, err3 := os.Executable()
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, false
	}
	mapping := "single"
	if b, err := os.ReadFile(fmt.Sprintf("/proc/%d/uid_map", pid)); err == nil && len(strings.Fields(string(b))) > 3 {
		mapping = "subid"
	}
	os.Setenv(EnvHostUID, strconv.Itoa(uid))
	args := append([]string{fmt.Sprintf("--user=/proc/%d/ns/user", pid), "--", unshare, "--mount", "--", exe}, os.Args[1:]...)
	cmd := exec.Command(nsenter, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	cmd.Env = setEnv(setEnv(os.Environ(), envStage, stageReady), envMapping, mapping)
	if err := cmd.Start(); err != nil {
		return 0, false
	}
	return forwardAndWait(cmd), true
}
