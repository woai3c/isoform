package main

import (
	"context"
	"errors"

	"wails-gui/storage"
)

func (a *App) searchPeople(ctx context.Context, words []string) ([]searchCandidate, error) {
	if a == nil || a.store == nil {
		return nil, nil
	}
	// Identity metadata has the Who you know page's direct-inspection policy. Do
	// not search or return notes, research or captured message previews.
	rows, err := a.store.SearchPeople(ctx, words)
	if err != nil {
		return nil, err
	}
	out := make([]searchCandidate, 0, len(rows))
	var privacy *mcpReadPrivacy
	var privacyErr error

nextPerson:
	for _, row := range rows {
		observed := true
		direct := false
		switch row.CreatedFrom {
		case "manual", "onboarding", "contacts", "contacts_import", "system_contacts", "vcard":
			direct = true
			if searchMatches(row.Title+"\n"+row.DirectBody, words) {
				// Never attach observed handles to a direct-field match's snippet.
				row.Body, observed = row.DirectBody, false
			}
		}
		// A native contact can be linked into an observed profile without
		// changing its creation source. Import-owned handles stay direct.
		if observed && row.ImportedHandles != "" && searchMatches(row.ImportedHandles, words) {
			row.Body, observed = row.ImportedHandles, false
		}
		if observed {
			if privacy == nil && privacyErr == nil {
				privacy, privacyErr = a.mcpReadPrivacy(ctx)
			}
			if errors.Is(privacyErr, errMCPReadPaused) {
				continue
			}
			if privacyErr != nil {
				return nil, privacyErr
			}
			var allowed bool
			var err error
			switch direct {
			case true:
				// Gate apps with recorded capture evidence. A missing import
				// ownership row alone does not make a typed handle observed.
				for _, app := range row.ObservedApps {
					if !privacy.appAllowed(app) {
						continue nextPerson
					}
				}
				allowed, err = privacy.personSightingsAllowedRead(row.ID)
			case false:
				allowed, err = privacy.personAllowedRead(storage.PrincipalRow{ID: row.ID, CreatedFrom: row.CreatedFrom})
			}
			if err != nil {
				return nil, err
			}
			if !allowed {
				continue
			}
		}

		out = append(out, searchCandidate{result: SearchResult{observed: observed, ID: row.ID, Place: "person", Title: row.Title, At: row.At, Target: SearchTarget{Kind: "person", ID: row.ID}}, body: row.Body, matchRawIdentity: true})
	}
	return out, ctx.Err()
}
