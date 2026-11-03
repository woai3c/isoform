## Project Context

This project uses Isoform for knowledge management. Project context and knowledge
are available in the `.something/` directory.

### Quick Reference

- **PRD**: `.something/prd.md` - product requirements defining scope and features
- **Scenarios**: `.something/scenarios/` - user scenarios and use cases
- **Project context**: `.something/context.md` - project overview and document index
- **Knowledge base**: `.something/knowledge/` - coding guidelines, patterns
- **Knowledge index**: `.something/knowledge/_index.md` - quick lookup

### Before Making Any Changes

**IMPORTANT:** You MUST understand the project context before writing code:

1. Read `.something/context.md` for project overview and document index
2. When implementing features or making design decisions:
   - Read `.something/prd.md` for full product requirements
   - Browse relevant `.something/scenarios/` for user scenarios and acceptance criteria
3. Check `.something/knowledge/_index.md` to find relevant guidelines
4. Read the specific knowledge files that match your task
5. Follow the patterns and conventions defined in the knowledge base

---

## CRITICAL: Knowledge Capture

**After completing ANY significant task, you MUST capture new knowledge.**

This is NOT optional. Knowledge capture ensures that:
- Future AI agents (and humans) can learn from your work
- Patterns and decisions are documented for consistency
- The team builds a shared understanding over time

### When to Capture Knowledge

You MUST create a new knowledge file when you:

- Discover a new coding pattern or convention
- Make an architectural decision
- Find a non-obvious solution to a problem
- Establish a new best practice
- Learn something important about the codebase
- Fix a tricky bug (document the root cause and solution)
- Integrate with an external API or service

### How to Capture Knowledge

**Immediately after completing your task**, write to `.something/new-knowledge/<descriptive-name>.md`:

```markdown
# Action
create

# Trigger
<specific scenario or question that should trigger this knowledge>
<be specific: "How to handle authentication errors" not just "error handling">

# Content
<detailed explanation of the pattern, decision, or learning>
<include code examples where helpful>
<explain WHY, not just WHAT>
```

### Examples of Good Knowledge Entries

**Example 1: Coding Pattern**
```markdown
# Action
create

# Trigger
How to handle database transactions in this project

# Content
Always use the TransactionManager wrapper for database transactions:

```go
err := tm.WithTransaction(ctx, func(tx *sql.Tx) error {
    // your code here
    return nil
})
```

This ensures proper rollback on errors and consistent timeout handling.
Never use db.Begin() directly.
```

**Example 2: Architectural Decision**
```markdown
# Action
create

# Trigger
Why do we use event sourcing for order management

# Content
We chose event sourcing for the order management module because:
1. Orders have complex state transitions that need audit trails
2. We need to replay events for debugging production issues
3. Integration with external fulfillment systems requires reliable event delivery

Trade-offs accepted:
- Higher storage costs
- More complex queries (mitigated by CQRS read models)
```

### Updating Existing Knowledge

If you find that existing knowledge is outdated or incorrect, update it:

```markdown
# Action
update

# UUID
<copy the UUID from the existing knowledge file>

# Trigger
<updated trigger>

# Content
<updated content>
```

---

## Activity Logging (Optional)

For significant completed work, you may also log activity to `.something/activity/<date>.md`:

```markdown
# Activity

- **Type**: feature_added | bug_fixed | refactored | documented
- **Summary**: <brief description of what was accomplished>

## Files Changed
- path/to/file1.go
- path/to/file2.go

## Knowledge Created
- .something/new-knowledge/new-pattern.md
```
