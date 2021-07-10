package pkg

import (
	"crypto/rand"
	"errors"
	"fmt"
	"log"
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
	Pin              int            `json:"pin"`
	Host             string         `json:"host"`    // session ID of game host
	Players          map[string]int `json:"players"` // scores of players
	Quiz             Quiz           `json:"quiz"`
	QuestionIndex    int            `json:"questionindex"`    // current question
	QuestionDeadline time.Time      `json:"questiondeadline"` // answers must come in at this time or before
	PlayersAnswered  map[string]struct{}
	//CorrectPlayers   map[string]struct{} // players that answered current question correctly
	//IncorrectPlayers map[string]struct{} // players that answered current question incorrectly
	Votes     []int `json:"votes"` // number of players that answered each choice
	GameState int   `json:"gamestate"`
}

// Queried by the host - either when the host first displays the question or
// when the host reconnects
type GameCurrentQuestion struct {
	QuestionIndex int      `json:"questionindex"`
	TimeLeft      int      `json:"timeleft"`
	Answered      int      `json:"answered"`     // number of players that have answered
	TotalPlayers  int      `json:"totalplayers"` // number of players in this game
	Question      string   `json:"question"`
	Answers       []string `json:"answers"`
}

type QuestionResults struct {
	QuestionIndex int      `json:"questionindex"`
	Question      string   `json:"question"`
	Answers       []string `json:"answers"`
	Correct       int      `json:"correct"`
	Votes         []int    `json:"votes"`
	TotalVotes    int      `json:"totalvotes"`
}

type PlayerScore struct {
	Sessionid string
	Score     int
}

type PlayerScoreList []PlayerScore

func (p PlayerScoreList) Len() int           { return len(p) }
func (p PlayerScoreList) Less(i, j int) bool { return p[i].Score < p[j].Score }
func (p PlayerScoreList) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }

type Games struct {
	mutex sync.RWMutex
	all   map[int]*Game // map key is the game pin
}

func InitGames() *Games {
	games := Games{
		all: make(map[int]*Game),
	}
	return &games
}

