package internal

import (
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"strconv"
	"sync"

	"github.com/kwkoo/go-quiz/internal/common"
	"github.com/kwkoo/go-quiz/internal/messaging"
	"github.com/kwkoo/go-quiz/internal/shutdown"
)

type Games struct {
	mutex  sync.RWMutex
	all    map[int]*common.Game // map key is the game pin
	engine *PersistenceEngine
	msghub *messaging.MessageHub
}

func InitGames(msghub *messaging.MessageHub, engine *PersistenceEngine) *Games {
	games := Games{
		all:    make(map[int]*common.Game),
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
		game, err := common.UnmarshalGame(data)
		if err != nil {
			log.Printf("error trying to unmarshal game %s from persistent store: %v", key, err)
			continue
		}
		games.all[game.Pin] = game
	}

	return &games
}

func (g *Games) Run() {
	shutdownChan := shutdown.GetShutdownChan()
	gamesHub := g.msghub.GetTopic(messaging.GamesTopic)

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
			if g.processUpdateGameMessage(msg) {
				continue
			}
			if g.processDeleteGameByPin(msg) {
				continue
			}
		case <-shutdownChan:
			log.Print("shutting down games handler")
			shutdown.NotifyShutdownComplete()
			return
		}
	}
}

func (g *Games) processDeleteGameByPin(message interface{}) bool {
	msg, ok := message.(DeleteGameByPin)
	if !ok {
		return false
	}

	g.delete(msg.pin)
	return true
}

