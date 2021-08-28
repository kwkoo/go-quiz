package pkg

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"sync"
	"time"
)

const (
	// Game states:
	// * The game starts off as GameNotStarted
	// * When the host starts the game, the state shifts to QuestionInProgress
	// * When the time is up or if all players have answered, the state shifts
	//   to Show Results
	// * When the host advances to the next question, the state shifts back to
	//   QuestionInProgress
	// * When the time is up or if all players have answered, the state shifts
	//   to Show Results
	// * After all the questions have been answered and the last result is
	//   shown, the state shifts to GameEnded - the UI can then show the
	//   the results of the game (the winners list)
	// * After that, the game can be deleted
	//
	GameNotStarted     = iota
	QuestionInProgress = iota
	ShowResults        = iota
	GameEnded          = iota
)

const winnerCount = 5

type UnexpectedStateError struct {
	CurrentState int
	Err          error
}

func (e *UnexpectedStateError) Error() string {
	return fmt.Sprintf("game is in an unexpected state: %v", e.Err)
}

func NewUnexpectedStateError(state int, message string) *UnexpectedStateError {
	return &UnexpectedStateError{
		CurrentState: state,
		Err:          errors.New(message),
	}
}

type NoSuchGameError struct {
	Pin int
}

func (e *NoSuchGameError) Error() string {
	return fmt.Sprintf("game %d does not exist", e.Pin)
}

func NewNoSuchGameError(pin int) *NoSuchGameError {
	return &NoSuchGameError{
		Pin: pin,
	}
}

// Queried by the host - either when the host first displays the question or
// when the host reconnects
type GameCurrentQuestion struct {
	QuestionIndex  int      `json:"questionindex"`
	TimeLeft       int      `json:"timeleft"`
	Answered       int      `json:"answered"`     // number of players that have answered
	TotalPlayers   int      `json:"totalplayers"` // number of players in this game
	Question       string   `json:"question"`
	Answers        []string `json:"answers"`
	Votes          []int    `json:"votes"`
	TotalVotes     int      `json:"totalvotes"`
	TotalQuestions int      `json:"totalquestions"`
}

// To be sent to the host when a player answers a question
type AnswersUpdate struct {
	AllAnswered  bool  `json:"allanswered"`
	Answered     int   `json:"answered"`
	TotalPlayers int   `json:"totalplayers"`
	Votes        []int `json:"votes"`
	TotalVotes   int   `json:"totalvotes"`
}

type QuestionResults struct {
	QuestionIndex  int           `json:"questionindex"`
	Question       string        `json:"question"`
	Answers        []string      `json:"answers"`
	Correct        int           `json:"correct"`
	Votes          []int         `json:"votes"`
	TotalVotes     int           `json:"totalvotes"`
	TotalQuestions int           `json:"totalquestions"`
	TotalPlayers   int           `json:"totalplayers"`
	TopScorers     []PlayerScore `json:"topscorers"`
}

type PlayerScore struct {
	id    string
	Name  string `json:"name"`
	Score int    `json:"score"`
}

type PlayerScoreList []PlayerScore

func (p PlayerScoreList) Len() int           { return len(p) }
func (p PlayerScoreList) Less(i, j int) bool { return p[i].Score < p[j].Score }
func (p PlayerScoreList) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }

type Game struct {
	Pin              int            `json:"pin"`
	Host             string         `json:"host"`    // session ID of game host
	Players          map[string]int `json:"players"` // scores of players
	PlayerNames      map[string]string
	Quiz             Quiz                `json:"quiz"`
	QuestionIndex    int                 `json:"questionindex"`    // current question
	QuestionDeadline time.Time           `json:"questiondeadline"` // answers must come in at this time or before
	PlayersAnswered  map[string]struct{} `json:"playersanswered"`
	CorrectPlayers   map[string]struct{} `json:"correctplayers"` // players that answered current question correctly
	Votes            []int               `json:"votes"`          // number of players that answered each choice
	GameState        int                 `json:"gamestate"`
}

func unmarshalGame(b []byte) (*Game, error) {
	var game Game
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&game); err != nil {
		return nil, err
	}
	return &game, nil
}

func (g *Game) marshal() ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	if err := enc.Encode(g); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func (g *Game) copy() Game {
	target := Game{
		Pin:              g.Pin,
		Host:             g.Host,
		Players:          make(map[string]int),
		Quiz:             g.Quiz,
		QuestionIndex:    g.QuestionIndex,
		QuestionDeadline: g.QuestionDeadline,
		PlayersAnswered:  make(map[string]struct{}),
		CorrectPlayers:   make(map[string]struct{}),
		Votes:            []int{},
		GameState:        g.GameState,
	}

	for k, v := range g.Players {
		target.Players[k] = v
	}

	for k := range g.PlayersAnswered {
		target.PlayersAnswered[k] = struct{}{}
	}

	for k := range g.CorrectPlayers {
		target.CorrectPlayers[k] = struct{}{}
	}

	copy(target.Votes, g.Votes)

	return target
}

func (g *Game) setupQuestion(newIndex int) error {
	g.QuestionIndex = newIndex
	question, err := g.Quiz.GetQuestion(newIndex)
	if err != nil {
		return err
	}
	g.GameState = QuestionInProgress
	g.PlayersAnswered = make(map[string]struct{})
	g.CorrectPlayers = make(map[string]struct{})
	g.Votes = make([]int, question.NumAnswers())
	g.QuestionDeadline = time.Now().Add(time.Second * time.Duration(g.Quiz.QuestionDuration))
	return nil
}

