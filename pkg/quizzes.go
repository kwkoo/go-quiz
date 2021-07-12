package pkg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sort"
	"sync"
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

type Quizzes struct {
	all    map[int]Quiz
	mutex  sync.RWMutex
	engine *PersistenceEngine
}

func InitQuizzes(engine *PersistenceEngine) (*Quizzes, error) {
	if engine == nil {
		log.Print("initializing quizzes with no persistence engine")
		return &Quizzes{all: make(map[int]Quiz)}, nil
	}

	keys, err := engine.GetKeys("quiz")
	if err != nil {
		return nil, fmt.Errorf("could not retrieve keys from redis: %v", err)
	}

	all := make(map[int]Quiz)

	for _, key := range keys {
		data, err := engine.Get(key)
		if err != nil {
			log.Print(err.Error())
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(data))
		var quiz Quiz
		if err := dec.Decode(&quiz); err != nil {
			log.Printf("error parsing JSON from redis for key %s: %v", key, err)
			continue
		}
		all[quiz.Id] = quiz
	}

	log.Printf("ingested %d quizzes", len(all))
	return &Quizzes{
		all:    all,
		engine: engine,
	}, nil
}

func (q *Quizzes) GetQuizzes() []Quiz {
	q.mutex.RLock()
	defer q.mutex.RUnlock()
	ids := make([]int, len(q.all))

	i := 0
	for k := range q.all {
		ids[i] = k
		i++
	}
	sort.Ints(ids)

	r := make([]Quiz, len(ids))
	for i, id := range ids {
		r[i] = q.all[id]
	}
	return r
}

func (q *Quizzes) Get(id int) (Quiz, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()
	quiz, ok := q.all[id]
	if !ok {
		return Quiz{}, fmt.Errorf("could not find quiz with id %d", id)
	}
	return quiz, nil
}

func (q *Quizzes) Delete(id int) {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	delete(q.all, id)
}

func (q *Quizzes) Add(quiz Quiz) (Quiz, error) {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	quiz.Id = q.nextID()

	if q.engine != nil {
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		if err := enc.Encode(quiz); err != nil {
			return Quiz{}, fmt.Errorf("error converting quiz to JSON: %v", err)
		}
		if err := q.engine.Set(fmt.Sprintf("quiz:%d", quiz.Id), b.Bytes(), 0); err != nil {
			return Quiz{}, fmt.Errorf("error persisting quiz to redis: %v", err)
		}
	}

	q.all[quiz.Id] = quiz
	return quiz, nil
}

func (q *Quizzes) Update(quiz Quiz) error {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	if _, ok := q.all[quiz.Id]; !ok {
		return fmt.Errorf("quiz id %d does not exist", quiz.Id)
	}
	q.all[quiz.Id] = quiz
	return nil
}

func (q *Quizzes) nextID() int {
	highest := 0
	for key := range q.all {
		if key > highest {
			highest = key
		}
	}
	return highest + 1
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
