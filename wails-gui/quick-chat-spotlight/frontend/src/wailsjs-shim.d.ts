// Ambient declarations so `tsc --noEmit` resolves the `wailsjs/...` imports
// that the vite redirect plugin (vite.config.ts) rewrites at bundle time to
// spotlight-runtime.ts / wails-app-stub.ts.
//
// The macOS native build ships no generated `wailsjs/`, and these imports
// (in App.tsx, PillInput.tsx, ConversationPanel.tsx, and the reused main-UI
// components) never resolve to real files there. The shorthand `declare
// module` (no body) types each as `any`, mirroring what the generated Wails
// bindings provided. When wailsjs/ DOES exist (the Windows Wails build), the
// real files win and these wildcards are ignored.
declare module "*/wailsjs/runtime/runtime";
declare module "*/wailsjs/go/main/App";
declare module "*/wailsjs/go/main/app";
