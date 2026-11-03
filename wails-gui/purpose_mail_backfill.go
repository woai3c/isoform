package main

import (
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"wails-gui/internal/platform/mailheaders"
)

const purposeMailBackfillCommand = "--backfill-purpose-mail-titles"

// Title repairs are presentation metadata, never observations, user corrections,
// or lifecycle events. The original ledger and its provenance remain immutable.
func migratePurposeMailTitleRepairs(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS purpose_mail_title_repairs_v1 (
			event_id TEXT PRIMARY KEY REFERENCES purpose_open_loop_events_v1(id) ON DELETE RESTRICT,
			original_title TEXT NOT NULL,
			replacement_title TEXT NOT NULL,
			created_at INTEGER NOT NULL CHECK (created_at > 0),
			reason TEXT NOT NULL CHECK (reason = 'recent_mail_subject'),
			authority_effect TEXT NOT NULL CHECK (authority_effect = 'none')
		);
		CREATE TRIGGER IF NOT EXISTS purpose_mail_title_repairs_v1_no_update
		BEFORE UPDATE ON purpose_mail_title_repairs_v1 BEGIN
			SELECT RAISE(ABORT, 'Purpose title repair metadata is immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS purpose_mail_title_repairs_v1_no_delete
		BEFORE DELETE ON purpose_mail_title_repairs_v1 BEGIN
			SELECT RAISE(ABORT, 'Purpose title repair metadata is immutable');
		END;
	`)
	return err
}

type purposeMailTitleRepair struct {
	OriginalTitle    string
	ReplacementTitle string
	CreatedAt        int64
}

type purposeMailRepairQueryer interface {
	purposeQueryer
	QueryRow(query string, args ...any) *sql.Row
}

func loadPurposeMailTitleRepairs(queryer purposeMailRepairQueryer) (map[string]purposeMailTitleRepair, error) {
	repairs := make(map[string]purposeMailTitleRepair)
	var exists int
	if err := queryer.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'purpose_mail_title_repairs_v1'`).Scan(&exists); err != nil {
		return nil, err
	}
	// An explicit dry run may inspect an older DB without creating its schema.
	if exists == 0 {
		return repairs, nil
	}
	rows, err := queryer.Query(`SELECT event_id, original_title, replacement_title, created_at
		FROM purpose_mail_title_repairs_v1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var repair purposeMailTitleRepair
		if err := rows.Scan(&id, &repair.OriginalTitle, &repair.ReplacementTitle, &repair.CreatedAt); err != nil {
			return nil, err
		}
		repairs[id] = repair
	}
	return repairs, rows.Err()
}

// projectPurposeMailTitleRepairs runs only after ledger validation. Current.Title
// is a presentation projection; Events and Current.PayloadJSON/PayloadHash retain
// the original evidence. All transactional writers continue to load the raw
// ledger, so a repair cannot invent user evidence, reopen a goal, or change replay.
func projectPurposeMailTitleRepairs(queryer purposeMailRepairQueryer, details []PurposeOpenLoopDetail, cutoffAt int64) ([]PurposeOpenLoopDetail, error) {
	repairs, err := loadPurposeMailTitleRepairs(queryer)
	if err != nil {
		return nil, err
	}
	for i := range details {
		if repair, ok := purposeMailTitleRepairForDetail(details[i], repairs, cutoffAt); ok {
			details[i].Current.Title = repair.ReplacementTitle
		}
	}
	return details, nil
}

// A due-date or lifecycle change inherits the display repair while it carries
// the same immutable title. An actual title change ends that lineage, even if a
// later explicit rename happens to restore the old words.
func purposeMailTitleRepairForDetail(detail PurposeOpenLoopDetail, repairs map[string]purposeMailTitleRepair, cutoffAt int64) (purposeMailTitleRepair, bool) {
	current := purposeCurrentLedgerEvent(detail)
	for i := len(detail.Events) - 1; i >= 0; i-- {
		event := detail.Events[i]
		if event.Sequence > current.Sequence {
			continue
		}
		if event.Title != current.Title {
			break
		}
		if repair, ok := repairs[event.ID]; ok && repair.CreatedAt <= cutoffAt && repair.OriginalTitle == event.Title {
			return repair, true
		}
	}
	return purposeMailTitleRepair{}, false
}

func purposeMailNeutralTitle(kind string) string {
	switch kind {
	case PurposeKindCommitment:
		return "Commitment to review"
	case PurposeKindResponsibility:
		return "Responsibility to review"
	case PurposeKindGoal:
		return "Goal to review"
	case PurposeKindConcern:
		return "Concern to review"
	case PurposeKindOpportunity:
		return "Opportunity to review"
	default:
		return "Purpose to review"
	}
}

func purposeMailTitleKey(title string) string {
	return strings.Join(strings.Fields(strings.ToLower(mailheaders.NormalizeSubject(title))), " ")
}

type purposeMailBackfillCounts struct {
	Scanned         int
	Matched         int
	Retitled        int
	AlreadyRepaired int
}

// backfillPurposeMailTitles only repairs exact normalized title matches against
// headers inside the existing seven-day retention window. No body is read, and
// partial matches are left to the independent mobile holdback.
func backfillPurposeMailTitles(activityDB, mailDB *sql.DB, dryRun bool, asOf time.Time) (purposeMailBackfillCounts, error) {
	counts := purposeMailBackfillCounts{}
	if activityDB == nil || mailDB == nil || asOf.IsZero() {
		return counts, errors.New("Purpose mail backfill requires both databases and a cutoff")
	}
	rows, err := mailDB.Query(`SELECT subject FROM mail_messages WHERE at >= ? AND at <= ?`,
		asOf.Add(-mailWindowDays*24*time.Hour).UnixMilli(), asOf.UnixMilli())
	if err != nil {
		return counts, err
	}
	subjects := make(map[string]bool)
	for rows.Next() {
		var subject string
		if err := rows.Scan(&subject); err != nil {
			rows.Close()
			return counts, err
		}
		if key := purposeMailTitleKey(subject); key != "" {
			subjects[key] = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return counts, err
	}
	if err := rows.Close(); err != nil {
		return counts, err
	}

	tx, err := activityDB.Begin()
	if err != nil {
		return counts, err
	}
	defer tx.Rollback()
	details, err := loadPurposeLedger(tx)
	if err != nil {
		return counts, err
	}
	repairs, err := loadPurposeMailTitleRepairs(tx)
	if err != nil {
		return counts, err
	}
	if !dryRun {
		if err := migratePurposeMailTitleRepairs(tx); err != nil {
			return counts, err
		}
	}
	for _, detail := range details {
		counts.Scanned++
		if !subjects[purposeMailTitleKey(detail.Current.Title)] {
			continue
		}
		counts.Matched++
		if _, exists := purposeMailTitleRepairForDetail(detail, repairs, asOf.UnixMilli()); exists {
			counts.AlreadyRepaired++
			continue
		}
		if dryRun {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO purpose_mail_title_repairs_v1
			(event_id, original_title, replacement_title, created_at, reason, authority_effect)
			VALUES (?, ?, ?, ?, 'recent_mail_subject', 'none')`,
			detail.Current.ID, detail.Current.Title, purposeMailNeutralTitle(detail.OpenLoop.Kind), asOf.UnixMilli()); err != nil {
			return counts, err
		}
		counts.Retitled++
	}
	if dryRun {
		return counts, nil
	}
	if err := tx.Commit(); err != nil {
		counts.Retitled = 0
		return counts, err
	}
	return counts, nil
}

