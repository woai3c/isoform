-- Schema v63: actual v62 fixture plus #140 migrateStreamItemTopics at 7ad3a51bc6874895504e0584afc3d8e3b682bdc1.
CREATE TABLE agent_run_routes (
                run_id TEXT PRIMARY KEY,
                session_id TEXT NOT NULL DEFAULT '',
                backend TEXT NOT NULL DEFAULT '',
                candidate_backends TEXT NOT NULL DEFAULT '[]',
                fallback_from TEXT NOT NULL DEFAULT '[]',
                backend_reason TEXT NOT NULL DEFAULT '',
                model TEXT NOT NULL DEFAULT '',
                status TEXT NOT NULL DEFAULT 'running',
                actions_started INTEGER NOT NULL DEFAULT 0,
                updated_at INTEGER NOT NULL
            );
CREATE TABLE app_handles (
				id             INTEGER PRIMARY KEY AUTOINCREMENT,
				principal_id   TEXT NOT NULL REFERENCES principals(id) ON DELETE CASCADE,
				app            TEXT NOT NULL,
				handle_id      TEXT NOT NULL DEFAULT '',
				display_handle TEXT NOT NULL DEFAULT '',
				created_at     INTEGER NOT NULL,
				UNIQUE (app, handle_id, display_handle)
			);
CREATE TABLE calendar_event_attendees (
				event_id     TEXT    NOT NULL REFERENCES calendar_events(id) ON DELETE CASCADE,
				email        TEXT    NOT NULL DEFAULT '',
				name         TEXT    NOT NULL DEFAULT '',
				role         TEXT    NOT NULL DEFAULT '',
				status       TEXT    NOT NULL DEFAULT '',
				is_me        INTEGER NOT NULL DEFAULT 0,
				principal_id TEXT    NOT NULL DEFAULT '',
				PRIMARY KEY (event_id, email, name)
			);
CREATE TABLE calendar_events (
				id              TEXT PRIMARY KEY,
				uid             TEXT    NOT NULL,
				local_id        TEXT    NOT NULL DEFAULT '',
				calendar_id     TEXT    NOT NULL DEFAULT '',
				calendar_title  TEXT    NOT NULL DEFAULT '',
				source          TEXT    NOT NULL,
				provider        TEXT    NOT NULL DEFAULT '',
				title           TEXT    NOT NULL DEFAULT '',
				start_at        INTEGER NOT NULL,
				end_at          INTEGER NOT NULL,
				all_day         INTEGER NOT NULL DEFAULT 0,
				time_zone       TEXT    NOT NULL DEFAULT '',
				location        TEXT    NOT NULL DEFAULT '',
				lat             REAL    NOT NULL DEFAULT 0,
				lon             REAL    NOT NULL DEFAULT 0,
				has_coordinate  INTEGER NOT NULL DEFAULT 0,
				conference_url  TEXT    NOT NULL DEFAULT '',
				conference_host TEXT    NOT NULL DEFAULT '',
				url             TEXT    NOT NULL DEFAULT '',
				availability    TEXT    NOT NULL DEFAULT '',
				status          TEXT    NOT NULL DEFAULT '',
				my_status       TEXT    NOT NULL DEFAULT '',
				organizer_email TEXT    NOT NULL DEFAULT '',
				organizer_name  TEXT    NOT NULL DEFAULT '',
				recurring       INTEGER NOT NULL DEFAULT 0,
				attendee_count  INTEGER NOT NULL DEFAULT 0,
				session_id      TEXT    NOT NULL DEFAULT '',
				first_seen_at   INTEGER NOT NULL,
				updated_at      INTEGER NOT NULL,
				deleted_at      INTEGER NOT NULL DEFAULT 0
			);
CREATE TABLE calendar_sources (
				id            TEXT PRIMARY KEY,
				kind          TEXT    NOT NULL,
				provider      TEXT    NOT NULL DEFAULT '',
				title         TEXT    NOT NULL DEFAULT '',
				detected_by   TEXT    NOT NULL DEFAULT '',
				calendars     INTEGER NOT NULL DEFAULT 0,
				first_seen_at INTEGER NOT NULL,
				last_seen_at  INTEGER NOT NULL,
				last_read_at  INTEGER NOT NULL DEFAULT 0
			);
CREATE TABLE chat_deliverables (
				id               TEXT PRIMARY KEY,
				session_id       TEXT NOT NULL,
				title            TEXT NOT NULL DEFAULT '',
				summary          TEXT NOT NULL DEFAULT '',
				files            TEXT NOT NULL DEFAULT '[]',
				work_dir         TEXT NOT NULL DEFAULT '',
				session_temp_dir TEXT NOT NULL DEFAULT '',
				created_at       INTEGER NOT NULL,
				updated_at       INTEGER NOT NULL
			);
