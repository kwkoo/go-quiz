package pkg

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
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
	q     map[int]Quiz
	mutex sync.RWMutex
}

func InitQuizzes() (*Quizzes, error) {
	s := `[{"_id":{"$oid":"60d977597c8643fd09ff86ed"},"id":1,"name":"Session 1 - Best Practices","questionDuration":30,"questions":[{"question":"What is the Star Trek ex-borg character name Kubernetes original project name refer to?","answers":["Seven Eleven","Seven of Nine","Seventh Heaven","Seven Deadly Sins"],"correct":1},{"question":"How can I pass my configuration to my container image?","answers":["Fedex","Grab","Secret / ConfigMap","Database"],"correct":2},{"question":"Which pod will be evicted first if the server runs out of resources?","answers":["Pod with \"Guaranteed\" QoS","Pod with \"Burstable\" QoS","Pod with \"BestEffort\" QoS","Pod with \"Important\" QoS"],"correct":2},{"question":"What is the name of the container images whose sole purpose is to build and compile projects?","answers":["Builder Image","Bob The Builder","Handy Manny","Builder VM"],"correct":0},{"question":"What is used to secure communications between microservices?","answers":["SMS / MMS","Grab / FoodPanda","TLS / SSL","Lock / Key"],"correct":2}]},{"_id":{"$oid":"60c9824caf4e0300177308d8"},"id":2,"name":"Session 2 - Quarkus","questionDuration":45,"questions":[{"question":"What is Quarkus' slogan?","answers":["Write once, run anywhere","Build great things at any scale","Supersonic Subatomic Java","There's more than one way to do it"],"correct":2},{"question":"How does Quarkus bring Developer joy?","answers":["Fast compiles","Live reloads","Platform independent","Dynamically typed"],"correct":1},{"question":"Which framework is not in Quarkus?","answers":["Vert.x","RESTEasy","Hibernate","JavaServer Faces"],"correct":3},{"question":"Which is the most popular language used on AWS Lambda?","answers":["Java","Node.js","Python","Rust"],"correct":1},{"question":"Which open-source license is Quarkus published under?","answers":["Apache","BSD","MIT","Creative Commons"],"correct":0}]},{"_id":{"$oid":"60e375d27c725aaf719325b0"},"id":3,"name":"Session 3 - Event Driven","questionDuration":30,"questions":[{"question":"Which component is responsible for hosting topics and delivering messages?","answers":["Kafka Connect","Mirror Maker","Kafka Streams API","Kafka Broker"],"correct":3},{"question":"Which database is not on Debezium's supported list?","answers":["Redis","MongoDB","Microsoft SQL Server","MySQL"],"correct":0},{"question":"Which attribute is not associated with Event Streaming?","answers":["Repeatable ordering","Message replays","Store-and-forward","Partitioning"],"correct":2}]},{"_id":{"$oid":"60e3798f7c725aa10c9325b1"},"id":4,"name":"Session 4 - Keycloak","questionDuration":30,"questions":[{"question":"What is the Red Hat product name for Keycloak?","answers":["Red Hat Keycloak SSO","Red Hat Keycloak Enterprise Edition","Red Hat Single Sign-On","Red Hat Identity Server"],"correct":2},{"question":"Which of the folowing is not a Keycloak feature?","answers":["Identity Brokering","Operating System SSO","Adapters","2-factor authentication"],"correct":1},{"question":"Which of the following is true about Keycloak Storage Federation?","answers":["Keycloak can replicate user databases for failover and DR","Keycloak can federate mulitple external user registries","Keycloak does not provide an API for customer plugins","Keycloak only works with Active Directory"],"correct":1},{"question":"The following are supported authorization mechanisms in Keycloak except:","answers":["Content-based","Role-based","Context-based","Time-based"],"correct":0},{"question":"One of the reasons OpenID Connect is better than OAuth 2.0","answers":["Industry open standard","To address authentication use cases","Designed for authorization flow","It's older"],"correct":1}]}]`
	dec := json.NewDecoder(strings.NewReader(s))

	var q []Quiz
	if err := dec.Decode(&q); err != nil {
		return nil, err
	}

	quizzes := &Quizzes{
		q: make(map[int]Quiz),
	}
	for _, quiz := range q {
		quizzes.q[quiz.Id] = quiz
	}

	log.Printf("ingested %d quizzes", len(quizzes.q))
	return quizzes, nil
}

func (q *Quizzes) Get(id int) (Quiz, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()
	quiz, ok := q.q[id]
	if !ok {
		return Quiz{}, fmt.Errorf("could not find quiz with id %d", id)
	}
	return quiz, nil
}

func (q *Quizzes) Delete(id int) {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	delete(q.q, id)
}

func (q *Quizzes) Add(quiz Quiz) (Quiz, error) {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	quiz.Id = q.nextID()
	q.q[quiz.Id] = quiz
	return quiz, nil
}

func (q *Quizzes) Update(quiz Quiz) error {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	if _, ok := q.q[quiz.Id]; !ok {
		return fmt.Errorf("quiz id %d does not exist", quiz.Id)
	}
	q.q[quiz.Id] = quiz
	return nil
}

func (q *Quizzes) nextID() int {
	highest := 0
	for key := range q.q {
		if key > highest {
			highest = key
		}
	}
	return highest + 1
}
