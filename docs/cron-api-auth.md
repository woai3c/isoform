# Scheduler API credentials

The desktop scheduler API listens on a random loopback port and requires its
per-process bearer secret on every request. The port stays in `cron-port` in
the active app data directory. The secret is no longer published to a file on
macOS.

On macOS, the desktop and `yansu` CLI share a generic-password Keychain
credential, with a unique item for each rotation:

- Service: `com.isoform.cron-api.generation.<UUID>` for new entries. Readers
  also accept the older `com.isoform.cron-api` and `com.isoform.cron-api.next`
  service names for migration.
- Account: the canonical absolute app data directory, so separate installations
  and test directories do not overwrite each other's credentials.
- Value: the bearer secret. The desktop rotates it each time its API starts.
- Generation: an eight-byte unsigned big-endian value in the generic attribute;
  an older item without this attribute is generation zero.

The desktop migrates an existing `cron-secret` file at startup by storing its
new process secret in Keychain and then deleting the file only after verifying
the write. Only the desktop writes the credential. It adds the next generation
under a unique service name, reads it back, and only then attempts to retire
previous items. A failed Add leaves the current credential intact. Failed
verification removes the candidate. Readers select the highest generation,
including after an interrupted retirement; equal or malformed generations fail
closed. Each successful rotation gives the active credential a new access list,
including when upgrading from an implementation that trusted the shared
`/usr/bin/security` executable. The trusted identities are the creating app
and, when present, its exact bundled `yansu` CLI. The shared security helper is
never trusted or invoked to read credentials.

macOS can refuse deletion of an item owned by an older signing identity.
That cleanup refusal does not invalidate a verified replacement or block the
next rotation: the unique new entry remains current. Obsolete encrypted items
can remain in Keychain until their owner or the user removes them. Their old
secrets are not published or accepted by the newly started API. Stable signed
upgrades normally allow cleanup; no silent approval or ACL override is used to
remove a different owner's item.

The CLI needs the credential for its scheduler, MCP, activity and memory API
commands. macOS CLI builds therefore use CGO and Security.framework directly.
The desktop selects the CLI only at
`Contents/Resources/yansu-cli-bundle/bin/yansu` within its own `.app`, rejects
symlink substitutions, and verifies the running app's code seal and the CLI
signature before trusting it. It never grants trust through PATH, the working
directory, environment variables or the user's installed CLI location. The
normal installed CLI is a copy of that bundled executable. The development
signing hook embeds and signs the CLI before sealing the app as well.

The CLI only reads: start the desktop once to migrate an old plaintext
credential or upgrade an old access list. An identity absent from the access
list has no silent read grant; macOS controls whether access needs approval.
CGO-free macOS builds fail closed. Native queries request noninteractive access,
and an error never falls back to a shared helper or a plaintext file on macOS.
Other platforms retain `cron-secret` with mode `0600`.

Use `yansu` commands instead of reading `cron-secret` directly. The CLI resolves
credentials through the same storage package as the desktop. Authenticated
requests always use the valid port published in that directory's `cron-port`
file. `YANSU_CRON_PORT` is ignored, even when an explicit `YANSU_CRON_SECRET`
is supplied. A missing or invalid port file fails before loading the stored
secret. HTTP redirects are refused so a response cannot forward the bearer
credential to another listener. `YANSU_CRON_SECRET` remains an optional
credential override; it never changes the destination.
Other local clients are not implicitly trusted. Calling
`yansu-agent/cronsecret.Load(dataDir)` does not grant access to the item.
`security find-generic-password -g` and `-w` cannot read it silently; the
system must obtain the user's approval. Never log a credential or put it in
command arguments.

`POST /mcp/install` refuses manual stdio registrations (including an omitted
source or transport) with HTTP `403` and `add servers in Settings`. Add those
servers through Settings. The Settings binding, local imports, registry
imports, and manual HTTP transports retain their existing behavior.

Keychain protects credential storage; the CLI's silent access is not an agent
isolation boundary. A same-user copy of the trusted CLI inherits its executable
identity and can use the master credential. A process running as the user can
also modify discovery files. Blocking direct `security` reads does not close
those paths, and this branch must not be described as keeping the master
secret beyond the reach of arbitrary same-user code.

The agent boundary is the agent sandbox from security round 5 (a separate hub
branch), which denies agent Keychain lookups, together with a planned scoped
agent token. The agent will receive its own limited token rather than the
master secret. That token and sandbox integration are not implemented here;
the current master bearer still authenticates the API's other routes. The hub
must preserve that separation when integrating the security work. Third-party
plugin execution remains outside phases 0 and 1.

Darwin integration tests create a disposable Keychain under a temporary HOME
and explicitly target it. The fixture's Security preferences stay in that
HOME; it does not modify the user's default Keychain or persistent search list.
Tests skip in CI if the Keychain service is unavailable. Real access-control
tests run the exact system `security` executable in an isolated background
session without GUI/TTY access: the former permissive access list is the
positive control, while the replacement list refuses its password read.
They exercise `-g`, `-w` and clustered `-ga` without the agent sandbox. The
package also accepts `cronsecret.WithKeychain(path)` for isolated tests.