func (g *Game) totalVotes() int {
	total := 0
	for _, v := range g.Votes {
		total += v
	}
	return total
}

func (g *Game) getPlayers() []string {
	players := make([]string, len(g.Players))

	i := 0
	for player := range g.Players {
		players[i] = player
		i++
	}
	return players
}

func (g *Game) getPlayerNames() []string {
	names := []string{}
	for _, v := range g.PlayerNames {
		names = append(names, v)
	}
	sort.Strings(names)
	return names
}

// Returns true if the player was added - false if the player is already in
// the game
func (g *Game) addPlayer(sessionid, name string) bool {
	if _, ok := g.Players[sessionid]; ok {
		// player is already in the game
		return false
	}

	// player is new in this game
	g.Players[sessionid] = 0
	g.PlayerNames[sessionid] = name
	log.Printf("added player %s to game %d", sessionid, g.Pin)
	return true
}

func (g *Game) setQuiz(quiz Quiz) {
	g.Quiz = quiz
}

func (g *Game) deletePlayer(sessionid string) {
	delete(g.Players, sessionid)
	delete(g.PlayersAnswered, sessionid)
	delete(g.CorrectPlayers, sessionid)
}

func (g *Game) nextState() (int, error) {
	switch g.GameState {
	case GameNotStarted:
		// if there are no questions or players, end the game immediately
		if g.Quiz.NumQuestions() == 0 || len(g.Players) == 0 {
			g.GameState = GameEnded
			return g.GameState, nil
		}
		if err := g.setupQuestion(0); err != nil {
			g.GameState = GameEnded
			return g.GameState, fmt.Errorf("error trying to start game: %v", err)
		}
		return g.GameState, nil

	case QuestionInProgress:
		g.GameState = ShowResults
		return g.GameState, nil

	case ShowResults:
		if g.QuestionIndex < g.Quiz.NumQuestions() {
			g.QuestionIndex++
		}
		if g.QuestionIndex >= g.Quiz.NumQuestions() {
			g.GameState = GameEnded
			return g.GameState, nil
		}
		if err := g.setupQuestion(g.QuestionIndex); err != nil {
			g.GameState = GameEnded
			return g.GameState, err
		}
		// setupQuestion() would have set the GameState to QuestionInProgress
		return g.GameState, nil

	default:
		g.GameState = GameEnded
		return g.GameState, nil
	}
}

func (g *Game) showResults() error {
	if g.GameState != QuestionInProgress && g.GameState != ShowResults {
		return NewUnexpectedStateError(g.GameState, fmt.Sprintf("game with pin %d is not in the expected state", g.Pin))
	}
	g.GameState = ShowResults
	return nil
}

// Returns true if state was changed
func (g *Game) getCurrentQuestion() (bool, GameCurrentQuestion, error) {
	if g.GameState != QuestionInProgress {
		return false, GameCurrentQuestion{}, NewUnexpectedStateError(g.GameState, fmt.Sprintf("game with pin %d is not showing a live question", g.Pin))
	}

	now := time.Now()
	timeLeft := int(g.QuestionDeadline.Unix() - now.Unix())
	if timeLeft <= 0 || len(g.PlayersAnswered) >= len(g.Players) {
		g.GameState = ShowResults
		return true, GameCurrentQuestion{}, NewUnexpectedStateError(ShowResults, fmt.Sprintf("game with pin %d should be showing results", g.Pin))
	}

	question, err := g.Quiz.GetQuestion(g.QuestionIndex)
	if err != nil {
		return false, GameCurrentQuestion{}, err
	}

	return false, GameCurrentQuestion{
		QuestionIndex:  g.QuestionIndex,
		TimeLeft:       timeLeft,
		Answered:       len(g.PlayersAnswered),
		TotalPlayers:   len(g.Players),
		Question:       question.Question,
		Answers:        question.Answers,
		Votes:          g.Votes,
		TotalVotes:     g.totalVotes(),
		TotalQuestions: g.Quiz.NumQuestions(),
	}, nil
}

// Returns true if changed
func (g *Game) registerAnswer(sessionid string, answerIndex int) (bool, AnswersUpdate, error) {
	if _, ok := g.Players[sessionid]; !ok {
		return false, AnswersUpdate{}, fmt.Errorf("player %s is not part of game %d", sessionid, g.Pin)
	}
	if g.GameState != QuestionInProgress {
		return false, AnswersUpdate{}, NewUnexpectedStateError(g.GameState, fmt.Sprintf("game %d is not showing a live question", g.Pin))
	}

	now := time.Now()
	if now.After(g.QuestionDeadline) {
		g.GameState = ShowResults
		return true, AnswersUpdate{}, NewUnexpectedStateError(ShowResults, fmt.Sprintf("question %d in game %d has expired", g.QuestionIndex, g.Pin))
	}

	question, err := g.Quiz.GetQuestion(g.QuestionIndex)
	if err != nil {
		return false, AnswersUpdate{}, err
	}

	if answerIndex < 0 || answerIndex >= question.NumAnswers() {
		return false, AnswersUpdate{}, errors.New("invalid answer")
	}

	if _, ok := g.PlayersAnswered[sessionid]; !ok {
		// player hasn't answered yet
		g.PlayersAnswered[sessionid] = struct{}{}

		if answerIndex == question.Correct {
			// calculate score, add to player score
			g.Players[sessionid] += calculateScore(int(g.QuestionDeadline.Unix()-now.Unix()), g.Quiz.QuestionDuration)
			g.CorrectPlayers[sessionid] = struct{}{}
		}
		g.Votes[answerIndex]++
	}

	answeredCount := len(g.PlayersAnswered)
	totalPlayers := len(g.Players)
	allAnswered := answeredCount >= totalPlayers
	if allAnswered {
		g.GameState = ShowResults
	}
	return true, AnswersUpdate{
		AllAnswered:  allAnswered,
		Answered:     answeredCount,
		TotalPlayers: totalPlayers,
		Votes:        g.Votes,
		TotalVotes:   g.totalVotes(),
	}, nil
}

