package internal

import (
	"testing"
)

func TestCalculateScore(t *testing.T) {
	tests := []struct {
		timeLeft         int
		questionDuration int
		expectedScore    int
	}{
		{0, 10, 100},
		{5, 10, 150},
		{10, 10, 200},
	}

	for _, test := range tests {
		score := calculateScore(test.timeLeft, test.questionDuration)
		if score != test.expectedScore {
			t.Errorf("expected a score of %d but got %d", test.expectedScore, score)
		}
	}
}