CREATE TABLE checkpoint_files (
				id               INTEGER PRIMARY KEY AUTOINCREMENT,
				session_local_id TEXT NOT NULL,
				sort_order       INTEGER NOT NULL,
				file_path        TEXT NOT NULL,
				backup_path      TEXT NOT NULL,
				existed          INTEGER NOT NULL DEFAULT 1,
				created_at       INTEGER NOT NULL
			);
CREATE TABLE composer_suggestions (
				id           TEXT PRIMARY KEY,
				generated_at INTEGER NOT NULL,
				suggestions  TEXT NOT NULL,
				source_hash  TEXT NOT NULL DEFAULT '',
				expires_at   INTEGER NOT NULL
			);
CREATE TABLE contact_handle_provenance (
            external_ref TEXT NOT NULL REFERENCES imported_contact_cards(external_ref) ON DELETE CASCADE,
            handle_id INTEGER NOT NULL REFERENCES app_handles(id) ON DELETE CASCADE,
            PRIMARY KEY (external_ref, handle_id)
        );
CREATE TABLE cron_jobs (
				id                 TEXT PRIMARY KEY,
				name               TEXT NOT NULL,
				description        TEXT NOT NULL DEFAULT '',
				enabled            INTEGER NOT NULL DEFAULT 1,
				schedule_kind      TEXT NOT NULL,
				schedule_config    TEXT NOT NULL,
				target_kind        TEXT NOT NULL DEFAULT 'agent',
				target_config      TEXT NOT NULL,
				delete_after_run   INTEGER NOT NULL DEFAULT 0,
				retry_policy       TEXT NOT NULL DEFAULT '{}',
				next_run_at        INTEGER NOT NULL DEFAULT 0,
				last_run_at        INTEGER NOT NULL DEFAULT 0,
				last_run_status    TEXT NOT NULL DEFAULT '',
				last_error         TEXT NOT NULL DEFAULT '',
				consecutive_errors INTEGER NOT NULL DEFAULT 0,
				retry_attempt      INTEGER NOT NULL DEFAULT 0,
				retry_run_at       INTEGER NOT NULL DEFAULT 0,
				created_at         INTEGER NOT NULL,
				updated_at         INTEGER NOT NULL
			, source_session_id TEXT NOT NULL DEFAULT '', source_platform TEXT NOT NULL DEFAULT '', fingerprint TEXT NOT NULL DEFAULT '', notify_dm INTEGER NOT NULL DEFAULT 1, sort_order INTEGER NOT NULL DEFAULT 0, notify_platform TEXT NOT NULL DEFAULT '', schedule_evidence TEXT NOT NULL DEFAULT '', surface_kind TEXT NOT NULL DEFAULT '');
CREATE TABLE cron_runs (
				id             TEXT PRIMARY KEY,
				job_id         TEXT NOT NULL REFERENCES cron_jobs(id) ON DELETE CASCADE,
				started_at     INTEGER NOT NULL,
				finished_at    INTEGER NOT NULL DEFAULT 0,
				status         TEXT NOT NULL DEFAULT 'running',
				trigger_kind   TEXT NOT NULL DEFAULT 'scheduled',
				error          TEXT NOT NULL DEFAULT '',
				duration_ms    INTEGER NOT NULL DEFAULT 0,
				result_summary TEXT NOT NULL DEFAULT ''
			, session_tag TEXT NOT NULL DEFAULT '', item_sort_start INTEGER NOT NULL DEFAULT -1, notify_result TEXT NOT NULL DEFAULT '', candidate_backends TEXT NOT NULL DEFAULT '', backend TEXT NOT NULL DEFAULT '', fallback_from TEXT NOT NULL DEFAULT '', backend_reason TEXT NOT NULL DEFAULT '', model TEXT NOT NULL DEFAULT '', actions_started INTEGER NOT NULL DEFAULT 0);
CREATE TABLE engagement_moments (
				id          TEXT PRIMARY KEY,
				kind        TEXT    NOT NULL,
				subject_id  TEXT    NOT NULL DEFAULT '',
				dedupe_key  TEXT    NOT NULL,
				score       REAL    NOT NULL DEFAULT 0,
				state       TEXT    NOT NULL DEFAULT 'pending',
				channel     TEXT    NOT NULL DEFAULT '',
				level       TEXT    NOT NULL DEFAULT '',
				reason      TEXT    NOT NULL DEFAULT '',
				params      TEXT    NOT NULL DEFAULT '{}',
				created_at  INTEGER NOT NULL,
				due_at      INTEGER NOT NULL DEFAULT 0,
				hold_until  INTEGER NOT NULL DEFAULT 0,
				sent_at     INTEGER NOT NULL DEFAULT 0,
				expires_at  INTEGER NOT NULL DEFAULT 0,
				updated_at  INTEGER NOT NULL
			, stakes REAL NOT NULL DEFAULT 0);
