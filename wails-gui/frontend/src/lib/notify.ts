import { toast, type ExternalToast } from 'sonner';
import { NotifyUser } from '../../wailsjs/go/main/App';

type NotifyType = 'success' | 'error' | 'info';

function showToast(
    title: string,
    body: string,
    type: NotifyType,
    toastOpts?: ExternalToast,
    action?: { label: string; onClick: () => void },
) {
    const opts: ExternalToast = {
        description: body,
        duration: type === 'info' ? 5000 : 8000,
        ...(action ? { action } : {}),
        ...toastOpts,
    };

    switch (type) {
        case 'success':
            toast.success(title, opts);
            break;
        case 'error':
            toast.error(title, opts);
            break;
        default:
            toast(title, opts);
            break;
    }
}

/**
 * Always show an in-app sonner toast (never a desktop notification).
 */
export function notifyToast(
    title: string,
    body: string,
    type: NotifyType = 'info',
    toastOpts?: ExternalToast,
    action?: { label: string; onClick: () => void },
) {
    showToast(title, body, type, toastOpts, action);
}

/**
 * Deliver a notification to the user. Prefers OS desktop notifications when
 * the permission has been granted; falls back to an in-app sonner toast
 * otherwise.
 *
 * Pass extra sonner options (e.g. `{ id: toastId }`) via `toastOpts` to
 * integrate with progressive toast patterns like toast.loading -> success.
 * When the desktop path is taken a pending loading toast is dismissed.
 */
export function notifyImportant(
    title: string,
    body: string,
    type: NotifyType = 'info',
    toastOpts?: ExternalToast,
    action?: { label: string; onClick: () => void },
    clickData?: string,
) {
    // Only promote "good news" to desktop notifications; errors and info
    // stay as in-app toasts so the OS notification center isn't noisy.
    if (type !== 'success') {
        showToast(title, body, type, toastOpts, action);
        return;
    }

    NotifyUser(title, body, clickData ?? '')
        .then((result) => {
            if (result === 'toast') {
                showToast(title, body, type, toastOpts, action);
            } else if (toastOpts?.id) {
                // Desktop notification sent — dismiss any lingering loading toast.
                toast.dismiss(toastOpts.id);
            }
        })
        .catch(() => {
            showToast(title, body, type, toastOpts, action);
        });
}
