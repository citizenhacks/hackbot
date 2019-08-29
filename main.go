package main

import (
	"flag"

	"github.com/keybase/go-keybase-chat-bot/kbchat"
	"github.com/op/go-logging"
)

var log = logging.MustGetLogger("main")

func main() {
	var kbLoc string
	var kbc *kbchat.API
	var err error

	flag.StringVar(&kbLoc, "keybase", "keybase", "the location of the keybase binary")
	flag.Parse()

	if kbc, err = kbchat.Start(kbchat.RunOptions{KeybaseLocation: kbLoc}); err != nil {
		log.Fatalf("error creating API: %s", err.Error())
	}

	sub, err := kbc.ListenForNewTextMessages()
	if err != nil {
		log.Fatalf("error listening: %s", err.Error())
	}

	for {
		msg, err := sub.Read()
		if err != nil {
			log.Fatalf("failed to read message: %s", err.Error())
		}

		// skip messages sent by the bot
		if msg.Message.Sender.Username == kbc.GetUsername() {
			continue
		}
	}

}
