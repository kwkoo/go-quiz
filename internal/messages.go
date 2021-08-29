package internal

import "github.com/kwkoo/go-quiz/internal/common"

type ClientErrorMessage struct {
	client     *Client
	sessionid  string
	message    string
	nextscreen string
}

// this is used in both the client-hub and sessions-hub
type SetSessionIDForClientMessage struct {
	client    *Client
	sessionid string
}

type SessionToScreenMessage struct {
	sessionid  string
	nextscreen string
}

type ClientMessage struct {
	client  *Client
	message string
}

type AddPlayerToGameMessage struct {
	sessionid string
	name      string
	pin       int
}

type ErrorToSessionMessage struct {
	sessionid  string
	message    string
	nextscreen string
}

type BindGameToSessionMessage struct {
	sessionid string
	name      string
	pin       int
}

type SendQuizzesToClientMessage struct {
	client    *Client
	sessionid string
}

type SendGameMetadataMessage struct {
	client    *Client
	sessionid string
	pin       int
}

type SetSessionScreenMessage struct {
	sessionid  string
	nextscreen string
}

type SetSessionGamePinMessage struct {
	sessionid string
	pin       int
}

type HostShowQuestionMessage struct {
	client    *Client
	sessionid string
	pin       int
}

type HostShowGameResultsMessage struct {
	client    *Client
	sessionid string
	pin       int
}

type SessionMessage struct {
	sessionid string
	message   string
}

type QueryDisplayChoicesMessage struct {
	client    *Client
	sessionid string
	pin       int
}

type QueryPlayerResultsMessage struct {
	client    *Client
	sessionid string
	pin       int
}

type RegisterAnswerMessage struct {
	client    *Client
	sessionid string
	pin       int
	answer    int
}

type CancelGameMessage struct {
	client    *Client
	sessionid string
	pin       int
}

type DeregisterGameFromSessionsMessage struct {
	sessions []string
}

type HostGameLobbyMessage struct {
	client    *Client
	sessionid string
	quizid    int
}

type LookupQuizForGameMessage struct {
	client    *Client
	sessionid string
	quizid    int
	pin       int
}

type SetQuizForGameMessage struct {
	pin  int
	quiz common.Quiz
}

type StartGameMessage struct {
	client    *Client
	sessionid string
	pin       int
}

type ShowResultsMessage struct {
	client    *Client
	sessionid string
	pin       int
}

type QueryHostResultsMessage struct {
	client    *Client
	sessionid string
	pin       int
}

type NextQuestionMessage struct {
	client    *Client
	sessionid string
	pin       int
}

type DeleteGameMessage struct {
	client    *Client
	sessionid string
	pin       int
}
