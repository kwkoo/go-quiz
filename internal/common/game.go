package common

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
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

type NameExistsInGameError struct {
	Pin  int
	Name string
}

func (e *NameExistsInGameError) Error() string {
	return fmt.Sprintf("%s already exists in game %d", e.Name, e.Pin)
}

func NewNameExistsInGameError(name string, pin int) *NameExistsInGameError {
	return &NameExistsInGameError{
		Pin:  pin,
		Name: name,
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
	Pin              int                 `json:"pin"`
	Host             string              `json:"host"`    // session ID of game host
	Players          map[string]int      `json:"players"` // scores of players
	PlayerNames      map[string]string   `json:"playernames"`
	Quiz             Quiz                `json:"quiz"`
	QuestionIndex    int                 `json:"questionindex"`    // current question
	QuestionDeadline time.Time           `json:"questiondeadline"` // answers must come in at this time or before
	PlayersAnswered  map[string]struct{} `json:"playersanswered"`
	CorrectPlayers   map[string]struct{} `json:"correctplayers"` // players that answered current question correctly
	Votes            []int               `json:"votes"`          // number of players that answered each choice
	GameState        int                 `json:"gamestate"`
}

func UnmarshalGame(b []byte) (*Game, error) {
	var game Game
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&game); err != nil {
		return nil, err
	}
	return &game, nil
}

func (g *Game) Marshal() ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	if err := enc.Encode(g); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func (g *Game) Copy() Game {
	target := Game{
		Pin:              g.Pin,
		Host:             g.Host,
		Players:          make(map[string]int),
		PlayerNames:      make(map[string]string),
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

	for k, v := range g.PlayerNames {
		target.PlayerNames[k] = v
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

func (g *Game) GetPlayers() []string {
	players := make([]string, len(g.Players))

	i := 0
	for player := range g.Players {
		players[i] = player
		i++
	}
	return players
}

func (g *Game) GetPlayerNames() []string {
	names := []string{}
	for _, v := range g.PlayerNames {
		names = append(names, v)
	}
	sort.Strings(names)
	return names
}

// Returns true if the player was added - false if the player is already in
// the game
func (g *Game) AddPlayer(sessionid, name string) bool {
	if _, ok := g.Players[sessionid]; ok {
		// player is already in the game
		return false
	}

	// player is new in this game
	g.Players[sessionid] = 0
	g.PlayerNames[sessionid] = name
	return true
}

func (g *Game) NameExistsInGame(name string) bool {
	lowerName := strings.ToLower(name)
	for _, v := range g.PlayerNames {
		if lowerName == strings.ToLower(v) {
			return true
		}
	}
	return false
}

func (g *Game) SetQuiz(quiz Quiz) {
	g.Quiz = quiz
}

func (g *Game) DeletePlayer(sessionid string) {
	delete(g.Players, sessionid)
	delete(g.PlayersAnswered, sessionid)
	delete(g.CorrectPlayers, sessionid)
}

func (g *Game) NextState() (int, error) {
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

func (g *Game) ShowResults() error {
	if g.GameState != QuestionInProgress && g.GameState != ShowResults {
		return NewUnexpectedStateError(g.GameState, fmt.Sprintf("game with pin %d is not in the expected state", g.Pin))
	}
	g.GameState = ShowResults
	return nil
}

// Returns true if state was changed
func (g *Game) GetCurrentQuestion() (bool, GameCurrentQuestion, error) {
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
func (g *Game) RegisterAnswer(sessionid string, answerIndex int) (bool, AnswersUpdate, error) {
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

func (g *Game) GetQuestionResults() (QuestionResults, error) {
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
		TopScorers:     g.GetWinners(),
	}

	return results, nil
}

func (g *Game) GetWinners() []PlayerScore {
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

func (g *Game) GetGameState() int {
	return g.GameState
}

func calculateScore(timeLeft, questionDuration int) int {
	if timeLeft < 0 {
		timeLeft = 0
	}
	return 100 + (timeLeft * 100 / questionDuration)
}
