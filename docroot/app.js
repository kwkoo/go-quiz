
// copied from https://stackoverflow.com/a/10730417
function readCookie(name) {
    var nameEQ = name + "=";
    var ca = document.cookie.split(';');
    for (var i = 0; i < ca.length; i++) {
        var c = ca[i];
        while (c.charAt(0) == ' ') c = c.substring(1, c.length);
        if (c.indexOf(nameEQ) == 0) return c.substring(nameEQ.length, c.length);
    }
    return null;
}

function processIncoming(app, s) {
    let cmd, arg

    s = s.trim()
    if (s.length == 0) return
    let space = s.indexOf(' ')
    if (space == -1) {
        cmd = s
        arg = ''
    } else {
        cmd = s.substring(0, space)
        arg = s.substring(space+1).trim()
    }

    console.log('cmd=' + cmd + ',arg=' + arg)
    switch (cmd) {
        case 'screen':
            app.screen = arg
            break
        case 'all-quizzes':
            try {
                app.selectquiz.quizzes = JSON.parse(arg)
            } catch (err) {
                console.log('err: ' + err)
            }
            break
        case 'lobby-game-metadata':
            try {
                app.gamelobby = JSON.parse(arg)
            } catch (err) {
                console.log('err: ' + err)
            }
            break
        case 'participants-list':
            try {
                app.gamelobby.players = JSON.parse(arg)
            } catch (err) {
                console.log('err: ' + err)
            }
            break
        default:
            console.log('oops!')
    }
}


var app = new Vue({
    el: '#app',
    data: {
        screen: 'start',
        message: 'Hello Vue!',
        selectquiz: {},
        gamelobby: { pin: 0, players: [] },
        enteridentity: { pin: 0, name: ''},
        error: { message: '' },
        sessionid: '',
        conn: {}
    },
    created: function() {
        console.log('created')
    },
    mounted: function() {
        this.sessionid = readCookie('quizsession')
        console.log('cookie: ' + this.sessionid)
        if (this.sessionid == null || this.sessionid.length == 0) {
            this.showError('Please enable cookies')
            return
        }
        if (window["WebSocket"]) {
            var that = this
            that.conn = new WebSocket("ws://" + document.location.host + "/ws")
            that.conn.onopen = function (evt) {
                that.sendCommand("session " + that.sessionid)
            }
            that.conn.onclose = function (evt) {
                that.showError('Connection closed')
            }
            that.conn.onmessage = function (evt) {
                let messages = evt.data.split('\n')
                for (var i=0; i<messages.length; i++) {
                    processIncoming(that, messages[i])
                }
            }
        } else {
            this.showError('Your browser does not support WebSockets')
        }
    },
    methods: {
        hello: function() {
        console.log('hello method')
        },
        showError: function(message, next) {
            this.error.message = message
            this.screen = 'error'
        },
        sendCommand: function(command) {
            this.conn.send(command)
        },
        selectQuiz: function(quizid) {
            this.sendCommand('game-lobby ' + quizid)
        },
        joinGame: function() {
            this.sendCommand('join-game ' + JSON.stringify({name: this.enteridentity.name, pin: this.enteridentity.pin}))

        },
        parse: function() {
            let arg = '[{"id":1,"string":"Session 1 - Best Practices"},{"id":2,"string":"Session 2 - Quarkus"},{"id":3,"string":"Session 3 - Event Driven"},{"id":4,"string":"Session 4 - Keycloak"}]'
            let obj = JSON.parse(arg)
            console.log(obj)
        }
    }
})
