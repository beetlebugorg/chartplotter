# chartplotter documentation

This is the source for the chartplotter documentation site. It is built with
[Docusaurus](https://docusaurus.io/) and published to GitHub Pages at
[beetlebugorg.github.io/chartplotter](https://beetlebugorg.github.io/chartplotter/).

## Develop

Install dependencies and start a local server with live reload:

```sh
npm install
npm start
```

## Build

Generate the static site into `build/`:

```sh
npm run build
```

## Deploy

Pushing to `main` builds and deploys the site through the
`.github/workflows/docs.yml` workflow. You do not need to deploy by hand.
