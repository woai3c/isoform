/** Only an older desktop may fall back. Transport and validation errors must
 * remain visible rather than pretending a new contract is unavailable. */
export function isMissingSetupBinding(error: unknown): boolean {
    const message = error instanceof Error ? error.message : String(error);
    return /binding .*not available|unknown method|method .*not found|method .*does not exist|not a function/i.test(
        message,
    );
}
