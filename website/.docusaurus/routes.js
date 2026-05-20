import React from 'react';
import ComponentCreator from '@docusaurus/ComponentCreator';

export default [
  {
    path: '/markdown-page',
    component: ComponentCreator('/markdown-page', '53a'),
    exact: true
  },
  {
    path: '/docs',
    component: ComponentCreator('/docs', '9f5'),
    routes: [
      {
        path: '/docs',
        component: ComponentCreator('/docs', '2f8'),
        routes: [
          {
            path: '/docs',
            component: ComponentCreator('/docs', '6ce'),
            routes: [
              {
                path: '/docs',
                component: ComponentCreator('/docs', '414'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/docs/getting-started/install',
                component: ComponentCreator('/docs/getting-started/install', '30b'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/docs/getting-started/usage',
                component: ComponentCreator('/docs/getting-started/usage', 'b83'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/docs/operations/kernel-requirements',
                component: ComponentCreator('/docs/operations/kernel-requirements', '5ff'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/docs/operations/native-network',
                component: ComponentCreator('/docs/operations/native-network', 'd7e'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/docs/operations/networking',
                component: ComponentCreator('/docs/operations/networking', '25b'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/docs/operations/qemu-alpine',
                component: ComponentCreator('/docs/operations/qemu-alpine', '303'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/docs/project/architecture',
                component: ComponentCreator('/docs/project/architecture', '969'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/docs/project/architecture-diagram-kit',
                component: ComponentCreator('/docs/project/architecture-diagram-kit', '82d'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/docs/project/benchmarks',
                component: ComponentCreator('/docs/project/benchmarks', 'a81'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/docs/project/roadmap',
                component: ComponentCreator('/docs/project/roadmap', 'c6d'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/docs/reference/api',
                component: ComponentCreator('/docs/reference/api', '855'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/docs/reference/api-reference',
                component: ComponentCreator('/docs/reference/api-reference', '4e7'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/docs/reference/api/compose-down',
                component: ComponentCreator('/docs/reference/api/compose-down', 'a76'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/docs/reference/api/compose-stop',
                component: ComponentCreator('/docs/reference/api/compose-stop', '0cb'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/docs/reference/api/compose-up',
                component: ComponentCreator('/docs/reference/api/compose-up', '494'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/docs/reference/api/container-logs',
                component: ComponentCreator('/docs/reference/api/container-logs', '0c5'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/docs/reference/api/exec-in-container',
                component: ComponentCreator('/docs/reference/api/exec-in-container', '7fa'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/docs/reference/api/get-version',
                component: ComponentCreator('/docs/reference/api/get-version', '6de'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/docs/reference/api/kill-container',
                component: ComponentCreator('/docs/reference/api/kill-container', 'ce1'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/docs/reference/api/list-containers',
                component: ComponentCreator('/docs/reference/api/list-containers', '622'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/docs/reference/api/list-images',
                component: ComponentCreator('/docs/reference/api/list-images', '967'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/docs/reference/api/nyxd-control-api',
                component: ComponentCreator('/docs/reference/api/nyxd-control-api', '267'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/docs/reference/api/ping',
                component: ComponentCreator('/docs/reference/api/ping', 'ceb'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/docs/reference/api/prune-images',
                component: ComponentCreator('/docs/reference/api/prune-images', 'ce0'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/docs/reference/api/pull-image',
                component: ComponentCreator('/docs/reference/api/pull-image', 'b51'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/docs/reference/api/remove-container',
                component: ComponentCreator('/docs/reference/api/remove-container', 'a5d'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/docs/reference/api/remove-image',
                component: ComponentCreator('/docs/reference/api/remove-image', '4bb'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/docs/reference/api/run-container',
                component: ComponentCreator('/docs/reference/api/run-container', '285'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/docs/reference/api/stop-container',
                component: ComponentCreator('/docs/reference/api/stop-container', '532'),
                exact: true,
                sidebar: "docsSidebar"
              }
            ]
          }
        ]
      }
    ]
  },
  {
    path: '/',
    component: ComponentCreator('/', 'e5f'),
    exact: true
  },
  {
    path: '*',
    component: ComponentCreator('*'),
  },
];
