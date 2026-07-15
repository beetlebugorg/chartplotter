// @ts-check

/** @type {import('@docusaurus/plugin-content-docs').SidebarsConfig} */
const sidebars = {
  docs: [
    'intro',
    'installation',
    'getting-started',
    'chart1',
    'widget',
    'cli',
    'architecture',
    {
      type: 'category',
      label: 'Plugins',
      items: [
        'plugins/plugins-overview',
        'plugins/plugins-getting-started',
        'plugins/plugins-manifest',
        'plugins/plugins-capabilities',
        'plugins/plugins-sdk',
        'plugins/plugins-protocol',
        'plugins/plugins-ui',
        'plugins/plugins-packaging',
        'plugins/plugins-examples',
      ],
    },
    'tile-schema',
    'limitations',
  ],
};

export default sidebars;
