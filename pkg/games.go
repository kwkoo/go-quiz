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
	Id    string `json:"id"` // this is set to the session ID in games, but can be replaced with the player name somewhere else
	Score int    `json:"score"`
}

type PlayerScoreList []PlayerScore

func (p PlayerScoreList) Len() int           { return len(p) }
func (p PlayerScoreList) Less(i, j int) bool { return p[i].Score < p[j].Score }
func (p PlayerScoreList) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }

type Game struct {
	Pin              int                 `json:"pin"`
	Host             string              `json:"host"`    // session ID of game host
	Players          map[string]int      `json:"players"` // scores of players
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

// Returns true if the player was added - false if the player is already in
// the game
func (g *Game) addPlayer(sessionid string) bool {
	if _, ok := g.Players[sessionid]; ok {
		// player is already in the game
		return false
	}

	// player is new in this game
	g.Players[sessionid] = 0
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
		pl[i] = PlayerScore{k, v}
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
}

func InitGames(engine *PersistenceEngine) *Games {
	games := Games{
		all:    make(map[int]*Game),
		engine: engine,
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

func (g *Games) AddPlayerToGame(sessionid string, pin int) error {
	game, err := g.getGamePointer(pin)
	if err != nil {
		return NewNoSuchGameError(pin)
	}

	if game.GameState != GameNotStarted {
		return errors.New("game is not accepting new players")
	}

	g.mutex.Lock()
	changed := game.addPlayer(sessionid)
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
