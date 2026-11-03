import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { SetupIcon, iconMarkup } from "../SetupIcons";
import { Run, ease, lerp, rel } from "../motion";
import type { SceneProps } from "../types";
import { TIMINGS } from "../timings";
import { escapeHTML } from "./NoticesScene";

// Screen 4 (handoff §4, "Screen 4 · Send"): Isoform types the draft and
// stops short of Send; the person presses it. Loops every 10 s. Nothing is
// asked here.

type Cursor = HTMLDivElement & { _x?: number; _y?: number };

const T = TIMINGS.send;

export function SendScene({ onNext, reduced, still, enter }: SceneProps) {
    const { t } = useTranslation("onboarding");
    const ill = useRef<HTMLDivElement>(null);
    const ub = useRef<HTMLDivElement>(null);
    const mail = useRef<HTMLDivElement>(null);
    const mtext = useRef<HTMLDivElement>(null);
    const sendb = useRef<HTMLButtonElement>(null);
    const ib = useRef<HTMLDivElement>(null);
    const ciso = useRef<Cursor>(null);
    const cyou = useRef<Cursor>(null);

    useEffect(() => {
        const root = ill.current;
        const text = mtext.current;
        const button = sendb.current;
        const iso = ciso.current;
        const you = cyou.current;
        if (!root || !text || !button || !iso || !you) return;
        const draft = t("v2.send.draft");
        const sendLabel = escapeHTML(t("v2.send.sendButton"));
        const run = new Run(reduced);
        const bubbles = [ub.current, mail.current, ib.current];
        const place = (c: Cursor, x: number, y: number) => {
            c.style.transform = `translate(${x}px, ${y}px)`;
            c._x = x;
            c._y = y;
        };
        const moveTo = (c: Cursor, x: number, y: number, ms: number) => {
            const x0 = c._x ?? 0;
            const y0 = c._y ?? 0;
            return run.tween(ms, (p) => {
                const e = ease(p);
                place(c, lerp(x0, x, e), lerp(y0, y, e));
            });
        };
        const reset = () => {
            bubbles.forEach((el) => el?.classList.remove("show-x"));
            text.textContent = "";
            button.className = "sendb";
            button.innerHTML = sendLabel;
            iso.style.opacity = "0";
            you.style.opacity = "0";
            root.classList.remove("fadeall");
        };
        button.innerHTML = sendLabel;
        if (reduced || still) {
            bubbles.forEach((el) => el?.classList.add("show-x"));
            text.textContent = draft;
            button.classList.add("ready");
            return () => run.cancel();
        }
        run.go(async () => {
            for (;;) {
                reset();
                await run.wait(T.ask);
                ub.current?.classList.add("show-x");
                await run.wait(T.mail);
                mail.current?.classList.add("show-x");
                await run.wait(T.cursorIn);
                const tp = rel(text, root);
                place(iso, 330, 70);
                iso.style.opacity = "1";
                await moveTo(iso, tp.x + 150, tp.y + 22, T.toText);
                const caret = document.createElement("i");
                caret.className = "caret";
                let typed = "";
                for (const ch of draft) {
                    typed += ch;
                    text.textContent = typed;
                    text.appendChild(caret);
                    await run.wait(ch === " " ? T.typeSpace : T.typeChar);
                }
                caret.remove();
                const sp = rel(button, root);
                await run.wait(T.beforeSend);
                await moveTo(iso, sp.x + button.offsetWidth / 2 - 4, sp.y + button.offsetHeight / 2 - 2, T.toSend);
                await run.wait(T.atSend);
                await moveTo(iso, sp.x - 90, sp.y - 58, T.backAway);
                ib.current?.classList.add("show-x");
                button.classList.add("ready");
                await run.wait(T.readyIn);
                iso.style.opacity = "0";
                await run.wait(T.personIn);
                place(you, 380, 270);
                you.style.opacity = "1";
                await moveTo(you, sp.x + button.offsetWidth / 2 - 2, sp.y + button.offsetHeight / 2 - 1, T.personMove);
                await run.wait(T.beforePress);
                button.classList.add("press");
                await run.wait(T.press);
                button.classList.remove("press", "ready");
                button.classList.add("sent");
                button.innerHTML = iconMarkup("i-check") + escapeHTML(t("v2.send.sent"));
                await run.wait(T.sentHold);
                you.style.opacity = "0";
                await run.wait(T.rest);
                root.classList.add("fadeall");
                await run.wait(T.fade);
            }
        });
        return () => run.cancel();
    }, [reduced, still, t]);

    return (
        <section className={`scene ${enter}`} data-testid="setup-v2-send">
            <h2 className="t">{t("v2.send.title")}</h2>
            <p className="s">{t("v2.send.line")}</p>
            <div className="ill">
                <div className="send" ref={ill}>
                    <div className="ubub" ref={ub}>
                        {t("v2.send.ask")}
                    </div>
                    <div className="mail" ref={mail}>
                        <div className="mrow">
                            <span>{t("v2.send.to")}</span>
                            {t("v2.send.toName")}
                        </div>
                        <div className="mrow">
                            <span>{t("v2.send.subject")}</span>
                            {t("v2.send.subjectLine")}
                        </div>
                        <div className="mtext" ref={mtext} />
                        {/* Drawn by the story: its label changes to Sent. */}
                        <button className="sendb" ref={sendb} type="button" tabIndex={-1} aria-hidden="true" />
                    </div>
                    <div className="ibub" ref={ib}>
                        <SetupIcon id="mk" />
                        {t("v2.send.ready")}
                    </div>
                    <div className="cur iso" ref={ciso}>
                        <SetupIcon id="i-arrow" />
                        <span className="cl">{t("v2.send.cursor")}</span>
                    </div>
                    <div className="cur you" ref={cyou}>
                        <SetupIcon id="i-arrow" />
                    </div>
                </div>
            </div>
            <div className="act">
                <button
                    className="btn primary"
                    type="button"
                    data-testid="setup-v2-continue"
                    onClick={onNext}
                >
                    {t("v2.continue")}
                </button>
            </div>
        </section>
    );
}
