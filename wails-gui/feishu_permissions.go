package main

import (
	"context"
	"fmt"
)

type FeishuPermissionCheck struct {
	Checked       bool     `json:"checked"`
	Ready         bool     `json:"ready"`
	Missing       []string `json:"missing,omitempty"`
	GrantedCount  int      `json:"granted_count"`
	RequiredCount int      `json:"required_count"`
}

type feishuScopeRequirement struct {
	Type string
	Name string
}

func requiredFeishuFullPermissionScopes() []feishuScopeRequirement {
	tenant := []string{
		"application:application:self_manage",
		"application:bot.basic_info:read",
		"application:bot.menu:write",
		"cardkit:card:read",
		"cardkit:card:write",
		"contact:contact.base:readonly",
		"docs:document.comment:create",
		"docs:document.comment:delete",
		"docs:document.comment:read",
		"docs:document.comment:update",
		"docs:document.comment:write_only",
		"docx:document:create",
		"docx:document:readonly",
		"docx:document:write_only",
		"docx:document.block:convert",
		"drive:drive.metadata:readonly",
		"im:chat.members:bot_access",
		"im:chat:create",
		"im:chat:read",
		"im:chat:update",
		"im:message.group_at_msg.include_bot:readonly",
		"im:message.group_at_msg:readonly",
		"im:message.p2p_msg:readonly",
		"im:message.pins:read",
		"im:message.pins:write_only",
		"im:message.reactions:read",
		"im:message.reactions:write_only",
		"im:message:readonly",
		"im:message:recall",
		"im:message:send_as_bot",
		"im:message:send_multi_users",
		"im:message:send_sys_msg",
		"im:message:update",
		"im:resource",
		"wiki:node:read",
	}
	user := []string{
		"contact:user.employee_id:readonly",
		"offline_access",
		"base:app:copy",
		"base:field:create",
		"base:field:delete",
		"base:field:read",
		"base:field:update",
		"base:record:create",
		"base:record:delete",
		"base:record:retrieve",
		"base:record:update",
		"base:table:create",
		"base:table:read",
		"base:table:update",
		"base:view:read",
		"base:view:write_only",
		"base:app:create",
		"base:app:update",
		"base:app:read",
		"sheets:spreadsheet.meta:read",
		"sheets:spreadsheet:read",
		"sheets:spreadsheet:create",
		"sheets:spreadsheet:write_only",
		"docs:document:export",
		"docs:document.media:upload",
		"board:whiteboard:node:create",
		"board:whiteboard:node:read",
		"calendar:calendar:read",
		"calendar:calendar.event:create",
		"calendar:calendar.event:read",
		"calendar:calendar.event:reply",
		"calendar:calendar.event:update",
		"calendar:calendar.free_busy:read",
		"contact:contact.base:readonly",
		"contact:user.base:readonly",
		"contact:user:search",
		"docs:document.comment:create",
		"docs:document.comment:read",
		"docs:document.comment:update",
		"docs:document.media:download",
		"docs:document:copy",
		"docx:document:create",
		"docx:document:readonly",
		"docx:document:write_only",
		"drive:drive.metadata:readonly",
		"drive:file:download",
		"drive:file:upload",
		"im:chat.members:read",
		"im:chat:read",
		"im:message",
		"im:message.group_msg:get_as_user",
		"im:message.p2p_msg:get_as_user",
		"im:message:readonly",
		"search:docs:read",
		"search:message",
		"space:document:move",
		"space:document:retrieve",
		"task:comment:read",
		"task:comment:write",
		"task:task:read",
		"task:task:write",
		"task:task:writeonly",
		"task:tasklist:read",
		"task:tasklist:write",
		"wiki:node:copy",
		"wiki:node:create",
		"wiki:node:move",
		"wiki:node:read",
		"wiki:node:retrieve",
		"wiki:space:read",
		"wiki:space:retrieve",
		"wiki:space:write_only",
		"contact:user.basic_profile:readonly",
		// Mail — lets lark-cli read/search/draft/send mail and manage folders.
		// Note: the bare "mail:user_mailbox" scope manages mailbox accounts
		// (create/modify/delete), which the agent does not need; the per-message
		// and folder scopes below cover reading and sending mail.
		"mail:user_mailbox.message:send",
		"mail:user_mailbox.message:readonly",
		"mail:user_mailbox.message:modify",
		"mail:user_mailbox.folder:read",
	}

	out := make([]feishuScopeRequirement, 0, len(tenant)+len(user))
	for _, scope := range tenant {
		out = append(out, feishuScopeRequirement{Type: "tenant", Name: scope})
	}
	for _, scope := range user {
		out = append(out, feishuScopeRequirement{Type: "user", Name: scope})
	}
	return out
}

func (p *feishuLocalPoster) checkFullPermissions(ctx context.Context) (*FeishuPermissionCheck, error) {
	token, err := p.tenantAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Scopes []struct {
				GrantStatus int    `json:"grant_status"`
				ScopeName   string `json:"scope_name"`
				ScopeType   string `json:"scope_type"`
			} `json:"scopes"`
		} `json:"data"`
	}
	if err := p.doJSON(ctx, "GET", "/open-apis/application/v6/scopes", token, nil, &resp); err != nil {
		return nil, err
	}
	if resp.Code != 0 {
		return nil, fmt.Errorf("query Feishu scopes error %d: %s", resp.Code, resp.Msg)
	}

	granted := make(map[feishuScopeRequirement]bool, len(resp.Data.Scopes))
	for _, scope := range resp.Data.Scopes {
		if scope.GrantStatus == 1 {
			granted[feishuScopeRequirement{Type: scope.ScopeType, Name: scope.ScopeName}] = true
		}
	}

	required := requiredFeishuFullPermissionScopes()
	missing := make([]string, 0)
	for _, scope := range required {
		if !granted[scope] {
			missing = append(missing, scope.Type+":"+scope.Name)
		}
	}
	return &FeishuPermissionCheck{
		Checked:       true,
		Ready:         len(missing) == 0,
		Missing:       missing,
		GrantedCount:  len(required) - len(missing),
		RequiredCount: len(required),
	}, nil
}