func (g *Game) getQuestionResults() (QuestionResults, error) {
	question, err := g.Quiz.GetQuestion(g.QuestionIndex)
	if err != nil {
		return QuestionResults{}, err
	}
	results := QuestionResults{
		QuestionIndex:  g.QuestionIndex,
		Question:       question.Question,
		Answers:        question.Answers,
		Correct:        question.Correct,
		Votes:          g.Votes,
		TotalVotes:     g.totalVotes(),
		TotalQuestions: g.Quiz.NumQuestions(),
		TotalPlayers:   len(g.Players),
		TopScorers:     g.getWinners(),
	}

	return results, nil
}

func (g *Game) getWinners() []PlayerScore {
	// copied from https://stackoverflow.com/a/18695740
	pl := make(PlayerScoreList, len(g.Players))
	i := 0
	for k, v := range g.Players {
		pl[i] = PlayerScore{
			id:    k,
			Name:  g.PlayerNames[k],
			Score: v,
		}
		i++
	}
	sort.Sort(sort.Reverse(pl))

	max := len(pl)
	if max > winnerCount {
		max = winnerCount
	}
	return pl[:max]
}

func (g *Game) getGameState() int {
	return g.GameState
}

type Games struct {
	mutex  sync.RWMutex
	all    map[int]*Game // map key is the game pin
	engine *PersistenceEngine
	msghub *MessageHub
}

func InitGames(msghub *MessageHub, engine *PersistenceEngine) *Games {
	games := Games{
		all:    make(map[int]*Game),
		engine: engine,
		msghub: msghub,
	}

	if engine == nil {
		return &games
	}

	keys, err := engine.GetKeys("game")
	if err != nil {
		log.Printf("error retrieving game keys from persistent store: %v", err)
		return &games
	}

	for _, key := range keys {
		data, err := engine.Get(key)
		if err != nil {
			log.Printf("error trying to retrieve game %s from persistent store: %v", key, err)
			continue
		}
		game, err := unmarshalGame(data)
		if err != nil {
			log.Printf("error trying to unmarshal game %s from persistent store: %v", key, err)
			continue
		}
		games.all[game.Pin] = game
	}

	return &games
}

func (g *Games) Run(shutdownChan chan struct{}) {
	gamesHub := g.msghub.GetTopic(gamesTopic)

	for {
		select {
		case msg, ok := <-gamesHub:
			if !ok {
				log.Print("message received from from-clients is not *ClientCommand")
				continue
			}
			if g.processAddPlayerToGameMessage(msg) {
				continue
			}
			if g.processSendGameMetadataMessage(msg) {
				continue
			}
			if g.processHostShowQuestionMessage(msg) {
				continue
			}
			if g.processHostShowGameResultsMessage(msg) {
				continue
			}
			if g.processQueryDisplayChoicesMessage(msg) {
				continue
			}
			if g.processQueryPlayerResultsMessage(msg) {
				continue
			}
			if g.processRegisterAnswerMessage(msg) {
				continue
			}
			if g.processCancelGameMessage(msg) {
				continue
			}
			if g.processHostGameLobbyMessage(msg) {
				continue
			}
			if g.processSetQuizForGameMessage(msg) {
				continue
			}
			if g.processStartGameMessage(msg) {
				continue
			}
			if g.processShowResultsMessage(msg) {
				continue
			}
			if g.processQueryHostResultsMessage(msg) {
				continue
			}
			if g.processNextQuestionMessage(msg) {
				continue
			}
			if g.processDeleteGameMessage(msg) {
				continue
			}
		case <-shutdownChan:
			g.msghub.NotifyShutdownComplete()
			return
		}
	}
}

func (g *Games) processDeleteGameMessage(message interface{}) bool {
	msg, ok := message.(DeleteGameMessage)
	if !ok {
		return false
	}

	_, ok = g.ensureUserIsGameHost(msg.client, msg.sessionid, msg.pin)
	if !ok {
		return true
	}

	g.Delete(msg.pin)
	g.msghub.Send(sessionsTopic, SetSessionGamePinMessage{
		sessionid: msg.sessionid,
		pin:       -1,
	})

	g.msghub.Send(sessionsTopic, SessionToScreenMessage{
		sessionid:  msg.sessionid,
		nextscreen: "host-select-quiz",
	})

	return true
}

