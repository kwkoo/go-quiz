package pkg

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
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

type Game struct {
	pin              int
	host             string         // session ID of game host
	players          map[string]int // scores of players
	quiz             Quiz
	questionIndex    int       // current question
	questionDeadline time.Time // answers must come in at this time or before
	correctPlayers   []string  // players that answered current question correctly
	incorrectPlayers []string  // players that answered current question incorrectly
	votes            []int     // number of players that answered each choice
	gameState        int
}

type QuestionResults struct {
	QuestionIndex    int
	Question         QuizQuestion
	Scores           map[string]int
	CorrectPlayers   []string
	IncorrectPlayers []string
	Votes            []int
}

type PlayerScore struct {
	Sessionid string
	Score     int
}

type PlayerScoreList []PlayerScore

func (p PlayerScoreList) Len() int           { return len(p) }
func (p PlayerScoreList) Less(i, j int) bool { return p[i].Score < p[j].Score }
func (p PlayerScoreList) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }

func NewGame(host string) *Game {
	return &Game{
		host:    host,
		players: make(map[string]int),
	}
}

type Games struct {
	mutex sync.RWMutex
	g     map[int]*Game // map key is the game pin
}

func InitGames() *Games {
	games := Games{
		g: make(map[int]*Game),
	}
	return &games
}

func (g *Games) Add(game Game) (Game, error) {
	g.mutex.Lock()
	defer g.mutex.Unlock()

	for i := 0; i < 5; i++ {
		pin := rand.Intn(1000)
		if _, ok := g.g[pin]; !ok {
			game.pin = pin
			g.g[pin] = &game
			return game, nil
		}
	}
	return Game{}, errors.New("could not generate unique game pin")
}

func (g *Games) Get(pin int) (Game, error) {
	g.mutex.RLock()
	defer g.mutex.RUnlock()
	game, ok := g.g[pin]
	if !ok {
		return Game{}, fmt.Errorf("could not find game with pin %d", pin)
	}
	return *game, nil
}

func (g *Games) Update(game Game) error {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	_, ok := g.g[game.pin]
	if !ok {
		return fmt.Errorf("game with pin %d does not exist", game.pin)
	}
	g.g[game.pin] = &game
	return nil
}

func (g *Games) Delete(pin int) {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	delete(g.g, pin)
}

func (g *Games) AddPlayerToGame(sessionid string, pin int) error {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	game, ok := g.g[pin]
	if !ok {
		return fmt.Errorf("game with pin %d does not exist", pin)
	}
	if _, ok := game.players[sessionid]; ok {
		// player is already in the game
		return nil
	}

	// player is new in this game
	game.players[sessionid] = 0
	return nil
}

func (g *Games) DeletePlayerFromGame(sessionid string, pin int) {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	game, ok := g.g[pin]
	if !ok {
		return
	}
	delete(game.players, sessionid)
	game.correctPlayers = deleteItemFromSlice(sessionid, game.correctPlayers)
	game.incorrectPlayers = deleteItemFromSlice(sessionid, game.incorrectPlayers)
}

func deleteItemFromSlice(item string, old []string) []string {
	var modified []string
	for _, value := range old {
		if value != item {
			modified = append(modified, value)
		}
	}
	return modified
}

// Advances the game state to the next state - returns the new state
func (g *Games) NextState(pin int) (int, error) {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	game, ok := g.g[pin]
	if !ok {
		return 0, fmt.Errorf("game with pin %d does not exist", pin)
	}
	switch game.gameState {
	case GameNotStarted:
		// if there are no questions or players, end the game immediately
		if game.quiz.NumQuestions() == 0 || len(game.players) == 0 {
			game.gameState = GameEnded
			return game.gameState, nil
		}
		if err := game.setupQuestion(0); err != nil {
			game.gameState = GameEnded
			return game.gameState, fmt.Errorf("error trying to start game: %v", err)
		}
		return game.gameState, nil
	case QuestionInProgress:
		game.gameState = ShowResults
		return game.gameState, nil
	case ShowResults:
		if game.questionIndex < game.quiz.NumQuestions() {
			game.questionIndex++
		}
		if game.questionIndex >= game.quiz.NumQuestions() {
			game.gameState = GameEnded
			return game.gameState, nil
		}
		if err := game.setupQuestion(game.questionIndex); err != nil {
			game.gameState = GameEnded
			return game.gameState, err
		}
		return game.gameState, nil
	default:
		game.gameState = GameEnded
		return game.gameState, nil
	}
}

// A special instance of NextState() - if we are in the QuestionInProgress
// state, change the state to ShowResults.
// If we are already in ShowResults, do not change the state.
func (g *Games) ShowResults(pin int) error {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	game, ok := g.g[pin]
	if !ok {
		return fmt.Errorf("game with pin %d does not exist", pin)
	}
	if game.gameState != QuestionInProgress && game.gameState != ShowResults {
		return fmt.Errorf("game with pin %d is not in the expected states", pin)
	}
	game.gameState = ShowResults
	return nil
}

func (g *Game) setupQuestion(newIndex int) error {
	g.questionIndex = newIndex
	question, err := g.quiz.GetQuestion(newIndex)
	if err != nil {
		return err
	}
	g.gameState = QuestionInProgress
	g.correctPlayers = []string{}
	g.incorrectPlayers = []string{}
	g.votes = make([]int, question.NumAnswers())
	g.questionDeadline = time.Now().Add(time.Second * time.Duration(g.quiz.QuestionDuration))
	return nil
}

