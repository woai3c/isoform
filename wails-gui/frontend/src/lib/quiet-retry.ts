/** Retry a transient read once without showing an error while it is pending.
 * Do not start the retry after the caller has withdrawn or replaced the read. */
export async function readWithQuietRetry<T>(read: () => Promise<T>, stillCurrent: () => boolean): Promise<T> {
    try {
        return await read();
    } catch (error) {
        if (!stillCurrent()) throw error;
        return read();
    }
}
