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

## UI screenshots

The screenshots in `static/img/ui/` are generated from the live app so they stay
in sync when the UI changes. To regenerate them, run from the repo root:

```sh
make docs-shots
```

This builds the binary, starts a throwaway server against your baked chart cache,
drives the `<chart-plotter>` UI through its public API (so each shot frames the
same view and panels every run), and writes high-resolution PNGs back into
`static/img/ui/`. It needs baked charts in the cache and a Chromium install. See
`scripts/docs-shots.mjs`.

## Deploy

Pushing to `main` builds and deploys the site through the
`.github/workflows/docs.yml` workflow. You do not need to deploy by hand.
