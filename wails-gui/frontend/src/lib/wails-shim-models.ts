// wails-shim-models.ts
// Transparent re-export of the real auto-generated wailsjs models so browser
// mode and native mode share the exact same TypeScript types.
//
// Prerequisite: `wailsjs/go/models.ts` is produced by `wails generate module`
// (or by any `wails dev`/`wails build` run). If that file is ever deleted
// (e.g. because wailsjs is .gitignored in some checkouts), restore it by
// running `./wails-gui/scripts/start-wails-gui.sh` once.
export * from '@wails-generated-models';
