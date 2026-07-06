// @ts-check
import {themes as prismThemes} from 'prism-react-renderer';

/** @type {import('@docusaurus/types').Config} */
const config = {
  title: 'chartplotter',
  tagline: '⚓ An S-101 marine chart plotter.',

  url: 'https://beetlebugorg.github.io',
  baseUrl: '/chartplotter/',

  organizationName: 'beetlebugorg',
  projectName: 'chartplotter',

  onBrokenLinks: 'warn',

  markdown: {
    hooks: {
      onBrokenMarkdownLinks: 'warn',
    },
  },

  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },

  presets: [
    [
      'classic',
      /** @type {import('@docusaurus/preset-classic').Options} */
      ({
        docs: {
          routeBasePath: '/',
          sidebarPath: './sidebars.js',
          editUrl:
            'https://github.com/beetlebugorg/chartplotter/tree/main/docs/',
        },
        blog: false,
        theme: {
          customCss: './src/css/custom.css',
        },
      }),
    ],
  ],

  themeConfig:
    /** @type {import('@docusaurus/preset-classic').ThemeConfig} */
    ({
      navbar: {
        title: 'chartplotter',
        items: [
          {
            href: 'https://beetlebugorg.github.io/chartplotter/demo/',
            label: 'Live demo',
            position: 'right',
          },
          {
            href: 'https://github.com/beetlebugorg/chartplotter',
            label: 'GitHub',
            position: 'right',
          },
        ],
      },
      footer: {
        style: 'dark',
        links: [
          {
            title: 'Docs',
            items: [
              {label: 'Introduction', to: '/'},
              {label: 'Installation', to: '/installation'},
              {label: 'CLI Reference', to: '/cli'},
            ],
          },
          {
            title: 'More',
            items: [
              {
                label: 'GitHub',
                href: 'https://github.com/beetlebugorg/chartplotter',
              },
              {
                label: 'Releases',
                href: 'https://github.com/beetlebugorg/chartplotter/releases',
              },
            ],
          },
        ],
        copyright: `Copyright © ${new Date().getFullYear()} Jeremy Collins.`,
      },
      prism: {
        theme: prismThemes.github,
        darkTheme: prismThemes.dracula,
        additionalLanguages: ['bash', 'json'],
      },
    }),
};

export default config;
