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

func (q Quiz) marshal() ([]byte, error) {
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
	delete(q.all, id)
	q.mutex.Unlock()

	if q.engine != nil {
		q.engine.Delete(fmt.Sprintf("quiz:%d", id))
	}
}

func (q *Quizzes) Add(quiz Quiz) (Quiz, error) {
	var err error
	quiz.Id, err = q.nextID()
	if err != nil {
		return Quiz{}, err
	}

	if q.engine != nil {
		encoded, err := quiz.marshal()
		if err != nil {
			return Quiz{}, fmt.Errorf("error converting quiz to JSON: %v", err)
		}
		if err := q.engine.Set(fmt.Sprintf("quiz:%d", quiz.Id), encoded, 0); err != nil {
			return Quiz{}, fmt.Errorf("error persisting quiz to redis: %v", err)
		}
	}

	q.mutex.Lock()
	q.all[quiz.Id] = quiz
	q.mutex.Unlock()
	return quiz, nil
}

func (q *Quizzes) Update(quiz Quiz) error {
	q.mutex.Lock()
	q.all[quiz.Id] = quiz
	q.mutex.Unlock()

	if q.engine != nil {
		encoded, err := quiz.marshal()
		if err != nil {
			return fmt.Errorf("error converting quiz to JSON: %v", err)
		}
		if err := q.engine.Set(fmt.Sprintf("quiz:%d", quiz.Id), encoded, 0); err != nil {
			return fmt.Errorf("error persisting quiz to redis: %v", err)
		}
	}
	return nil
}

func (q *Quizzes) nextID() (int, error) {
	if q.engine == nil {
		q.mutex.RLock()
		defer q.mutex.RUnlock()
		highest := 0
		for key := range q.all {
			if key > highest {
				highest = key
			}
		}
		return highest + 1, nil
	}
	id, err := q.engine.Incr("quizid")
	if err != nil {
		return 0, fmt.Errorf("error generating quiz ID from persistent store: %v", err)
	}
	return id, nil
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
