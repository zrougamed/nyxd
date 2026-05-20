package image

import "github.com/zrougamed/nyxd/pkg/oci"

// PullEvent is one line of application/x-ndjson during a streamed image pull.
// Phases: begin, manifest, layer, progress, layer_done, config, meta, done, error.
type PullEvent struct {
	Phase   string `json:"phase"`
	Ref     string `json:"ref,omitempty"`
	Message string `json:"message,omitempty"`

	// manifest
	MediaType  string `json:"media_type,omitempty"`
	LayerCount int    `json:"layer_count,omitempty"`

	// layer / progress / layer_done
	Index   int    `json:"index,omitempty"`
	Count   int    `json:"count,omitempty"`
	Digest  string `json:"digest,omitempty"`
	Size    int64  `json:"size,omitempty"`
	Cached  bool   `json:"cached,omitempty"`
	Current int64  `json:"current,omitempty"` // absolute bytes transferred for this blob

	// done (subset of oci.ImageConfig for API stability)
	OK     bool           `json:"ok,omitempty"`
	Config *PullSummary   `json:"config,omitempty"`
	RefOut string         `json:"ref_out,omitempty"` // normalized ref echoed back
	Error  string         `json:"error,omitempty"`

	// phase "run" — terminal event for POST /v1/containers/run when stream=true (after any pull).
	ContainerID string `json:"container_id,omitempty"`
}

// PullSummary is a small view of the pulled image config for the CLI / NDJSON tail.
type PullSummary struct {
	OS             string   `json:"os"`
	Architecture   string   `json:"architecture"`
	Entrypoint     []string `json:"entrypoint,omitempty"`
	Cmd            []string `json:"cmd,omitempty"`
	WorkingDir     string   `json:"working_dir,omitempty"`
	EnvLen         int      `json:"env_len,omitempty"`
	RootFSDiffIDs  int      `json:"rootfs_diff_ids,omitempty"`
}

func SummaryFromConfig(cfg *oci.ImageConfig) *PullSummary {
	if cfg == nil {
		return nil
	}
	return &PullSummary{
		OS:            cfg.OS,
		Architecture:  cfg.Architecture,
		Entrypoint:    cfg.Config.Entrypoint,
		Cmd:           cfg.Config.Cmd,
		WorkingDir:    cfg.Config.WorkingDir,
		EnvLen:        len(cfg.Config.Env),
		RootFSDiffIDs: len(cfg.RootFS.DiffIDs),
	}
}