func openPurposeMailBackfillDB(path string, readOnly bool) (*sql.DB, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("database path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("database must be an existing regular file")
	}
	mode := "rw"
	if readOnly {
		mode = "ro"
	}
	dsn := url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
	query := url.Values{"mode": {mode}, "_pragma": {"foreign_keys(1)", "busy_timeout(5000)"}}
	dsn.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// This early-main command has no default data directory, store initialization,
// app logging, migration replay, UI, or background services. Its only writes are
// the explicit repair metadata transaction; dry-run opens both DBs read-only.
func runPurposeMailBackfillCommand(args []string, output io.Writer) (exitCode int, handled bool) {
	if len(args) == 0 || args[0] != purposeMailBackfillCommand {
		return 0, false
	}
	flags := flag.NewFlagSet("Purpose mail title backfill", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	activityPath := flags.String("activity-db", "", "existing activity database")
	mailPath := flags.String("mail-db", "", "existing sessions database with mail headers")
	dryRun := flags.Bool("dry-run", false, "report counts without writing")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || *activityPath == "" || *mailPath == "" {
		fmt.Fprintf(output, "Usage: %s --activity-db PATH --mail-db PATH [--dry-run]\n", purposeMailBackfillCommand)
		return 2, true
	}
	activityDB, err := openPurposeMailBackfillDB(*activityPath, *dryRun)
	if err != nil {
		fmt.Fprintln(output, "[purpose-mail-backfill] cannot open activity database")
		return 1, true
	}
	defer activityDB.Close()
	mailDB, err := openPurposeMailBackfillDB(*mailPath, true)
	if err != nil {
		fmt.Fprintln(output, "[purpose-mail-backfill] cannot open mail database")
		return 1, true
	}
	defer mailDB.Close()
	counts, err := backfillPurposeMailTitles(activityDB, mailDB, *dryRun, time.Now())
	if err != nil {
		// Database errors can include private row values or file paths.
		fmt.Fprintln(output, "[purpose-mail-backfill] failed; no repairs committed")
		return 1, true
	}
	fmt.Fprintf(output, "[purpose-mail-backfill] dry_run=%t scanned=%d matched=%d retitled=%d already_repaired=%d\n",
		*dryRun, counts.Scanned, counts.Matched, counts.Retitled, counts.AlreadyRepaired)
	return 0, true
}
