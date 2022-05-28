package internal

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"strconv"
	"sync"

	"github.com/kwkoo/go-quiz/internal/common"
	"github.com/kwkoo/go-quiz/internal/messaging"
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

func (g *Games) Run(ctx context.Context, shutdownComplete func()) {
	gamesHub := g.msghub.GetTopic(messaging.GamesTopic)

	for {
		select {

		case msg, ok := <-gamesHub:
			if !ok {
				log.Printf("received empty message from %s", messaging.GamesTopic)
				continue
			}
			switch m := msg.(type) {
			case common.AddPlayerToGameMessage:
				g.processAddPlayerToGameMessage(m)
			case common.SendGameMetadataMessage:
				g.processSendGameMetadataMessage(m)
			case common.HostShowQuestionMessage:
				g.processHostShowQuestionMessage(m)
			case common.HostShowGameResultsMessage:
				g.processHostShowGameResultsMessage(m)
			case common.QueryDisplayChoicesMessage:
				g.processQueryDisplayChoicesMessage(m)
			case common.QueryPlayerResultsMessage:
				g.processQueryPlayerResultsMessage(m)
			case common.RegisterAnswerMessage:
				g.processRegisterAnswerMessage(m)
			case common.CancelGameMessage:
				g.processCancelGameMessage(m)
			case common.HostGameLobbyMessage:
				g.processHostGameLobbyMessage(m)
			case common.SetQuizForGameMessage:
				g.processSetQuizForGameMessage(m)
			case common.StartGameMessage:
				g.processStartGameMessage(m)
			case common.ShowResultsMessage:
				g.processShowResultsMessage(m)
			case common.QueryHostResultsMessage:
				g.processQueryHostResultsMessage(m)
			case common.NextQuestionMessage:
				g.processNextQuestionMessage(m)
			case common.DeleteGameMessage:
				g.processDeleteGameMessage(m)
			case common.UpdateGameMessage:
				g.processUpdateGameMessage(m)
			case common.DeleteGameByPin:
				g.processDeleteGameByPin(m)
			case *common.GetGamesMessage:
				g.processGetGamesMessage(m)
			case *common.GetGameMessage:
				g.processGetGameMessage(m)
			default:
				log.Printf("unrecognized message type %T received on %s topic", msg, messaging.GamesTopic)
			}

		case <-ctx.Done():
			log.Print("shutting down games handler")
			shutdownComplete()
			return
		}
	}
}

func (g *Games) processGetGameMessage(msg *common.GetGameMessage) {
	game, err := g.get(msg.Pin)
	msg.Result <- common.GetGameResult{
		Game:  game,
		Error: err,
	}
	close(msg.Result)
}

func (g *Games) processGetGamesMessage(msg *common.GetGamesMessage) {
	msg.Result <- g.getAll()
	close(msg.Result)
}

func (g *Games) processDeleteGameByPin(msg common.DeleteGameByPin) {
	g.delete(msg.Pin)
}

func (g *Games) processUpdateGameMessage(msg common.UpdateGameMessage) {
	g.update(msg.Game)
}

func (g *Games) processDeleteGameMessage(msg common.DeleteGameMessage) {
	if _, ok := g.ensureUserIsGameHost(msg.Clientid, msg.Sessionid, msg.Pin); !ok {
		log.Printf("could not delete game because %s is not a game host", msg.Sessionid)
		return
	}

	g.delete(msg.Pin)
	g.msghub.Send(messaging.SessionsTopic, common.SetSessionGamePinMessage{
		Sessionid: msg.Sessionid,
		Pin:       -1,
	})

	g.msghub.Send(messaging.SessionsTopic, common.SessionToScreenMessage{
		Sessionid:  msg.Sessionid,
		Nextscreen: "host-select-quiz",
	})
}

