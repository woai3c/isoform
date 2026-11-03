// The setup v2 prototype's SVG symbols (handoff 2026-10-28-1215 §3, "Icons"),
// drawn once per window and used with <use>. "mk" is the brand mark.

export type SetupIconId =
    | "mk"
    | "i-play"
    | "i-mail"
    | "i-cal"
    | "i-flag"
    | "i-check"
    | "i-lock"
    | "i-chev"
    | "i-term"
    | "i-screen"
    | "i-mic"
    | "i-cursor"
    | "i-folder"
    | "i-doc"
    | "i-spark"
    | "i-sun"
    | "i-chat"
    | "i-tasks"
    | "i-up"
    | "i-wifi"
    | "i-batt"
    | "i-code"
    | "i-arrow";

export function SetupIconSprite() {
    return (
        <svg width="0" height="0" style={{ position: "absolute" }} aria-hidden="true">
            <defs>
                <symbol id="sv2-mk" viewBox="0 0 19 19"><path fill="currentColor" d="M12.7953 8.48523C12.7953 7.41031 13.6666 6.53906 14.7415 6.53906C15.8163 6.53906 16.6876 7.41031 16.6876 8.48523C16.6876 9.56015 15.8163 10.4314 14.7415 10.4314C13.6666 10.4314 12.7953 9.56015 12.7953 8.48523Z"/><path fill="currentColor" d="M10.0205 0C10.5638 3.33167e-05 11.0047 0.442587 11.0049 0.988281C11.0049 1.53416 10.564 1.97751 10.0205 1.97754H9.43236C8.69024 1.97754 8.16294 2.70001 8.38921 3.4068C8.68217 4.32189 10.028 4.485 10.7821 3.88954C11.1183 3.62404 11.4993 3.38239 11.9316 3.17578C14.2252 2.07992 16.3303 2.66503 17.7041 3.42285C18.1803 3.68556 18.355 4.28626 18.0938 4.76465C17.8321 5.24313 17.2332 5.41808 16.7568 5.15527C15.7308 4.5893 14.3155 4.22617 12.7773 4.96094C11.8343 5.41157 11.2451 6.10223 10.8522 6.84285C10.1904 8.09039 10.3688 9.58799 10.7997 10.9329L11.927 14.4509C12.2684 15.5164 13.2591 16.2393 14.3779 16.2393C14.9213 16.2394 15.3613 16.6817 15.3613 17.2275C15.3612 17.7733 14.9212 18.2157 14.3779 18.2158H8.26856C7.72515 18.2158 7.28428 17.7734 7.28418 17.2275C7.28418 16.6816 7.72509 16.2393 8.26856 16.2393H8.86485C9.64004 16.2393 10.1907 15.4845 9.95418 14.7463L9.12844 12.1696C9.11065 12.1141 9.03683 12.11 9.01075 12.1621C8.45147 13.2791 7.58115 14.5191 6.24512 15.1826C4.85841 15.8712 3.53994 15.667 2.50879 15.2041C1.50444 14.7533 0.725164 14.0412 0.287113 13.5996C-0.0963769 13.213 -0.0955171 12.5875 0.289066 12.2021C0.673941 11.8169 1.29703 11.8177 1.68067 12.2041C2.05949 12.586 2.6361 13.0962 3.31153 13.3994C3.96008 13.6905 4.64564 13.7723 5.37305 13.4111C6.15124 13.0247 6.7741 12.2299 7.25293 11.2734C7.31534 11.1488 7.37417 11.0235 7.42961 10.8986C7.97844 9.66239 7.87492 8.25701 7.46246 6.96885L6.45986 3.83764C6.10502 2.72944 5.07475 1.97754 3.91114 1.97754C3.36767 1.97754 2.92676 1.53418 2.92676 0.988281C2.92698 0.442566 3.3678 0 3.91114 0H10.0205Z"/></symbol>
                <symbol id="sv2-i-play" viewBox="0 0 16 16"><path fill="currentColor" d="M5 3.3v9.4L12.4 8z"/></symbol>
                <symbol id="sv2-i-mail" viewBox="0 0 16 16"><g fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinejoin="round"><rect x="2" y="3.5" width="12" height="9" rx="1.6"/><path d="M2.6 4.6 8 8.6l5.4-4"/></g></symbol>
                <symbol id="sv2-i-cal" viewBox="0 0 16 16"><g fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round"><rect x="2.5" y="3.5" width="11" height="10" rx="1.6"/><path d="M2.5 6.8h11M5.5 2v2.5M10.5 2v2.5"/></g></symbol>
                <symbol id="sv2-i-flag" viewBox="0 0 16 16"><g fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinejoin="round" strokeLinecap="round"><path d="M3.5 14V2.5"/><path d="M3.5 3h8.2L10 5.6l1.7 2.6H3.5"/></g></symbol>
                <symbol id="sv2-i-check" viewBox="0 0 16 16"><path fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" d="m3.5 8.5 3 3 6-7"/></symbol>
                <symbol id="sv2-i-lock" viewBox="0 0 16 16"><g fill="none" stroke="currentColor" strokeWidth="1.5"><rect x="3.5" y="7" width="9" height="6.5" rx="1.6"/><path d="M5.5 7V5.5a2.5 2.5 0 0 1 5 0V7"/></g></symbol>
                <symbol id="sv2-i-chev" viewBox="0 0 16 16"><path fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" d="M10 3.5 5.5 8l4.5 4.5"/></symbol>
                <symbol id="sv2-i-term" viewBox="0 0 16 16"><g fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round"><rect x="1.5" y="2.5" width="13" height="11" rx="2.2"/><path d="m4.6 6 2 2-2 2M8.2 10.4h3.2"/></g></symbol>
                <symbol id="sv2-i-screen" viewBox="0 0 16 16"><g fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round"><rect x="1.5" y="2.5" width="13" height="8.6" rx="1.6"/><path d="M5.5 13.6h5"/></g></symbol>
                <symbol id="sv2-i-mic" viewBox="0 0 16 16"><g fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round"><rect x="5.8" y="1.6" width="4.4" height="8" rx="2.2"/><path d="M3.4 7.6a4.6 4.6 0 0 0 9.2 0M8 12.4v2"/></g></symbol>
                <symbol id="sv2-i-cursor" viewBox="0 0 16 16"><path fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinejoin="round" d="M3.5 2.5 12.5 8l-4 1 2.2 4.2-1.6.8-2.2-4.2-3.4 2.6z"/></symbol>
                <symbol id="sv2-i-folder" viewBox="0 0 16 16"><path fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinejoin="round" d="M2 4.6c0-.6.4-1.1 1-1.1h3.2l1.5 1.6H13c.6 0 1 .4 1 1V12c0 .6-.4 1-1 1H3c-.6 0-1-.4-1-1z"/></symbol>
                <symbol id="sv2-i-doc" viewBox="0 0 16 16"><g fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinejoin="round"><path d="M4 1.8h5.2L12.5 5v9.2H4z"/><path d="M9 1.8V5.2h3.4"/></g></symbol>
                <symbol id="sv2-i-spark" viewBox="0 0 16 16"><path fill="currentColor" d="M8 1.5c.4 2.9 1.6 4.1 4.5 4.5-2.9.4-4.1 1.6-4.5 4.5-.4-2.9-1.6-4.1-4.5-4.5 2.9-.4 4.1-1.6 4.5-4.5zM12.5 10c.2 1.4.8 2 2.2 2.2-1.4.2-2 .8-2.2 2.2-.2-1.4-.8-2-2.2-2.2 1.4-.2 2-.8 2.2-2.2z"/></symbol>
                <symbol id="sv2-i-sun" viewBox="0 0 16 16"><g fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round"><circle cx="8" cy="8" r="2.8"/><path d="M8 1.6v1.6M8 12.8v1.6M1.6 8h1.6M12.8 8h1.6M3.5 3.5l1.1 1.1M11.4 11.4l1.1 1.1M3.5 12.5l1.1-1.1M11.4 4.6l1.1-1.1"/></g></symbol>
                <symbol id="sv2-i-chat" viewBox="0 0 16 16"><path fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinejoin="round" d="M2.5 3.5h11v7.2H7.6L4.6 13v-2.3H2.5z"/></symbol>
                <symbol id="sv2-i-tasks" viewBox="0 0 16 16"><g fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round"><path d="m2.5 4.5 1.4 1.4 2.4-2.6M2.5 10.5l1.4 1.4 2.4-2.6M8.5 5h5M8.5 11h5"/></g></symbol>
                <symbol id="sv2-i-up" viewBox="0 0 16 16"><path fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" d="M8 13V3.5M4 7.5l4-4 4 4"/></symbol>
                <symbol id="sv2-i-wifi" viewBox="0 0 16 16"><g fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"><path d="M1.8 6.2a9 9 0 0 1 12.4 0M4 8.6a5.8 5.8 0 0 1 8 0M6.2 11a2.6 2.6 0 0 1 3.6 0"/></g></symbol>
                <symbol id="sv2-i-batt" viewBox="0 0 22 16"><g fill="none" stroke="currentColor" strokeWidth="1.2"><rect x="1" y="4" width="17" height="8.5" rx="2.4"/><path d="M20 6.8v3"/></g><rect x="2.8" y="5.8" width="12" height="4.9" rx="1.2" fill="currentColor"/></symbol>
                <symbol id="sv2-i-code" viewBox="0 0 16 16"><path fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" d="M5.5 4.5 2 8l3.5 3.5M10.5 4.5 14 8l-3.5 3.5M9 3 7 13"/></symbol>
                <symbol id="sv2-i-arrow" viewBox="0 0 16 20"><path fill="currentColor" strokeWidth="1.3" strokeLinejoin="round" d="M1.6 1.4v14.4l3.8-3.4 2.5 5.6 2.4-1-2.5-5.6 5.1-.2z"/></symbol>
            </defs>
        </svg>
    );
}

export function SetupIcon({
    id,
    className,
    style,
}: {
    id: SetupIconId;
    className?: string;
    style?: React.CSSProperties;
}) {
    return (
        <svg className={className} style={style} aria-hidden="true">
            <use href={`#sv2-${id}`} />
        </svg>
    );
}

/** Markup for imperatively built pieces (moving chips). */
export const iconMarkup = (id: SetupIconId) =>
    `<svg aria-hidden="true"><use href="#sv2-${id}"/></svg>`;
