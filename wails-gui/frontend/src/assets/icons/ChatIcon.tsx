import { type IconProps } from './CollapseIcon';

export function ChatIcon({ className, color = 'currentColor', size = 24 }: IconProps) {
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
            <path
                d="M12.007 19.9797C10.5157 19.9827 9.04318 19.6476 7.7 18.9997L3 19.9997L4.3 16.0997C1.976 12.6627 2.874 8.22772 6.4 5.72572C9.926 3.22472 14.99 3.42972 18.245 6.20572C20.237 7.90572 21.175 10.2457 20.992 12.5457"
                stroke={color}
                strokeLinecap="round"
                strokeLinejoin="round"
            />
            <path d="M16 19H22" stroke={color} strokeLinecap="round" strokeLinejoin="round" />
            <path d="M19 16V22" stroke={color} strokeLinecap="round" strokeLinejoin="round" />
        </svg>
    );
}
