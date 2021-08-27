# Event Driven Design

## session

* Hub
* | SessionMessage(*Client, sessionid)
* Session
	* Check for validity
	* Check that session exists
		* If session exists, tie *Client to sessionid
		* If session doesn't exist, create new entry, send message to Hub to direct to entrance

## join-game

* Hub
* | JoinGameMessage(*Client, pin, name)
* Session
	* Check for validity
	* Check that session exists
		* If session doesn't exist, send error message to hub about no session
		* If session exists, send message to Games
		* | JoinGameMessage(*Client, Session, pin, name)
		* Games
			* 