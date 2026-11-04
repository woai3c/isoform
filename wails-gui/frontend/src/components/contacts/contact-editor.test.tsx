import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import type { PrincipalDTO } from '@/lib/wails-shim-app';
const mocks=vi.hoisted(()=>({update:vi.fn(),bind:vi.fn(),unbind:vi.fn(),promote:vi.fn(),list:vi.fn(),create:vi.fn()}));
vi.mock('@/lib/wails-shim-app',()=>({UpdatePrincipal:mocks.update,BindAppHandle:mocks.bind,UnbindAppHandle:mocks.unbind,CreateContact:mocks.create,EnsureSelfProfile:vi.fn(),PromoteAnonymousToContact:mocks.promote,ListPrincipals:mocks.list}));
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
    (mocks as unknown as Record<string, ReturnType<typeof vi.fn>>)[operation].mockRejectedValueOnce(new Error("failed"));
    await expect(syncHandles("p", operation === "unbind" ? [handle] : [], operation === "bind" ? [handle] : [])).rejects.toThrow("failed");
});


it('reports a partial handle failure and retries only unfinished changes after promotion', async () => {
    await i18n.changeLanguage('en');
    vi.clearAllMocks();
    const person = { id: 'p', kind: 'anonymous', displayName: 'Sam', relationship: '', aliases: [], appHandles: [
        { id: 1, principalId: 'p', app: 'slack', handleId: 'a', displayHandle: 'a', createdAt: 0 },
        { id: 2, principalId: 'p', app: 'wechat', handleId: 'b', displayHandle: 'b', createdAt: 0 },
    ] } as unknown as PrincipalDTO;
    const promoted = { ...person, kind: 'contact' } as PrincipalDTO;
    mocks.promote.mockResolvedValue(promoted);
    mocks.update.mockResolvedValue(promoted);
    mocks.unbind.mockResolvedValueOnce(undefined).mockRejectedValueOnce(new Error('failed')).mockResolvedValueOnce(undefined);
    const saved = vi.fn();
    render(<ContactEditorDialog mode="promote" initial={person} open onOpenChange={vi.fn()} onSaved={saved} />);
    fireEvent.click(screen.getAllByRole('button', { name: i18n.t('settings:contacts.removeHandleAria') })[0]);
    fireEvent.click(screen.getByRole('button', { name: i18n.t('settings:contacts.removeHandleAria') }));
    fireEvent.click(screen.getByRole('button', { name: i18n.t('settings:contacts.save') }));
    // N22: in the dialog, in copper, with Try again that saves only what's left.
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent("Saved. Couldn't remove b on WeChat.");
    expect(alert).toHaveClass('text-copper');
    expect(saved).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    await waitFor(() => expect(saved).toHaveBeenCalledOnce());
    expect(mocks.promote).toHaveBeenCalledOnce();
    expect(mocks.unbind.mock.calls.map(([id]) => id)).toEqual([1, 2, 2]);
});