func (g *Games) processNextQuestionMessage(message interface{}) bool {
	msg, ok := message.(NextQuestionMessage)
	if !ok {
		return false
	}

	game, ok := g.ensureUserIsGameHost(msg.client, msg.sessionid, msg.pin)
	if !ok {
		return true
	}

	gameState, err := g.NextState(game.Pin)
	if err != nil {
		g.msghub.Send(sessionsTopic, SetSessionGamePinMessage{
			sessionid: msg.sessionid,
			pin:       -1,
		})
		if _, ok := err.(*NoSuchGameError); ok {
			g.msghub.Send(sessionsTopic, ErrorToSessionMessage{
				sessionid:  msg.sessionid,
				message:    err.Error(),
				nextscreen: "entrance",
			})
			return true
		}
		g.msghub.Send(sessionsTopic, ErrorToSessionMessage{
			sessionid:  msg.sessionid,
			message:    "error setting game to next state: " + err.Error(),
			nextscreen: "host-select-quiz",
		})
		return true
	}

	if gameState == QuestionInProgress {
		g.msghub.Send(sessionsTopic, SessionToScreenMessage{
			sessionid:  msg.sessionid,
			nextscreen: "host-show-question",
		})

		g.sendGamePlayersToAnswerQuestionScreen(msg.sessionid, game)
		return true
	}

	// assume that game has ended
	g.msghub.Send(sessionsTopic, SessionToScreenMessage{
		sessionid:  msg.sessionid,
		nextscreen: "host-show-game-results",
	})

	players := game.getPlayers()
	g.msghub.Send(sessionsTopic, DeregisterGameFromSessionsMessage{
		sessions: players,
	})

	for _, playerid := range players {
		g.msghub.Send(sessionsTopic, SessionToScreenMessage{
			sessionid:  playerid,
			nextscreen: "entrance",
		})
	}
	return true
}

func (g *Games) processQueryHostResultsMessage(message interface{}) bool {
	msg, ok := message.(QueryHostResultsMessage)
	if !ok {
		return false
	}

	g.sendQuestionResultsToHost(msg.client, msg.sessionid, msg.pin)
	return true
}

// returns ok if successful
func (g *Games) sendQuestionResultsToHost(client *Client, sessionid string, pin int) (Game, bool) {
	game, ok := g.ensureUserIsGameHost(client, sessionid, pin)
	if !ok {
		return Game{}, false
	}

	if err := g.ShowResults(pin); err != nil {
		g.msghub.Send(sessionsTopic, ErrorToSessionMessage{
			sessionid:  sessionid,
			message:    fmt.Sprintf("error moving game to show results state: %v", err),
			nextscreen: "",
		})
		return Game{}, false
	}

	results, err := g.GetQuestionResults(pin)
	if err != nil {
		g.msghub.Send(sessionsTopic, ErrorToSessionMessage{
			sessionid:  sessionid,
			message:    fmt.Sprintf("error getting question results: %v", err),
			nextscreen: "",
		})
		return Game{}, false
	}

	encoded, err := convertToJSON(&results)
	if err != nil {
		g.msghub.Send(sessionsTopic, ErrorToSessionMessage{
			sessionid:  sessionid,
			message:    fmt.Sprintf("error converting question results payload to JSON: %v", err),
			nextscreen: "",
		})
		return Game{}, false
	}

	g.msghub.Send(clientHubTopic, ClientMessage{
		client:  client,
		message: "question-results " + encoded,
	})

	return game, true
}

func (g *Games) sendGamePlayersToAnswerQuestionScreen(sessionid string, game Game) {
	question, err := game.Quiz.GetQuestion(game.QuestionIndex)
	if err != nil {
		g.msghub.Send(sessionsTopic, ErrorToSessionMessage{
			sessionid:  sessionid,
			message:    fmt.Sprintf("error getting question: %v", err),
			nextscreen: "",
		})
		return
	}
	answerCount := len(question.Answers)
	for pid := range game.Players {
		g.msghub.Send(sessionsTopic, SessionMessage{
			sessionid: pid,
			message:   fmt.Sprintf("display-choices %d", answerCount),
		})
		g.msghub.Send(sessionsTopic, SessionToScreenMessage{
			sessionid:  pid,
			nextscreen: "answer-question",
		})
	}
}

func (g *Games) processShowResultsMessage(message interface{}) bool {
	msg, ok := message.(ShowResultsMessage)
	if !ok {
		return false
	}

	game, ok := g.sendQuestionResultsToHost(msg.client, msg.sessionid, msg.pin)
	if !ok {
		return true
	}

	g.msghub.Send(sessionsTopic, SessionToScreenMessage{
		sessionid:  msg.sessionid,
		nextscreen: "host-show-results",
	})

	playerResults := struct {
		Correct bool `json:"correct"`
		Score   int  `json:"score"`
	}{}

	for pid, score := range game.Players {
		_, playerCorrect := game.CorrectPlayers[pid]
		playerResults.Correct = playerCorrect
		playerResults.Score = score

		// we're doing this here to set the state for disconnected players
		g.msghub.Send(sessionsTopic, SessionToScreenMessage{
			sessionid:  pid,
			nextscreen: "display-player-results",
		})

		encoded, err := convertToJSON(&playerResults)
		if err != nil {
			log.Printf("error converting player-results payload to JSON: %v", err)
			continue
		}
		g.msghub.Send(sessionsTopic, SessionMessage{
			sessionid: pid,
			message:   "player-results " + encoded,
		})
	}

	return true
}

