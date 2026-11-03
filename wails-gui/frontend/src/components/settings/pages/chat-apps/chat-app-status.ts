/** What a chat-app setup reports to the Chat apps page after each status check. */
export interface ChatAppStatus {
    /** The app has been set up on this computer. */
    configured: boolean;
    /** Set up and its receiver is running. */
    online: boolean;
}
