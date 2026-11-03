#!/bin/bash
set -e

# Yansu Agent Local Testing Script
# Usage: ./scripts/test-local.sh

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
AGENT_DIR="$(dirname "$SCRIPT_DIR")"
PROJECT_ROOT="$(dirname "$AGENT_DIR")"

echo "========================================"
echo "  Yansu Agent Local Testing"
echo "========================================"
echo ""

# Check if dev server is running
echo "Checking dev server..."
if ! curl -sk https://localhost:3000/api/health > /dev/null 2>&1; then
    echo "Error: Dev server is not running!"
    echo ""
    echo "Please start the development environment first:"
    echo "  cd $PROJECT_ROOT && ./start-demo.sh"
    exit 1
fi
echo "OK: Dev server is running"

# Build yansu agent
echo ""
echo "Building yansu agent..."
cd "$AGENT_DIR"
go build -o yansu ./cmd/yansu/
echo "OK: Built successfully"

# Set environment
export YANSU_URL=https://localhost:3000

# Check if already logged in
echo ""
echo "Checking authentication..."
if ./yansu status 2>&1 | grep -q "Not logged in"; then
    echo ""
    echo "Not logged in. Starting login..."
    echo ""
    ./yansu login
    if [ $? -ne 0 ]; then
        echo "Error: Login failed!"
        exit 1
    fi
fi
echo "OK: Authenticated"

# Show available commands
echo ""
echo "========================================"
echo "  Ready to test!"
echo "========================================"
echo ""
echo "Available commands:"
echo ""
echo "  ./yansu status                          # Show auth status"
echo "  ./yansu clone <org>/<product>/<project> # Clone a project"
echo "  ./yansu sync                            # Sync knowledge"
echo "  ./yansu push                            # Push code + knowledge"
echo "  ./yansu hook install                    # Install git hook"
echo "  ./yansu daemon                          # Start sync daemon"
echo ""
echo "Environment:"
echo "  YANSU_URL=$YANSU_URL"
echo ""