func (g *Games) processNextQuestionMessage(msg common.NextQuestionMessage) {
	game, ok := g.ensureUserIsGameHost(msg.Clientid, msg.Sessionid, msg.Pin)
	if !ok {
		log.Printf("could not move game to next question because %s is not a game host", msg.Sessionid)
		return
	}

	gameState, err := g.nextState(game.Pin)
	if err != nil {
		g.msghub.Send(messaging.SessionsTopic, common.SetSessionGamePinMessage{
			Sessionid: msg.Sessionid,
			Pin:       -1,
		})
		if _, ok := err.(*common.NoSuchGameError); ok {
			g.msghub.Send(messaging.SessionsTopic, common.ErrorToSessionMessage{
				Sessionid:  msg.Sessionid,
				Message:    err.Error(),
				Nextscreen: "entrance",
			})
			return
		}
		g.msghub.Send(messaging.SessionsTopic, common.ErrorToSessionMessage{
			Sessionid:  msg.Sessionid,
			Message:    "error setting game to next state: " + err.Error(),
			Nextscreen: "host-select-quiz",
		})
		return
	}

	if gameState == common.QuestionInProgress {
		g.msghub.Send(messaging.SessionsTopic, common.SessionToScreenMessage{
			Sessionid:  msg.Sessionid,
			Nextscreen: "host-show-question",
		})

		g.sendGamePlayersToAnswerQuestionScreen(msg.Sessionid, *game)
		return
	}

	// assume that game has ended
	g.msghub.Send(messaging.SessionsTopic, common.SessionToScreenMessage{
		Sessionid:  msg.Sessionid,
		Nextscreen: "host-show-game-results",
	})

	players := game.GetPlayers()
	g.msghub.Send(messaging.SessionsTopic, common.DeregisterGameFromSessionsMessage{
		Sessions: players,
	})

	for _, playerid := range players {
		g.msghub.Send(messaging.SessionsTopic, common.SessionToScreenMessage{
			Sessionid:  playerid,
			Nextscreen: "entrance",
		})
	}
}

func (g *Games) processQueryHostResultsMessage(msg common.QueryHostResultsMessage) {
	g.sendQuestionResultsToHost(msg.Clientid, msg.Sessionid, msg.Pin)
}

// returns ok if successful
func (g *Games) sendQuestionResultsToHost(client uint64, sessionid string, pin int) (common.Game, bool) {
	game, ok := g.ensureUserIsGameHost(client, sessionid, pin)
	if !ok {
		log.Printf("not sending question results to host because %s is not a game host", sessionid)
		return common.Game{}, false
	}

	if err := g.showResults(pin); err != nil {
		g.msghub.Send(messaging.SessionsTopic, common.ErrorToSessionMessage{
			Sessionid:  sessionid,
			Message:    fmt.Sprintf("error moving game to show results state: %v", err),
			Nextscreen: "",
		})
		return common.Game{}, false
	}

	results, err := g.getQuestionResults(pin)
	if err != nil {
		g.msghub.Send(messaging.SessionsTopic, common.ErrorToSessionMessage{
			Sessionid:  sessionid,
			Message:    fmt.Sprintf("error getting question results: %v", err),
			Nextscreen: "",
		})
		return common.Game{}, false
	}

	encoded, err := common.ConvertToJSON(&results)
	if err != nil {
		g.msghub.Send(messaging.SessionsTopic, common.ErrorToSessionMessage{
			Sessionid:  sessionid,
			Message:    fmt.Sprintf("error converting question results payload to JSON: %v", err),
			Nextscreen: "",
		})
		return common.Game{}, false
	}

	g.msghub.Send(messaging.ClientHubTopic, common.ClientMessage{
		Clientid: client,
		Message:  "question-results " + encoded,
	})

	return *game, true
}

func (g *Games) sendGamePlayersToAnswerQuestionScreen(sessionid string, game common.Game) {
	question, err := game.Quiz.GetQuestion(game.QuestionIndex)
	if err != nil {
		g.msghub.Send(messaging.SessionsTopic, common.ErrorToSessionMessage{
			Sessionid:  sessionid,
			Message:    fmt.Sprintf("error getting question: %v", err),
			Nextscreen: "",
		})
		return
	}
	answerCount := len(question.Answers)
	for pid := range game.Players {
		g.msghub.Send(messaging.SessionsTopic, common.SessionMessage{
			Sessionid: pid,
			Message:   fmt.Sprintf("display-choices %d", answerCount),
		})
		g.msghub.Send(messaging.SessionsTopic, common.SessionToScreenMessage{
			Sessionid:  pid,
			Nextscreen: "answer-question",
		})
	}
}

