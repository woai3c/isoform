/// <reference types="vite/client" />

interface ImportMetaEnv {
    readonly VITE_YANSU_PORT?: string;
}

interface ImportMeta {
    readonly env: ImportMetaEnv;
}
