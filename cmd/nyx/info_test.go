package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestInfoCommand(t *testing.T) {
	body := `{"server_version":"v1.3.0","commit":"abc12345","build_date":"2026-11-17","go_version":"go1.27",
"mode":"rootless","host_uid":1000,"subordinate_ids":false,"network_driver":"host","dns":"auto",
"base_dir":"/home/pi/.local/share/nyxd","log_dir":"/run/nyxd/logs","storage_driver":"overlay",
"overlay_userxattr":true,"free_bytes":15000000000,"os":"linux","arch":"arm64","kernel":"6.12.25-v8",
"cgroup_v2":true,"resource_limits":false,"apparmor":true,"crun_path":"/usr/bin/crun","crun_version":"1.21",
"containers_running":5,"containers_stopped":2,"images":12,
"registry_mirrors":{"registry-1.docker.io":["https://mirror.lan:5000"],"ghcr.io":["https://ghcr-cache.lan"]},"registry_mirror_only":true}`
	sock := stub(t, routes(map[string]http.HandlerFunc{"/v1/info": reply(200, body)}))
	out, _, err := capture(t, "", func() error { return run([]string{"-socket", sock, "info"}) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Server version:   v1.3.0 (commit abc12345)",
		"Mode:             rootless (uid 1000, single ID)",
		"Storage:          /home/pi/.local/share/nyxd (overlay, userxattr)  14.0GiB free",
		"Logs:             /run/nyxd/logs",
		"Kernel:           6.12.25-v8 linux/arm64",
		"cgroup:           v2, resource limits are ignored",
		"crun:             1.21 (/usr/bin/crun)",
		"Containers:       5 running, 2 stopped",
		"Images:           12",
		"Registry mirrors: ghcr.io -> https://ghcr-cache.lan (mirror only)",
		"                  registry-1.docker.io -> https://mirror.lan:5000 (mirror only)",
	} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	out, _, err = capture(t, "", func() error { return run([]string{"-socket", sock, "info", "--json"}) })
	if err != nil || out != body {
		t.Fatalf("--json passes the daemon's answer through: %v\n%s", err, out)
	}
	if err := run([]string{"-socket", sock, "info", "-x"}); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("bad flag: %v", err)
	}
	old := stub(t, reply(404, "404 page not found"))
	if err := run([]string{"-socket", old, "info"}); err == nil || !strings.Contains(err.Error(), "too old") {
		t.Fatalf("old daemon: %v", err)
	}
	broken := stub(t, reply(500, "boom"))
	if err := run([]string{"-socket", broken, "info"}); err == nil {
		t.Fatal("500 accepted")
	}
}

func TestWriteInfoOtherModes(t *testing.T) {
	var b strings.Builder
	writeInfo(&b, infoJSON{ServerVersion: "v1", Mode: "userns-remap", CgroupV2: false, Limits: true}, "/run/nyxd/nyxd.sock")
	for _, want := range []string{"userns-remap (root mapped", "cgroup:           v1 or unavailable, resource limits applied", "crun:             ?", "AppArmor:         disabled"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("missing %q in:\n%s", want, b.String())
		}
	}
}
