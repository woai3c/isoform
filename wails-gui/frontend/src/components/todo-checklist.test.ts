import { describe, expect, it } from 'vitest';
import { parseTodoInput } from './todo-checklist';

describe('parseTodoInput', () => {
    it('parses a well-formed TodoWrite input', () => {
        const todos = parseTodoInput({
            todos: [
                { content: 'Read the config', status: 'completed', activeForm: 'Reading the config' },
                { content: 'Fix the parser', status: 'in_progress', activeForm: 'Fixing the parser' },
                { content: 'Run the tests', status: 'pending', activeForm: 'Running the tests' },
            ],
        });
        expect(todos).toHaveLength(3);
        expect(todos[1]).toEqual({
            content: 'Fix the parser',
            status: 'in_progress',
            activeForm: 'Fixing the parser',
        });
    });

    it('drops malformed entries instead of failing the whole list', () => {
        const todos = parseTodoInput({
            todos: [
                { content: 'Valid', status: 'pending' },
                { content: '', status: 'pending' },
                { content: 'Bad status', status: 'done' },
                { status: 'pending' },
                null,
                'not-an-object',
            ],
        });
        expect(todos).toEqual([
            { content: 'Valid', status: 'pending', activeForm: undefined },
        ]);
    });

    it('returns [] for non-TodoWrite-shaped input', () => {
        expect(parseTodoInput(undefined)).toEqual([]);
        expect(parseTodoInput(null)).toEqual([]);
        expect(parseTodoInput('{"todos": []}')).toEqual([]);
        expect(parseTodoInput({ todos: 'nope' })).toEqual([]);
        expect(parseTodoInput({})).toEqual([]);
    });
});
