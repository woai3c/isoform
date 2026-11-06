import { createContext } from 'react';

/** Whether the retained page around a pane is the one on screen. Null outside
 *  a RetainedPage. A pane opens on its newest item when this goes from false
 *  to true: the page was opened or shown again. Window focus, the screen
 *  unlocking or the display waking never change it, so the reader keeps
 *  their place. */
export const RetainedPageActiveContext = createContext<boolean | null>(null);