// returns true if successful (treat it as an ok flag)
func (g *Games) ensureUserIsGameHost(client *Client, sessionid string, pin int) (Game, bool) {
	game, err := g.Get(pin)
	if err != nil {
		g.msghub.Send(sessionsTopic, SetSessionGamePinMessage{
			sessionid: sessionid,
			pin:       -1,
		})

		if _, ok := err.(*NoSuchGameError); ok {
			g.msghub.Send(sessionsTopic, ErrorToSessionMessage{
				sessionid:  sessionid,
				message:    err.Error(),
				nextscreen: "entrance",
			})
			return Game{}, false
		}

		g.msghub.Send(sessionsTopic, ErrorToSessionMessage{
			sessionid:  sessionid,
			message:    "error fetching game: " + err.Error(),
			nextscreen: "entrance",
		})

		return Game{}, false
	}

	if sessionid != game.Host {
		g.msghub.Send(sessionsTopic, ErrorToSessionMessage{
			sessionid:  sessionid,
			message:    "you are not the host of the game",
			nextscreen: "entrance",
		})
		return Game{}, false
	}

	return game, true
}

func (g *Games) processStartGameMessage(message interface{}) bool {
	msg, ok := message.(StartGameMessage)
	if !ok {
		return false
	}

	game, ok := g.ensureUserIsGameHost(msg.client, msg.sessionid, msg.pin)
	if !ok {
		return true
	}

	gameState, err := g.NextState(game.Pin)
	if err != nil {
		g.msghub.Send(sessionsTopic, ErrorToSessionMessage{
			sessionid:  msg.sessionid,
			message:    "error starting game: " + err.Error(),
			nextscreen: "host-select-quiz",
		})
		return true
	}
	if gameState != QuestionInProgress {
		if gameState == ShowResults {
			g.msghub.Send(gamesTopic, ShowResultsMessage(msg))
			return true
		}
		if gameState == GameEnded {
			g.msghub.Send(sessionsTopic, SessionToScreenMessage{
				sessionid:  msg.sessionid,
				nextscreen: "host-select-quiz",
			})
			return true
		}

		g.msghub.Send(sessionsTopic, ErrorToSessionMessage{
			sessionid:  msg.sessionid,
			message:    fmt.Sprintf("game was not in an expected state: %d", gameState),
			nextscreen: "",
		})
		return true
	}

	g.msghub.Send(sessionsTopic, SessionToScreenMessage{
		sessionid:  msg.sessionid,
		nextscreen: "host-show-question",
	})

	g.sendGamePlayersToAnswerQuestionScreen(msg.sessionid, game)

	return true
}

func (g *Games) processSetQuizForGameMessage(message interface{}) bool {
	msg, ok := message.(SetQuizForGameMessage)
	if !ok {
		return false
	}

	g.SetGameQuiz(msg.pin, msg.quiz)
	return true
}

func (g *Games) processHostGameLobbyMessage(message interface{}) bool {
	msg, ok := message.(HostGameLobbyMessage)
	if !ok {
		return false
	}

	// create new game
	pin, err := g.Add(msg.sessionid)
	if err != nil {
		g.msghub.Send(sessionsTopic, ErrorToSessionMessage{
			sessionid:  msg.sessionid,
			message:    "could not add game: " + err.Error(),
			nextscreen: "host-select-quiz",
		})
		log.Printf("could not add game: " + err.Error())
		return true
	}

	g.msghub.Send(sessionsTopic, SetSessionGamePinMessage{
		sessionid: msg.sessionid,
		pin:       pin,
	})

	g.msghub.Send(quizzesTopic, LookupQuizForGameMessage{
		client:    msg.client,
		sessionid: msg.sessionid,
		quizid:    msg.quizid,
		pin:       pin,
	})
	return true
}

func (g *Games) processCancelGameMessage(message interface{}) bool {
	msg, ok := message.(CancelGameMessage)
	if !ok {
		return false
	}

	game, ok := g.ensureUserIsGameHost(msg.client, msg.sessionid, msg.pin)
	if !ok {
		return true
	}

	players := game.getPlayers()
	players = append(players, game.Host)
	g.msghub.Send(sessionsTopic, DeregisterGameFromSessionsMessage{
		sessions: players,
	})

	for _, playerid := range players {
		g.msghub.Send(sessionsTopic, SessionToScreenMessage{
			sessionid:  playerid,
			nextscreen: "entrance",
		})
	}

	g.Delete(game.Pin)

	return true
}

func (g *Games) processRegisterAnswerMessage(message interface{}) bool {
	msg, ok := message.(RegisterAnswerMessage)
	if !ok {
		return false
	}

	answersUpdate, err := g.RegisterAnswer(msg.pin, msg.sessionid, msg.answer)
	if err != nil {
		g.msghub.Send(sessionsTopic, SetSessionGamePinMessage{
			sessionid: msg.sessionid,
			pin:       -1,
		})

		if _, ok := err.(*NoSuchGameError); ok {
			g.msghub.Send(sessionsTopic, ErrorToSessionMessage{
				sessionid:  msg.sessionid,
				message:    err.Error(),
				nextscreen: "entrance",
			})
			return true
		}

		if errState, ok := err.(*UnexpectedStateError); ok {
			switch errState.CurrentState {
			case GameNotStarted:
				g.msghub.Send(sessionsTopic, SessionToScreenMessage{
					sessionid:  msg.sessionid,
					nextscreen: "wait-for-game-start",
				})

			case ShowResults:
				g.msghub.Send(sessionsTopic, SessionToScreenMessage{
					sessionid:  msg.sessionid,
					nextscreen: "display-player-results",
				})

			default:
				g.msghub.Send(sessionsTopic, SessionToScreenMessage{
					sessionid:  msg.sessionid,
					nextscreen: "entrance",
				})
			}
			return true
		}

		g.msghub.Send(sessionsTopic, ErrorToSessionMessage{
			sessionid:  msg.sessionid,
			message:    "error registering answer: " + err.Error(),
			nextscreen: "",
		})
		return true
	}

	// send this player to wait for question to end screen
	g.msghub.Send(sessionsTopic, SessionToScreenMessage{
		sessionid:  msg.sessionid,
		nextscreen: "wait-for-question-end",
	})

	encoded, err := convertToJSON(&answersUpdate)
	if err != nil {
		log.Printf("error converting players-answered payload to JSON: %v", err)
		return true
	}

	game, err := g.Get(msg.pin)
	if err != nil {
		log.Printf("could not retrieve game %d: %v", msg.pin, err)
		return true
	}
	host := game.Host
	if host == "" {
		return true
	}

	g.msghub.Send(sessionsTopic, SessionMessage{
		sessionid: host,
		message:   "players-answered " + encoded,
	})

	return true
}

