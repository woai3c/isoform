import { type IconProps } from './CollapseIcon';

export function ExpandIcon({ className, color = 'currentColor', variant = 'stroke', size = 16 }: IconProps) {
    if (variant === 'fill') {
        return (
            <svg width={size} height={size} viewBox="0 0 20 20" fill="none" xmlns="http://www.w3.org/2000/svg" className={className}>
                {/* Left panel - filled */}
                <path
                    d="M3.33366 15.0002C3.33366 15.4422 3.50925 15.8662 3.82181 16.1787C4.13437 16.4913 4.5583 16.6669 5.00032 16.6669H12.5L12.5 3.33354L5.00032 3.33354C4.5583 3.33354 4.13437 3.50913 3.82181 3.82169C3.50925 4.13425 3.33366 4.55818 3.33366 5.0002L3.33366 15.0002Z"
                    fill="currentColor"
                    stroke={color}
                    strokeWidth="1.25"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                />
                {/* Right panel - outline only */}
                <path
                    d="M12.5 3.33354L15.0003 3.33354C15.4424 3.33354 15.8663 3.50913 16.1788 3.82169C16.4914 4.13425 16.667 4.55818 16.667 5.0002L16.667 15.0002C16.667 15.4422 16.4914 15.8662 16.1788 16.1787C15.8663 16.4913 15.4424 16.6669 15.0003 16.6669L12.5 16.6669L12.5 3.33354Z"
                    stroke={color}
                    strokeWidth="1.25"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                />
                {/* Divider line */}
                <path d="M12.5 16.6669L12.5 3.33354" stroke={color} strokeWidth="1.25" strokeLinecap="round" strokeLinejoin="round" />
                {/* Arrow - contrasting color */}
                <path d="M7.50033 11.6669L9.16699 10.0002L7.50033 8.33354" stroke="var(--panel, white)" strokeWidth="1.25" strokeLinecap="round" strokeLinejoin="round" />
            </svg>
        );
    }

    return (
        <svg width={size} height={size} viewBox="0 0 20 20" fill="none" xmlns="http://www.w3.org/2000/svg" className={className}>
            <path
                d="M16.667 15.0002C16.667 15.4422 16.4914 15.8662 16.1788 16.1787C15.8663 16.4913 15.4424 16.6669 15.0003 16.6669L5.00033 16.6669C4.5583 16.6669 4.13438 16.4913 3.82181 16.1787C3.50925 15.8662 3.33366 15.4422 3.33366 15.0002L3.33366 5.0002C3.33366 4.55818 3.50925 4.13425 3.82181 3.82169C4.13437 3.50913 4.5583 3.33354 5.00032 3.33354L15.0003 3.33354C15.4424 3.33354 15.8663 3.50913 16.1788 3.82169C16.4914 4.13425 16.667 4.55818 16.667 5.0002L16.667 15.0002Z"
                stroke={color}
                strokeWidth="1.25"
                strokeLinecap="round"
                strokeLinejoin="round"
            />
            <path d="M12.5 16.6669L12.5 3.33354" stroke={color} strokeWidth="1.25" strokeLinecap="round" strokeLinejoin="round" />
            <path d="M7.50033 11.6669L9.16699 10.0002L7.50033 8.33354" stroke={color} strokeWidth="1.25" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
    );
}
