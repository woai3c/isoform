export function fitChipLabel(name: string, available: number, measure: (text: string) => number, _initial?: string): string {
    const label = name.trim();
    return label && !/^\p{Script=Latin}$/u.test(label) && Number.isFinite(available) && measure(label) <= available ? label : '';
}
