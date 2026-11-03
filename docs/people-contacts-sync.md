# People and Contacts

After consent version 3 and the computer's Contacts grant, People saves all
contacts quietly. An app-owned worker refreshes at startup and every six hours.
It never asks for permission or starts person research. Both native enumeration
and the import loop honor cancellation; shutdown cancels and joins the worker.
The People header reports the last completed refresh and offers cancellation
while a refresh runs. A failed or cancelled pass never advances that timestamp.

The source is enumerated on refresh; unchanged serialized cards are skipped.
Changed names, thumbnails, phones and emails update through the existing
resolver and importer. Phone/email ownership conflicts remain with their
existing person. Successfully applied cards are checkpointed independently so a
cancelled pass can resume without duplicates. Partial cards retain their display
metadata and are retried. Removed source cards withdraw their imported handles.
Cards cleared to notes-only or unsupported fields follow the same withdrawal
path, removing cached names/photos and source-owned bindings while keeping
curated data and contributions from other cards.

The picker searches the imported cache by name, formatted or compact phone, and
email. Phone and email matches come first, followed by name matches from the
identity resolver, including transliteration and family-name ordering. Names
are suggestions only and never authorize an automatic merge. Cards already linked explicitly are marked as connected. A card that the
importer previously deduplicated into a curated person can still be linked;
its original profile and activity are retained in the undo receipt.

Linking keeps the seen person's ID and uses the same transactional merge as
`MergePrincipals`. A receipt preserves the imported card's original profile,
handle ownership, contributed aliases and the seen person's original kind.
Unlink restores those identity fields, including handles added by later syncs.
Unlink also restores the original contact and person activity, including
preview collisions and overlapping meeting sightings. Observations and other
merges made after linking stay with the seen person. Existing dossiers and
voice samples survive linking and unlinking, including later sample deletions.
Unlink never restarts an old research job. Profile edits made after linking are
preserved.
The saved contact name and embedded thumbnail are presentation overlays and
vanish on unlink. External photo URLs are never loaded.

“Remove imported contacts” first pauses future syncs, cancels the current pass,
unlinks explicit connections, and removes source-owned handles and imported
principals. Existing people and their original handles remain. The import gate
checks the persisted pause again under the shared import/removal lock. Connecting
Contacts again resumes syncing; restarting alone cannot undo removal.

Schema v62 adds the local card cache, per-handle source ownership and link
receipts. Destination `source` / `external_ref` provenance remains unchanged by a
merge. The normal pre-migration snapshot policy applies. To run a schema-61
binary after rollback, restore the verified pre-migration snapshot; never lower
the version stamp. The next free schema version is 63.

On a v61 upgrade, migration seeds ownership of native Contacts identifiers.
The first reconciliation adopts phone/email bindings that still match the
native snapshot on import-created profiles, before checkpointing any cards so
shared fields receive every native contribution. Curated owners, mixed
vCard/native profiles, and unmatched legacy fields remain conservatively
unattributed: v61 did not store enough field-level history to safely withdraw
those bindings. Curated additions not present in the native card are preserved.
Adoption also requires the v61 importer's exact shape: the same nonempty value
in `handle_id` and `display_handle`, including native identifier anchors.
Display-only, identifier-only and unequal-field editor bindings stay unowned,
even when their values match a native phone or email.

Native signed-app checks remain in [contacts-import-qa.md](contacts-import-qa.md).
The vCard file fallback remains on computers without native Contacts support;
file imports do not refresh automatically. Browser screenshots use synthetic cards and do not exercise the native Contacts
prompt or enumeration.