CREATE TABLE engagement_prefs (
				kind         TEXT PRIMARY KEY,
				weight       REAL    NOT NULL DEFAULT -1,
				enabled      INTEGER NOT NULL DEFAULT -1,
				muted_until  INTEGER NOT NULL DEFAULT 0,
				updated_at   INTEGER NOT NULL
			);
CREATE TABLE engagement_receipts (
				id         INTEGER PRIMARY KEY AUTOINCREMENT,
				moment_id  TEXT    NOT NULL REFERENCES engagement_moments(id) ON DELETE CASCADE,
				channel    TEXT    NOT NULL DEFAULT '',
				state      TEXT    NOT NULL,
				action     TEXT    NOT NULL DEFAULT '',
				at         INTEGER NOT NULL,
				UNIQUE (moment_id, channel, state, action)
			);
CREATE TABLE extractor_records (
				id               TEXT PRIMARY KEY,
				pipeline_run_id  TEXT NOT NULL,
				platform         TEXT NOT NULL,
				channel_name     TEXT NOT NULL,
				processed_at     INTEGER NOT NULL,
				title            TEXT NOT NULL,
				full_output      TEXT NOT NULL,
				source_digest    TEXT NOT NULL,
				deliverables     TEXT NOT NULL DEFAULT '[]',
				message_count    INTEGER NOT NULL,
				participants     TEXT,
				work_dir         TEXT NOT NULL DEFAULT '',
				session_temp_dir TEXT NOT NULL DEFAULT '',
				created_at       INTEGER NOT NULL
			);
CREATE TABLE git_snapshots (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			session_local_id TEXT NOT NULL,
			sort_order       INTEGER NOT NULL,
			stash_sha        TEXT NOT NULL,
			untracked_files  TEXT NOT NULL DEFAULT '',
			work_dir         TEXT NOT NULL,
			created_at       INTEGER NOT NULL
		, head_sha TEXT NOT NULL DEFAULT '');
CREATE TABLE imported_contact_cards (
            external_ref TEXT PRIMARY KEY,
            payload TEXT NOT NULL
        );
CREATE TABLE mail_messages (
				id            TEXT PRIMARY KEY,
				mailbox       TEXT    NOT NULL,
				sender_email  TEXT    NOT NULL DEFAULT '',
				sender_name   TEXT    NOT NULL DEFAULT '',
				to_addresses  TEXT    NOT NULL DEFAULT '',
				cc_addresses  TEXT    NOT NULL DEFAULT '',
				to_me         INTEGER NOT NULL DEFAULT 0,
				subject       TEXT    NOT NULL DEFAULT '',
				thread_key    TEXT    NOT NULL DEFAULT '',
				at            INTEGER NOT NULL,
				read          INTEGER NOT NULL DEFAULT 0,
				flagged       INTEGER NOT NULL DEFAULT 0,
				replied       INTEGER NOT NULL DEFAULT 0,
				principal_id  TEXT    NOT NULL DEFAULT '',
				first_seen_at INTEGER NOT NULL,
				updated_at    INTEGER NOT NULL
			);
CREATE TABLE meeting_followups (
				session_id     TEXT PRIMARY KEY,
				event_id       TEXT    NOT NULL DEFAULT '',
				title          TEXT    NOT NULL DEFAULT '',
				decisions_json TEXT    NOT NULL DEFAULT '[]',
				followups_json TEXT    NOT NULL DEFAULT '[]',
				created_at     INTEGER NOT NULL
			);
CREATE TABLE mobile_receipts (
				request_key TEXT    PRIMARY KEY,
				device_id   TEXT    NOT NULL,
				route       TEXT    NOT NULL,
				status      INTEGER NOT NULL,
				body        TEXT    NOT NULL,
				at          INTEGER NOT NULL
			);
CREATE TABLE model_calls (
				id          INTEGER PRIMARY KEY AUTOINCREMENT,
				at          INTEGER NOT NULL,
				session_id  TEXT    NOT NULL DEFAULT '',
				run_id      TEXT    NOT NULL DEFAULT '',
				requested   TEXT    NOT NULL DEFAULT '',
				provider    TEXT    NOT NULL DEFAULT '',
				model       TEXT    NOT NULL DEFAULT '',
				priority    TEXT    NOT NULL DEFAULT '',
				source      TEXT    NOT NULL DEFAULT ''
			);
CREATE TABLE principal_aliases (
				principal_id  TEXT NOT NULL REFERENCES principals(id) ON DELETE CASCADE,
				alias         TEXT NOT NULL,
				PRIMARY KEY (principal_id, alias)
			);
CREATE TABLE principal_contact_link_updates (
            principal_id TEXT NOT NULL REFERENCES principal_contact_links(principal_id) ON DELETE CASCADE,
            app TEXT NOT NULL,
            session_id TEXT NOT NULL,
            message_count INTEGER NOT NULL,
            first_seen_at INTEGER NOT NULL,
            last_seen_at INTEGER NOT NULL,
            PRIMARY KEY(principal_id,app,session_id)
        );
