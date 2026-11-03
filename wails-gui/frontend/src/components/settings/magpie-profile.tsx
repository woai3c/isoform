import { useCallback, useEffect, useState } from "react";
import { ArrowDown, ArrowUp, Trash2 } from "lucide-react";
import { useTranslation } from "react-i18next";
import { GetMagpieProfile, SetBackgroundUsesChoice, SetMagpieProfile, type MagpieProfileMember, type MagpieProfileView } from "@/lib/wails-shim-app";
import { EventsOn } from "../../../wailsjs/runtime/runtime";
import { Button } from "../ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../ui/select";
import { Switch } from "../ui/switch";
import { SettingsCard, SettingsRow } from "./settings-primitives";

const efforts = ["low", "medium", "high", "xhigh", "max"] as const;
const providerOf = (model: string) => model.split("/", 1)[0];

export function MagpieProfileCard() {
    const { t } = useTranslation("settings");
    const providerName = (provider: string) => {
        switch (provider) {
            case "claude": return t("assistantChoice.agents.claudeCode");
            case "codex": return t("assistantChoice.agents.codex");
            case "gemini": return t("magpieProfile.gemini");
            default: return provider;
        }
    };
    const [view, setView] = useState<MagpieProfileView | null>(null);
    const [busy, setBusy] = useState(false);
    const [failed, setFailed] = useState(false);
    const load = useCallback(() => {
        GetMagpieProfile().then(setView).catch(() => setFailed(true));
    }, []);
    useEffect(() => {
        load();
        return EventsOn("model-accounts:updated", load);
    }, [load]);

    const save = (members: MagpieProfileMember[]) => {
        setBusy(true);
        setFailed(false);
        SetMagpieProfile(members).then(setView).catch(() => { setFailed(true); load(); }).finally(() => setBusy(false));
    };
    const change = (index: number, member: MagpieProfileMember) => {
        if (view) save(view.members.map((item, i) => i === index ? member : item));
    };
    const move = (index: number, by: number) => {
        if (!view) return;
        const members = [...view.members];
        [members[index], members[index + by]] = [members[index + by], members[index]];
        save(members);
    };
    const addable = view?.models.filter((model) => !view.members.some((member) => providerOf(member.model) === providerOf(model))) ?? [];
    const providers = [...new Set(addable.map(providerOf))];

    return (
        <SettingsCard title={t("magpieProfile.title")} description={t("magpieProfile.description")}>
            {!view && !failed && <p role="status" className="settings-row-desc">{t("assistantChoice.loading")}</p>}
            {view && <div className="flex flex-col gap-3">
                {view.members.map((member, index) => {
                    const provider = providerOf(member.model);
                    const name = providerName(provider);
                    const models = [...new Set([member.model, ...view.models.filter((model) => providerOf(model) === provider)])];
                    return <div key={provider} className="rounded-lg border border-border px-3 py-2.5">
                        <div className="flex items-center justify-between gap-2">
                            <span className="text-[13px] font-medium text-ink">{name}</span>
                            <div className="flex gap-1">
                                <Button variant="outline" size="sm" disabled={busy || index === 0} aria-label={t("magpieProfile.moveUp", { name })} onClick={() => move(index, -1)}><ArrowUp className="size-3.5" /></Button>
                                <Button variant="outline" size="sm" disabled={busy || index === view.members.length - 1} aria-label={t("magpieProfile.moveDown", { name })} onClick={() => move(index, 1)}><ArrowDown className="size-3.5" /></Button>
                                <Button variant="outline" size="sm" disabled={busy || view.members.length === 1} aria-label={t("magpieProfile.remove", { name })} onClick={() => save(view.members.filter((_, i) => i !== index))}><Trash2 className="size-3.5" /></Button>
                            </div>
                        </div>
                        <SettingsRow title={t("assistantChoice.model")} control={<Select value={member.model} disabled={busy} onValueChange={(model) => change(index, { ...member, model })}>
                            <SelectTrigger className="w-[240px] text-xs" aria-label={t("assistantChoice.modelFor", { agent: name })}><SelectValue /></SelectTrigger>
                            <SelectContent>{models.map((model) => <SelectItem key={model} value={model}>{model}</SelectItem>)}</SelectContent>
                        </Select>} />
                        <SettingsRow title={t("assistantChoice.effort")} control={<div className="settings-segment" role="radiogroup" aria-label={t("assistantChoice.effortFor", { agent: name })}>
                            {efforts.map((effort) => <button key={effort} type="button" role="radio" aria-checked={member.effort === effort} data-active={member.effort === effort} disabled={busy} onClick={() => change(index, { ...member, effort })}>{t(`assistantChoice.efforts.${effort}`)}</button>)}
                        </div>} />
                    </div>;
                })}
                {providers.length > 0 && <Select value="" disabled={busy} onValueChange={(provider) => {
                    const model = addable.find((item) => providerOf(item) === provider);
                    if (model) save([...view.members, { model, effort: "high" }]);
                }}>
                    <SelectTrigger className="w-[240px] text-xs" aria-label={t("magpieProfile.add")}><SelectValue placeholder={t("magpieProfile.add")} /></SelectTrigger>
                    <SelectContent>{providers.map((provider) => <SelectItem key={provider} value={provider}>{providerName(provider)}</SelectItem>)}</SelectContent>
                </Select>}
                <SettingsRow title={t("assistantChoice.background.title")} description={t("magpieProfile.background")} control={<Switch checked={view.backgroundUsesChoice} disabled={busy} aria-label={t("assistantChoice.background.title")} onCheckedChange={(on) => {
                    setBusy(true);
                    SetBackgroundUsesChoice(on).then(() => setView({ ...view, backgroundUsesChoice: on })).catch(() => setFailed(true)).finally(() => setBusy(false));
                }} />} />
                <p className="text-xs text-muted">{t("magpieProfile.scope")}</p>
            </div>}
            {failed && <p role="alert" className="text-xs text-muted">{t("assistantChoice.saveFailed")}</p>}
        </SettingsCard>
    );
}
