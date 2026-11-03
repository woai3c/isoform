package storage

import (
	"runtime"
	"testing"
)

func TestMCPRunHistoryReadsMetadataWithoutLargeOutputs(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveCronJob(ctx, CronJobRow{ID: "job", Name: "Metadata test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO cron_runs(id,job_id,started_at,finished_at,status,result_summary,error)
		VALUES('run','job',1,2,'ok',zeroblob(33554432),'private error')`); err != nil {
		t.Fatal(err)
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	rows, err := s.ListMCPRunMetadata(ctx, 0, 3, 20)
	runtime.ReadMemStats(&after)
	if err != nil || len(rows) != 1 || rows[0].ID != "run" || rows[0].ResultSummary != "" || rows[0].Error != "" {
		t.Fatalf("metadata read: %+v %v", rows, err)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 8<<20 {
		t.Fatalf("metadata read allocated %d bytes for a 32MiB output", allocated)
	}
}

func TestMCPSearchPrincipalsKeepsUnicodeAndOmitsPrivateFields(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CreatePrincipal(ctx, PrincipalRow{ID: "person", Kind: PrincipalKindContact, DisplayName: "Ålice", Notes: "private notes", CreatedFrom: "manual", Aliases: []string{"private alias"}}); err != nil {
		t.Fatal(err)
	}
	rows, limited, err := s.SearchMCPPrincipals(ctx, "åli", 20)
	if err != nil || limited || len(rows) != 1 || rows[0].ID != "person" || rows[0].Notes != "" || len(rows[0].Aliases) != 0 {
		t.Fatalf("name metadata: %+v %v %v", rows, limited, err)
	}
}
