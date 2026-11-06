import { describe, expect, it } from 'vitest';
import { hasPrivacyMarker, stripPrivacyMarkers } from './activity-privacy-marker';

// AR62: a redaction tag never shows. These cases mirror
// wails-gui/activity_privacy_marker_test.go so the Go and display rules stay in step.
describe('stripPrivacyMarkers', () => {
    it.each([
        ['Isoform · [REDACTED:code] release', 'Isoform release'],
        ['Isoform: [REDACTED:code] release', 'Isoform release'],
        ['Pick up [REDACTED:api_key]', 'Pick up'],
        ['[REDACTED:account_number]sed', ''],
        ['[REDACTED:code', ''],
        ['[REDACTED:code]', ''],
        ['Writing [REDACTED:email] memo', 'Writing memo'],
        ['[redacted:future_kind] Halcyon memo', 'Halcyon memo'],
        ["Review agent's rebuild", "Review agent's rebuild"],
    ])('turns %j into %j', (input, want) => {
        expect(stripPrivacyMarkers(input)).toBe(want);
    });

    it('reports whether a marker is present', () => {
        expect(hasPrivacyMarker('Isoform [REDACTED:code]')).toBe(true);
        expect(hasPrivacyMarker('Isoform release')).toBe(false);
    });
});
