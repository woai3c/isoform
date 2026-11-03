import { useEffect, useRef } from "react";
import { RecordFirstBriefSeen } from "../../wailsjs/go/main/App";
import { productWindowActive } from "./useProductTelemetry";

/** Only the fact that a nonempty brief entered the focused viewport crosses
 * the bridge. The backend persists the once-per-install marker even when
 * sharing is off, and separately gates whether it can send the event. */
export function useFirstBriefSeen(hasBrief: boolean) {
    const elementRef = useRef<HTMLElement>(null);
    useEffect(() => {
        const element = elementRef.current;
        if (!hasBrief || !element) return;
        let inView = false;
        let recorded = false;
        const record = () => {
            if (!recorded && inView && productWindowActive()) {
                recorded = true;
                void RecordFirstBriefSeen().catch(() => {});
            }
        };
        const observer = new IntersectionObserver((entries) => {
            inView = entries.some((entry) => entry.isIntersecting);
            record();
        });
        observer.observe(element);
        window.addEventListener("focus", record);
        document.addEventListener("visibilitychange", record);
        return () => {
            observer.disconnect();
            window.removeEventListener("focus", record);
            document.removeEventListener("visibilitychange", record);
        };
    }, [hasBrief]);
    return elementRef;
}