// Returns - questionIndex, number of seconds left, question, error
func (g *Games) GetCurrentQuestion(pin int) (int, int, QuizQuestion, error) {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	game, ok := g.g[pin]
	if !ok {
		return 0, 0, QuizQuestion{}, fmt.Errorf("game with pin %d does not exist", pin)
	}

	if game.gameState != QuestionInProgress {
		return 0, 0, QuizQuestion{}, fmt.Errorf("game with pin %d is not showing a live question", pin)
	}

	now := time.Now()
	timeLeft := int(game.questionDeadline.Unix() - now.Unix())
	if timeLeft <= 0 || (len(game.correctPlayers)+len(game.incorrectPlayers)) >= len(game.players) {
		game.gameState = ShowResults
		return 0, 0, QuizQuestion{}, fmt.Errorf("game with pin %d should be showing results", pin)
	}

	question, err := game.quiz.GetQuestion(game.questionIndex)
	if err != nil {
		return 0, 0, QuizQuestion{}, err
	}

	return game.questionIndex, timeLeft, question, nil
}

func (g *Games) GetAnsweredCount(pin int) (int, int, error) {
	g.mutex.RLock()
	defer g.mutex.RUnlock()
	game, ok := g.g[pin]
	if !ok {
		return 0, 0, fmt.Errorf("game with pin %d does not exist", pin)
	}

	if game.gameState != QuestionInProgress {
		return 0, 0, fmt.Errorf("game with pin %d is not showing a live question", pin)
	}

	return len(game.correctPlayers) + len(game.incorrectPlayers), len(game.players), nil
}

// Results:
// * all players answered
// * number of players answered
// * total players in game
// * error
func (g *Games) RegisterAnswer(pin int, sessionid string, answerIndex int) (bool, int, int, error) {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	game, ok := g.g[pin]
	if !ok {
		return false, 0, 0, fmt.Errorf("game with pin %d does not exist", pin)
	}
	if _, ok := game.players[sessionid]; !ok {
		return false, 0, 0, fmt.Errorf("player %s is not part of game %d", sessionid, pin)
	}
	if game.gameState != QuestionInProgress {
		return false, 0, 0, fmt.Errorf("game %d is not showing a live question", pin)
	}

	now := time.Now()
	if now.After(game.questionDeadline) {
		game.gameState = ShowResults
		return false, 0, 0, fmt.Errorf("question %d in game %d has expired", game.questionIndex, pin)
	}

	question, err := game.quiz.GetQuestion(game.questionIndex)
	if err != nil {
		return false, 0, 0, err
	}

	if answerIndex < 0 || answerIndex >= question.NumAnswers() {
		return false, 0, 0, errors.New("invalid answer")
	}

	if answerIndex == question.Correct {
		// calculate score, add to player score
		// add player to correct answers list
		game.correctPlayers = append(game.correctPlayers, sessionid)
		game.players[sessionid] += calculateScore(int(game.questionDeadline.Unix()-now.Unix()), game.quiz.QuestionDuration)
	} else {
		// add player to incorrect answers list
		game.incorrectPlayers = append(game.incorrectPlayers, sessionid)
	}
	game.votes[answerIndex]++

	answeredCount := len(game.correctPlayers) + len(game.incorrectPlayers)
	totalPlayers := len(game.players)
	allAnswered := answeredCount >= totalPlayers
	if allAnswered {
		game.gameState = ShowResults
	}
	return allAnswered, answeredCount, totalPlayers, nil
}

// GetQuestionResults
func (g *Games) GetQuestionResults(pin int) (QuestionResults, error) {
	g.mutex.RLock()
	defer g.mutex.RUnlock()
	game, ok := g.g[pin]
	if !ok {
		return QuestionResults{}, fmt.Errorf("game with pin %d does not exist", pin)
	}
	question, err := game.quiz.GetQuestion(game.questionIndex)
	if err != nil {
		return QuestionResults{}, err
	}
	results := QuestionResults{
		QuestionIndex:    game.questionIndex,
		Question:         question,
		Scores:           game.players,
		CorrectPlayers:   game.correctPlayers,
		IncorrectPlayers: game.incorrectPlayers,
		Votes:            game.votes,
	}
	return results, nil
}

func (g *Games) GetWinners(pin int) ([]PlayerScore, error) {
	g.mutex.RLock()
	defer g.mutex.RUnlock()
	game, ok := g.g[pin]
	if !ok {
		return []PlayerScore{}, fmt.Errorf("game with pin %d does not exist", pin)
	}

	// copied from https://stackoverflow.com/a/18695740
	pl := make(PlayerScoreList, len(game.players))
	i := 0
	for k, v := range game.players {
		pl[i] = PlayerScore{k, v}
		i++
	}
	sort.Sort(sort.Reverse(pl))

	max := len(pl)
	if max > winnerCount {
		max = winnerCount
	}
	return pl[:max], nil
}

func (g *Games) GetGameState(pin int) (int, error) {
	g.mutex.RLock()
	defer g.mutex.RUnlock()
	game, ok := g.g[pin]
	if !ok {
		return 0, fmt.Errorf("game with pin %d does not exist", pin)
	}
	return game.gameState, nil
}

func calculateScore(timeLeft, questionDuration int) int {
	if timeLeft < 0 {
		timeLeft = 0
	}
	return 100 + (timeLeft * 100 / questionDuration)
}
