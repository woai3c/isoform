// Yansu UI primitives — prop types as documentation, from
// desktop/wails-gui/frontend/src/components/ui/*.tsx. No bundle ships with
// this system: the components are React + Radix + Tailwind v4 in the app.
import type * as React from 'react';

export interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: 'default' | 'secondary' | 'connect' | 'ghost' | 'outline' | 'destructive' | 'link' | 'quiet';
  size?: 'default' | 'sm' | 'lg' | 'icon';
  /** Render the styles onto the child element (Radix Slot). */
  asChild?: boolean;
}
export declare const Button: React.ForwardRefExoticComponent<ButtonProps & React.RefAttributes<HTMLButtonElement>>;

export type CardProps = React.HTMLAttributes<HTMLDivElement>;
export declare const Card: React.FC<CardProps>;
export declare const CardHeader: React.FC<CardProps>;
export declare const CardTitle: React.FC<React.HTMLAttributes<HTMLHeadingElement>>;
export declare const CardDescription: React.FC<React.HTMLAttributes<HTMLParagraphElement>>;
export declare const CardContent: React.FC<CardProps>;

export interface CheckboxProps {
  checked?: boolean | 'indeterminate';
  defaultChecked?: boolean;
  onCheckedChange?: (checked: boolean | 'indeterminate') => void;
  disabled?: boolean;
  className?: string;
}
export declare const Checkbox: React.FC<CheckboxProps>;

export interface SwitchProps {
  checked?: boolean;
  defaultChecked?: boolean;
  onCheckedChange?: (checked: boolean) => void;
  disabled?: boolean;
  className?: string;
}
export declare const Switch: React.FC<SwitchProps>;

export type TextareaProps = React.TextareaHTMLAttributes<HTMLTextAreaElement>;
export declare const Textarea: React.FC<TextareaProps>;

export interface AvatarProps extends React.HTMLAttributes<HTMLDivElement> {
  src?: string | null;
  /** Default "User avatar". */
  alt?: string;
  /** A name; its first two initials show when there is no image. */
  fallback?: string;
  size?: 'xs' | 'sm' | 'md' | 'lg';
}
export declare function Avatar(props: AvatarProps): React.ReactElement;

export interface DialogProps { open?: boolean; defaultOpen?: boolean; onOpenChange?: (open: boolean) => void; children?: React.ReactNode; }
export declare const Dialog: React.FC<DialogProps>;
export declare const DialogTrigger: React.FC<{ asChild?: boolean; children?: React.ReactNode }>;
export declare const DialogContent: React.FC<React.HTMLAttributes<HTMLDivElement>>;
export declare const DialogHeader: React.FC<React.HTMLAttributes<HTMLDivElement>>;
export declare const DialogTitle: React.FC<React.HTMLAttributes<HTMLHeadingElement>>;
export declare const DialogDescription: React.FC<React.HTMLAttributes<HTMLParagraphElement>>;
export declare const DialogClose: React.FC<{ asChild?: boolean; children?: React.ReactNode }>;
export declare const DialogCloseButton: React.FC<{ className?: string }>;
export declare const DialogOverlay: React.FC<{ className?: string }>;
export declare const DialogPortal: React.FC<{ children?: React.ReactNode }>;

export type SheetProps = DialogProps;
export declare const Sheet: React.FC<SheetProps>;
export declare const SheetTrigger: React.FC<{ asChild?: boolean; children?: React.ReactNode }>;
export declare const SheetContent: React.FC<React.HTMLAttributes<HTMLDivElement>>;
export declare const SheetHeader: React.FC<React.HTMLAttributes<HTMLDivElement>>;
export declare const SheetTitle: React.FC<React.HTMLAttributes<HTMLHeadingElement>>;
export declare const SheetDescription: React.FC<React.HTMLAttributes<HTMLParagraphElement>>;
export declare const SheetClose: React.FC<{ asChild?: boolean; children?: React.ReactNode }>;
export declare const SheetCloseButton: React.FC<{ className?: string }>;

export interface DropdownMenuItemProps extends React.HTMLAttributes<HTMLDivElement> { destructive?: boolean; disabled?: boolean; onSelect?: (e: Event) => void; }
export declare const DropdownMenu: React.FC<DialogProps>;
export declare const DropdownMenuTrigger: React.FC<{ asChild?: boolean; children?: React.ReactNode }>;
export declare const DropdownMenuContent: React.FC<React.HTMLAttributes<HTMLDivElement> & { align?: 'start' | 'center' | 'end'; sideOffset?: number }>;
export declare const DropdownMenuItem: React.FC<DropdownMenuItemProps>;
export declare const DropdownMenuLabel: React.FC<React.HTMLAttributes<HTMLDivElement>>;
export declare const DropdownMenuSeparator: React.FC<{ className?: string }>;
export declare const DropdownMenuSub: React.FC<{ children?: React.ReactNode }>;
export declare const DropdownMenuSubTrigger: React.FC<React.HTMLAttributes<HTMLDivElement>>;
export declare const DropdownMenuSubContent: React.FC<React.HTMLAttributes<HTMLDivElement>>;

