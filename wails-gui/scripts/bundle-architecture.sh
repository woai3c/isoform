#!/usr/bin/env bash
# Shared checks for macOS build inputs. Never execute a target binary to
# determine its architecture: cross-build hosts may not have Rosetta.
bundle_macho_matches() {
    local path="$1" target="$2" expected arches
    case "$target" in
        arm64|aarch64) expected=arm64 ;;
        amd64|x64|x86_64) expected=x86_64 ;;
        *) return 1 ;;
    esac
    [ -f "$path" ] || return 1
    if command -v lipo >/dev/null 2>&1; then
        arches="$(lipo -archs "$path" 2>/dev/null)" || return 1
    else
        arches="$(file -b "$path" 2>/dev/null)" || return 1
        case "$expected:$arches" in
            arm64:*arm64*|arm64:*aarch64*|x86_64:*x86_64*|x86_64:*x86-64*) return 0 ;;
            *) return 1 ;;
        esac
    fi
    case " $arches " in *" $expected "*) return 0 ;; *) return 1 ;; esac
}

bundle_sha256() {
    if command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | awk '{print $1}'
    else
        sha256sum "$1" | awk '{print $1}'
    fi
}

bundle_macho_tree_matches() {
    local dir="$1" target="$2" path
    [ -d "$dir" ] || return 1
    while IFS= read -r -d '' path; do
        if file -b "$path" | grep -q 'Mach-O' && ! bundle_macho_matches "$path" "$target"; then
            echo "[bundle-architecture] Wrong architecture: $path" >&2
            return 1
        fi
    done < <(find "$dir" -type f -print0)
}

bundle_host_matches() {
    local host target
    host="$(uname -s | tr '[:upper:]' '[:lower:]')/$(uname -m)"
    target="$1/$2"
    case "$host" in darwin/aarch64) host=darwin/arm64 ;; darwin/amd64) host=darwin/x86_64 ;; esac
    case "$target" in darwin/aarch64|darwin/arm64) target=darwin/arm64 ;; darwin/amd64|darwin/x64|darwin/x86_64) target=darwin/x86_64 ;; esac
    [ "$host" = "$target" ]
}