func (g *Games) Add(host string) (int, error) {
	g.mutex.Lock()
	defer g.mutex.Unlock()

	game := Game{
		Host:            host,
		Players:         make(map[string]int),
		PlayersAnswered: make(map[string]struct{}),
	}

	for i := 0; i < 5; i++ {
		pin := generatePin()
		if _, ok := g.all[pin]; !ok {
			game.Pin = pin
			g.all[pin] = &game
			return pin, nil
		}
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

func (g *Games) Get(pin int) (Game, error) {
	g.mutex.RLock()
	defer g.mutex.RUnlock()
	game, ok := g.all[pin]
	if !ok {
		return Game{}, fmt.Errorf("could not find game with pin %d", pin)
	}
	return *game, nil
}

func (g *Games) GetHostForGame(pin int) string {
	g.mutex.RLock()
	defer g.mutex.RUnlock()
	game, ok := g.all[pin]
	if !ok {
		return ""
	}
	return game.Host
}

func (g *Games) GetPlayersForGame(pin int) []string {
	g.mutex.RLock()
	defer g.mutex.RUnlock()
	p := []string{}
	game, ok := g.all[pin]
	if !ok {
		return p
	}
	for k := range game.Players {
		p = append(p, k)
	}
	return p
}

func (g *Games) Update(game Game) error {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	_, ok := g.all[game.Pin]
	if !ok {
		return fmt.Errorf("game with pin %d does not exist", game.Pin)
	}
	g.all[game.Pin] = &game
	return nil
}

func (g *Games) Delete(pin int) {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	delete(g.all, pin)
}

func (g *Games) AddPlayerToGame(sessionid string, pin int) error {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	game, ok := g.all[pin]
	if !ok {
		return fmt.Errorf("game with pin %d does not exist", pin)
	}
	if _, ok := game.Players[sessionid]; ok {
		// player is already in the game
		return nil
	}

	// player is new in this game
	log.Printf("### added player %s", sessionid)
	game.Players[sessionid] = 0

	return nil
}

func (g *Games) SetGameQuiz(pin int, quiz Quiz) {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	game, ok := g.all[pin]
	if !ok {
		return
	}
	game.Quiz = quiz
}

func (g *Games) DeletePlayerFromGame(sessionid string, pin int) {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	game, ok := g.all[pin]
	if !ok {
		return
	}
	delete(game.Players, sessionid)
	delete(game.PlayersAnswered, sessionid)
}

// Advances the game state to the next state - returns the new state
func (g *Games) NextState(pin int) (int, error) {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	game, ok := g.all[pin]
	if !ok {
		return 0, fmt.Errorf("game with pin %d does not exist", pin)
	}
	switch game.GameState {
	case GameNotStarted:
		// if there are no questions or players, end the game immediately
		if game.Quiz.NumQuestions() == 0 || len(game.Players) == 0 {
			game.GameState = GameEnded
			return game.GameState, nil
		}
		if err := game.setupQuestion(0); err != nil {
			game.GameState = GameEnded
			return game.GameState, fmt.Errorf("error trying to start game: %v", err)
		}
		return game.GameState, nil
	case QuestionInProgress:
		game.GameState = ShowResults
		return game.GameState, nil
	case ShowResults:
		if game.QuestionIndex < game.Quiz.NumQuestions() {
			game.QuestionIndex++
		}
		if game.QuestionIndex >= game.Quiz.NumQuestions() {
			game.GameState = GameEnded
			return game.GameState, nil
		}
		if err := game.setupQuestion(game.QuestionIndex); err != nil {
			game.GameState = GameEnded
			return game.GameState, err
		}
		return game.GameState, nil
	default:
		game.GameState = GameEnded
		return game.GameState, nil
	}
}

// A special instance of NextState() - if we are in the QuestionInProgress
// state, change the state to ShowResults.
// If we are already in ShowResults, do not change the state.
func (g *Games) ShowResults(pin int) error {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	game, ok := g.all[pin]
	if !ok {
		return fmt.Errorf("game with pin %d does not exist", pin)
	}
	if game.GameState != QuestionInProgress && game.GameState != ShowResults {
		return fmt.Errorf("game with pin %d is not in the expected states", pin)
	}
	game.GameState = ShowResults
	return nil
}

func (g *Game) setupQuestion(newIndex int) error {
	g.QuestionIndex = newIndex
	question, err := g.Quiz.GetQuestion(newIndex)
	if err != nil {
		return err
	}
	g.GameState = QuestionInProgress
	g.PlayersAnswered = make(map[string]struct{})
	g.Votes = make([]int, question.NumAnswers())
	g.QuestionDeadline = time.Now().Add(time.Second * time.Duration(g.Quiz.QuestionDuration))
	return nil
}

// Returns - questionIndex, number of seconds left, question, error
func (g *Games) GetCurrentQuestion(pin int) (GameCurrentQuestion, error) {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	game, ok := g.all[pin]
	if !ok {
		return GameCurrentQuestion{}, fmt.Errorf("game with pin %d does not exist", pin)
	}

	if game.GameState != QuestionInProgress {
		return GameCurrentQuestion{}, fmt.Errorf("game with pin %d is not showing a live question", pin)
	}

	now := time.Now()
	timeLeft := int(game.QuestionDeadline.Unix() - now.Unix())
	if timeLeft <= 0 || len(game.PlayersAnswered) >= len(game.Players) {
		game.GameState = ShowResults
		return GameCurrentQuestion{}, fmt.Errorf("game with pin %d should be showing results", pin)
	}

	question, err := game.Quiz.GetQuestion(game.QuestionIndex)
	if err != nil {
		return GameCurrentQuestion{}, err
	}

	return GameCurrentQuestion{
		QuestionIndex: game.QuestionIndex,
		TimeLeft:      timeLeft,
		Answered:      len(game.PlayersAnswered),
		TotalPlayers:  len(game.Players),
		Question:      question.Question,
		Answers:       question.Answers,
	}, nil
}

func (g *Games) GetAnsweredCount(pin int) (int, int, error) {
	g.mutex.RLock()
	defer g.mutex.RUnlock()
	game, ok := g.all[pin]
	if !ok {
		return 0, 0, fmt.Errorf("game with pin %d does not exist", pin)
	}

	if game.GameState != QuestionInProgress {
		return 0, 0, fmt.Errorf("game with pin %d is not showing a live question", pin)
	}

	return len(game.PlayersAnswered), len(game.Players), nil
}

// Results:
// * all players answered
// * number of players answered
// * total players in game
// * error
func (g *Games) RegisterAnswer(pin int, sessionid string, answerIndex int) (bool, int, int, error) {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	game, ok := g.all[pin]
	if !ok {
		return false, 0, 0, fmt.Errorf("game with pin %d does not exist", pin)
	}
	if _, ok := game.Players[sessionid]; !ok {
		return false, 0, 0, fmt.Errorf("player %s is not part of game %d", sessionid, pin)
	}
	if game.GameState != QuestionInProgress {
		return false, 0, 0, fmt.Errorf("game %d is not showing a live question", pin)
	}

	now := time.Now()
	if now.After(game.QuestionDeadline) {
		game.GameState = ShowResults
		return false, 0, 0, fmt.Errorf("question %d in game %d has expired", game.QuestionIndex, pin)
	}

	question, err := game.Quiz.GetQuestion(game.QuestionIndex)
	if err != nil {
		return false, 0, 0, err
	}

	if answerIndex < 0 || answerIndex >= question.NumAnswers() {
		return false, 0, 0, errors.New("invalid answer")
	}

	if _, ok := game.PlayersAnswered[sessionid]; !ok {
		// player hasn't answered yet
		game.PlayersAnswered[sessionid] = struct{}{}

		if answerIndex == question.Correct {
			// calculate score, add to player score
			game.Players[sessionid] += calculateScore(int(game.QuestionDeadline.Unix()-now.Unix()), game.Quiz.QuestionDuration)
		}
		game.Votes[answerIndex]++
	}

	answeredCount := len(game.PlayersAnswered)
	totalPlayers := len(game.Players)
	allAnswered := answeredCount >= totalPlayers
	if allAnswered {
		game.GameState = ShowResults
	}
	return allAnswered, answeredCount, totalPlayers, nil
}

// GetQuestionResults
func (g *Games) GetQuestionResults(pin int) (QuestionResults, error) {
	g.mutex.RLock()
	defer g.mutex.RUnlock()
	game, ok := g.all[pin]
	if !ok {
		return QuestionResults{}, fmt.Errorf("game with pin %d does not exist", pin)
	}
	question, err := game.Quiz.GetQuestion(game.QuestionIndex)
	if err != nil {
		return QuestionResults{}, err
	}
	results := QuestionResults{
		QuestionIndex: game.QuestionIndex,
		Question:      question.Question,
		Answers:       question.Answers,
		Correct:       question.Correct,
		Votes:         game.Votes,
	}

	total := 0
	for _, v := range game.Votes {
		total += v
	}
	results.TotalVotes = total

	return results, nil
}

func (g *Games) GetWinners(pin int) ([]PlayerScore, error) {
	g.mutex.RLock()
	defer g.mutex.RUnlock()
	game, ok := g.all[pin]
	if !ok {
		return []PlayerScore{}, fmt.Errorf("game with pin %d does not exist", pin)
	}

	// copied from https://stackoverflow.com/a/18695740
	pl := make(PlayerScoreList, len(game.Players))
	i := 0
	for k, v := range game.Players {
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
	game, ok := g.all[pin]
	if !ok {
		return 0, fmt.Errorf("game with pin %d does not exist", pin)
	}
	return game.GameState, nil
}

func calculateScore(timeLeft, questionDuration int) int {
	if timeLeft < 0 {
		timeLeft = 0
	}
	return 100 + (timeLeft * 100 / questionDuration)
}
