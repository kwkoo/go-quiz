package internal

import "strings"

type ClientCommand struct {
	client uint64
	cmd    string
	arg    string
}

func NewClientCommand(client uint64, message []byte) *ClientCommand {
	cmd, arg := parseCommand(message)
	return &ClientCommand{
		client: client,
		cmd:    cmd,
		arg:    arg,
	}
}

func parseCommand(b []byte) (string, string) {
	s := strings.TrimSpace(string(b))
	space := strings.Index(s, " ")
	if space == -1 {
		return s, ""
	}
	return s[:space], strings.TrimSpace(s[space+1:])
}
