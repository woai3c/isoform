#!/usr/bin/env bash
# Fail closed until GitHub can enforce the approved policy for this repository.
set -euo pipefail
repo="${GITHUB_REPOSITORY:?Set GITHUB_REPOSITORY to the public owner/repository}"
owner_type=$(gh api "repos/$repo" --jq '.owner.type')
if [[ "$owner_type" != Organization ]]; then
    echo "Cannot apply protection: GitHub documents merge queues for organization-owned public repositories; $repo owner type is $owner_type." >&2
    exit 1
fi
cat >&2 <<'MSG'
Cannot apply protection: GitHub's native approving-review rule applies to all PRs,
while the approved policy requires review only for outside authors. A merger
bypass can also bypass protection on external PRs. Install and verify an
independent author-sensitive review check before implementing the ruleset;
never substitute an unrestricted bypass. No settings were changed.
MSG
exit 1
