import { type IconProps } from './CollapseIcon';

export function ChatBubbleIcon({ className, color = 'currentColor', size = 24 }: IconProps) {
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
                d="M2.5 16.6673L3.58333 13.4173C2.64704 12.0325 2.30833 10.3926 2.63018 8.80255C2.95204 7.21246 3.91255 5.78036 5.33314 4.77253C6.75373 3.7647 8.53772 3.24972 10.3534 3.32334C12.1691 3.39695 13.8929 4.05416 15.2044 5.17277C16.5159 6.29137 17.3257 7.79525 17.4834 9.40479C17.6411 11.0143 17.1358 12.62 16.0616 13.9232C14.9873 15.2265 13.4172 16.1386 11.6432 16.4901C9.86911 16.8415 8.01184 16.6084 6.41667 15.834L2.5 16.6673Z"
                stroke={color}
                strokeLinecap="round"
                strokeLinejoin="round"
            />
        </svg>
    );
}
