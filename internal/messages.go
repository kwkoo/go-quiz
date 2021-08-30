package internal

import "github.com/kwkoo/go-quiz/internal/common"

// --------------------
// Client Hub Messages
// --------------------

type ClientErrorMessage struct {
	client     *Client
	sessionid  string
	message    string
	nextscreen string
}

type ClientMessage struct {
	client  *Client
	message string
}

// this is used in both the client-hub and sessions-hub
type SetSessionIDForClientMessage struct {
	client    *Client
	sessionid string
}

// --------------------
// Session Hub Messages
// --------------------

type SessionToScreenMessage struct {
	sessionid  string
	nextscreen string
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

type SetSessionScreenMessage struct {
	sessionid  string
	nextscreen string
}

type SessionMessage struct {
	sessionid string
	message   string
}

type DeregisterGameFromSessionsMessage struct {
	sessions []string
}

type SetSessionGamePinMessage struct {
	sessionid string
	pin       int
}

type ExtendSessionExpiryMessage struct {
	sessionid string
}

type DeleteSessionMessage struct {
	sessionid string
}

// --------------------
// Games Hub Messages
// --------------------

type AddPlayerToGameMessage struct {
	sessionid string
	name      string
	pin       int
}

type SendGameMetadataMessage struct {
	client    *Client
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

type HostGameLobbyMessage struct {
	client    *Client
	sessionid string
	quizid    int
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

// used by frontend
type DeleteGameMessage struct {
	client    *Client
	sessionid string
	pin       int
}

type UpdateGameMessage struct {
	common.Game
}

// used by REST API
type DeleteGameByPin struct {
	pin int
}

// --------------------
// Quiz Messages
// --------------------

type SendQuizzesToClientMessage struct {
	client    *Client
	sessionid string
}

type LookupQuizForGameMessage struct {
	client    *Client
	sessionid string
	quizid    int
	pin       int
}

type DeleteQuizMessage struct {
	quizid int
}
