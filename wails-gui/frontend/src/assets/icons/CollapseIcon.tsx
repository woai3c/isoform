export interface IconProps {
    className?: string;
    color?: string;
    variant?: 'stroke' | 'fill';
    size?: number;
}

export function CollapseIcon({ className, color = 'currentColor', variant = 'stroke', size = 16 }: IconProps) {
    if (variant === 'fill') {
        return (
            <svg width={size} height={size} viewBox="0 0 20 20" fill="none" xmlns="http://www.w3.org/2000/svg" className={className}>
                {/* Left panel - outline only */}
                <path
                    d="M3.33301 5.00016C3.33301 4.55814 3.5086 4.13421 3.82116 3.82165C4.13372 3.50909 4.55765 3.3335 4.99967 3.3335H7.5V16.6668H4.99967C4.55765 16.6668 4.13372 16.4912 3.82116 16.1787C3.5086 15.8661 3.33301 15.4422 3.33301 15.0002V5.00016Z"
                    stroke={color}
                    strokeWidth="1.25"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                />
                {/* Right panel - filled */}
                <path
                    d="M7.5 3.3335H14.9997C15.4417 3.3335 15.8656 3.50909 16.1782 3.82165C16.4907 4.13421 16.6663 4.55814 16.6663 5.00016V15.0002C16.6663 15.4422 16.4907 15.8661 16.1782 16.1787C15.8656 16.4912 15.4417 16.6668 14.9997 16.6668H7.5V3.3335Z"
                    fill="currentColor"
                    stroke={color}
                    strokeWidth="1.25"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                />
                {/* Divider line */}
                <path d="M7.5 3.3335V16.6668" stroke={color} strokeWidth="1.25" strokeLinecap="round" strokeLinejoin="round" />
                {/* Arrow - contrasting color */}
                <path d="M12.4997 8.3335L10.833 10.0002L12.4997 11.6668" stroke="var(--panel, white)" strokeWidth="1.25" strokeLinecap="round" strokeLinejoin="round" />
            </svg>
        );
    }

    return (
        <svg width={size} height={size} viewBox="0 0 20 20" fill="none" xmlns="http://www.w3.org/2000/svg" className={className}>
            <path
                d="M3.33301 5.00016C3.33301 4.55814 3.5086 4.13421 3.82116 3.82165C4.13372 3.50909 4.55765 3.3335 4.99967 3.3335H14.9997C15.4417 3.3335 15.8656 3.50909 16.1782 3.82165C16.4907 4.13421 16.6663 4.55814 16.6663 5.00016V15.0002C16.6663 15.4422 16.4907 15.8661 16.1782 16.1787C15.8656 16.4912 15.4417 16.6668 14.9997 16.6668H4.99967C4.55765 16.6668 4.13372 16.4912 3.82116 16.1787C3.5086 15.8661 3.33301 15.4422 3.33301 15.0002V5.00016Z"
                stroke={color}
                strokeWidth="1.25"
                strokeLinecap="round"
                strokeLinejoin="round"
            />
            <path d="M7.5 3.3335V16.6668" stroke={color} strokeWidth="1.25" strokeLinecap="round" strokeLinejoin="round" />
            <path d="M12.4997 8.3335L10.833 10.0002L12.4997 11.6668" stroke={color} strokeWidth="1.25" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
    );
}
