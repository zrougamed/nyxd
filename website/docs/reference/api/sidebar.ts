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
      ],
    },
    {
      type: "category",
      label: "containers",
      items: [
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
      ],
    },
  ],
};

export default sidebar.apisidebar;
