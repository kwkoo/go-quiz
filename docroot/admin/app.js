var app = new Vue({
    el: '#app',

    data: {
        screen: 'start',
        list: { quizzes: null },
        message: { text: '', next: ''},
        quiz: {
            name: '',
            questionDuration: 20,
            questions: [
                {
                    question: '',
                    answers: ['', '', '', ''],
                    correct: 0
                }
            ]
        },
    },

    mounted: function() {
        this.loadQuizzes()
    },

    methods: {

        showScreen: function(screen) {
            if (screen == 'start') {
                this.loadQuizzes()
            }
            this.screen = screen
        },

        loadQuizzes: function() {
            let xhr = new XMLHttpRequest()
            let that = this
            xhr.onreadystatechange = function() {
                if (this.readyState == 4) {
                    try {
                        that.list.quizzes = JSON.parse(xhr.responseText)
                    } catch (err) {
                        that.showMessage(err, '')
                    }
                }
            }
            xhr.open('GET', '/api/quiz', true)
            xhr.send()
        },

        deleteQuiz: function(id) {
            let xhr = new XMLHttpRequest()
            let that = this
            xhr.onreadystatechange = function() {
                if (this.readyState == 4) {
                    that.loadQuizzes()
                }
            }
            xhr.open('DELETE', '/api/quiz/' + id)
            xhr.send()
        },

        newQuiz: function() {
            this.quiz = {
                name: '',
                questionDuration: 20,
                questions: [
                    {
                        question: '',
                        answers: ['', '', '', ''],
                        correct: 0
                    }
                ]
            }
            this.showScreen('creator')
        },

        editQuiz: function(index) {
            let copy = JSON.parse(JSON.stringify(this.list.quizzes[index]))

            // ensure that there are 4 answers for every question
            copy.questions.forEach(function (question, index) {
                while (question.answers.length < 4) {
                    question.answers.push('')
                }
            })
            this.quiz = copy
            this.showScreen('creator')
        },

        addQuestion: function() {
            this.quiz.questions.push({
                question: '',
                answers: ['', '', '', ''],
                correct: 0
            })
        },

        deleteQuestion: function(index) {
            this.quiz.questions.splice(index, 1)
        },

        updateDatabase: function() {
            if (this.quiz.name == '') {
                this.showMessage('Please fill in the quiz title', 'creator')
                return
            }

            // remove empty answers
            let errors = []
            copy = JSON.parse(JSON.stringify(this.quiz))
            copy.questions.forEach(function (question, index) {
                while (question.answers.length > 0 && question.answers[question.answers.length-1] == '') {
                    question.answers.splice(-1, 1)
                }
                if (question.correct < 0 || question.correct >= question.answers.length) {
                    errors.push("Invalid correct field for question " + index)
                }
            })

            if (errors.length > 0) {
                this.showMessage(errors.join(', '), 'creator')
                return
            }

            let xhr = new XMLHttpRequest()
            let that = this
            xhr.onreadystatechange = function() {
                if (this.readyState == 4) {
                    try {
                        let data = JSON.parse(xhr.responseText)
                        if (data.success) {
                            that.showMessage('Quiz added', 'start')
                            console.log('success')
                        } else {
                            console.log('error')
                            that.showMessage(data.error, '')
                        }
                    } catch (err) {
                        console.log('exception')
                        that.showMessage(err, '')
                    }
                }
            }
            xhr.open('PUT', '/api/quiz')
            xhr.setRequestHeader('Content-Type', 'application/json;charset=UTF-8')
            xhr.send(JSON.stringify(copy))
        },

        cancelQuiz: function() {
            this.showScreen('start')
        },

        showMessage: function(message, next) {
            this.message.text = message
            this.message.next = next
            this.screen = 'message'
        },

        dismissMessage: function() {
            if (this.message.next != '') {
                this.showScreen(this.message.next)
                this.message.next = ''
            }
            this.message.text = ''
        },
    }
})