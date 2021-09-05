package common

// --------------------
// Client Hub Messages
// --------------------

type ClientErrorMessage struct {
	Clientid   uint64
	Sessionid  string
	Message    string
	Nextscreen string
}

type ClientMessage struct {
	Clientid uint64
	Message  string
}

// --------------------
// Session Hub Messages
// --------------------

type SessionToScreenMessage struct {
	Sessionid  string
	Nextscreen string
}

type ErrorToSessionMessage struct {
	Sessionid  string
	Message    string
	Nextscreen string
}

type BindGameToSessionMessage struct {
	Sessionid string
	Name      string
	Pin       int
}

type SetSessionScreenMessage struct {
	Sessionid  string
	Nextscreen string
}

type SessionMessage struct {
	Sessionid string
	Message   string
}

type DeregisterGameFromSessionsMessage struct {
	Sessions []string
}

type SetSessionGamePinMessage struct {
	Sessionid string
	Pin       int
}

type ExtendSessionExpiryMessage struct {
	Sessionid string
}

type DeleteSessionMessage struct {
	Sessionid string
}

type DeregisterClientMessage struct {
	Clientid uint64
}

// --------------------
// Games Hub Messages
// --------------------

type AddPlayerToGameMessage struct {
	Sessionid string
	Name      string
	Pin       int
}

type SendGameMetadataMessage struct {
	Clientid  uint64
	Sessionid string
	Pin       int
}

type HostShowQuestionMessage struct {
	Clientid  uint64
	Sessionid string
	Pin       int
}

type HostShowGameResultsMessage struct {
	Clientid  uint64
	Sessionid string
	Pin       int
}

type QueryDisplayChoicesMessage struct {
	Clientid  uint64
	Sessionid string
	Pin       int
}

type QueryPlayerResultsMessage struct {
	Clientid  uint64
	Sessionid string
	Pin       int
}

type RegisterAnswerMessage struct {
	Clientid  uint64
	Sessionid string
	Pin       int
	Answer    int
}

type CancelGameMessage struct {
	Clientid  uint64
	Sessionid string
	Pin       int
}

type HostGameLobbyMessage struct {
	Clientid  uint64
	Sessionid string
	Quizid    int
}

type SetQuizForGameMessage struct {
	Pin  int
	Quiz Quiz
}

type StartGameMessage struct {
	Clientid  uint64
	Sessionid string
	Pin       int
}

type ShowResultsMessage struct {
	Clientid  uint64
	Sessionid string
	Pin       int
}

type QueryHostResultsMessage struct {
	Clientid  uint64
	Sessionid string
	Pin       int
}

type NextQuestionMessage struct {
	Clientid  uint64
	Sessionid string
	Pin       int
}

// used by frontend
type DeleteGameMessage struct {
	Clientid  uint64
	Sessionid string
	Pin       int
}

type UpdateGameMessage struct {
	Game
}

// used by REST API
type DeleteGameByPin struct {
	Pin int
}

// --------------------
// Quiz Messages
// --------------------

type SendQuizzesToClientMessage struct {
	Clientid  uint64
	Sessionid string
}

type LookupQuizForGameMessage struct {
	Clientid  uint64
	Sessionid string
	Quizid    int
	Pin       int
}

type DeleteQuizMessage struct {
	Quizid int
}

// --------------------
// REST API Messages
// --------------------

type GetQuizzesMessage struct {
	Result chan []Quiz
}

type GetQuizMessage struct {
	Quizid int
	Result chan GetQuizResult
}

type GetQuizResult struct {
	Quiz  Quiz
	Error error
}

type AddQuizMessage struct {
	Quiz   Quiz
	Result chan error
}

type UpdateQuizMessage struct {
	Quiz   Quiz
	Result chan error
}

type GetSessionsMessage struct {
	Result chan []Session
}

type GetSessionMessage struct {
	Sessionid string
	Result    chan *Session
}

type GetGamesMessage struct {
	Result chan []Game
}

type GetGameMessage struct {
	Pin    int
	Result chan GetGameResult
}

type GetGameResult struct {
	Game  Game
	Error error
}
