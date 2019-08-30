package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/citizenhacks/hackbot/hackbot"
	"github.com/keybase/go-keybase-chat-bot/kbchat"
)

func main() {
	rc := mainInner()
	os.Exit(rc)
}

func mainInner() int {
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
			fmt.Printf("error clearing advertisemnts: %v\n", err)
			return 1
		}
		if err := kbc.ClearCommands(); err != nil {
			fmt.Printf("error clearing advertisemnts: %v\n", err)
		}
		return 0
	}
	bs := hackbot.NewBotServer(opts)
	if err := bs.Start(); err != nil {
		fmt.Printf("error running chat loop: %v\n", err)
		return 1
	}
	return 0
}