// player may have been disconnected - now they need to know about
// their results
func (g *Games) processQueryPlayerResultsMessage(message interface{}) bool {
	msg, ok := message.(QueryPlayerResultsMessage)
	if !ok {
		return false
	}

	game, err := g.Get(msg.pin)
	if err != nil {
		g.msghub.Send(sessionsTopic, SetSessionGamePinMessage{
			sessionid: msg.sessionid,
			pin:       -1,
		})

		if _, ok := err.(*NoSuchGameError); ok {
			g.msghub.Send(sessionsTopic, ErrorToSessionMessage{
				sessionid:  msg.sessionid,
				message:    err.Error(),
				nextscreen: "entrance",
			})
			return true
		}

		g.msghub.Send(sessionsTopic, ErrorToSessionMessage{
			sessionid:  msg.sessionid,
			message:    "error fetching game: " + err.Error(),
			nextscreen: "entrance",
		})

		return true
	}

	_, correct := game.CorrectPlayers[msg.sessionid]
	score, ok := game.Players[msg.sessionid]
	if !ok {
		g.msghub.Send(sessionsTopic, SetSessionGamePinMessage{
			sessionid: msg.sessionid,
			pin:       -1,
		})
		g.msghub.Send(sessionsTopic, ErrorToSessionMessage{
			sessionid:  msg.sessionid,
			message:    "you do not have a score in this game",
			nextscreen: "entrance",
		})
		return true
	}

	playerResults := struct {
		Correct bool `json:"correct"`
		Score   int  `json:"score"`
	}{
		Correct: correct,
		Score:   score,
	}

	encoded, err := convertToJSON(&playerResults)
	if err != nil {
		log.Printf("error converting player-results payload to JSON: %v", err)
		return true
	}

	g.msghub.Send(clientHubTopic, ClientMessage{
		client:  msg.client,
		message: "player-results " + encoded,
	})

	return true
}

// player may have been disconnected - now they need to know how many
// answers to enable
func (g *Games) processQueryDisplayChoicesMessage(message interface{}) bool {
	msg, ok := message.(QueryDisplayChoicesMessage)
	if !ok {
		return false
	}

	currentQuestion, err := g.GetCurrentQuestion(msg.pin)
	if err != nil {
		g.msghub.Send(sessionsTopic, SetSessionGamePinMessage{
			sessionid: msg.sessionid,
			pin:       -1,
		})

		if _, ok := err.(*NoSuchGameError); ok {
			g.msghub.Send(sessionsTopic, ErrorToSessionMessage{
				sessionid:  msg.sessionid,
				message:    err.Error(),
				nextscreen: "entrance",
			})
			return true
		}

		g.msghub.Send(sessionsTopic, ErrorToSessionMessage{
			sessionid:  msg.sessionid,
			message:    "error retrieving current question: " + err.Error(),
			nextscreen: "",
		})
		return true
	}

	g.msghub.Send(clientHubTopic, ClientMessage{
		client:  msg.client,
		message: fmt.Sprintf("display-choices %d", len(currentQuestion.Answers)),
	})

	return true
}

func (g *Games) processHostShowGameResultsMessage(message interface{}) bool {
	msg, ok := message.(HostShowGameResultsMessage)
	if !ok {
		return false
	}

	winners, err := g.GetWinners(msg.pin)
	if err != nil {
		g.msghub.Send(sessionsTopic, ErrorToSessionMessage{
			sessionid:  msg.sessionid,
			message:    "error retrieving game winners: " + err.Error(),
			nextscreen: "",
		})

		return true
	}

	encoded, err := convertToJSON(&winners)
	if err != nil {
		g.msghub.Send(sessionsTopic, ErrorToSessionMessage{
			sessionid:  msg.sessionid,
			message:    "error converting show-winners payload to JSON: " + err.Error(),
			nextscreen: "",
		})
		return true
	}
	log.Printf("winners for game %d: %s", msg.pin, encoded)

	g.msghub.Send(clientHubTopic, ClientMessage{
		client:  msg.client,
		message: "show-winners " + encoded,
	})

	return true
}