CREATE TABLE principal_contact_links (
            principal_id TEXT PRIMARY KEY REFERENCES principals(id) ON DELETE CASCADE,
            external_ref TEXT NOT NULL UNIQUE REFERENCES imported_contact_cards(external_ref) ON DELETE CASCADE,
            receipt TEXT NOT NULL
        );
CREATE TABLE principal_recent_messages (
				principal_id  TEXT NOT NULL REFERENCES principals(id) ON DELETE CASCADE,
				app           TEXT NOT NULL,
				seen_at       INTEGER NOT NULL,
				preview       TEXT NOT NULL DEFAULT '',
				PRIMARY KEY (principal_id, seen_at)
			);
CREATE TABLE principal_research (
				principal_id  TEXT PRIMARY KEY REFERENCES principals(id) ON DELETE CASCADE,
				status        TEXT    NOT NULL DEFAULT 'idle',
				summary       TEXT    NOT NULL DEFAULT '',
				markdown      TEXT    NOT NULL DEFAULT '',
				sources       TEXT    NOT NULL DEFAULT '[]',
				model         TEXT    NOT NULL DEFAULT '',
				error         TEXT    NOT NULL DEFAULT '',
				researched_at INTEGER NOT NULL DEFAULT 0,
				created_at    INTEGER NOT NULL,
				updated_at    INTEGER NOT NULL
			);
CREATE TABLE principal_session_sightings (
				principal_id  TEXT NOT NULL REFERENCES principals(id) ON DELETE CASCADE,
				session_id    TEXT NOT NULL,
				first_seen_at INTEGER NOT NULL,
				last_seen_at  INTEGER NOT NULL,
				PRIMARY KEY (principal_id, session_id)
			);
CREATE TABLE principal_sightings (
				principal_id   TEXT NOT NULL REFERENCES principals(id) ON DELETE CASCADE,
				app            TEXT NOT NULL,
				message_count  INTEGER NOT NULL DEFAULT 0,
				first_seen_at  INTEGER NOT NULL,
				last_seen_at   INTEGER NOT NULL,
				PRIMARY KEY (principal_id, app)
			);
CREATE TABLE principal_voice_embeddings (
				id             INTEGER PRIMARY KEY AUTOINCREMENT,
				principal_id   TEXT    NOT NULL REFERENCES principals(id) ON DELETE CASCADE,
				embedding      BLOB    NOT NULL,
				dim            INTEGER NOT NULL,
				sample_rate    INTEGER NOT NULL,
				audio_seconds  REAL    NOT NULL,
				label          TEXT    NOT NULL DEFAULT '',
				captured_at    INTEGER NOT NULL
			);
CREATE TABLE principals (
				id            TEXT PRIMARY KEY,
				kind          TEXT NOT NULL,
				display_name  TEXT NOT NULL DEFAULT '',
				relationship  TEXT NOT NULL DEFAULT '',
				notes         TEXT NOT NULL DEFAULT '',
				created_from  TEXT NOT NULL DEFAULT '',
				confidence    REAL NOT NULL DEFAULT 1.0,
				created_at    INTEGER NOT NULL,
				updated_at    INTEGER NOT NULL
			, source TEXT NOT NULL DEFAULT '', external_ref TEXT NOT NULL DEFAULT '');
CREATE TABLE projects (
				id                TEXT PRIMARY KEY,
				name              TEXT NOT NULL,
				path              TEXT NOT NULL UNIQUE,
				organization      TEXT NOT NULL DEFAULT '',
				product           TEXT NOT NULL DEFAULT '',
				is_yansu_project  INTEGER NOT NULL DEFAULT 0,
				active_session_id TEXT NOT NULL DEFAULT '',
				created_at        INTEGER NOT NULL,
				updated_at        INTEGER NOT NULL
			, active_workspace_id TEXT NOT NULL DEFAULT '', sort_order INTEGER NOT NULL DEFAULT 0, setup_command TEXT NOT NULL DEFAULT '', hidden INTEGER NOT NULL DEFAULT 0);
CREATE TABLE reminders (
				uid           TEXT PRIMARY KEY,
				title         TEXT    NOT NULL DEFAULT '',
				due_at        INTEGER NOT NULL DEFAULT 0,
				due_all_day   INTEGER NOT NULL DEFAULT 0,
				list          TEXT    NOT NULL DEFAULT '',
				priority      INTEGER NOT NULL DEFAULT 0,
				completed_at  INTEGER NOT NULL DEFAULT 0,
				first_seen_at INTEGER NOT NULL,
				updated_at    INTEGER NOT NULL,
				deleted_at    INTEGER NOT NULL DEFAULT 0
			);
