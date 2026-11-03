import { afterEach, describe, expect, it } from 'vitest';
import { hostBridgeMediaURL, setHostBridgeMediaToken, snapshotImageURL, SNAPSHOT_TIER_MISSING } from './host-bridge-media';

describe('hostBridgeMediaURL', () => {
    afterEach(() => setHostBridgeMediaToken(''));

    it('adds the media token to every activity media URL', () => {
        setHostBridgeMediaToken('media-token');
        expect(hostBridgeMediaURL(4242, 'snapshot', { path: '2026-03-04/a b.jpg' })).toBe(
            'http://127.0.0.1:4242/api/activity/snapshot?path=2026-03-04%2Fa%20b.jpg&token=media-token',
        );
        expect(hostBridgeMediaURL(4242, 'frame', { path: 'x.hevc', ts: 1709500005123 })).toBe(
            'http://127.0.0.1:4242/api/activity/frame?path=x.hevc&ts=1709500005123&token=media-token',
        );
    });
});

describe('snapshotImageURL', () => {
    afterEach(() => setHostBridgeMediaToken(''));

    it('builds the screenshot URL for a row with an image', () => {
        setHostBridgeMediaToken('t');
        expect(snapshotImageURL(4242, { filePath: '2026-10-15/a.jpg', storageTier: 'hot' })).toBe(
            'http://127.0.0.1:4242/api/activity/snapshot?path=2026-10-15%2Fa.jpg&token=t',
        );
    });

    it('returns null for a row whose image is marked missing', () => {
        expect(snapshotImageURL(4242, { filePath: '2026-10-15/a.jpg', storageTier: SNAPSHOT_TIER_MISSING })).toBeNull();
        expect(snapshotImageURL(4242, { filePath: '' })).toBeNull();
    });
});
