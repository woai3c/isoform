// release-redirect worker
//
// Resolves /latest/<filename> requests on release.yansu.app to the current
// versioned URL declared in version.json, so the public-facing download
// links can stay stable while the underlying files carry version numbers.
//
// Lookup order for each request:
//   1. Parse platform key from the requested filename ("darwin-arm64", etc.)
//   2. Fetch and cache version.json from the same origin.
//   3. Look up manifest.downloads[platform] for the user-facing artifact URL
//      (.dmg / .zip / .tar.gz). The manifest.assets field is intentionally
//      not consulted: those URLs point to updater-format archives
//      (.app.tar.gz / .gz) and would hand the wrong file to a user who
//      clicked a .dmg link from the website.
//   4. Issue a 302 to the resolved URL.
//
// On any failure the request is passed through to the origin unchanged, so
// the worker never takes the download path down: missing manifest, missing
// downloads field, or unknown platform all degrade to whatever R2 currently
// serves at /latest/<filename> directly.

interface Manifest {
	version: string;
	downloads?: Record<string, string>;
}

const MANIFEST_URL = "https://release.yansu.app/version.json";
const MANIFEST_CACHE_TTL_SECONDS = 60;

export default {
	async fetch(request: Request, _env: unknown, ctx: ExecutionContext): Promise<Response> {
		const url = new URL(request.url);
		const filename = url.pathname.split("/").pop() ?? "";
		const platform = parsePlatform(filename);

		if (!platform) {
			return fetch(request);
		}

		let manifest: Manifest;
		try {
			manifest = await fetchManifest(ctx);
		} catch {
			return fetch(request);
		}

		const target = manifest.downloads?.[platform];
		if (!target || wouldLoop(target, url)) {
			return fetch(request);
		}

		return Response.redirect(target, 302);
	},
};

function parsePlatform(filename: string): string | null {
	const match = filename.match(/(darwin|windows|linux)-(x64|arm64)/);
	return match ? `${match[1]}-${match[2]}` : null;
}

// Guards against a misconfigured manifest pointing back at the same
// /latest/* path the worker handles, which would 302-loop until the
// browser/CDN gives up.
function wouldLoop(target: string, requestUrl: URL): boolean {
	let parsed: URL;
	try {
		parsed = new URL(target);
	} catch {
		return true;
	}
	return parsed.host === requestUrl.host && parsed.pathname.startsWith("/latest/");
}

async function fetchManifest(ctx: ExecutionContext): Promise<Manifest> {
	const cacheKey = new Request(MANIFEST_URL, { method: "GET" });
	const cache = caches.default;

	const cached = await cache.match(cacheKey);
	if (cached) {
		return cached.json<Manifest>();
	}

	const response = await fetch(MANIFEST_URL, {
		cf: { cacheTtl: MANIFEST_CACHE_TTL_SECONDS, cacheEverything: true },
	});
	if (!response.ok) {
		throw new Error(`manifest fetch failed: ${response.status}`);
	}

	const cacheable = new Response(response.body, response);
	cacheable.headers.set("Cache-Control", `public, max-age=${MANIFEST_CACHE_TTL_SECONDS}`);
	ctx.waitUntil(cache.put(cacheKey, cacheable.clone()));

	return cacheable.json<Manifest>();
}
