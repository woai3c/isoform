import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import type { PrincipalDTO } from '@/lib/wails-shim-app';
const mocks=vi.hoisted(()=>({update:vi.fn(),bind:vi.fn(),unbind:vi.fn(),promote:vi.fn(),error:vi.fn()}));
vi.mock('@/lib/wails-shim-app',()=>({UpdatePrincipal:mocks.update,BindAppHandle:mocks.bind,UnbindAppHandle:mocks.unbind,CreateContact:vi.fn(),EnsureSelfProfile:vi.fn(),PromoteAnonymousToContact:mocks.promote}));
vi.mock('sonner',()=>({toast:{error:mocks.error}}));
vi.mock('@/hooks/useUserProfile',()=>({refreshUserProfile:vi.fn()}));
import { ContactEditorDialog, syncHandles } from './contact-editor';

it('hides imported source identifiers and preserves their bindings on save',async()=>{
    await i18n.changeLanguage('en');
    const person={id:'seen',kind:'contact',displayName:'Maya Chen',relationship:'',aliases:[],appHandles:[
        {id:1,app:'contacts',handleId:'synthetic-native-7F3A9C',displayHandle:'synthetic-native-7F3A9C'},
        {id:2,app:'wechat',handleId:'maya-wechat',displayHandle:'MayaC'},
    ]} as unknown as PrincipalDTO;
    mocks.update.mockResolvedValue(person);
    const saved=vi.fn();
    render(<ContactEditorDialog mode="edit" initial={person} open onOpenChange={vi.fn()} onSaved={saved}/>);
    expect(document.body.innerHTML).not.toContain('synthetic-native-7F3A9C');
    expect(screen.getByText('MayaC')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button',{name:i18n.t('settings:contacts.save')}));
    await waitFor(()=>expect(saved).toHaveBeenCalled());
    expect(mocks.unbind).not.toHaveBeenCalled();expect(mocks.bind).not.toHaveBeenCalled();
});


it.each(["bind", "unbind"])("rejects a failed handle %s so the editor can report it", async (operation) => {
    const handle = { id: 5, principalId: "p", app: "slack", handleId: "one", displayHandle: "one", createdAt: 0 };
    mocks.bind.mockResolvedValue(undefined);
    mocks.unbind.mockResolvedValue(undefined);
    mocks[operation].mockRejectedValueOnce(new Error("failed"));
    await expect(syncHandles("p", operation === "unbind" ? [handle] : [], operation === "bind" ? [handle] : [])).rejects.toThrow("failed");
});


it('reports a partial handle failure and retries only unfinished changes after promotion', async () => {
    await i18n.changeLanguage('en');
    vi.clearAllMocks();
    const person = { id: 'p', kind: 'anonymous', displayName: 'Sam', relationship: '', aliases: [], appHandles: [
        { id: 1, principalId: 'p', app: 'slack', handleId: 'a', displayHandle: 'a', createdAt: 0 },
        { id: 2, principalId: 'p', app: 'wechat', handleId: 'b', displayHandle: 'b', createdAt: 0 },
    ] } as PrincipalDTO;
    const promoted = { ...person, kind: 'contact' } as PrincipalDTO;
    mocks.promote.mockResolvedValue(promoted);
    mocks.update.mockResolvedValue(promoted);
    mocks.unbind.mockResolvedValueOnce(undefined).mockRejectedValueOnce(new Error('failed')).mockResolvedValueOnce(undefined);
    const saved = vi.fn();
    render(<ContactEditorDialog mode="promote" initial={person} open onOpenChange={vi.fn()} onSaved={saved} />);
    fireEvent.click(screen.getAllByRole('button', { name: i18n.t('settings:contacts.removeHandleAria') })[0]);
    fireEvent.click(screen.getByRole('button', { name: i18n.t('settings:contacts.removeHandleAria') }));
    fireEvent.click(screen.getByRole('button', { name: i18n.t('settings:contacts.save') }));
    await waitFor(() => expect(mocks.error).toHaveBeenCalled());
    expect(saved).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: i18n.t('settings:contacts.save') }));
    await waitFor(() => expect(saved).toHaveBeenCalledOnce());
    expect(mocks.promote).toHaveBeenCalledOnce();
    expect(mocks.unbind.mock.calls.map(([id]) => id)).toEqual([1, 2, 2]);
});
