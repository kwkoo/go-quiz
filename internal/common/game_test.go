package common

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

func TestNameExistsInGame(t *testing.T) {
	tests := []struct {
		playerNames      []string
		newPlayer        string
		expectedResponse bool
	}{
		{[]string{"abc"}, "ABC", true}, // case-sensitivity
		{[]string{"abc"}, "abc", true},
		{[]string{"abc"}, "abcd", false},
	}

	game := Game{}

	for testIndex, test := range tests {
		m := make(map[string]string)
		for _, p := range test.playerNames {
			m[p] = p
		}
		game.PlayerNames = m

		response := game.NameExistsInGame(test.newPlayer)
		if response != test.expectedResponse {
			t.Errorf("expected a response of %v but got %v instead for test index %d", test.expectedResponse, response, testIndex)
		}
	}

}