CREATE TABLE sessions (
				local_id    TEXT PRIMARY KEY,
				project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
				session_id  TEXT NOT NULL DEFAULT '',
				label       TEXT NOT NULL,
				model       TEXT NOT NULL,
				prd_enabled INTEGER NOT NULL DEFAULT 0,
				created_at  INTEGER NOT NULL,
				updated_at  INTEGER NOT NULL
			, thinking_level TEXT NOT NULL DEFAULT '', plan_mode INTEGER NOT NULL DEFAULT 0, thinking_enabled INTEGER NOT NULL DEFAULT 0, pinned INTEGER NOT NULL DEFAULT 0, pinned_at INTEGER NOT NULL DEFAULT 0, workspace_type TEXT NOT NULL DEFAULT 'session', worktree_path TEXT NOT NULL DEFAULT '', worktree_branch TEXT NOT NULL DEFAULT '', workspace_id TEXT NOT NULL DEFAULT '', hidden INTEGER NOT NULL DEFAULT 0, automation_result INTEGER NOT NULL DEFAULT 0, meeting_id TEXT NOT NULL DEFAULT '', kind TEXT NOT NULL DEFAULT '', parent_id TEXT NOT NULL DEFAULT '');
CREATE TABLE sidebar_group_items (
				id         TEXT PRIMARY KEY,
				group_id   TEXT NOT NULL REFERENCES sidebar_groups(id) ON DELETE CASCADE,
				item_type  TEXT NOT NULL,
				ref_id     TEXT NOT NULL,
				sort_order INTEGER NOT NULL DEFAULT 0,
				added_at   INTEGER NOT NULL
			);
CREATE TABLE sidebar_groups (
				id         TEXT PRIMARY KEY,
				name       TEXT NOT NULL,
				sort_order INTEGER NOT NULL DEFAULT 0,
				collapsed  INTEGER NOT NULL DEFAULT 0,
				created_at INTEGER NOT NULL,
				updated_at INTEGER NOT NULL
			);
CREATE TABLE ssh_configs (
				id         TEXT PRIMARY KEY,
				name       TEXT NOT NULL,
				host       TEXT NOT NULL,
				port       INTEGER NOT NULL DEFAULT 22,
				user       TEXT NOT NULL DEFAULT '',
				auth_type  TEXT NOT NULL DEFAULT 'key',
				key_path   TEXT NOT NULL DEFAULT '',
				created_at INTEGER NOT NULL,
				updated_at INTEGER NOT NULL
			, exec_mode TEXT NOT NULL DEFAULT 'subscription', cli_tool TEXT NOT NULL DEFAULT '', password TEXT NOT NULL DEFAULT '', auto_connect INTEGER NOT NULL DEFAULT 0);
CREATE TABLE stream_item_proactive_children (
			stream_item_id   TEXT NOT NULL REFERENCES stream_items(id) ON DELETE CASCADE,
			session_local_id TEXT NOT NULL,
			moment_id        TEXT NOT NULL,
			sort_order       INTEGER NOT NULL,
			PRIMARY KEY (stream_item_id, moment_id)
		);
CREATE TABLE stream_item_status_entries (
			stream_item_id   TEXT NOT NULL REFERENCES stream_items(id) ON DELETE CASCADE,
			session_local_id TEXT NOT NULL,
			entry_id        TEXT NOT NULL,
			sort_order       INTEGER NOT NULL,
			PRIMARY KEY (stream_item_id, entry_id)
		);
CREATE TABLE stream_item_topics (
            stream_item_id TEXT NOT NULL REFERENCES stream_items(id) ON DELETE CASCADE,
            session_id TEXT NOT NULL REFERENCES sessions(local_id) ON DELETE CASCADE,
            topic_id TEXT NOT NULL,
            created_at INTEGER NOT NULL,
            PRIMARY KEY (stream_item_id, topic_id)
        );
CREATE TABLE stream_items (
				id               TEXT PRIMARY KEY,
				session_local_id TEXT NOT NULL REFERENCES sessions(local_id) ON DELETE CASCADE,
				kind             TEXT NOT NULL,
				content          TEXT NOT NULL DEFAULT '',
				tool_name        TEXT NOT NULL DEFAULT '',
				tool_input       TEXT NOT NULL DEFAULT '',
				run_id           TEXT NOT NULL DEFAULT '',
				session_id       TEXT NOT NULL DEFAULT '',
				sort_order       INTEGER NOT NULL,
				created_at       INTEGER NOT NULL
			, images TEXT NOT NULL DEFAULT '', tool_use_id TEXT NOT NULL DEFAULT '', automation_context TEXT NOT NULL DEFAULT '', files TEXT NOT NULL DEFAULT '');
CREATE TABLE ui_state (
				key   TEXT PRIMARY KEY,
				value TEXT NOT NULL
			);
