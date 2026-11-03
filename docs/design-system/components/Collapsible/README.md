# Collapsible

A thin re-export of Radix Collapsible for show/hide sections, such as the "Technical details" layer that holds IDs, enums and raw values.

**Parts** `Collapsible`, `CollapsibleTrigger`, `CollapsibleContent`.

**Consumer provides** the trigger label (a noun: `Technical details`) and the content. Per the content rules, exact implementation values live here, never in the scan path.

Source: `desktop/wails-gui/frontend/src/components/ui/collapsible.tsx`. Static rendition (click to toggle; animation borrowed from CollapsePanel).
