# cmdutil

Cross-platform `exec.Command` wrappers that suppress console window popups on Windows while acting as simple pass-throughs on other platforms.

## Purpose

In a Wails desktop application, spawning subprocesses (e.g. `git clone`) without this package causes brief console window flashes on Windows. `cmdutil` centralizes the platform-specific suppression logic so the rest of the codebase can use `cmdutil.Command` / `cmdutil.CommandContext` instead of `exec.Command` without any per-callsite `SysProcAttr` boilerplate.

## Key Components

| File | Build constraint | Description |
|------|-----------------|-------------|
| `cmdutil.go` | all platforms | Public API: `Command`, `CommandContext`, `StartWithJobObject` |
| `hide_windows.go` | `windows` | Sets `CREATE_NO_WINDOW` + `HideWindow` on `SysProcAttr`; implements Windows Job Object helpers |
| `hide_other.go` | `!windows` | No-op stubs so the package compiles unchanged on macOS/Linux |

## API

### `Command(name string, arg ...string) *exec.Cmd`

Drop-in replacement for `exec.Command`. Automatically hides the console window on Windows.

### `CommandContext(ctx context.Context, name string, arg ...string) *exec.Cmd`

Drop-in replacement for `exec.CommandContext` with the same console-hiding behaviour.

### `StartWithJobObject(cmd *exec.Cmd) (cleanup func(), err error)`

Starts a long-running command and (on Windows) places it inside a [Job Object](https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects) with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`. This ensures all descendant processes (e.g. git sub-workers) are cleaned up when the parent exits.

The returned `cleanup` function **must** be called after `cmd.Wait()` to release the Job Object handle.

```go
cmd := cmdutil.CommandContext(ctx, "git", "clone", repoURL, dest)
cleanup, err := cmdutil.StartWithJobObject(cmd)
if err != nil {
    return err
}
defer cleanup()
err = cmd.Wait()
```

On non-Windows platforms `StartWithJobObject` simply calls `cmd.Start()` and returns a no-op cleanup.

## Windows Implementation Details

Both `Command`/`CommandContext` and `StartWithJobObject` apply `CREATE_NO_WINDOW` (`0x08000000`) and `HideWindow = true` to the command's `SysProcAttr`. This prevents any console window from being allocated for the child process or its descendants (e.g. `git-index-pack`).

`StartWithJobObject` additionally creates a Windows Job Object with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` and assigns the spawned process to it, so all descendant processes are terminated if the parent exits unexpectedly.

Job Object creation is best-effort: if `CreateJobObject` or `AssignProcessToJobObject` fails, the process is still started and a no-op cleanup is returned.

## Dependencies

- Standard library: `os/exec`, `context`, `syscall`
- Windows only: `golang.org/x/sys/windows`
