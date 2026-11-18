import type { SidebarsConfig } from "@docusaurus/plugin-content-docs";

const sidebar: SidebarsConfig = {
  apisidebar: [
    {
      type: "doc",
      id: "reference/api/nyxd-control-api",
    },
    {
      type: "category",
      label: "system",
      items: [
        {
          type: "doc",
          id: "reference/api/ping",
          label: "Liveness probe",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "reference/api/get-version",
          label: "Daemon version metadata",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "reference/api/get-info",
          label: "How the daemon runs and on what host",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "reference/api/get-metrics",
          label: "Prometheus metrics",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "reference/api/system-df",
          label: "Disk use (nyx system df)",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "reference/api/system-prune",
          label: "Remove what nothing references (nyx system prune)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "reference/api/get-events",
          label: "Stream container and image events (nyx events)",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "reference/api/get-network",
          label: "Active network identity",
          className: "api-method get",
        },
      ],
    },
    {
      type: "category",
      label: "images",
      items: [
        {
          type: "doc",
          id: "reference/api/pull-image",
          label: "Pull an image from a registry",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "reference/api/list-images",
          label: "List locally stored image references",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "reference/api/inspect-image",
          label: "Check that an image is in the local store",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "reference/api/remove-image",
          label: "Remove local image metadata for a ref",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "reference/api/prune-images",
          label: "Remove local image metadata not referenced by any supervised container",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "reference/api/tag-image",
          label: "Give a local image another reference",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "reference/api/push-image",
          label: "Push a local image to its registry",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "reference/api/save-images",
          label: "Export images as an archive",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "reference/api/load-images",
          label: "Import images from an archive",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "reference/api/build-image",
          label: "Build an image from a Dockerfile",
          className: "api-method post",
        },
      ],
    },
    {
      type: "category",
      label: "containers",
      items: [
        {
          type: "doc",
          id: "reference/api/container-archive-get",
          label: "Copy a file or directory out of a running container (nyx cp)",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "reference/api/container-archive-put",
          label: "Copy a tar stream into a running container (nyx cp)",
          className: "api-method put",
        },
        {
          type: "doc",
          id: "reference/api/check-updates",
          label: "Check image updates for containers labelled nyx.update=digest",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "reference/api/run-updates",
          label: "Update containers labelled nyx.update=digest now",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "reference/api/list-containers",
          label: "List supervised containers",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "reference/api/run-container",
          label: "Create and start a container",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "reference/api/compose-up",
          label: "Apply a nyx compose file and start the stack",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "reference/api/compose-start",
          label: "Start stopped compose services",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "reference/api/compose-restart",
          label: "Restart compose services",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "reference/api/compose-stop",
          label: "Stop all services from a compose file",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "reference/api/compose-down",
          label: "Remove compose stack containers (and optionally named volume dirs)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "reference/api/stop-container",
          label: "Stop a container",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "reference/api/start-container",
          label: "Start an exited container",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "reference/api/restart-container",
          label: "Restart a container",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "reference/api/remove-container",
          label: "Remove a container",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "reference/api/exec-in-container",
          label: "Run a command inside a running container",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "reference/api/kill-container",
          label: "Send a signal to a container (default SIGKILL)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "reference/api/container-logs",
          label: "Container stdout/stderr log file",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "reference/api/inspect-container",
          label: "Low-level container details",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "reference/api/container-stats",
          label: "Container resource usage snapshot",
          className: "api-method get",
        },
      ],
    },
  ],
};

export default sidebar.apisidebar;