CREATE TABLE workspaces (
				id                TEXT PRIMARY KEY,
				project_id        TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
				label             TEXT NOT NULL,
				is_default        INTEGER NOT NULL DEFAULT 0,
				worktree_path     TEXT NOT NULL DEFAULT '',
				worktree_branch   TEXT NOT NULL DEFAULT '',
				base_branch       TEXT NOT NULL DEFAULT '',
				active_session_id TEXT NOT NULL DEFAULT '',
				created_at        INTEGER NOT NULL,
				updated_at        INTEGER NOT NULL
			, sort_order INTEGER NOT NULL DEFAULT 0, hidden INTEGER NOT NULL DEFAULT 0, remote_env TEXT NOT NULL DEFAULT '');
CREATE INDEX idx_agent_run_routes_updated ON agent_run_routes(updated_at DESC);
CREATE INDEX idx_app_handles_display
				ON app_handles(app, display_handle);
CREATE INDEX idx_app_handles_lookup
				ON app_handles(app, handle_id);
CREATE INDEX idx_calendar_event_attendees_principal
				ON calendar_event_attendees(principal_id) WHERE principal_id != '';
CREATE INDEX idx_calendar_events_session
				ON calendar_events(session_id) WHERE session_id != '';
CREATE INDEX idx_calendar_events_start
				ON calendar_events(start_at) WHERE deleted_at = 0;
CREATE INDEX idx_chat_deliverables_updated_at
				ON chat_deliverables(updated_at DESC);
CREATE INDEX idx_checkpoint_session_order
				ON checkpoint_files(session_local_id, sort_order);
CREATE INDEX idx_composer_suggestions_generated_at
				ON composer_suggestions(generated_at DESC);
CREATE UNIQUE INDEX idx_cron_jobs_fingerprint ON cron_jobs(fingerprint) WHERE fingerprint != '';
CREATE INDEX idx_cron_runs_job ON cron_runs(job_id, started_at DESC);
CREATE INDEX idx_cron_runs_started ON cron_runs(started_at DESC);
CREATE INDEX idx_engagement_moments_kind_sent
				ON engagement_moments(kind, sent_at);
CREATE INDEX idx_engagement_moments_state
				ON engagement_moments(state, hold_until);
CREATE INDEX idx_engagement_receipts_moment
				ON engagement_receipts(moment_id);
CREATE UNIQUE INDEX idx_extractor_records_pipeline_run_id
			ON extractor_records(pipeline_run_id)
		;
CREATE INDEX idx_extractor_records_platform ON extractor_records(platform, processed_at);
CREATE INDEX idx_extractor_records_processed_at ON extractor_records(processed_at);
CREATE UNIQUE INDEX idx_git_snapshots_session_order
			ON git_snapshots(session_local_id, sort_order);
CREATE INDEX idx_mail_messages_at ON mail_messages(at);
CREATE INDEX idx_mail_messages_thread ON mail_messages(thread_key);
CREATE INDEX idx_mobile_receipts_at ON mobile_receipts(at);
CREATE INDEX idx_model_calls_at ON model_calls(at);
CREATE INDEX idx_principal_aliases_alias
				ON principal_aliases(alias);
CREATE INDEX idx_principal_recent_messages_principal
				ON principal_recent_messages(principal_id, seen_at DESC);
CREATE INDEX idx_principal_session_sightings_last_seen
				ON principal_session_sightings(principal_id, last_seen_at DESC);
CREATE INDEX idx_principal_sightings_last_seen
				ON principal_sightings(last_seen_at DESC);
CREATE INDEX idx_principal_voice_embeddings_principal
				ON principal_voice_embeddings(principal_id, captured_at DESC);
CREATE INDEX idx_principals_kind
				ON principals(kind);
CREATE INDEX idx_proactive_children_identity
			ON stream_item_proactive_children(session_local_id, moment_id, sort_order);
CREATE INDEX idx_reminders_open_due
				ON reminders(due_at) WHERE deleted_at = 0 AND completed_at = 0;
CREATE INDEX idx_sessions_kind ON sessions(kind) WHERE kind != '';
CREATE INDEX idx_sessions_meeting_id ON sessions(meeting_id) WHERE meeting_id != '';
CREATE INDEX idx_sessions_parent_id ON sessions(parent_id) WHERE parent_id != '';
CREATE INDEX idx_sessions_project ON sessions(project_id);
CREATE UNIQUE INDEX idx_sidebar_group_items_unique
				ON sidebar_group_items(group_id, item_type, ref_id);
CREATE INDEX idx_status_entries_identity
			ON stream_item_status_entries(session_local_id, entry_id, sort_order);
CREATE INDEX idx_stream_item_topics_topic ON stream_item_topics(session_id, topic_id, created_at, stream_item_id);
CREATE INDEX idx_stream_items_proactive_moment
			ON stream_items(session_local_id, json_extract(CASE WHEN json_valid(tool_input) THEN tool_input ELSE '{}' END, '$.momentId'))
			WHERE tool_name = 'main_chat_proactive';
CREATE INDEX idx_stream_items_session ON stream_items(session_local_id, sort_order);
CREATE INDEX idx_stream_items_session_tool_order
			ON stream_items(session_local_id, tool_name, sort_order);
