import { type IconProps } from './CollapseIcon';

/** A topic (RC 1): a tag, for topics pinned to the sidebar. */
export function TopicIcon({ className, color = 'currentColor', size = 24 }: IconProps) {
    return (
        <svg
            width={size}
            height={size}
            viewBox="0 0 20 20"
            fill="none"
            strokeWidth={1.5}
            xmlns="http://www.w3.org/2000/svg"
            className={className}
        >
            <path
                d="M3.33 4.58V9.3c0 .44.18.87.49 1.18l5.86 5.86a1.67 1.67 0 0 0 2.36 0l4.3-4.3a1.67 1.67 0 0 0 0-2.36L10.48 3.82a1.67 1.67 0 0 0-1.18-.49H4.58c-.69 0-1.25.56-1.25 1.25Z"
                stroke={color}
                strokeLinecap="round"
                strokeLinejoin="round"
            />
            <circle cx="6.67" cy="6.67" r="1.04" stroke={color} />
        </svg>
    );
}
