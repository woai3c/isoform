#!/bin/bash
set -e

# Yansu Agent Release Script
# Usage: ./release.sh [major|minor|patch|VERSION]
#
# Examples:
#   ./release.sh patch    # v0.2.0 -> v0.2.1
#   ./release.sh minor    # v0.2.0 -> v0.3.0
#   ./release.sh major    # v0.2.0 -> v1.0.0
#   ./release.sh v1.0.0   # Release specific version

RELEASES_REPO="Isoform/yansu-releases"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

info() {
    echo -e "${BLUE}ℹ${NC} $1"
}

success() {
    echo -e "${GREEN}✓${NC} $1"
}

warn() {
    echo -e "${YELLOW}⚠${NC} $1"
}

error() {
    echo -e "${RED}✗${NC} $1"
    exit 1
}

# Get current latest version from GitHub releases
get_latest_version() {
    local version
    version=$(gh release view --repo "$RELEASES_REPO" --json tagName -q '.tagName' 2>/dev/null || echo "")
    if [ -z "$version" ]; then
        echo "v0.0.0"
    else
        echo "$version"
    fi
}

# Parse version into components
parse_version() {
    local version="$1"
    version="${version#v}"  # Remove 'v' prefix
    echo "$version"
}

# Increment version based on type
increment_version() {
    local version="$1"
    local type="$2"

    version=$(parse_version "$version")

    local major minor patch
    IFS='.' read -r major minor patch <<< "$version"

    # Default to 0 if empty
    major=${major:-0}
    minor=${minor:-0}
    patch=${patch:-0}

    case "$type" in
        major)
            major=$((major + 1))
            minor=0
            patch=0
            ;;
        minor)
            minor=$((minor + 1))
            patch=0
            ;;
        patch)
            patch=$((patch + 1))
            ;;
        *)
            error "Invalid increment type: $type"
            ;;
    esac

    echo "v${major}.${minor}.${patch}"
}

# Check if version format is valid
is_valid_version() {
    local version="$1"
    if [[ "$version" =~ ^v?[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
        return 0
    fi
    return 1
}

# Trigger the release workflow
trigger_release() {
    local version="$1"

    info "Triggering release workflow for ${version}..."

    if gh api "repos/${RELEASES_REPO}/dispatches" \
        -f event_type=release \
        -f "client_payload[version]=${version}" 2>/dev/null; then
        success "Release workflow triggered successfully!"
        return 0
    else
        error "Failed to trigger release workflow"
    fi
}

# Wait for workflow to start and get run ID
get_workflow_run() {
    local max_attempts=10
    local attempt=0

    info "Waiting for workflow to start..."

    while [ $attempt -lt $max_attempts ]; do
        sleep 2
        local run_info
        run_info=$(gh run list --repo "$RELEASES_REPO" --limit 1 --json databaseId,status,createdAt -q '.[0]' 2>/dev/null || echo "")

        if [ -n "$run_info" ]; then
            local run_id
            run_id=$(echo "$run_info" | jq -r '.databaseId')
            echo "$run_id"
            return 0
        fi

        attempt=$((attempt + 1))
    done

    return 1
}

# Watch workflow progress
watch_workflow() {
    local run_id="$1"

    info "Watching workflow progress (Run ID: ${run_id})..."
    echo ""

    gh run watch "$run_id" --repo "$RELEASES_REPO" --exit-status || {
        error "Workflow failed! Check: https://github.com/${RELEASES_REPO}/actions/runs/${run_id}"
    }
}

# Main function
main() {
    local bump_type="$1"
    local new_version=""

    # Check if gh CLI is available
    if ! command -v gh &> /dev/null; then
        error "GitHub CLI (gh) is required. Install it from https://cli.github.com/"
    fi

    # Check if authenticated
    if ! gh auth status &> /dev/null; then
        error "Not authenticated with GitHub CLI. Run 'gh auth login' first."
    fi

    # Get current version
    info "Fetching current version..."
    local current_version
    current_version=$(get_latest_version)
    success "Current version: ${current_version}"

    # Determine new version
    if [ -z "$bump_type" ]; then
        # Interactive mode
        echo ""
        echo "Select version bump type:"
        echo "  1) patch  (${current_version} -> $(increment_version "$current_version" patch))"
        echo "  2) minor  (${current_version} -> $(increment_version "$current_version" minor))"
        echo "  3) major  (${current_version} -> $(increment_version "$current_version" major))"
        echo "  4) custom (enter specific version)"
        echo ""
        read -rp "Choice [1-4]: " choice

        case "$choice" in
            1) bump_type="patch" ;;
            2) bump_type="minor" ;;
            3) bump_type="major" ;;
            4)
                read -rp "Enter version (e.g., v1.0.0): " new_version
                if ! is_valid_version "$new_version"; then
                    error "Invalid version format: ${new_version}"
                fi
                # Ensure 'v' prefix
                new_version="v${new_version#v}"
                ;;
            *)
                error "Invalid choice"
                ;;
        esac
    fi

    # Calculate new version if not already set
    if [ -z "$new_version" ]; then
        if is_valid_version "$bump_type"; then
            # User provided a specific version
            new_version="v${bump_type#v}"
        else
            # User provided bump type
            new_version=$(increment_version "$current_version" "$bump_type")
        fi
    fi

    # Confirm release
    echo ""
    warn "About to release: ${current_version} -> ${new_version}"
    read -rp "Continue? [y/N]: " confirm

    if [[ ! "$confirm" =~ ^[Yy]$ ]]; then
        info "Release cancelled."
        exit 0
    fi

    echo ""

    # Trigger release
    trigger_release "$new_version"

    # Get and watch workflow
    echo ""
    local run_id
    run_id=$(get_workflow_run)

    if [ -n "$run_id" ]; then
        echo ""
        info "View in browser: https://github.com/${RELEASES_REPO}/actions/runs/${run_id}"
        echo ""

        read -rp "Watch workflow progress? [Y/n]: " watch_choice
        if [[ ! "$watch_choice" =~ ^[Nn]$ ]]; then
            watch_workflow "$run_id"

            echo ""
            success "Release ${new_version} completed!"
            echo ""
            info "Install/update with:"
            echo "  curl -fsSL https://raw.githubusercontent.com/${RELEASES_REPO}/main/install.sh | bash"
            echo ""
            info "Or view release:"
            echo "  gh release view ${new_version} --repo ${RELEASES_REPO}"
        fi
    else
        warn "Could not get workflow run ID. Check GitHub Actions manually."
        info "https://github.com/${RELEASES_REPO}/actions"
    fi
}

main "$@"