func (g *Games) processShowResultsMessage(msg common.ShowResultsMessage) {
	game, ok := g.sendQuestionResultsToHost(msg.Clientid, msg.Sessionid, msg.Pin)
	if !ok {
		return
	}

	g.msghub.Send(messaging.SessionsTopic, common.SessionToScreenMessage{
		Sessionid:  msg.Sessionid,
		Nextscreen: "host-show-results",
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
		g.msghub.Send(messaging.SessionsTopic, common.SessionToScreenMessage{
			Sessionid:  pid,
			Nextscreen: "display-player-results",
		})

		encoded, err := common.ConvertToJSON(&playerResults)
		if err != nil {
			log.Printf("error converting player-results payload to JSON: %v", err)
			continue
		}
		g.msghub.Send(messaging.SessionsTopic, common.SessionMessage{
			Sessionid: pid,
			Message:   "player-results " + encoded,
		})
	}
}

// returns true if successful (treat it as an ok flag)
func (g *Games) ensureUserIsGameHost(client uint64, sessionid string, pin int) (*common.Game, bool) {
	game, err := g.getGamePointer(pin)
	if err != nil {
		g.msghub.Send(messaging.SessionsTopic, common.SetSessionGamePinMessage{
			Sessionid: sessionid,
			Pin:       -1,
		})

		if _, ok := err.(*common.NoSuchGameError); ok {
			g.msghub.Send(messaging.SessionsTopic, common.ErrorToSessionMessage{
				Sessionid:  sessionid,
				Message:    err.Error(),
				Nextscreen: "entrance",
			})
			return nil, false
		}

		g.msghub.Send(messaging.SessionsTopic, common.ErrorToSessionMessage{
			Sessionid:  sessionid,
			Message:    "error fetching game: " + err.Error(),
			Nextscreen: "entrance",
		})

		return nil, false
	}

	if sessionid != game.Host {
		g.msghub.Send(messaging.SessionsTopic, common.ErrorToSessionMessage{
			Sessionid:  sessionid,
			Message:    "you are not the host of the game",
			Nextscreen: "entrance",
		})
		return nil, false
	}

	return game, true
}

func (g *Games) processStartGameMessage(msg common.StartGameMessage) {
	game, ok := g.ensureUserIsGameHost(msg.Clientid, msg.Sessionid, msg.Pin)
	if !ok {
		log.Printf("not starting game because %s is not a game host", msg.Sessionid)
		return
	}

	gameState, err := g.nextState(game.Pin)
	if err != nil {
		g.msghub.Send(messaging.SessionsTopic, common.ErrorToSessionMessage{
			Sessionid:  msg.Sessionid,
			Message:    "error starting game: " + err.Error(),
			Nextscreen: "host-select-quiz",
		})
		return
	}
	if gameState != common.QuestionInProgress {
		if gameState == common.ShowResults {
			g.msghub.Send(messaging.GamesTopic, common.ShowResultsMessage(msg))
			return
		}
		if gameState == common.GameEnded {
			g.msghub.Send(messaging.SessionsTopic, common.SessionToScreenMessage{
				Sessionid:  msg.Sessionid,
				Nextscreen: "host-select-quiz",
			})
			return
		}

		g.msghub.Send(messaging.SessionsTopic, common.ErrorToSessionMessage{
			Sessionid:  msg.Sessionid,
			Message:    fmt.Sprintf("game was not in an expected state: %d", gameState),
			Nextscreen: "",
		})
		return
	}

	g.msghub.Send(messaging.SessionsTopic, common.SessionToScreenMessage{
		Sessionid:  msg.Sessionid,
		Nextscreen: "host-show-question",
	})

	g.sendGamePlayersToAnswerQuestionScreen(msg.Sessionid, *game)
}

func (g *Games) processSetQuizForGameMessage(msg common.SetQuizForGameMessage) {
	g.setGameQuiz(msg.Pin, msg.Quiz)
}

func (g *Games) processHostGameLobbyMessage(msg common.HostGameLobbyMessage) {
	// create new game
	pin, err := g.add(msg.Sessionid)
	if err != nil {
		g.msghub.Send(messaging.SessionsTopic, common.ErrorToSessionMessage{
			Sessionid:  msg.Sessionid,
			Message:    "could not add game: " + err.Error(),
			Nextscreen: "host-select-quiz",
		})
		log.Printf("could not add game: " + err.Error())
		return
	}

	g.msghub.Send(messaging.SessionsTopic, common.SetSessionGamePinMessage{
		Sessionid: msg.Sessionid,
		Pin:       pin,
	})

	g.msghub.Send(messaging.QuizzesTopic, common.LookupQuizForGameMessage{
		Clientid:  msg.Clientid,
		Sessionid: msg.Sessionid,
		Quizid:    msg.Quizid,
		Pin:       pin,
	})
}

func (g *Games) processCancelGameMessage(msg common.CancelGameMessage) {
	game, ok := g.ensureUserIsGameHost(msg.Clientid, msg.Sessionid, msg.Pin)
	if !ok {
		log.Printf("not cancelling game because %s is not a game host", msg.Sessionid)
		return
	}

	players := game.GetPlayers()
	players = append(players, game.Host)
	g.msghub.Send(messaging.SessionsTopic, common.DeregisterGameFromSessionsMessage{
		Sessions: players,
	})

	for _, playerid := range players {
		g.msghub.Send(messaging.SessionsTopic, common.SessionToScreenMessage{
			Sessionid:  playerid,
			Nextscreen: "entrance",
		})
	}

	g.delete(game.Pin)
}

func (g *Games) processRegisterAnswerMessage(msg common.RegisterAnswerMessage) {
	answersUpdate, err := g.registerAnswer(msg.Pin, msg.Sessionid, msg.Answer)
	if err != nil {
		g.msghub.Send(messaging.SessionsTopic, common.SetSessionGamePinMessage{
			Sessionid: msg.Sessionid,
			Pin:       -1,
		})

		if _, ok := err.(*common.NoSuchGameError); ok {
			g.msghub.Send(messaging.SessionsTopic, common.ErrorToSessionMessage{
				Sessionid:  msg.Sessionid,
				Message:    err.Error(),
				Nextscreen: "entrance",
			})
			return
		}

		if errState, ok := err.(*common.UnexpectedStateError); ok {
			switch errState.CurrentState {
			case common.GameNotStarted:
				g.msghub.Send(messaging.SessionsTopic, common.SessionToScreenMessage{
					Sessionid:  msg.Sessionid,
					Nextscreen: "wait-for-game-start",
				})

			case common.ShowResults:
				g.msghub.Send(messaging.SessionsTopic, common.SessionToScreenMessage{
					Sessionid:  msg.Sessionid,
					Nextscreen: "display-player-results",
				})

			default:
				g.msghub.Send(messaging.SessionsTopic, common.SessionToScreenMessage{
					Sessionid:  msg.Sessionid,
					Nextscreen: "entrance",
				})
			}
			return
		}

		g.msghub.Send(messaging.SessionsTopic, common.ErrorToSessionMessage{
			Sessionid:  msg.Sessionid,
			Message:    "error registering answer: " + err.Error(),
			Nextscreen: "",
		})
		return
	}

	// send this player to wait for question to end screen
	g.msghub.Send(messaging.SessionsTopic, common.SessionToScreenMessage{
		Sessionid:  msg.Sessionid,
		Nextscreen: "wait-for-question-end",
	})

	encoded, err := common.ConvertToJSON(&answersUpdate)
	if err != nil {
		log.Printf("error converting players-answered payload to JSON: %v", err)
		return
	}

	game, err := g.get(msg.Pin)
	if err != nil {
		log.Printf("could not retrieve game %d: %v", msg.Pin, err)
		return
	}
	host := game.Host
	if host == "" {
		return
	}

	g.msghub.Send(messaging.SessionsTopic, common.SessionMessage{
		Sessionid: host,
		Message:   "players-answered " + encoded,
	})
}

// player may have been disconnected - now they need to know about
// their results
func (g *Games) processQueryPlayerResultsMessage(msg common.QueryPlayerResultsMessage) {
	game, err := g.get(msg.Pin)
	if err != nil {
		g.msghub.Send(messaging.SessionsTopic, common.SetSessionGamePinMessage{
			Sessionid: msg.Sessionid,
			Pin:       -1,
		})

		if _, ok := err.(*common.NoSuchGameError); ok {
			g.msghub.Send(messaging.SessionsTopic, common.ErrorToSessionMessage{
				Sessionid:  msg.Sessionid,
				Message:    err.Error(),
				Nextscreen: "entrance",
			})
			return
		}

		g.msghub.Send(messaging.SessionsTopic, common.ErrorToSessionMessage{
			Sessionid:  msg.Sessionid,
			Message:    "error fetching game: " + err.Error(),
			Nextscreen: "entrance",
		})

		return
	}

	_, correct := game.CorrectPlayers[msg.Sessionid]
	score, ok := game.Players[msg.Sessionid]
	if !ok {
		g.msghub.Send(messaging.SessionsTopic, common.SetSessionGamePinMessage{
			Sessionid: msg.Sessionid,
			Pin:       -1,
		})
		g.msghub.Send(messaging.SessionsTopic, common.ErrorToSessionMessage{
			Sessionid:  msg.Sessionid,
			Message:    "you do not have a score in this game",
			Nextscreen: "entrance",
		})
		return
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
		return
	}

	g.msghub.Send(messaging.ClientHubTopic, common.ClientMessage{
		Clientid: msg.Clientid,
		Message:  "player-results " + encoded,
	})
}

// player may have been disconnected - now they need to know how many
// answers to enable
func (g *Games) processQueryDisplayChoicesMessage(msg common.QueryDisplayChoicesMessage) {
	currentQuestion, err := g.getCurrentQuestion(msg.Pin)
	if err != nil {
		g.msghub.Send(messaging.SessionsTopic, common.SetSessionGamePinMessage{
			Sessionid: msg.Sessionid,
			Pin:       -1,
		})

		if _, ok := err.(*common.NoSuchGameError); ok {
			g.msghub.Send(messaging.SessionsTopic, common.ErrorToSessionMessage{
				Sessionid:  msg.Sessionid,
				Message:    err.Error(),
				Nextscreen: "entrance",
			})
			return
		}

		g.msghub.Send(messaging.SessionsTopic, common.ErrorToSessionMessage{
			Sessionid:  msg.Sessionid,
			Message:    "error retrieving current question: " + err.Error(),
			Nextscreen: "",
		})
		return
	}

	g.msghub.Send(messaging.ClientHubTopic, common.ClientMessage{
		Clientid: msg.Clientid,
		Message:  fmt.Sprintf("display-choices %d", len(currentQuestion.Answers)),
	})
}

func (g *Games) processHostShowGameResultsMessage(msg common.HostShowGameResultsMessage) {
	winners, err := g.getWinners(msg.Pin)
	if err != nil {
		g.msghub.Send(messaging.SessionsTopic, common.ErrorToSessionMessage{
			Sessionid:  msg.Sessionid,
			Message:    "error retrieving game winners: " + err.Error(),
			Nextscreen: "",
		})

		return
	}

	encoded, err := common.ConvertToJSON(&winners)
	if err != nil {
		g.msghub.Send(messaging.SessionsTopic, common.ErrorToSessionMessage{
			Sessionid:  msg.Sessionid,
			Message:    "error converting show-winners payload to JSON: " + err.Error(),
			Nextscreen: "",
		})
		return
	}
	log.Printf("winners for game %d: %s", msg.Pin, encoded)

	g.msghub.Send(messaging.ClientHubTopic, common.ClientMessage{
		Clientid: msg.Clientid,
		Message:  "show-winners " + encoded,
	})
}

func (g *Games) processHostShowQuestionMessage(msg common.HostShowQuestionMessage) {
	currentQuestion, err := g.getCurrentQuestion(msg.Pin)
	if err != nil {
		// if the host disconnected while the question was live, and if
		// the game state has now changed, we may need to move the host to
		// the relevant screen
		unexpectedState, ok := err.(*common.UnexpectedStateError)
		if ok && unexpectedState.CurrentState == common.ShowResults {
			g.msghub.Send(messaging.SessionsTopic, common.SessionToScreenMessage{
				Sessionid:  msg.Sessionid,
				Nextscreen: "show-results",
			})
			return
		}

		g.msghub.Send(messaging.SessionsTopic, common.ErrorToSessionMessage{
			Sessionid:  msg.Sessionid,
			Message:    "error retrieving question: " + err.Error(),
			Nextscreen: "",
		})
		return
	}

	encoded, err := common.ConvertToJSON(&currentQuestion)
	if err != nil {
		g.msghub.Send(messaging.SessionsTopic, common.ErrorToSessionMessage{
			Sessionid:  msg.Sessionid,
			Message:    "error converting question to JSON: " + err.Error(),
			Nextscreen: "",
		})
		return
	}

	g.msghub.Send(messaging.ClientHubTopic, common.ClientMessage{
		Clientid: msg.Clientid,
		Message:  "host-show-question " + encoded,
	})
}

func (g *Games) processSendGameMetadataMessage(msg common.SendGameMetadataMessage) {
	game, err := g.get(msg.Pin)
	if err != nil {
		g.msghub.Send(messaging.SessionsTopic, common.ErrorToSessionMessage{
			Sessionid:  msg.Sessionid,
			Message:    fmt.Sprintf("could not retrieve game %d", msg.Pin),
			Nextscreen: "entrance",
		})
		return
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
		g.msghub.Send(messaging.SessionsTopic, common.ErrorToSessionMessage{
			Sessionid:  msg.Sessionid,
			Message:    "error converting lobby-game-metadata payload to JSON: " + err.Error(),
			Nextscreen: "",
		})
		return
	}

	g.msghub.Send(messaging.ClientHubTopic, common.ClientMessage{
		Clientid: msg.Clientid,
		Message:  "lobby-game-metadata " + encoded,
	})
}

// returns true if processed
func (g *Games) processAddPlayerToGameMessage(msg common.AddPlayerToGameMessage) {
	if err := g.addPlayerToGame(msg); err != nil {
		g.msghub.Send(messaging.SessionsTopic, common.ErrorToSessionMessage{
			Sessionid:  msg.Sessionid,
			Message:    "could not add player to game: " + err.Error(),
			Nextscreen: "entrance",
		})
		return
	}

	g.msghub.Send(messaging.SessionsTopic, common.BindGameToSessionMessage(msg))
	g.msghub.Send(messaging.SessionsTopic, common.SessionToScreenMessage{
		Sessionid:  msg.Sessionid,
		Nextscreen: "wait-for-game-start",
	})

	// inform game host of new player
	game, err := g.get(msg.Pin)
	if err != nil {
		log.Printf("could not retrieve game %d: %v", msg.Pin, err)
		return
	}
	host := game.Host
	if host == "" {
		log.Printf("could not inform host of new player because game %d has not host", msg.Pin)
		return
	}
	players := game.GetPlayerNames()
	encoded, err := common.ConvertToJSON(&players)

	if err != nil {
		log.Printf("error encoding player names: %v", err)
		return
	}

	g.msghub.Send(messaging.SessionsTopic, common.SessionMessage{
		Sessionid: host,
		Message:   "participants-list " + encoded,
	})
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
func (g *Games) getAll() []common.Game {
	if g.engine == nil {
		all := []common.Game{}
		for _, game := range g.all {
			all = append(all, *game)
		}
		return all
	}

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
		game, err := g.get(keyInt)
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
func (g *Games) get(pin int) (common.Game, error) {
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

func (g *Games) addPlayerToGame(msg common.AddPlayerToGameMessage) error {
	game, err := g.getGamePointer(msg.Pin)
	if err != nil {
		return common.NewNoSuchGameError(msg.Pin)
	}

	if game.GameState != common.GameNotStarted {
		return errors.New("game is not accepting new players")
	}

	g.mutex.Lock()
	changed := game.AddPlayer(msg.Sessionid, msg.Name)
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

	if quiz.ShuffleQuestions {
		quiz.Shuffle()
	}

	if quiz.ShuffleAnswers {
		for i, question := range quiz.Questions {
			quiz.Questions[i] = question.ShuffleAnswers()
		}
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
