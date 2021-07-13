var app = new Vue({
    el: '#app',

    data: {
        screen: 'start',
        message: '',
        quiz: {
            name: '',
            questionduration: 20,
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
            console.log("add")
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
            console.log("update")
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
                this.message = errors.join(', ')
                this.screen = 'message'
                return
            }

            console.log(JSON.stringify(copy))
            // todo: send this to the server
            // todo: send this to list quiz view
        },

        cancelQuiz: function() {
            // todo: send this to list quiz view
        },

        dismissMessage: function() {
            this.screen = 'start'
            this.message = ''
        },
    }
})