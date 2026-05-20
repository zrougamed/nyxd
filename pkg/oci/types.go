// Package oci provides minimal OCI image-spec types for nyxd.
// Only what we need - no bloat.
package oci

import "time"

// MediaType constants from OCI image-spec v1.1.
const (
	MediaTypeImageManifest     = "application/vnd.oci.image.manifest.v1+json"
	MediaTypeImageIndex        = "application/vnd.oci.image.index.v1+json"
	MediaTypeImageConfig       = "application/vnd.oci.image.config.v1+json"
	MediaTypeImageLayerGzip    = "application/vnd.oci.image.layer.v1.tar+gzip"
	MediaTypeImageLayerZstd    = "application/vnd.oci.image.layer.v1.tar+zstd"
	MediaTypeDockerManifestV2  = "application/vnd.docker.distribution.manifest.v2+json"
	MediaTypeDockerManifestList = "application/vnd.docker.distribution.manifest.list.v2+json"
	MediaTypeDockerLayerGzip   = "application/vnd.docker.image.rootfs.diff.tar.gzip"
)

// Descriptor is a content-addressable reference to a blob.
type Descriptor struct {
	MediaType string            `json:"mediaType"`
	Digest    string            `json:"digest"` // sha256:<hex>
	Size      int64             `json:"size"`
	Platform  *Platform         `json:"platform,omitempty"`
	URLs      []string          `json:"urls,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// Platform describes the OS/arch a manifest targets.
type Platform struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Variant      string `json:"variant,omitempty"`
}

// Manifest is an OCI image manifest (single-platform).
type Manifest struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType,omitempty"`
	Config        Descriptor   `json:"config"`
	Layers        []Descriptor `json:"layers"`
	Annotations   map[string]string `json:"annotations,omitempty"`
}

// Index is an OCI image index (multi-platform).
type Index struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType,omitempty"`
	Manifests     []Descriptor `json:"manifests"`
	Annotations   map[string]string `json:"annotations,omitempty"`
}

// ImageConfig is the OCI image configuration blob.
type ImageConfig struct {
	Created      *time.Time        `json:"created,omitempty"`
	Architecture string            `json:"architecture"`
	OS           string            `json:"os"`
	Config       ContainerConfig   `json:"config"`
	RootFS       RootFS            `json:"rootfs"`
	History      []HistoryEntry    `json:"history,omitempty"`
}

// ContainerConfig holds the runtime configuration for the container.
type ContainerConfig struct {
	User         string            `json:"User,omitempty"`
	Env          []string          `json:"Env,omitempty"`
	Cmd          []string          `json:"Cmd,omitempty"`
	Entrypoint   []string          `json:"Entrypoint,omitempty"`
	WorkingDir   string            `json:"WorkingDir,omitempty"`
	Labels       map[string]string `json:"Labels,omitempty"`
	ExposedPorts map[string]struct{} `json:"ExposedPorts,omitempty"`
	StopSignal   string            `json:"StopSignal,omitempty"`
}

// RootFS describes the layers that make up the image rootfs.
type RootFS struct {
	Type    string   `json:"type"`
	DiffIDs []string `json:"diff_ids"` // uncompressed sha256
}

// HistoryEntry records a layer build step.
type HistoryEntry struct {
	Created    *time.Time `json:"created,omitempty"`
	CreatedBy  string     `json:"created_by,omitempty"`
	Comment    string     `json:"comment,omitempty"`
	EmptyLayer bool       `json:"empty_layer,omitempty"`
}