describe('N19/N22: the person editor', () => {
    const person = { id: 'p', kind: 'contact', displayName: 'Sam', relationship: '', aliases: [], appHandles: [] } as unknown as PrincipalDTO;
    beforeEach(async () => {
        vi.clearAllMocks();
        await i18n.changeLanguage('en');
        mocks.update.mockResolvedValue(person);
        mocks.list.mockResolvedValue([]);
    });
    it('says Other names and Chat accounts, with app names and no id field', () => {
        render(<ContactEditorDialog mode="edit" initial={person} open onOpenChange={vi.fn()} onSaved={vi.fn()} />);
        expect(screen.getByText('Other names')).toBeInTheDocument();
        expect(screen.getByPlaceholderText('Type a name, then press Return')).toBeInTheDocument();
        expect(screen.getByText('Isoform uses these to recognize them in chats.')).toBeInTheDocument();
        expect(screen.getByText('Chat accounts')).toBeInTheDocument();
        expect(screen.queryByPlaceholderText(/id/i)).not.toBeInTheDocument();
        const options = Array.from(document.querySelectorAll('option')).map((o) => o.textContent);
        expect(options).toEqual(expect.arrayContaining(['WeChat', 'Telegram', 'Slack', 'Discord', 'Feishu', 'iMessage', 'Email']));
        expect(options).not.toContain('wechat');
        expect(screen.getByRole('heading', { name: 'Edit person' })).toBeInTheDocument();
        expect(i18n.t('settings:contacts.fieldAliases', { lng: 'zh-CN' })).toBe('其他称呼');
        expect(i18n.t('settings:contacts.removeHandleAria', { lng: 'zh-CN' })).toBe('移除账号');
    });
    it('N29: Add a person starts with no relationship', () => {
        render(<ContactEditorDialog mode="create" open onOpenChange={vi.fn()} onSaved={vi.fn()} />);
        expect(screen.getByRole('heading', { name: 'Add a person' })).toBeInTheDocument();
        expect((document.querySelector('select') as HTMLSelectElement).value).toBe('');
    });
    it('a failed add keeps the dialog open, updates the list behind, and Try again adds only that account', async () => {
        mocks.bind.mockRejectedValueOnce(new Error('sql: constraint')).mockResolvedValue({ id: 9, principalId: 'p', app: 'slack', handleId: '', displayHandle: '@sam', createdAt: 0 });
        const saved = vi.fn(); const changed = vi.fn();
        render(<ContactEditorDialog mode="edit" initial={person} open onOpenChange={vi.fn()} onSaved={saved} onChanged={changed} />);
        fireEvent.change(document.querySelectorAll('select')[1], { target: { value: 'slack' } });
        fireEvent.change(screen.getByPlaceholderText('Name or username, e.g. @zhangsan'), { target: { value: '@sam' } });
        fireEvent.click(screen.getByRole('button', { name: 'Add account' }));
        fireEvent.click(screen.getByRole('button', { name: 'Save' }));
        expect(await screen.findByText("Saved. Couldn't add @sam on Slack.")).toBeInTheDocument();
        expect(document.body).not.toHaveTextContent('constraint');
        expect(changed).toHaveBeenCalledOnce();
        expect(saved).not.toHaveBeenCalled();
        fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
        await waitFor(() => expect(saved).toHaveBeenCalledOnce());
        expect(mocks.update).toHaveBeenCalledOnce();
        expect(mocks.bind).toHaveBeenCalledTimes(2);
    });
    it('says when the person is gone, and when nothing saved', async () => {
        mocks.bind.mockRejectedValue(new Error('BindAppHandle principal "p": sql: no rows in result set'));
        render(<ContactEditorDialog mode="edit" initial={person} open onOpenChange={vi.fn()} onSaved={vi.fn()} />);
        fireEvent.change(document.querySelectorAll('select')[1], { target: { value: 'slack' } });
        fireEvent.change(screen.getByPlaceholderText('Name or username, e.g. @zhangsan'), { target: { value: '@sam' } });
        fireEvent.click(screen.getByRole('button', { name: 'Add account' }));
        fireEvent.click(screen.getByRole('button', { name: 'Save' }));
        expect(await screen.findByText("Couldn't add @sam: this person is no longer in Who you know.")).toBeInTheDocument();
    });
    it("says Couldn't save when the name didn't save", async () => {
        mocks.update.mockRejectedValue(new Error('x'));
        render(<ContactEditorDialog mode="edit" initial={person} open onOpenChange={vi.fn()} onSaved={vi.fn()} />);
        fireEvent.click(screen.getByRole('button', { name: 'Save' }));
        expect(await screen.findByText("Couldn't save Sam.")).toBeInTheDocument();
    });
    it("asks before moving someone else's account", async () => {
        mocks.list.mockResolvedValue([{ id: 'q', kind: 'contact', displayName: 'Jordan Lee', appHandles: [{ id: 3, principalId: 'q', app: 'slack', handleId: '', displayHandle: '@sam', createdAt: 0 }] }]);
        mocks.bind.mockResolvedValue({ id: 3, principalId: 'p', app: 'slack', handleId: '', displayHandle: '@sam', createdAt: 0 });
        const saved = vi.fn();
        render(<ContactEditorDialog mode="edit" initial={person} open onOpenChange={vi.fn()} onSaved={saved} />);
        fireEvent.change(document.querySelectorAll('select')[1], { target: { value: 'slack' } });
        fireEvent.change(screen.getByPlaceholderText('Name or username, e.g. @zhangsan'), { target: { value: '@sam' } });
        fireEvent.click(screen.getByRole('button', { name: 'Add account' }));
        fireEvent.click(screen.getByRole('button', { name: 'Save' }));
        expect(await screen.findByText("@sam is Jordan Lee's. Move it to Sam?")).toBeInTheDocument();
        expect(mocks.bind).not.toHaveBeenCalled();
        fireEvent.click(screen.getByRole('button', { name: 'Move' }));
        await waitFor(() => expect(saved).toHaveBeenCalledOnce());
        expect(mocks.bind).toHaveBeenCalledWith('p', 'slack', '', '@sam');
        expect(i18n.t('settings:contacts.moveQuestion', { handle: 'a', other: 'b', name: 'c', lng: 'zh-CN' })).toBe('a 属于 b。要移到 c 名下吗？');
    });
});
