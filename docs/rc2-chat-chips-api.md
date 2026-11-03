# RC 2 C2: prepared requests under main-chat answers

`ListMainChatAnswerChips(itemIds: string[]): Promise<Record<string, string[]>>`
reads stored requests for the first 100 supplied IDs. Only existing assistant
text rows in the main session are returned. An absent key has no stored
attempt; an empty array is an attempted answer with no prepared requests.
Neither this read nor generation sends a message or creates an action.

Listen for `main-chat:chips` with `{ sessionId, itemId }`, then read that answer's
chips. Events carry identifiers only. A chip fills the composer; the consumer
must wait for the user to press Send. C2 adds no rendering or phone fields.

Only successful attended main-chat runs whose prompt matches the stored user
words qualify, including ordinary sends,
phone-origin user sends, and `SendMainChatWithTopic`. Background continuations,
proactive/report/status/setup rows, scheduled sessions and handoff execution
chats do not qualify. The backend uses the final stamped assistant row's ID,
waits the existing renderer-insert grace period, completes its streamed text
without inserting a missing row, and starts a separate bounded generation.
No historical backfill or read-triggered generation runs.

Generation uses Isoform's native runner and light model role, without tools,
system context, chat history, memories, Activity, attachments or source refs.
Only the stored user words and final answer enter the prompt, redacted before
being cut to 4,000 runes each. The request asks for zero to two next requests in
the current UI language. Strict JSON parsing, length/duplicate/URL checks,
redaction and conservative English/Chinese completion/direct-send/pay/delete
prefix guards filter the result. The main answer is not rewritten by the model.

Closed Eyes blocks generation, native dispatch, publication and reads. Reads
otherwise follow direct chat-page inspection: no observed source reader or
extra context is consulted. Publication rechecks the exact answer text, run,
session and row kind. Deleting, rewinding, moving or editing the answer removes
its chips; late results cannot recreate the row or its attempt.

Schema **v66** adds `stream_item_chips` with cascading row/session foreign keys,
an index on creation time and row-move/content-identity invalidation triggers.
An empty attempt is committed before dispatch, giving concurrent completions
and process restarts a durable single-flight marker. Failures and invalid
output leave this empty marker. If Closed Eyes becomes active during the call,
no generated requests are published and the empty attempt remains. Each call
has a 20-second deadline and at most 60 attempts fit in a rolling hour. A bounded,
identifier-free Unix-millisecond timestamp list in `ui_state` keeps that budget
through deletion, rewind and restart. Skips at the cap log only a count.

A v65 binary refuses the v66 stamp. To roll back to v65, restore the verified
pre-v66 database snapshot; never lower the stamp manually. A forward-compatible
rollback can disable the hook/read binding while retaining schema v66 and its
cleanup triggers. Restoring a snapshot loses changes made after that snapshot.
