// @ts-check

/** @type {import('@docusaurus/plugin-content-docs').SidebarsConfig} */
const sidebars = {
  docs: [
    'intro',
    {
      // Plain-language pages for people USING chartplotter on a boat.
      type: 'category',
      label: 'Using chartplotter',
      items: [
        'installation',
        'getting-started',
        'chart1',
        'nmea0183',
        'weather',
        'widget',
        'cli',
        'limitations',
      ],
    },
    {
      // How chartplotter works inside, and how to extend it.
      type: 'category',
      label: 'Developing',
      items: [
        'architecture',
        'style-guide',
        'tile-schema',
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
            'plugins/weather-grid',
            'plugins/plugins-packaging',
            'plugins/plugins-examples',
          ],
        },
      ],
    },
  ],
};

export default sidebars;
