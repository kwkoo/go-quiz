package common

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

type QuizQuestion struct {
	Question string   `json:"question"`
	Answers  []string `json:"answers"`
	Correct  int      `json:"correct"`
}

func (q QuizQuestion) NumAnswers() int {
	return len(q.Answers)
}

type Quiz struct {
	Id               int            `json:"id"`
	Name             string         `json:"name"`
	QuestionDuration int            `json:"questionDuration"`
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
