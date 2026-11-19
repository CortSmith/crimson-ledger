package app

// All text the user can see from Go lives here (one place to translate or reword). Plain language, no jargon.
const (
	msgNeedSettings   = "Enter the server address and API key, then press Start."
	msgNeedLogsDir    = "Choose the folder that holds your World of Warcraft combat logs."
	msgBadLogsDir     = "That folder does not exist or is not a folder."
	msgKeyRejected    = "API key rejected. Check the key and press Start."
	msgNoServerYet    = "Can't reach the server yet. The scout will keep trying."
	msgInsecureLAN    = "This address is not encrypted (http://). That is only safe on your own computer."
	msgStopped        = "Stopped."
	msgStarted        = "Started."
	msgReading        = "Reading the newest combat log."
	msgDroppedPattern = "%d batches dropped (offline too long)"
)
