# Go Quiz App

A Kahoot clone.

## Resources

* [gorilla websocket example](https://github.com/gorilla/websocket/tree/master/examples/chat)


## To-Do

* let host start game
* broadcast question to game participants


## Quiz Host Messages

* *host starts in connecting to server screen*
* host → server: session SESSION-ID
* server → host: screen enter-identity
* *host clicks on host a game*
* host → server: host-game
* server → host: all-quizzes [{"id":1,"name":"Quiz 1"},{"id":2,"name":"Quiz 2"}]
* server → host: screen select-quiz
* host → server: game-lobby 1
* server → host: lobby-game-metadata {"id":1,"name":"Quiz 1","pin":1234}
* server → host: screen game-lobby
* server → host: participants-list ["user1", "user2", "user3"]
* host → server: start-game
* server → host: show-question {"questionindex":0, "timeleft":30, "question":"What did I eat for breakfast?", "answers":["answer 0", "answer 1", "answer 2", "answer 3"]}
* server → host: screen show-question
* server → host: players-answered {"answered": 2, "total": 10}
* server → host: players-answered {"answered": 3, "total": 10}
* *time runs out, stop timer, enable show results button*
* host → server: show-results
* host → server: question-results {"questionindex":0, "question":"What did I eat for breakfast?", "answers":["answer 0", "answer 1", "answer 2", "answer 3"], "correct": 0, "votes":[1,2,3,3], "totalvotes": 9}
* server → host: screen show-question-results
* host → server: next-question
* server → host: show-question {"questionindex":1, "timeleft":30, "question":"What did I eat for lunch?", "answers":["answer 0", "answer 1", "answer 2", "answer 3"]}
* server → host: screen show-question
* server → host: players-answered {"answered": 10, "total": 10}
* server → host: stop-timer
* *enable show results button*
* host → server: show-results
* host → server: question-results {"questionindex":1, "question":"What did I eat for lunch?", "answers":["answer 0", "answer 1", "answer 2", "answer 3"], "correct": 0, "votes":[1,2,3,4], "totalvotes": 10}
* server → host: screen show-question-results
* host → server: next-question
* server → host: show-winners [{"name": "user1", "score": 500}, {"name": "user2", "score": 300}, {"name": "user3", "score": 200}]
* server → host: screen show-game-results
* host → server: delete-game
* server → host: all-quizzes [{"id":1,"name":"Quiz 1"},{"id":2,"name":"Quiz 2"}]
* server → host: screen select-quizzes


## Player Messages

* *player starts in connecting to server screen*
* player → server: session SESSION-ID
* server → player: screen enter-identity
* player → server: join-game {"pin": 1234, "name": "user1"}
* server → player: screen wait-for-game-start
* server → player: screen answer-question
* player → server: answer 2
* server → player: screen wait-for-question-end
* server → player: player-results {"correct": true, "score": 180}
* server → player: screen display-player-results
* server → player: screen answer-question
* *player does not answer the question*
* server → player: player-results {"correct": false, "score": 180}
* *the game ends*
* server → player: screen enter-identity
