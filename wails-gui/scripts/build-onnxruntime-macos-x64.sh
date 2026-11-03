#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WAILS_DIR="$(dirname "$SCRIPT_DIR")"
source "$SCRIPT_DIR/bundle-architecture.sh"
ORT_VERSION=1.24.4
ORT_SOURCE_SHA=0cf4d2ee4392fbb8aedaabc6b2ba11b4a680d1071fa4f75546c2289ca5b404cf
PROTOC_SHA=1acab3d1f774d120d5a38bccb78fd379d35226b254dc258978bc45593478ca4c
OUT="$WAILS_DIR/onnxruntime-bundle"
LIB="$OUT/lib/libonnxruntime.${ORT_VERSION}.dylib"
valid_library() {
    local path="$1" minos symbols
    [[ -f "$path" ]] || return 1
    [[ "$(lipo -archs "$path" 2>/dev/null)" = x86_64 ]] || return 1
    minos="$(otool -l "$path" | awk '/LC_BUILD_VERSION/{f=1;next} f && /minos/ && !found {print $2;found=1}')"
    [[ "$minos" = 14.0 ]] || return 1
    symbols="$(nm -m "$path" | grep '_OBJC_CLASS_\$_MLComputePlan' || true)"
    if printf '%s\n' "$symbols" | grep 'undefined.*external.*_OBJC_CLASS_\$_MLComputePlan' | grep -v 'weak external' >/dev/null; then
        return 1
    fi
    nm -gU "$path" | grep '_OrtGetApiBase' >/dev/null || return 1
}
if [[ -f "$OUT/.sha256" && -f "$LIB" ]] && [[ "$(cat "$OUT/.sha256")" = "$(bundle_sha256 "$LIB")" ]] && valid_library "$LIB"; then
    # Restore the linker alias if the cache was partially copied.
    ln -sfn "libonnxruntime.${ORT_VERSION}.dylib" "$OUT/lib/libonnxruntime.dylib"
    echo "[build-onnxruntime] Reusing verified macOS 14 x86_64 ONNX Runtime ${ORT_VERSION}"
    exit 0
fi
mkdir -p "$OUT"
tmp="$(mktemp -d "$OUT/.build.XXXXXXXX")"
trap 'rm -rf -- "$tmp"' EXIT
curl -fSL --retry 5 --retry-all-errors -o "$tmp/source.tar.gz" "https://github.com/microsoft/onnxruntime/archive/refs/tags/v${ORT_VERSION}.tar.gz"
[[ "$(bundle_sha256 "$tmp/source.tar.gz")" = "$ORT_SOURCE_SHA" ]] || { echo '[build-onnxruntime] Source SHA-256 mismatch' >&2; exit 1; }
curl -fSL --retry 5 --retry-all-errors -o "$tmp/protoc.zip" 'https://github.com/protocolbuffers/protobuf/releases/download/v21.12/protoc-21.12-osx-universal_binary.zip'
[[ "$(bundle_sha256 "$tmp/protoc.zip")" = "$PROTOC_SHA" ]] || { echo '[build-onnxruntime] protoc SHA-256 mismatch' >&2; exit 1; }
mkdir -p "$tmp/source" "$tmp/protoc"
tar -xzf "$tmp/source.tar.gz" --strip-components=1 -C "$tmp/source"
unzip -q "$tmp/protoc.zip" -d "$tmp/protoc"
(cd "$tmp/source" && patch -p1 < "$SCRIPT_DIR/patches/onnxruntime-1.24.4-coreml-availability.patch")
case "$(uname -m)" in arm64|aarch64) host=arm64 ;; x86_64|amd64) host=amd64 ;; *) echo '[build-onnxruntime] Unsupported host architecture' >&2; exit 1 ;; esac
bundle_macho_matches "$tmp/protoc/bin/protoc" "$host" || { echo '[build-onnxruntime] protoc cannot execute on this host' >&2; exit 1; }
python=python3
if command -v python3.12 >/dev/null 2>&1; then python=python3.12; fi
cd "$tmp/source"
MACOSX_DEPLOYMENT_TARGET=14.0 "$python" tools/ci_build/build.py --build_dir "$tmp/build" --config Release --update --build --target onnxruntime --build_shared_lib --skip_tests --skip_submodule_sync --skip_pip_install --parallel 2 --use_xcode --osx_arch x86_64 --use_coreml --path_to_protoc_exe "$tmp/protoc/bin/protoc" --cmake_extra_defines CMAKE_OSX_DEPLOYMENT_TARGET=14.0 CMAKE_SYSTEM_NAME=Darwin CMAKE_SYSTEM_PROCESSOR=x86_64 onnxruntime_CROSS_COMPILING=ON onnxruntime_BUILD_UNIT_TESTS=OFF FLATBUFFERS_BUILD_FLATC=OFF CMAKE_XCODE_ATTRIBUTE_CODE_SIGNING_ALLOWED=NO CMAKE_POLICY_VERSION_MINIMUM=3.5 FETCHCONTENT_TRY_FIND_PACKAGE_MODE=NEVER
built="$(find "$tmp/build" -type f -name "libonnxruntime.${ORT_VERSION}.dylib" -print -quit)"
[[ -n "$built" ]] && valid_library "$built" || { echo '[build-onnxruntime] Built runtime failed architecture, minimum-version or symbol checks' >&2; exit 1; }
mkdir -p "$OUT/lib"
cp "$built" "$tmp/runtime.dylib"
printf '%s\n' "$(bundle_sha256 "$tmp/runtime.dylib")" > "$tmp/sha256"
mv -f "$tmp/runtime.dylib" "$LIB"
ln -sf "libonnxruntime.${ORT_VERSION}.dylib" "$OUT/lib/libonnxruntime.dylib"
mv -f "$tmp/sha256" "$OUT/.sha256"
echo "[build-onnxruntime] Built and verified macOS 14 x86_64 ONNX Runtime ${ORT_VERSION}"
