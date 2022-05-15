package common

import (
	"math/rand"
	"testing"
	"time"
)

func TestShufflAnswers(t *testing.T) {
	rand.Seed(time.Now().UnixNano())

	tests := []struct {
		quizQuestion  QuizQuestion
		correctAnswer string
	}{
		{
			quizQuestion: QuizQuestion{
				Question: "question 0",
				Answers:  []string{"zero", "one", "two", "three"},
				Correct:  2,
			},
			correctAnswer: "two",
		},
		{
			quizQuestion: QuizQuestion{
				Question: "question 1",
				Answers:  []string{"hello", "world", "my", "name"},
				Correct:  0,
			},
			correctAnswer: "hello",
		},
		{
			quizQuestion: QuizQuestion{
				Question: "question 2",
				Answers:  []string{"wrong 0", "wrong 1", "wrong 2", "correct"},
				Correct:  3,
			},
			correctAnswer: "correct",
		},
	}

	for _, test := range tests {
		t.Logf("before shuffling: %v", test.quizQuestion)
		shuffled := test.quizQuestion.ShuffleAnswers()
		t.Logf("after shuffling: %v", shuffled)
		if test.correctAnswer != shuffled.Answers[shuffled.Correct] {
			t.Errorf("expected correct ansewr of %s but got %s", test.correctAnswer, shuffled.Answers[shuffled.Correct])
		}
	}
}