func (g *Games) processHostShowQuestionMessage(message interface{}) bool {
	msg, ok := message.(HostShowQuestionMessage)
	if !ok {
		return false
	}

	currentQuestion, err := g.GetCurrentQuestion(msg.pin)
	if err != nil {
		// if the host disconnected while the question was live, and if
		// the game state has now changed, we may need to move the host to
		// the relevant screen
		unexpectedState, ok := err.(*UnexpectedStateError)
		if ok && unexpectedState.CurrentState == ShowResults {
			g.msghub.Send(sessionsTopic, SessionToScreenMessage{
				sessionid:  msg.sessionid,
				nextscreen: "show-results",
			})
			return true
		}

		g.msghub.Send(sessionsTopic, ErrorToSessionMessage{
			sessionid:  msg.sessionid,
			message:    "error retrieving question: " + err.Error(),
			nextscreen: "",
		})
		return true
	}

	encoded, err := convertToJSON(&currentQuestion)
	if err != nil {
		g.msghub.Send(sessionsTopic, ErrorToSessionMessage{
			sessionid:  msg.sessionid,
			message:    "error converting question to JSON: " + err.Error(),
			nextscreen: "",
		})
		return true
	}

	g.msghub.Send(clientHubTopic, ClientMessage{
		client:  msg.client,
		message: "host-show-question " + encoded,
	})

	return true
}

func (g *Games) processSendGameMetadataMessage(message interface{}) bool {
	msg, ok := message.(SendGameMetadataMessage)
	if !ok {
		return false
	}
	game, err := g.Get(msg.pin)
	if err != nil {
		g.msghub.Send(sessionsTopic, ErrorToSessionMessage{
			sessionid:  msg.sessionid,
			message:    fmt.Sprintf("could not retrieve game %d", msg.pin),
			nextscreen: "entrance",
		})
		return true
	}

	// send over game object with lobby-game-metadata
	gameMetadata := struct {
		Pin     int      `json:"pin"`
		Name    string   `json:"name"`
		Host    string   `json:"host"`
		Players []string `json:"players"`
	}{
		Pin:     game.Pin,
		Name:    game.Quiz.Name,
		Host:    game.Host,
		Players: game.getPlayerNames(),
	}

	encoded, err := convertToJSON(&gameMetadata)
	if err != nil {
		g.msghub.Send(sessionsTopic, ErrorToSessionMessage{
			sessionid:  msg.sessionid,
			message:    "error converting lobby-game-metadata payload to JSON: " + err.Error(),
			nextscreen: "",
		})
		return true
	}

	g.msghub.Send(clientHubTopic, ClientMessage{
		client:  msg.client,
		message: "lobby-game-metadata " + encoded,
	})

	return true
}

// returns true if processed
func (g *Games) processAddPlayerToGameMessage(message interface{}) bool {
	msg, ok := message.(AddPlayerToGameMessage)
	if !ok {
		return false
	}
	if err := g.AddPlayerToGame(msg); err != nil {
		g.msghub.Send(sessionsTopic, ErrorToSessionMessage{
			sessionid:  msg.sessionid,
			message:    "could not add player to game: " + err.Error(),
			nextscreen: "entrance",
		})
		return true
	}

	// inform game host of new player
	game, err := g.Get(msg.pin)
	if err != nil {
		log.Printf("could not retrieve game %d: %v", msg.pin, err)
		return true
	}
	host := game.Host
	if host == "" {
		return true
	}
	players := game.getPlayerNames()
	encoded, err := convertToJSON(&players)

	if err != nil {
		log.Printf("error encoding player names: %v", err)
		return true
	}

	g.msghub.Send(sessionsTopic, SessionMessage{
		sessionid: host,
		message:   "participants-list " + encoded,
	})

	return true
}

func (g *Games) persist(game *Game) {
	if g.engine == nil {
		return
	}
	data, err := game.marshal()
	if err != nil {
		log.Printf("error trying to convert game %d to JSON: %v", game.Pin, err)
		return
	}
	if err := g.engine.Set(fmt.Sprintf("game:%d", game.Pin), data, 0); err != nil {
		log.Printf("error trying to persist game %d: %v", game.Pin, err)
	}
}

func (g *Games) getAll() []Game {
	keys, err := g.engine.GetKeys("game")
	if err != nil {
		log.Printf("error getting all game keys from persistent store: %v", err)
		return nil
	}
	all := []Game{}
	for _, key := range keys {
		key = key[len("game:"):]
		keyInt, err := strconv.Atoi(key)
		if err != nil {
			log.Printf("could not convert game key %s to int: %v", key[len("game:"):], err)
			continue
		}
		game, err := g.Get(keyInt)
		if err != nil {
			log.Print(err.Error())
			continue
		}
		all = append(all, game)
	}
	return all
}

func (g *Games) Add(host string) (int, error) {
	game := Game{
		Host:            host,
		Players:         make(map[string]int),
		PlayersAnswered: make(map[string]struct{}),
	}

	for i := 0; i < 5; i++ {
		pin := generatePin()
		if exists, _ := g.getGamePointer(pin); exists != nil {
			continue
		}
		game.Pin = pin
		g.mutex.Lock()
		g.all[pin] = &game
		g.mutex.Unlock()
		g.persist(&game)
		return pin, nil
	}
	return 0, errors.New("could not generate unique game pin")
}

func generatePin() int {
	b := make([]byte, 4)
	rand.Read(b)

	total := int(b[0]) + int(b[1]) + int(b[2]) + int(b[3])
	total = total % 998
	total++
	return total
}

