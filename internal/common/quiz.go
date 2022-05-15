package common

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
)

type QuizQuestion struct {
	Question string   `json:"question"`
	Answers  []string `json:"answers"`
	Correct  int      `json:"correct"`
}

func (q QuizQuestion) NumAnswers() int {
	return len(q.Answers)
}

func (q QuizQuestion) ShuffleAnswers() QuizQuestion {
	places := []int{}
	for i := 0; i < len(q.Answers); i++ {
		places = append(places, i)
	}

	newIndex := []int{}
	for len(places) > 0 {
		selected := rand.Intn(len(places))
		newIndex = append(newIndex, places[selected])
		places = append(places[:selected], places[selected+1:]...)
	}

	q.Correct = newIndex[q.Correct]
	newAnswers := make([]string, len(q.Answers))
	for i, answer := range q.Answers {
		newAnswers[newIndex[i]] = answer
	}
	q.Answers = newAnswers
	return q
}

func (q QuizQuestion) String() string {
	s, _ := ConvertToJSON(q)
	return s
}

type Quiz struct {
	Id               int            `json:"id"`
	Name             string         `json:"name"`
	QuestionDuration int            `json:"questionDuration"`
	ShuffleAnswers   bool           `json:"shuffleAnswers"`
	Questions        []QuizQuestion `json:"questions"`
}

func (q Quiz) NumQuestions() int {
	return len(q.Questions)
}

func (q Quiz) GetQuestion(i int) (QuizQuestion, error) {
	if i < 0 || i >= len(q.Questions) {
		return QuizQuestion{}, fmt.Errorf("%d is an invalid question index", i)
	}
	return q.Questions[i], nil
}

func (q Quiz) Marshal() ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	if err := enc.Encode(q); err != nil {
		return nil, fmt.Errorf("error converting quiz to JSON: %v", err)
	}
	return b.Bytes(), nil
}

// Ingests a single Quiz object in JSON
func UnmarshalQuiz(r io.Reader) (Quiz, error) {
	dec := json.NewDecoder(r)
	var quiz Quiz
	if err := dec.Decode(&quiz); err != nil {
		return Quiz{}, err
	}
	return quiz, nil
}

// Ingests an array of Quiz objects in JSON
func UnmarshalQuizzes(r io.Reader) ([]Quiz, error) {
	dec := json.NewDecoder(r)
	var quizzes []Quiz
	if err := dec.Decode(&quizzes); err != nil {
		return nil, err
	}
	return quizzes, nil
}
