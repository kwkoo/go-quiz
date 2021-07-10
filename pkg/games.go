package pkg

import (
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

type UnexpectedStateError struct {
	CurrentState int
	Err          error
}

func (e *UnexpectedStateError) Error() string {
	return fmt.Sprintf("Game is in an unexpected state: %v", e.Err.Error())
}

func NewUnexpectedStateError(state int, message string) *UnexpectedStateError {
	return &UnexpectedStateError{
		CurrentState: state,
		Err:          errors.New(message),
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
	QuestionIndex  int      `json:"questionindex"`
	Question       string   `json:"question"`
	Answers        []string `json:"answers"`
	Correct        int      `json:"correct"`
	Votes          []int    `json:"votes"`
	TotalVotes     int      `json:"totalvotes"`
	TotalQuestions int      `json:"totalquestions"`
	TotalPlayers   int      `json:"totalplayers"`
}

type PlayerScore struct {
	Sessionid string
	Score     int
}

type PlayerScoreList []PlayerScore

func (p PlayerScoreList) Len() int           { return len(p) }
func (p PlayerScoreList) Less(i, j int) bool { return p[i].Score < p[j].Score }
func (p PlayerScoreList) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }

type Game struct {
	Pin              int            `json:"pin"`
	Host             string         `json:"host"`    // session ID of game host
	Players          map[string]int `json:"players"` // scores of players
	Quiz             Quiz           `json:"quiz"`
	QuestionIndex    int            `json:"questionindex"`    // current question
	QuestionDeadline time.Time      `json:"questiondeadline"` // answers must come in at this time or before
	PlayersAnswered  map[string]struct{}
	CorrectPlayers   map[string]struct{} // players that answered current question correctly
	Votes            []int               `json:"votes"` // number of players that answered each choice
	GameState        int                 `json:"gamestate"`
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

func (g *Game) addPlayer(sessionid string) {
	if _, ok := g.Players[sessionid]; ok {
		// player is already in the game
		return
	}

	// player is new in this game
	g.Players[sessionid] = 0
	log.Printf("added player %s to game %d", sessionid, g.Pin)
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
		return NewUnexpectedStateError(g.GameState, fmt.Sprintf("game with pin %d is not in the expected states", g.Pin))
	}
	g.GameState = ShowResults
	return nil
}

// Returns - questionIndex, number of seconds left, question, error
func (g *Game) getCurrentQuestion() (GameCurrentQuestion, error) {
	if g.GameState != QuestionInProgress {
		return GameCurrentQuestion{}, NewUnexpectedStateError(g.GameState, fmt.Sprintf("game with pin %d is not showing a live question", g.Pin))
	}

	now := time.Now()
	timeLeft := int(g.QuestionDeadline.Unix() - now.Unix())
	if timeLeft <= 0 || len(g.PlayersAnswered) >= len(g.Players) {
		g.GameState = ShowResults
		return GameCurrentQuestion{}, fmt.Errorf("game with pin %d should be showing results", g.Pin)
	}

	question, err := g.Quiz.GetQuestion(g.QuestionIndex)
	if err != nil {
		return GameCurrentQuestion{}, err
	}

	return GameCurrentQuestion{
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

func (g *Game) registerAnswer(sessionid string, answerIndex int) (AnswersUpdate, error) {
	if _, ok := g.Players[sessionid]; !ok {
		return AnswersUpdate{}, fmt.Errorf("player %s is not part of game %d", sessionid, g.Pin)
	}
	if g.GameState != QuestionInProgress {
		return AnswersUpdate{}, fmt.Errorf("game %d is not showing a live question", g.Pin)
	}

	now := time.Now()
	if now.After(g.QuestionDeadline) {
		g.GameState = ShowResults
		return AnswersUpdate{}, fmt.Errorf("question %d in game %d has expired", g.QuestionIndex, g.Pin)
	}

	question, err := g.Quiz.GetQuestion(g.QuestionIndex)
	if err != nil {
		return AnswersUpdate{}, err
	}

	if answerIndex < 0 || answerIndex >= question.NumAnswers() {
		return AnswersUpdate{}, errors.New("invalid answer")
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
	return AnswersUpdate{
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
	}

	return results, nil
}

func (g *Game) getWinners() ([]PlayerScore, error) {
	// copied from https://stackoverflow.com/a/18695740
	pl := make(PlayerScoreList, len(g.Players))
	i := 0
	for k, v := range g.Players {
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

func (g *Game) getGameState() int {
	return g.GameState
}

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
	return 1
	// todo: commented this out to make testing easier
	/*
		b := make([]byte, 4)
		rand.Read(b)

		total := int(b[0]) + int(b[1]) + int(b[2]) + int(b[3])
		total = total % 998
		total++
		return total
	*/
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
	game, ok := g.all[pin]
	if !ok {
		return []string{}
	}
	return game.getPlayers()
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

	game.addPlayer(sessionid)
	return nil
}

func (g *Games) SetGameQuiz(pin int, quiz Quiz) {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	game, ok := g.all[pin]
	if !ok {
		return
	}
	game.setQuiz(quiz)
}

func (g *Games) DeletePlayerFromGame(sessionid string, pin int) {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	game, ok := g.all[pin]
	if !ok {
		return
	}
	game.deletePlayer(sessionid)
}

// Advances the game state to the next state - returns the new state
func (g *Games) NextState(pin int) (int, error) {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	game, ok := g.all[pin]
	if !ok {
		return 0, fmt.Errorf("game with pin %d does not exist", pin)
	}
	return game.nextState()
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
	return game.showResults()
}

// Returns - questionIndex, number of seconds left, question, error
func (g *Games) GetCurrentQuestion(pin int) (GameCurrentQuestion, error) {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	game, ok := g.all[pin]
	if !ok {
		return GameCurrentQuestion{}, fmt.Errorf("game with pin %d does not exist", pin)
	}

	return game.getCurrentQuestion()
}

/*
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
*/

func (g *Games) RegisterAnswer(pin int, sessionid string, answerIndex int) (AnswersUpdate, error) {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	game, ok := g.all[pin]
	if !ok {
		return AnswersUpdate{}, fmt.Errorf("game with pin %d does not exist", pin)
	}
	return game.registerAnswer(sessionid, answerIndex)
}

func (g *Games) GetQuestionResults(pin int) (QuestionResults, error) {
	g.mutex.RLock()
	defer g.mutex.RUnlock()
	game, ok := g.all[pin]
	if !ok {
		return QuestionResults{}, fmt.Errorf("game with pin %d does not exist", pin)
	}
	return game.getQuestionResults()
}

func (g *Games) GetWinners(pin int) ([]PlayerScore, error) {
	g.mutex.RLock()
	defer g.mutex.RUnlock()
	game, ok := g.all[pin]
	if !ok {
		return []PlayerScore{}, fmt.Errorf("game with pin %d does not exist", pin)
	}
	return game.getWinners()
}

func (g *Games) GetGameState(pin int) (int, error) {
	g.mutex.RLock()
	defer g.mutex.RUnlock()
	game, ok := g.all[pin]
	if !ok {
		return 0, fmt.Errorf("game with pin %d does not exist", pin)
	}
	return game.getGameState(), nil
}

func calculateScore(timeLeft, questionDuration int) int {
	if timeLeft < 0 {
		timeLeft = 0
	}
	return 100 + (timeLeft * 100 / questionDuration)
}
