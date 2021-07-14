var app = new Vue({
    el: '#app',

    data: {
        screen: 'creator',
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

    methods: {

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
                console.log('readyState: ' + this.readyState)
                if (this.readyState == 4) {
                    try {
                        let data = JSON.parse(xhr.responseText)
                        if (data.success) {
                            that.showMessage('Quiz added', 'creator') // todo: should send this to list view
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
            // todo: send this to list quiz view
        },

        showMessage: function(message, next) {
            this.message.text = message
            this.message.next = next
            this.screen = 'message'
        },

        dismissMessage: function() {
            if (this.message.next != '') {
                this.screen = this.message.next
                this.message.next = ''
            }
            this.message.text = ''
        },
    }
})