CREATE INDEX idx_stream_items_topic_run ON stream_items(session_local_id, run_id) WHERE run_id != '';
CREATE INDEX idx_workspaces_project ON workspaces(project_id);
CREATE UNIQUE INDEX uq_engagement_moments_open
				ON engagement_moments(dedupe_key) WHERE state IN ('pending', 'scheduled');
CREATE UNIQUE INDEX uq_principals_self
				ON principals(kind) WHERE kind = 'self';
CREATE TRIGGER stream_items_proactive_children_insert
		AFTER INSERT ON stream_items WHEN NEW.tool_name = 'main_chat_proactive'
		BEGIN
	INSERT INTO stream_item_proactive_children (stream_item_id, session_local_id, moment_id, sort_order)
	SELECT DISTINCT source.id, source.session_local_id,
		json_extract(CASE WHEN child.type = 'object' THEN child.value ELSE '{}' END, '$.momentId'), source.sort_order
	FROM stream_items AS source,
		json_each(CASE WHEN json_valid(source.tool_input) THEN source.tool_input ELSE '{}' END, '$.items') AS child
	WHERE source.tool_name = 'main_chat_proactive'
		AND json_type(CASE WHEN json_valid(source.tool_input) THEN source.tool_input ELSE '{}' END, '$.items') = 'array'
		AND json_type(CASE WHEN child.type = 'object' THEN child.value ELSE '{}' END, '$.momentId') = 'text'
		AND json_extract(CASE WHEN child.type = 'object' THEN child.value ELSE '{}' END, '$.momentId') != '' AND source.id = NEW.id; END;
CREATE TRIGGER stream_items_proactive_children_update
		AFTER UPDATE OF tool_input, tool_name, session_local_id, sort_order ON stream_items
		WHEN OLD.tool_name = 'main_chat_proactive' OR NEW.tool_name = 'main_chat_proactive'
		BEGIN
			DELETE FROM stream_item_proactive_children WHERE stream_item_id = OLD.id;

	INSERT INTO stream_item_proactive_children (stream_item_id, session_local_id, moment_id, sort_order)
	SELECT DISTINCT source.id, source.session_local_id,
		json_extract(CASE WHEN child.type = 'object' THEN child.value ELSE '{}' END, '$.momentId'), source.sort_order
	FROM stream_items AS source,
		json_each(CASE WHEN json_valid(source.tool_input) THEN source.tool_input ELSE '{}' END, '$.items') AS child
	WHERE source.tool_name = 'main_chat_proactive'
		AND json_type(CASE WHEN json_valid(source.tool_input) THEN source.tool_input ELSE '{}' END, '$.items') = 'array'
		AND json_type(CASE WHEN child.type = 'object' THEN child.value ELSE '{}' END, '$.momentId') = 'text'
		AND json_extract(CASE WHEN child.type = 'object' THEN child.value ELSE '{}' END, '$.momentId') != '' AND source.id = NEW.id;
		END;
CREATE TRIGGER stream_items_status_entries_insert
		AFTER INSERT ON stream_items WHEN NEW.tool_name = 'main_chat_status'
		BEGIN
	INSERT INTO stream_item_status_entries (stream_item_id, session_local_id, entry_id, sort_order)
	SELECT DISTINCT source.id, source.session_local_id,
		json_extract(CASE WHEN entry.type = 'object' THEN entry.value ELSE '{}' END, '$.id'), source.sort_order
	FROM stream_items AS source,
		json_each(CASE WHEN json_valid(source.tool_input) THEN source.tool_input ELSE '{}' END, '$.entries') AS entry
	WHERE source.tool_name = 'main_chat_status'
		AND json_type(CASE WHEN json_valid(source.tool_input) THEN source.tool_input ELSE '{}' END, '$.entries') = 'array'
		AND json_type(CASE WHEN entry.type = 'object' THEN entry.value ELSE '{}' END, '$.id') = 'text'
		AND json_extract(CASE WHEN entry.type = 'object' THEN entry.value ELSE '{}' END, '$.id') != '' AND source.id = NEW.id; END;
CREATE TRIGGER stream_items_status_entries_update
		AFTER UPDATE OF tool_input, tool_name, session_local_id, sort_order ON stream_items
		WHEN OLD.tool_name = 'main_chat_status' OR NEW.tool_name = 'main_chat_status'
		BEGIN
			DELETE FROM stream_item_status_entries WHERE stream_item_id = OLD.id;

	INSERT INTO stream_item_status_entries (stream_item_id, session_local_id, entry_id, sort_order)
	SELECT DISTINCT source.id, source.session_local_id,
		json_extract(CASE WHEN entry.type = 'object' THEN entry.value ELSE '{}' END, '$.id'), source.sort_order
	FROM stream_items AS source,
		json_each(CASE WHEN json_valid(source.tool_input) THEN source.tool_input ELSE '{}' END, '$.entries') AS entry
	WHERE source.tool_name = 'main_chat_status'
		AND json_type(CASE WHEN json_valid(source.tool_input) THEN source.tool_input ELSE '{}' END, '$.entries') = 'array'
		AND json_type(CASE WHEN entry.type = 'object' THEN entry.value ELSE '{}' END, '$.id') = 'text'
		AND json_extract(CASE WHEN entry.type = 'object' THEN entry.value ELSE '{}' END, '$.id') != '' AND source.id = NEW.id;
		END;