func (g *Games) getGamePointer(pin int) (*Game, error) {
	g.mutex.RLock()
	game, ok := g.all[pin]
	g.mutex.RUnlock()

	if ok {
		return game, nil
	}

	if g.engine == nil {
		return nil, NewNoSuchGameError(pin)
	}

	// game doesn't exist in memory - see if it's in the persistent store
	data, err := g.engine.Get(fmt.Sprintf("game:%d", pin))
	if err != nil {
		return nil, NewNoSuchGameError(pin)
	}
	game, err = unmarshalGame(data)
	if err != nil {
		return nil, fmt.Errorf("could not retrieve game %d from persistent store: %v", pin, err)
	}

	g.mutex.Lock()
	g.all[pin] = game
	g.mutex.Unlock()

	return game, nil
}

func (g *Games) Get(pin int) (Game, error) {
	gp, err := g.getGamePointer(pin)
	if err != nil {
		return Game{}, err
	}

	return gp.copy(), nil
}

func (g *Games) GetHostForGame(pin int) string {
	game, err := g.getGamePointer(pin)
	if err != nil {
		return ""
	}
	return game.Host
}

func (g *Games) GetPlayersForGame(pin int) []string {
	game, err := g.Get(pin)
	if err != nil {
		return []string{}
	}
	return game.getPlayers()
}

func (g *Games) Update(game Game) error {
	p := &game

	g.mutex.Lock()
	g.all[game.Pin] = p
	g.mutex.Unlock()

	g.persist(p)

	return nil
}

func (g *Games) Delete(pin int) {
	g.mutex.Lock()
	delete(g.all, pin)
	g.mutex.Unlock()

	if g.engine != nil {
		g.engine.Delete(fmt.Sprintf("game:%d", pin))
	}

}

func (g *Games) AddPlayerToGame(msg AddPlayerToGameMessage) error {
	game, err := g.getGamePointer(msg.pin)
	if err != nil {
		return NewNoSuchGameError(msg.pin)
	}

	if game.GameState != GameNotStarted {
		return errors.New("game is not accepting new players")
	}

	g.mutex.Lock()
	changed := game.addPlayer(msg.sessionid, msg.name)
	g.mutex.Unlock()
	if changed {
		g.persist(game)
	}
	return nil
}

func (g *Games) SetGameQuiz(pin int, quiz Quiz) {
	game, err := g.getGamePointer(pin)
	if err != nil {
		return
	}

	g.mutex.Lock()
	game.setQuiz(quiz)
	g.all[pin] = game // this is redundant
	g.mutex.Unlock()

	g.persist(game)
}

func (g *Games) DeletePlayerFromGame(sessionid string, pin int) {
	game, err := g.getGamePointer(pin)
	if err != nil {
		return
	}

	g.mutex.Lock()
	game.deletePlayer(sessionid)
	g.all[pin] = game // this is redundant
	g.mutex.Unlock()

	g.persist(game)
}

// Advances the game state to the next state - returns the new state
func (g *Games) NextState(pin int) (int, error) {
	game, err := g.getGamePointer(pin)
	if err != nil {
		return 0, NewNoSuchGameError(pin)
	}

	g.mutex.Lock()
	state, err := game.nextState()
	g.mutex.Unlock()
	g.persist(game)
	return state, err
}

// A special instance of NextState() - if we are in the QuestionInProgress
// state, change the state to ShowResults.
// If we are already in ShowResults, do not change the state.
func (g *Games) ShowResults(pin int) error {
	game, err := g.getGamePointer(pin)
	if err != nil {
		return NewNoSuchGameError(pin)
	}

	g.mutex.Lock()
	err = game.showResults()
	g.mutex.Unlock()
	if err == nil {
		g.persist(game)
	}
	return err
}

// Returns - questionIndex, number of seconds left, question, error
func (g *Games) GetCurrentQuestion(pin int) (GameCurrentQuestion, error) {
	game, err := g.getGamePointer(pin)
	if err != nil {
		return GameCurrentQuestion{}, NewNoSuchGameError(pin)
	}

	g.mutex.Lock()
	changed, currentQuestion, err := game.getCurrentQuestion()
	g.mutex.Unlock()
	if changed {
		g.persist(game)
	}

	return currentQuestion, err
}

func (g *Games) RegisterAnswer(pin int, sessionid string, answerIndex int) (AnswersUpdate, error) {
	game, err := g.getGamePointer(pin)
	if err != nil {
		return AnswersUpdate{}, NewNoSuchGameError(pin)
	}

	g.mutex.Lock()
	changed, update, err := game.registerAnswer(sessionid, answerIndex)
	g.mutex.Unlock()
	if changed {
		g.persist(game)
	}
	return update, err
}

func (g *Games) GetQuestionResults(pin int) (QuestionResults, error) {
	game, err := g.getGamePointer(pin)
	if err != nil {
		return QuestionResults{}, NewNoSuchGameError(pin)
	}

	g.mutex.RLock()
	defer g.mutex.RUnlock()
	return game.getQuestionResults()
}

func (g *Games) GetWinners(pin int) ([]PlayerScore, error) {
	game, err := g.getGamePointer(pin)
	if err != nil {
		return []PlayerScore{}, NewNoSuchGameError(pin)
	}

	g.mutex.RLock()
	defer g.mutex.RUnlock()
	return game.getWinners(), nil
}

func (g *Games) GetGameState(pin int) (int, error) {
	game, err := g.getGamePointer(pin)
	if err != nil {
		return 0, NewNoSuchGameError(pin)
	}

	return game.getGameState(), nil
}

func calculateScore(timeLeft, questionDuration int) int {
	if timeLeft < 0 {
		timeLeft = 0
	}
	return 100 + (timeLeft * 100 / questionDuration)
}
