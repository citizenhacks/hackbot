package main

import (
	"flag"

	"github.com/citizenhacks/hackbot/hackbot"
	"github.com/keybase/go-keybase-chat-bot/kbchat"
	"github.com/op/go-logging"
)

var log = logging.MustGetLogger("hackbot")

func main() {
	var opts kbchat.RunOptions
	var oneshot kbchat.OneshotOptions
	flag.StringVar(&opts.KeybaseLocation, "keybase", "keybase", "keybase command")
	flag.StringVar(&opts.HomeDir, "home", "", "Home directory")

	flag.StringVar(&oneshot.Username, "username", "", "bot's username")
	flag.StringVar(&oneshot.PaperKey, "paperkey", "", "bot's paperkey")
	var clearCmds bool
	flag.BoolVar(&clearCmds, "clear-cmds", false,
		"clear command advertisements without running the bot. For testing.")
	flag.Parse()

	if oneshot.Username != "" && oneshot.PaperKey != "" {
		opts.Oneshot = &oneshot
	}

	if opts.HomeDir != "" {
		opts.StartService = true
	}

	if clearCmds {
		kbc, err := kbchat.Start(opts)
		if err != nil {
			log.Fatalf("error clearing advertisemnts: %v\n", err)
		}
		if err := kbc.ClearCommands(); err != nil {
			log.Fatalf("error clearing advertisemnts: %v\n", err)
		}
	}
	bs := hackbot.NewBotServer(opts)
	if err := bs.Start(); err != nil {
		log.Fatalf("error running chat loop: %v\n", err)
	}
}