CREATE TRIGGER stream_items_topic_insert AFTER INSERT ON stream_items
            WHEN NEW.run_id!='' AND NEW.kind!='user'
            BEGIN
                INSERT INTO stream_item_topics(stream_item_id,session_id,topic_id,created_at)
                SELECT NEW.id,NEW.session_local_id,tags.topic_id,NEW.created_at
                FROM stream_items source JOIN stream_item_topics tags ON tags.stream_item_id=source.id AND tags.session_id=source.session_local_id
                WHERE source.session_local_id=NEW.session_local_id AND source.run_id=NEW.run_id
                ON CONFLICT(stream_item_id,topic_id) DO NOTHING;

    UPDATE stream_items SET automation_context=json_set(
        CASE WHEN json_valid(automation_context) THEN CASE WHEN json_type(automation_context)='object' THEN automation_context ELSE '{}' END ELSE '{}' END,
        '$.topicContext', json((SELECT json_extract(source.automation_context, '$.topicContext')
    FROM stream_items source JOIN stream_item_topics tags ON tags.stream_item_id=source.id
        AND tags.session_id=source.session_local_id
    WHERE source.session_local_id=NEW.session_local_id AND source.run_id=NEW.run_id
        AND json_type(CASE WHEN json_valid(source.automation_context) THEN source.automation_context ELSE '{}' END, '$.topicContext')='object'
    LIMIT 1)))
    WHERE session_local_id=NEW.session_local_id AND run_id=NEW.run_id AND kind!='user'
        AND EXISTS (SELECT json_extract(source.automation_context, '$.topicContext')
    FROM stream_items source JOIN stream_item_topics tags ON tags.stream_item_id=source.id
        AND tags.session_id=source.session_local_id
    WHERE source.session_local_id=NEW.session_local_id AND source.run_id=NEW.run_id
        AND json_type(CASE WHEN json_valid(source.automation_context) THEN source.automation_context ELSE '{}' END, '$.topicContext')='object'
    LIMIT 1); END;
CREATE TRIGGER stream_items_topic_move AFTER UPDATE OF session_local_id ON stream_items
            WHEN OLD.session_local_id != NEW.session_local_id
            BEGIN DELETE FROM stream_item_topics WHERE stream_item_id=NEW.id; END;
CREATE TRIGGER stream_items_topic_run_update AFTER UPDATE OF run_id ON stream_items
            WHEN NEW.run_id!=''
            BEGIN
                INSERT INTO stream_item_topics(stream_item_id,session_id,topic_id,created_at)
                SELECT dest.id,dest.session_local_id,tags.topic_id,dest.created_at
                FROM stream_items source JOIN stream_item_topics tags ON tags.stream_item_id=source.id AND tags.session_id=source.session_local_id
                JOIN stream_items dest ON dest.session_local_id=source.session_local_id AND dest.run_id=source.run_id
                WHERE source.session_local_id=NEW.session_local_id AND source.run_id=NEW.run_id AND dest.kind!='user'
                ON CONFLICT(stream_item_id,topic_id) DO NOTHING;

    UPDATE stream_items SET automation_context=json_set(
        CASE WHEN json_valid(automation_context) THEN CASE WHEN json_type(automation_context)='object' THEN automation_context ELSE '{}' END ELSE '{}' END,
        '$.topicContext', json((SELECT json_extract(source.automation_context, '$.topicContext')
    FROM stream_items source JOIN stream_item_topics tags ON tags.stream_item_id=source.id
        AND tags.session_id=source.session_local_id
    WHERE source.session_local_id=NEW.session_local_id AND source.run_id=NEW.run_id
        AND json_type(CASE WHEN json_valid(source.automation_context) THEN source.automation_context ELSE '{}' END, '$.topicContext')='object'
    LIMIT 1)))
    WHERE session_local_id=NEW.session_local_id AND run_id=NEW.run_id AND kind!='user'
        AND EXISTS (SELECT json_extract(source.automation_context, '$.topicContext')
    FROM stream_items source JOIN stream_item_topics tags ON tags.stream_item_id=source.id
        AND tags.session_id=source.session_local_id
    WHERE source.session_local_id=NEW.session_local_id AND source.run_id=NEW.run_id
        AND json_type(CASE WHEN json_valid(source.automation_context) THEN source.automation_context ELSE '{}' END, '$.topicContext')='object'
    LIMIT 1); END;
PRAGMA user_version = 63;
