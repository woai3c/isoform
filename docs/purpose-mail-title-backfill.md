# Repair existing Purpose titles copied from mail

The one-off repair compares current Purpose titles with mail subjects retained in
the explicit mail database from the last seven days. Matching is exact after
removing reply/forward prefixes, folding case and collapsing whitespace. Short
subjects are included; partial matches and older mail are not changed.

Use an existing Isoform binary containing this change. The command returns before
normal app startup: it does not open a window, run app services, acquire the app
instance lock, or find a home data directory. Both paths must identify existing
database files. Start with a dry run on copies of the databases:

```sh
/path/to/Isoform.app/Contents/MacOS/Yansu --backfill-purpose-mail-titles \
    --activity-db /path/to/copy/activity.db \
    --mail-db /path/to/copy/sessions.db \
    --dry-run
```

Remove `--dry-run` to apply the repair to those explicit files. The dry run opens
both files read-only and creates no schema. Application opens only the activity
database for writing; the mail database remains read-only. No unrelated app
migrations or startup maintenance run. A single counts-only line reports
`dry_run`, `scanned`, `matched`, `retitled`, and `already_repaired`; neither titles
nor paths appear in command logs. A failed run rolls back the transaction.

Each repaired title becomes a neutral label such as "Goal to review" or
"Commitment to review". The local, append-only
`purpose_mail_title_repairs_v1` metadata retains `event_id`, `original_title`,
`replacement_title`, `created_at`, `reason=recent_mail_subject`, and
`authority_effect=none`. This metadata is not sent to the phone. Repeating the
command does not append duplicate repairs.

The immutable Purpose ledger, evidence, outcomes, confidence, eligibility, and
lifecycle are unchanged, including completed or dismissed Purposes. Public
readers overlay only the current displayed title after validating the ledger.
Raw event history and its payload hashes remain available for local auditing;
current task-context references include the display repair in their projection
hash. Historical reads before the repair retain their original titles. The repair
follows unchanged-title lifecycle and due-date updates; internal writers carry
the original ledger title, never the display label, into their evidence. An actual
title change ends that repair lineage, including a later rename back to the old
words. The mail holdback continues
to inspect all outgoing text, including an unchanged outcome that repeats mail.

The implementation was verified with temporary database fixtures only. No live
app data was read or repaired, and no app was built or launched for this backfill.
