//go:build !darwin

package tools

import "context"

func (*BashTool) bashInvocation(_ context.Context, _ ToolCall, command string) (bashInvocation, error) {
	shell, args := shellInvocation(command)
	return bashInvocation{executable: shell, args: args}, nil
}
