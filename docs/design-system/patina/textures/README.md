# Patina texture sources

These five SVG tiles preserve the supplied filter definitions, including their
seeds, frequencies, octaves, alpha and SVG filter color space. They are offline
sources, never loaded by the application. The copper tile remains an unused
compatibility token; it is not applied to a surface.

The shipped `tokens.css` and `wails-gui/frontend/src/index.css` contain PNG data
URIs baked from these sources once. Each source is rendered at 320 by 320 pixels;
the helper classes retain the specified 320px grain and 260px/300px mottle tiling.
The app performs no SVG turbulence or canvas rendering at runtime.

To regenerate from the frontend directory, with Playwright WebKit installed:

```sh
node scripts/rasterize-patina-textures.mjs
```

Run the Patina tests afterwards. They verify source/app parity, PNG dimensions,
grain alpha and text contrast over every grain pixel in Paper and One Dark.
The `html[data-texture="off"]` override still sets all texture variables to `none`.
