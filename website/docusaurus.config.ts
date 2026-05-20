import {themes as prismThemes} from 'prism-react-renderer';
import type {Config} from '@docusaurus/types';
import type * as Preset from '@docusaurus/preset-classic';
import type * as OpenApiPlugin from 'docusaurus-plugin-openapi-docs';

// This runs in Node.js - Don't use client-side code here (browser APIs, JSX...)

const config: Config = {
  title: 'nyxd',
  tagline: 'Minimal OCI orchestration for Linux, built around crun.',
  favicon: 'img/nyxd-mark.svg',

  // Future flags, see https://docusaurus.io/docs/api/docusaurus-config#future
  future: {
    v4: true, // Improve compatibility with the upcoming Docusaurus v4
  },

  // Set the production url of your site here
  url: 'https://nyxd.io',
  // Set the /<baseUrl>/ pathname under which your site is served
  // For GitHub pages deployment, it is often '/<projectName>/'
  baseUrl: '/',

  // GitHub pages deployment config.
  // If you aren't using GitHub pages, you don't need these.
  organizationName: 'zrougamed',
  projectName: 'nyxd',

  onBrokenLinks: 'throw',

  // Even if you don't use internationalization, you can use this field to set
  // useful metadata like html lang. For example, if your site is Chinese, you
  // may want to replace "en" with "zh-Hans".
  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },

  presets: [
    [
      'classic',
      {
        docs: {
          sidebarPath: './sidebars.ts',
          routeBasePath: 'docs',
          docItemComponent: '@theme/ApiItem',
          editUrl: 'https://github.com/zrougamed/nyxd/tree/main/website/',
        },
        blog: false,
        theme: {
          customCss: './src/css/custom.css',
        },
      } satisfies Preset.Options,
    ],
  ],

  themes: ['docusaurus-theme-openapi-docs'],

  plugins: [
    [
      'docusaurus-plugin-openapi-docs',
      {
        id: 'openapi',
        docsPluginId: 'classic',
        config: {
          nyxd: {
            specPath: 'static/openapi.yaml',
            outputDir: 'docs/reference/api',
            sidebarOptions: {
              groupPathsBy: 'tag',
            },
          } satisfies OpenApiPlugin.Options,
        },
      },
    ],
  ],

  themeConfig: {
    image: 'img/logo.svg',
    colorMode: {
      defaultMode: 'light',
      disableSwitch: false,
      respectPrefersColorScheme: true,
    },
    navbar: {
      title: 'nyxd',
      logo: {
        alt: 'nyxd logo',
        src: 'img/logo.svg',
      },
      items: [
        {
          type: 'docSidebar',
          sidebarId: 'docsSidebar',
          position: 'left',
          label: 'Documentation',
        },
        {to: '/docs/reference/api/', label: 'API', position: 'left'},
        {
          href: 'https://github.com/zrougamed/nyxd',
          label: 'GitHub',
          position: 'right',
        },
      ],
    },
    footer: {
      style: 'light',
      links: [
        {
          title: 'Docs',
          items: [
            {
              label: 'Overview',
              to: '/docs/',
            },
            {
              label: 'Install',
              to: '/docs/getting-started/install',
            },
          ],
        },
        {
          title: 'Operations',
          items: [
            {
              label: 'Networking',
              to: '/docs/operations/networking',
            },
            {
              label: 'Kernel Requirements',
              to: '/docs/operations/kernel-requirements',
            },
          ],
        },
        {
          title: 'More',
          items: [
            {
              label: 'GitHub',
              href: 'https://github.com/zrougamed/nyxd',
            },
            {
              label: 'OpenAPI Spec',
              href: 'https://raw.githubusercontent.com/zrougamed/nyxd/main/docs/openapi.yaml',
            },
          ],
        },
      ],
      copyright: `Copyright © ${new Date().getFullYear()} nyxd contributors. Built with Docusaurus.`,
    },
    prism: {
      theme: prismThemes.github,
      darkTheme: prismThemes.dracula,
    },
  } satisfies Preset.ThemeConfig,
};

export default config;
