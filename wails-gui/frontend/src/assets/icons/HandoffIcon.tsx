import { type IconProps } from './CollapseIcon';

export function HandoffIcon({ className, color = 'currentColor', size = 20 }: IconProps) {
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
                d="M9.16667 9.99935H10.8333C11.2754 9.99935 11.6993 9.82375 12.0118 9.51119C12.3244 9.19863 12.5 8.77471 12.5 8.33268C12.5 7.89065 12.3244 7.46673 12.0118 7.15417C11.6993 6.84161 11.2754 6.66602 10.8333 6.66602H8.33333C7.83333 6.66602 7.41667 6.83268 7.16667 7.16602L2.5 11.666"
                stroke={color}
                strokeLinecap="round"
                strokeLinejoin="round"
            />
            <path
                d="M5.83337 14.9993L7.16671 13.8326C7.41671 13.4993 7.83337 13.3326 8.33337 13.3326H11.6667C12.5834 13.3326 13.4167 12.9993 14 12.3326L17.8334 8.66592C18.1549 8.36202 18.3426 7.94283 18.3551 7.50056C18.3676 7.05828 18.2039 6.62916 17.9 6.30758C17.5961 5.98601 17.177 5.79833 16.7347 5.78582C16.2924 5.77332 15.8633 5.93702 15.5417 6.24092L12.0417 9.49092"
                stroke={color}
                strokeLinecap="round"
                strokeLinejoin="round"
            />
            <path
                d="M1.66663 10.834L6.66663 15.834"
                stroke={color}
                strokeLinecap="round"
                strokeLinejoin="round"
            />
        </svg>
    );
}