func (g *Games) processUpdateGameMessage(message interface{}) bool {
	msg, ok := message.(UpdateGameMessage)
	if !ok {
		return false
	}

	g.update(msg.Game)

	return true
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

	g.delete(msg.pin)
	g.msghub.Send(messaging.SessionsTopic, SetSessionGamePinMessage{
		sessionid: msg.sessionid,
		pin:       -1,
	})

	g.msghub.Send(messaging.SessionsTopic, SessionToScreenMessage{
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

	gameState, err := g.nextState(game.Pin)
	if err != nil {
		g.msghub.Send(messaging.SessionsTopic, SetSessionGamePinMessage{
			sessionid: msg.sessionid,
			pin:       -1,
		})
		if _, ok := err.(*common.NoSuchGameError); ok {
			g.msghub.Send(messaging.SessionsTopic, ErrorToSessionMessage{
				sessionid:  msg.sessionid,
				message:    err.Error(),
				nextscreen: "entrance",
			})
			return true
		}
		g.msghub.Send(messaging.SessionsTopic, ErrorToSessionMessage{
			sessionid:  msg.sessionid,
			message:    "error setting game to next state: " + err.Error(),
			nextscreen: "host-select-quiz",
		})
		return true
	}

	if gameState == common.QuestionInProgress {
		g.msghub.Send(messaging.SessionsTopic, SessionToScreenMessage{
			sessionid:  msg.sessionid,
			nextscreen: "host-show-question",
		})

		g.sendGamePlayersToAnswerQuestionScreen(msg.sessionid, game)
		return true
	}

	// assume that game has ended
	g.msghub.Send(messaging.SessionsTopic, SessionToScreenMessage{
		sessionid:  msg.sessionid,
		nextscreen: "host-show-game-results",
	})

	players := game.GetPlayers()
	g.msghub.Send(messaging.SessionsTopic, DeregisterGameFromSessionsMessage{
		sessions: players,
	})

	for _, playerid := range players {
		g.msghub.Send(messaging.SessionsTopic, SessionToScreenMessage{
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
func (g *Games) sendQuestionResultsToHost(client *Client, sessionid string, pin int) (common.Game, bool) {
	game, ok := g.ensureUserIsGameHost(client, sessionid, pin)
	if !ok {
		return common.Game{}, false
	}

	if err := g.showResults(pin); err != nil {
		g.msghub.Send(messaging.SessionsTopic, ErrorToSessionMessage{
			sessionid:  sessionid,
			message:    fmt.Sprintf("error moving game to show results state: %v", err),
			nextscreen: "",
		})
		return common.Game{}, false
	}

	results, err := g.getQuestionResults(pin)
	if err != nil {
		g.msghub.Send(messaging.SessionsTopic, ErrorToSessionMessage{
			sessionid:  sessionid,
			message:    fmt.Sprintf("error getting question results: %v", err),
			nextscreen: "",
		})
		return common.Game{}, false
	}

	encoded, err := common.ConvertToJSON(&results)
	if err != nil {
		g.msghub.Send(messaging.SessionsTopic, ErrorToSessionMessage{
			sessionid:  sessionid,
			message:    fmt.Sprintf("error converting question results payload to JSON: %v", err),
			nextscreen: "",
		})
		return common.Game{}, false
	}

	g.msghub.Send(messaging.ClientHubTopic, ClientMessage{
		client:  client,
		message: "question-results " + encoded,
	})

	return game, true
}

func (g *Games) sendGamePlayersToAnswerQuestionScreen(sessionid string, game common.Game) {
	question, err := game.Quiz.GetQuestion(game.QuestionIndex)
	if err != nil {
		g.msghub.Send(messaging.SessionsTopic, ErrorToSessionMessage{
			sessionid:  sessionid,
			message:    fmt.Sprintf("error getting question: %v", err),
			nextscreen: "",
		})
		return
	}
	answerCount := len(question.Answers)
	for pid := range game.Players {
		g.msghub.Send(messaging.SessionsTopic, SessionMessage{
			sessionid: pid,
			message:   fmt.Sprintf("display-choices %d", answerCount),
		})
		g.msghub.Send(messaging.SessionsTopic, SessionToScreenMessage{
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

	g.msghub.Send(messaging.SessionsTopic, SessionToScreenMessage{
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
		g.msghub.Send(messaging.SessionsTopic, SessionToScreenMessage{
			sessionid:  pid,
			nextscreen: "display-player-results",
		})

		encoded, err := common.ConvertToJSON(&playerResults)
		if err != nil {
			log.Printf("error converting player-results payload to JSON: %v", err)
			continue
		}
		g.msghub.Send(messaging.SessionsTopic, SessionMessage{
			sessionid: pid,
			message:   "player-results " + encoded,
		})
	}

	return true
}

// returns true if successful (treat it as an ok flag)
func (g *Games) ensureUserIsGameHost(client *Client, sessionid string, pin int) (common.Game, bool) {
	game, err := g.Get(pin)
	if err != nil {
		g.msghub.Send(messaging.SessionsTopic, SetSessionGamePinMessage{
			sessionid: sessionid,
			pin:       -1,
		})

		if _, ok := err.(*common.NoSuchGameError); ok {
			g.msghub.Send(messaging.SessionsTopic, ErrorToSessionMessage{
				sessionid:  sessionid,
				message:    err.Error(),
				nextscreen: "entrance",
			})
			return common.Game{}, false
		}

		g.msghub.Send(messaging.SessionsTopic, ErrorToSessionMessage{
			sessionid:  sessionid,
			message:    "error fetching game: " + err.Error(),
			nextscreen: "entrance",
		})

		return common.Game{}, false
	}

	if sessionid != game.Host {
		g.msghub.Send(messaging.SessionsTopic, ErrorToSessionMessage{
			sessionid:  sessionid,
			message:    "you are not the host of the game",
			nextscreen: "entrance",
		})
		return common.Game{}, false
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

	gameState, err := g.nextState(game.Pin)
	if err != nil {
		g.msghub.Send(messaging.SessionsTopic, ErrorToSessionMessage{
			sessionid:  msg.sessionid,
			message:    "error starting game: " + err.Error(),
			nextscreen: "host-select-quiz",
		})
		return true
	}
	if gameState != common.QuestionInProgress {
		if gameState == common.ShowResults {
			g.msghub.Send(messaging.GamesTopic, ShowResultsMessage(msg))
			return true
		}
		if gameState == common.GameEnded {
			g.msghub.Send(messaging.SessionsTopic, SessionToScreenMessage{
				sessionid:  msg.sessionid,
				nextscreen: "host-select-quiz",
			})
			return true
		}

		g.msghub.Send(messaging.SessionsTopic, ErrorToSessionMessage{
			sessionid:  msg.sessionid,
			message:    fmt.Sprintf("game was not in an expected state: %d", gameState),
			nextscreen: "",
		})
		return true
	}

	g.msghub.Send(messaging.SessionsTopic, SessionToScreenMessage{
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

	g.setGameQuiz(msg.pin, msg.quiz)
	return true
}

func (g *Games) processHostGameLobbyMessage(message interface{}) bool {
	msg, ok := message.(HostGameLobbyMessage)
	if !ok {
		return false
	}

	// create new game
	pin, err := g.add(msg.sessionid)
	if err != nil {
		g.msghub.Send(messaging.SessionsTopic, ErrorToSessionMessage{
			sessionid:  msg.sessionid,
			message:    "could not add game: " + err.Error(),
			nextscreen: "host-select-quiz",
		})
		log.Printf("could not add game: " + err.Error())
		return true
	}

	g.msghub.Send(messaging.SessionsTopic, SetSessionGamePinMessage{
		sessionid: msg.sessionid,
		pin:       pin,
	})

	g.msghub.Send(messaging.QuizzesTopic, LookupQuizForGameMessage{
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

	players := game.GetPlayers()
	players = append(players, game.Host)
	g.msghub.Send(messaging.SessionsTopic, DeregisterGameFromSessionsMessage{
		sessions: players,
	})

	for _, playerid := range players {
		g.msghub.Send(messaging.SessionsTopic, SessionToScreenMessage{
			sessionid:  playerid,
			nextscreen: "entrance",
		})
	}

	g.delete(game.Pin)

	return true
}

func (g *Games) processRegisterAnswerMessage(message interface{}) bool {
	msg, ok := message.(RegisterAnswerMessage)
	if !ok {
		return false
	}

	answersUpdate, err := g.registerAnswer(msg.pin, msg.sessionid, msg.answer)
	if err != nil {
		g.msghub.Send(messaging.SessionsTopic, SetSessionGamePinMessage{
			sessionid: msg.sessionid,
			pin:       -1,
		})

		if _, ok := err.(*common.NoSuchGameError); ok {
			g.msghub.Send(messaging.SessionsTopic, ErrorToSessionMessage{
				sessionid:  msg.sessionid,
				message:    err.Error(),
				nextscreen: "entrance",
			})
			return true
		}

		if errState, ok := err.(*common.UnexpectedStateError); ok {
			switch errState.CurrentState {
			case common.GameNotStarted:
				g.msghub.Send(messaging.SessionsTopic, SessionToScreenMessage{
					sessionid:  msg.sessionid,
					nextscreen: "wait-for-game-start",
				})

			case common.ShowResults:
				g.msghub.Send(messaging.SessionsTopic, SessionToScreenMessage{
					sessionid:  msg.sessionid,
					nextscreen: "display-player-results",
				})

			default:
				g.msghub.Send(messaging.SessionsTopic, SessionToScreenMessage{
					sessionid:  msg.sessionid,
					nextscreen: "entrance",
				})
			}
			return true
		}

		g.msghub.Send(messaging.SessionsTopic, ErrorToSessionMessage{
			sessionid:  msg.sessionid,
			message:    "error registering answer: " + err.Error(),
			nextscreen: "",
		})
		return true
	}

	// send this player to wait for question to end screen
	g.msghub.Send(messaging.SessionsTopic, SessionToScreenMessage{
		sessionid:  msg.sessionid,
		nextscreen: "wait-for-question-end",
	})

	encoded, err := common.ConvertToJSON(&answersUpdate)
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

	g.msghub.Send(messaging.SessionsTopic, SessionMessage{
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
		g.msghub.Send(messaging.SessionsTopic, SetSessionGamePinMessage{
			sessionid: msg.sessionid,
			pin:       -1,
		})

		if _, ok := err.(*common.NoSuchGameError); ok {
			g.msghub.Send(messaging.SessionsTopic, ErrorToSessionMessage{
				sessionid:  msg.sessionid,
				message:    err.Error(),
				nextscreen: "entrance",
			})
			return true
		}

		g.msghub.Send(messaging.SessionsTopic, ErrorToSessionMessage{
			sessionid:  msg.sessionid,
			message:    "error fetching game: " + err.Error(),
			nextscreen: "entrance",
		})

		return true
	}

	_, correct := game.CorrectPlayers[msg.sessionid]
	score, ok := game.Players[msg.sessionid]
	if !ok {
		g.msghub.Send(messaging.SessionsTopic, SetSessionGamePinMessage{
			sessionid: msg.sessionid,
			pin:       -1,
		})
		g.msghub.Send(messaging.SessionsTopic, ErrorToSessionMessage{
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

	encoded, err := common.ConvertToJSON(&playerResults)
	if err != nil {
		log.Printf("error converting player-results payload to JSON: %v", err)
		return true
	}

	g.msghub.Send(messaging.ClientHubTopic, ClientMessage{
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

	currentQuestion, err := g.getCurrentQuestion(msg.pin)
	if err != nil {
		g.msghub.Send(messaging.SessionsTopic, SetSessionGamePinMessage{
			sessionid: msg.sessionid,
			pin:       -1,
		})

		if _, ok := err.(*common.NoSuchGameError); ok {
			g.msghub.Send(messaging.SessionsTopic, ErrorToSessionMessage{
				sessionid:  msg.sessionid,
				message:    err.Error(),
				nextscreen: "entrance",
			})
			return true
		}

		g.msghub.Send(messaging.SessionsTopic, ErrorToSessionMessage{
			sessionid:  msg.sessionid,
			message:    "error retrieving current question: " + err.Error(),
			nextscreen: "",
		})
		return true
	}

	g.msghub.Send(messaging.ClientHubTopic, ClientMessage{
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

	winners, err := g.getWinners(msg.pin)
	if err != nil {
		g.msghub.Send(messaging.SessionsTopic, ErrorToSessionMessage{
			sessionid:  msg.sessionid,
			message:    "error retrieving game winners: " + err.Error(),
			nextscreen: "",
		})

		return true
	}

	encoded, err := common.ConvertToJSON(&winners)
	if err != nil {
		g.msghub.Send(messaging.SessionsTopic, ErrorToSessionMessage{
			sessionid:  msg.sessionid,
			message:    "error converting show-winners payload to JSON: " + err.Error(),
			nextscreen: "",
		})
		return true
	}
	log.Printf("winners for game %d: %s", msg.pin, encoded)

	g.msghub.Send(messaging.ClientHubTopic, ClientMessage{
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

	currentQuestion, err := g.getCurrentQuestion(msg.pin)
	if err != nil {
		// if the host disconnected while the question was live, and if
		// the game state has now changed, we may need to move the host to
		// the relevant screen
		unexpectedState, ok := err.(*common.UnexpectedStateError)
		if ok && unexpectedState.CurrentState == common.ShowResults {
			g.msghub.Send(messaging.SessionsTopic, SessionToScreenMessage{
				sessionid:  msg.sessionid,
				nextscreen: "show-results",
			})
			return true
		}

		g.msghub.Send(messaging.SessionsTopic, ErrorToSessionMessage{
			sessionid:  msg.sessionid,
			message:    "error retrieving question: " + err.Error(),
			nextscreen: "",
		})
		return true
	}

	encoded, err := common.ConvertToJSON(&currentQuestion)
	if err != nil {
		g.msghub.Send(messaging.SessionsTopic, ErrorToSessionMessage{
			sessionid:  msg.sessionid,
			message:    "error converting question to JSON: " + err.Error(),
			nextscreen: "",
		})
		return true
	}

	g.msghub.Send(messaging.ClientHubTopic, ClientMessage{
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
		g.msghub.Send(messaging.SessionsTopic, ErrorToSessionMessage{
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
		Players: game.GetPlayerNames(),
	}

	encoded, err := common.ConvertToJSON(&gameMetadata)
	if err != nil {
		g.msghub.Send(messaging.SessionsTopic, ErrorToSessionMessage{
			sessionid:  msg.sessionid,
			message:    "error converting lobby-game-metadata payload to JSON: " + err.Error(),
			nextscreen: "",
		})
		return true
	}

	g.msghub.Send(messaging.ClientHubTopic, ClientMessage{
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
	if err := g.addPlayerToGame(msg); err != nil {
		g.msghub.Send(messaging.SessionsTopic, ErrorToSessionMessage{
			sessionid:  msg.sessionid,
			message:    "could not add player to game: " + err.Error(),
			nextscreen: "entrance",
		})
		return true
	}

	g.msghub.Send(messaging.SessionsTopic, BindGameToSessionMessage(msg))
	g.msghub.Send(messaging.SessionsTopic, SessionToScreenMessage{
		sessionid:  msg.sessionid,
		nextscreen: "wait-for-game-start",
	})

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
	players := game.GetPlayerNames()
	encoded, err := common.ConvertToJSON(&players)

	if err != nil {
		log.Printf("error encoding player names: %v", err)
		return true
	}

	g.msghub.Send(messaging.SessionsTopic, SessionMessage{
		sessionid: host,
		message:   "participants-list " + encoded,
	})

	return true
}

func (g *Games) persist(game *common.Game) {
	if g.engine == nil {
		return
	}
	data, err := game.Marshal()
	if err != nil {
		log.Printf("error trying to convert game %d to JSON: %v", game.Pin, err)
		return
	}
	if err := g.engine.Set(fmt.Sprintf("game:%d", game.Pin), data, 0); err != nil {
		log.Printf("error trying to persist game %d: %v", game.Pin, err)
	}
}

// called by the REST API
func (g *Games) GetAll() []common.Game {
	keys, err := g.engine.GetKeys("game")
	if err != nil {
		log.Printf("error getting all game keys from persistent store: %v", err)
		return nil
	}
	all := []common.Game{}
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

func (g *Games) add(host string) (int, error) {
	game := common.Game{
		Host:            host,
		Players:         make(map[string]int),
		PlayerNames:     make(map[string]string),
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

func (g *Games) getGamePointer(pin int) (*common.Game, error) {
	g.mutex.RLock()
	game, ok := g.all[pin]
	g.mutex.RUnlock()

	if ok {
		return game, nil
	}

	if g.engine == nil {
		return nil, common.NewNoSuchGameError(pin)
	}

	// game doesn't exist in memory - see if it's in the persistent store
	data, err := g.engine.Get(fmt.Sprintf("game:%d", pin))
	if err != nil {
		return nil, common.NewNoSuchGameError(pin)
	}
	game, err = common.UnmarshalGame(data)
	if err != nil {
		return nil, fmt.Errorf("could not retrieve game %d from persistent store: %v", pin, err)
	}

	g.mutex.Lock()
	g.all[pin] = game
	g.mutex.Unlock()

	return game, nil
}

// called by the REST API
func (g *Games) Get(pin int) (common.Game, error) {
	gp, err := g.getGamePointer(pin)
	if err != nil {
		return common.Game{}, err
	}

	return gp.Copy(), nil
}

func (g *Games) update(game common.Game) {
	p := &game

	g.mutex.Lock()
	g.all[game.Pin] = p
	g.mutex.Unlock()

	g.persist(p)
}

func (g *Games) delete(pin int) {
	g.mutex.Lock()
	delete(g.all, pin)
	g.mutex.Unlock()

	if g.engine != nil {
		g.engine.Delete(fmt.Sprintf("game:%d", pin))
	}

}

func (g *Games) addPlayerToGame(msg AddPlayerToGameMessage) error {
	game, err := g.getGamePointer(msg.pin)
	if err != nil {
		return common.NewNoSuchGameError(msg.pin)
	}

	if game.GameState != common.GameNotStarted {
		return errors.New("game is not accepting new players")
	}

	g.mutex.Lock()
	changed := game.AddPlayer(msg.sessionid, msg.name)
	g.mutex.Unlock()
	if changed {
		g.persist(game)
	}
	return nil
}

func (g *Games) setGameQuiz(pin int, quiz common.Quiz) {
	game, err := g.getGamePointer(pin)
	if err != nil {
		return
	}

	g.mutex.Lock()
	game.SetQuiz(quiz)
	g.all[pin] = game // this is redundant
	g.mutex.Unlock()

	g.persist(game)
}

// Advances the game state to the next state - returns the new state
func (g *Games) nextState(pin int) (int, error) {
	game, err := g.getGamePointer(pin)
	if err != nil {
		return 0, common.NewNoSuchGameError(pin)
	}

	g.mutex.Lock()
	state, err := game.NextState()
	g.mutex.Unlock()
	g.persist(game)
	return state, err
}

// A special instance of NextState() - if we are in the QuestionInProgress
// state, change the state to showResults.
// If we are already in showResults, do not change the state.
func (g *Games) showResults(pin int) error {
	game, err := g.getGamePointer(pin)
	if err != nil {
		return common.NewNoSuchGameError(pin)
	}

	g.mutex.Lock()
	err = game.ShowResults()
	g.mutex.Unlock()
	if err == nil {
		g.persist(game)
	}
	return err
}

// Returns - questionIndex, number of seconds left, question, error
func (g *Games) getCurrentQuestion(pin int) (common.GameCurrentQuestion, error) {
	game, err := g.getGamePointer(pin)
	if err != nil {
		return common.GameCurrentQuestion{}, common.NewNoSuchGameError(pin)
	}

	g.mutex.Lock()
	changed, currentQuestion, err := game.GetCurrentQuestion()
	g.mutex.Unlock()
	if changed {
		g.persist(game)
	}

	return currentQuestion, err
}

func (g *Games) registerAnswer(pin int, sessionid string, answerIndex int) (common.AnswersUpdate, error) {
	game, err := g.getGamePointer(pin)
	if err != nil {
		return common.AnswersUpdate{}, common.NewNoSuchGameError(pin)
	}

	g.mutex.Lock()
	changed, update, err := game.RegisterAnswer(sessionid, answerIndex)
	g.mutex.Unlock()
	if changed {
		g.persist(game)
	}
	return update, err
}

func (g *Games) getQuestionResults(pin int) (common.QuestionResults, error) {
	game, err := g.getGamePointer(pin)
	if err != nil {
		return common.QuestionResults{}, common.NewNoSuchGameError(pin)
	}

	g.mutex.RLock()
	defer g.mutex.RUnlock()
	return game.GetQuestionResults()
}

func (g *Games) getWinners(pin int) ([]common.PlayerScore, error) {
	game, err := g.getGamePointer(pin)
	if err != nil {
		return []common.PlayerScore{}, common.NewNoSuchGameError(pin)
	}

	g.mutex.RLock()
	defer g.mutex.RUnlock()
	return game.GetWinners(), nil
}
