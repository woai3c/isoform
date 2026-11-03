# Contributing

Thanks for helping improve Isoform. Keep changes focused and include tests for behavior changes when the relevant package already has tests.

## Build

Install Go 1.25 or later, Node.js 20.19 or later, pnpm, and the Wails v2 CLI. See [the development guide](docs/development.md) for setup and bundled dependencies.

Start the desktop app in development mode:

```bash
./wails-gui/scripts/start-wails-gui.sh
```

Create a production desktop build:

```bash
./wails-gui/scripts/build-wails.sh
```

Build the Go modules and frontend without packaging a desktop release:

```bash
cd wails-gui && go build ./...
cd yansu-agent && go build ./...
cd relay && go build ./...

cd wails-gui && wails generate module
cd frontend && pnpm install --frozen-lockfile && pnpm build
```

## Run tests

Run the tests related to your change:

```bash
cd wails-gui && go test ./...
cd wails-gui/frontend && pnpm test
cd yansu-agent && make test
cd relay && go test ./...
```

Format Go code with `go fmt` and follow the style of the surrounding code.

## Open a pull request

Create a branch, commit your changes, and push the branch to your fork. Open a pull request with a short summary, the reason for the change, and the tests you ran. Use a draft pull request for unfinished work.

Do not include credentials, private user data, or unrelated generated files. Report security issues privately using [SECURITY.md](SECURITY.md), not in a pull request or public issue.
