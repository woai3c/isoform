import * as React from "react";
import { DayPicker } from "react-day-picker";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { cn } from "@/lib/utils";

export type CalendarProps = React.ComponentProps<typeof DayPicker>;

function Calendar({
    className,
    classNames,
    showOutsideDays = true,
    components,
    ...props
}: CalendarProps) {
    return (
        <DayPicker
            showOutsideDays={showOutsideDays}
            className={cn("p-3", className)}
            classNames={{
                months: "relative flex flex-col sm:flex-row gap-2",
                month: "flex flex-col gap-4",
                month_caption: "flex justify-center h-7 items-center",
                caption_label: "text-[13px] font-medium text-ink",
                nav: "absolute inset-x-1 top-3 flex items-center justify-between z-10 h-7",
                button_previous: cn(
                    "h-7 w-7 bg-transparent p-0 text-muted hover:text-ink",
                    "relative -top-[3px]",
                    "inline-flex items-center justify-center rounded-md hover:bg-border/50 transition-colors",
                ),
                button_next: cn(
                    "h-7 w-7 bg-transparent p-0 text-muted hover:text-ink",
                    "relative -top-[3px]",
                    "inline-flex items-center justify-center rounded-md hover:bg-border/50 transition-colors",
                ),
                month_grid: "w-full border-collapse",
                weekdays: "flex",
                weekday: "text-muted/70 rounded-md w-9 text-[10px] font-normal",
                week: "flex w-full mt-0.5",
                day: cn(
                    "relative p-0 text-center text-[12px] focus-within:relative focus-within:z-20",
                ),
                day_button: cn(
                    "relative h-8 w-9 p-0 text-[12px] font-normal",
                    "inline-flex items-center justify-center rounded-md transition-colors",
                    "hover:bg-border/50 hover:text-ink",
                    "disabled:cursor-not-allowed disabled:hover:bg-transparent disabled:hover:text-muted/30",
                    "focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent/40",
                ),
                range_end: "day-range-end",
                selected:
                    "[&>button]:bg-accent [&>button]:text-panel [&>button:hover]:bg-accent [&>button:hover]:text-panel",
                today: "text-ink [&>button]:ring-1 [&>button]:ring-inset [&>button]:ring-border/80 [&[data-selected]>button]:ring-0",
                outside: "day-outside text-muted/40",
                disabled: "text-muted/30 cursor-not-allowed",
                hidden: "invisible",
                ...classNames,
            }}
            components={{
                Chevron: ({ orientation }) =>
                    orientation === "left" ? (
                        <ChevronLeft className="h-4 w-4" />
                    ) : (
                        <ChevronRight className="h-4 w-4" />
                    ),
                ...components,
            }}
            {...props}
        />
    );
}
Calendar.displayName = "Calendar";

export { Calendar };
