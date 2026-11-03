import { type IconProps } from './CollapseIcon';

// A clean, thin-stroke sun for the "Today" (Morning Brief) entry. Matches the
// custom sidebar icon family (ChatIcon / AutomationIcon): 24 viewBox,
// currentColor stroke, round caps — so it inherits the row's ink color and
// never introduces a separate accent color.
export function TodayIcon({ className, color = 'currentColor', size = 24 }: IconProps) {
    return (
        <svg
            width={size}
            height={size}
            viewBox="0 0 24 24"
            fill="none"
            strokeWidth={1.75}
            xmlns="http://www.w3.org/2000/svg"
            className={className}
        >
            <circle cx="12" cy="12" r="4" stroke={color} />
            <path
                d="M12 3.5V5M12 19V20.5M20.5 12H19M5 12H3.5M18.01 5.99L16.95 7.05M7.05 16.95L5.99 18.01M18.01 18.01L16.95 16.95M7.05 7.05L5.99 5.99"
                stroke={color}
                strokeLinecap="round"
                strokeLinejoin="round"
            />
        </svg>
    );
}
