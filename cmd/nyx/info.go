package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// infoJSON mirrors GET /v1/info (internal/control InfoResponse).
type infoJSON struct {
	ServerVersion     string `json:"server_version"`
	Commit            string `json:"commit"`
	BuildDate         string `json:"build_date"`
	GoVersion         string `json:"go_version"`
	Mode              string `json:"mode"`
	HostUID           *int   `json:"host_uid"`
	SubIDs            bool   `json:"subordinate_ids"`
	NetworkDriver     string `json:"network_driver"`
	DNS               string `json:"dns"`
	Socket            string `json:"socket"`
	BaseDir           string `json:"base_dir"`
	LogDir            string `json:"log_dir"`
	StorageDriver     string `json:"storage_driver"`
	UserXattr         bool   `json:"overlay_userxattr"`
	FreeBytes         uint64 `json:"free_bytes"`
	OS                string `json:"os"`
	Arch              string `json:"arch"`
	Kernel            string `json:"kernel"`
	CgroupV2          bool   `json:"cgroup_v2"`
	Limits            bool   `json:"resource_limits"`
	AppArmor          bool   `json:"apparmor"`
	CrunPath          string `json:"crun_path"`
	CrunVersion       string `json:"crun_version"`
	ContainersRunning int    `json:"containers_running"`
	ContainersStopped int    `json:"containers_stopped"`
	Images            int    `json:"images"`
}

// doInfo is nyx info [--json]: facts about the daemon and its host, for bug
// reports (--json prints the daemon's answer as is).
func doInfo(socket string, args []string) error {
	jsonOut := false
	for _, a := range args {
		switch a {
		case "--json":
			jsonOut = true
		default:
			return fmt.Errorf("usage: nyx info [--json]")
		}
	}
	resp, err := httpClient(socket).Get("http://unix/v1/info")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("info: this nyxd is too old for nyx info (upgrade the daemon)")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("info: %w", daemonError(resp.StatusCode, b))
	}
	if jsonOut {
		_, err = os.Stdout.Write(b)
		return err
	}
	var in infoJSON
	if err := json.Unmarshal(b, &in); err != nil {
		return fmt.Errorf("info: bad response: %w", err)
	}
	writeInfo(os.Stdout, in, socket)
	return nil
}

func writeInfo(w io.Writer, in infoJSON, endpoint string) {
	row := func(k, v string) { fmt.Fprintf(w, "%-17s %s\n", k+":", v) }
	or := func(v, fallback string) string {
		if v == "" {
			return fallback
		}
		return v
	}
	version := in.ServerVersion
	if in.Commit != "" {
		version += " (commit " + in.Commit + ")"
	}
	row("Server version", version)
	if in.BuildDate != "" || in.GoVersion != "" {
		row("Built", strings.TrimSpace(in.BuildDate+" "+in.GoVersion))
	}
	mode := in.Mode
	switch in.Mode {
	case "rootless":
		var parts []string
		if in.HostUID != nil {
			parts = append(parts, fmt.Sprintf("uid %d", *in.HostUID))
		}
		if in.SubIDs {
			parts = append(parts, "subordinate IDs")
		} else {
			parts = append(parts, "single ID")
		}
		mode += " (" + strings.Join(parts, ", ") + ")"
	case "userns-remap":
		mode += " (root mapped to an unprivileged ID range)"
	}
	row("Mode", mode)
	row("Endpoint", endpoint)
	row("Network driver", in.NetworkDriver+" (dns: "+or(in.DNS, "?")+")")
	storage := fmt.Sprintf("%s (%s", in.BaseDir, or(in.StorageDriver, "overlay"))
	if in.UserXattr {
		storage += ", userxattr"
	}
	storage += ")"
	if in.FreeBytes > 0 {
		storage += "  " + humanBytes(in.FreeBytes) + " free"
	}
	row("Storage", storage)
	row("Logs", in.LogDir)
	row("Kernel", strings.TrimSpace(or(in.Kernel, "?")+" "+in.OS+"/"+in.Arch))
	cg := "v1 or unavailable"
	if in.CgroupV2 {
		cg = "v2"
	}
	if in.Limits {
		cg += ", resource limits applied"
	} else {
		cg += ", resource limits are ignored"
	}
	row("cgroup", cg)
	row("crun", strings.TrimSpace(or(in.CrunVersion, "?")+" "+parenthesize(in.CrunPath)))
	row("AppArmor", map[bool]string{true: "enabled", false: "disabled"}[in.AppArmor])
	row("Containers", fmt.Sprintf("%d running, %d stopped", in.ContainersRunning, in.ContainersStopped))
	row("Images", fmt.Sprintf("%d", in.Images))
}

func parenthesize(s string) string {
	if s == "" {
		return ""
	}
	return "(" + s + ")"
}