export interface ContextMenuItemProps extends React.HTMLAttributes<HTMLDivElement> { destructive?: boolean; disabled?: boolean; onSelect?: (e: Event) => void; }
export declare const ContextMenu: React.FC<{ children?: React.ReactNode; onOpenChange?: (open: boolean) => void }>;
export declare const ContextMenuTrigger: React.FC<{ asChild?: boolean; children?: React.ReactNode }>;
export declare const ContextMenuContent: React.FC<React.HTMLAttributes<HTMLDivElement>>;
export declare const ContextMenuItem: React.FC<ContextMenuItemProps>;
export declare const ContextMenuSeparator: React.FC<{ className?: string }>;

export interface SelectProps { value?: string; defaultValue?: string; onValueChange?: (value: string) => void; disabled?: boolean; children?: React.ReactNode; }
export declare const Select: React.FC<SelectProps>;
export declare const SelectGroup: React.FC<{ children?: React.ReactNode }>;
export declare const SelectValue: React.FC<{ placeholder?: string }>;
export declare const SelectTrigger: React.FC<React.ButtonHTMLAttributes<HTMLButtonElement>>;
export declare const SelectContent: React.FC<React.HTMLAttributes<HTMLDivElement> & { position?: 'item-aligned' | 'popper' }>;
export declare const SelectLabel: React.FC<React.HTMLAttributes<HTMLDivElement>>;
export declare const SelectItem: React.FC<React.HTMLAttributes<HTMLDivElement> & { value: string; disabled?: boolean }>;
export declare const SelectSeparator: React.FC<{ className?: string }>;

export declare const Popover: React.FC<DialogProps>;
export declare const PopoverTrigger: React.FC<{ asChild?: boolean; children?: React.ReactNode }>;
export declare const PopoverContent: React.FC<React.HTMLAttributes<HTMLDivElement> & { align?: 'start' | 'center' | 'end'; sideOffset?: number }>;
export declare const PopoverAnchor: React.FC<{ asChild?: boolean; children?: React.ReactNode }>;

export interface ComboboxOption { value: string; label: string; }
export interface ComboboxProps {
  options: ComboboxOption[];
  value?: string;
  onValueChange: (value: string) => void;
  placeholder?: string;
  /** Allow entering a value that is not in `options`. */
  allowCustom?: boolean;
  renderOption?: (option: ComboboxOption) => React.ReactNode;
  className?: string;
}
export declare function Combobox(props: ComboboxProps): React.ReactElement;

export declare const Command: React.FC<React.HTMLAttributes<HTMLDivElement>>;
export declare const CommandDialog: React.FC<DialogProps>;
export declare const CommandInput: React.FC<React.InputHTMLAttributes<HTMLInputElement>>;
export declare const CommandList: React.FC<React.HTMLAttributes<HTMLDivElement>>;
export declare const CommandEmpty: React.FC<{ children?: React.ReactNode }>;
export declare const CommandGroup: React.FC<React.HTMLAttributes<HTMLDivElement> & { heading?: React.ReactNode }>;
export declare const CommandSeparator: React.FC<{ className?: string }>;
export declare const CommandItem: React.FC<React.HTMLAttributes<HTMLDivElement> & { value?: string; onSelect?: (value: string) => void; disabled?: boolean }>;

/** react-day-picker's DayPicker props. */
export type CalendarProps = Record<string, unknown> & { mode?: 'single' | 'range' | 'multiple'; className?: string };
export declare const Calendar: React.FC<CalendarProps>;

export declare const ScrollArea: React.FC<React.HTMLAttributes<HTMLDivElement>>;
export declare const ScrollBar: React.FC<{ orientation?: 'vertical' | 'horizontal'; className?: string }>;

export declare const Collapsible: React.FC<{ open?: boolean; defaultOpen?: boolean; onOpenChange?: (open: boolean) => void; children?: React.ReactNode }>;
export declare const CollapsibleTrigger: React.FC<{ asChild?: boolean; children?: React.ReactNode }>;
export declare const CollapsibleContent: React.FC<React.HTMLAttributes<HTMLDivElement>>;

export interface CollapsePanelProps { open: boolean; children: React.ReactNode; innerClassName?: string; }
export declare function CollapsePanel(props: CollapsePanelProps): React.ReactElement;
