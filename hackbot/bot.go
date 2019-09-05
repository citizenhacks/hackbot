package hackbot

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/buger/jsonparser"
	"github.com/keybase/go-keybase-chat-bot/kbchat"
	"github.com/keybase/go-keybase-chat-bot/kbchat/types/chat1"
	"github.com/op/go-logging"
	"github.com/syndtr/goleveldb/leveldb"
)

var log = logging.MustGetLogger("hackbot")

const (
	dbPath     = "store.lvldb"
	dbSentinal = "sentinal"
)

const (
	userCardEndpoint   = "https://keybase.io/_/api/1.0/user/card.json?username=%s"
	userLookupEndpoint = "https://keybase.io/_/api/1.0/user/lookup.json?username=%s&fields=proofs_summary"
)

const showcaseTeamName = "citizenhacks.2019"

var admins = map[string]struct{}{
	"joshblum":    struct{}{},
	"marceloneil": struct{}{},
	"client4":     struct{}{},
}

// TODO location
const (
	followingTrigger = "i am a follower!"
	leaderTrigger    = "i am a leader!"
	proofsTrigger    = "i have the proof!"
	pauseTrigger     = "pause"
	resumeTrigger    = "resume"
	helpTrigger      = "help"
)

const (
	followingNeeded = 25
	followersNeeded = 25
	proofsNeeded    = 5
)

// in XLM
const (
	leaderPrize    = 100
	followingPrize = 20
	proofsPrize    = 50
)

// prefixed by the sender's username
var paymentMsgs = []string{
	"you are an inspiration.",
	"success!",
	"here you go.",
	"I'm so proud of you.",
	"such an example.",
	"just for you. You're welcome.",
	"don't spend it all in once place.",
	"https://media2.giphy.com/media/5f2XMzubbKHqE/giphy.mp4#height=160&width=160&isvideo=true",
}

var paymentReactions = []string{
	":moneybag:",
	":money_mouth_face:",
	":money_with_wings:",
	":bank:",
	":yen:",
	":euro:",
	":dollar:",
}

var duplicateErrMsgs = []string{
	"Nice try.",
	"Why are you trying to pull a fast one?",
	"Not so fast.",
	"Seriously?",
	"I wasn't born yesterday.",
	"Nope.",
	"You.",
	"https://media1.giphy.com/media/spfi6nabVuq5y/giphy.mp4#height=129&width=158&isvideo=true",
	"https://media0.giphy.com/media/wYyTHMm50f4Dm/giphy.mp4#height=277&width=480&isvideo=true",
	"https://media1.giphy.com/media/gnE4FFhtFoLKM/giphy.mp4#height=337&width=337&isvideo=true",
}

var duplicateErrReactions = []string{
	":no_entry:",
	":no_entry_sign:",
	":no_bicycles:",
	":no_smoking:",
	":non-potable_water:",
	":no_mobile_phones:",
	":no_mouth:",
	":no_good:",
	":see_no_evil:",
	":speak_no_evil:",
	":hear_no_evil:",
	":x:",
	":angry:",
}

// hackbotErr holds a human readable error message to be returned directly.
type hackbotErr struct {
	msg      string
	reaction string
}

func newHackbotErr(msg string, args ...interface{}) hackbotErr {
	return hackbotErr{msg: fmt.Sprintf(msg, args...)}
}

func newHackbotDuplicateEntryError() hackbotErr {
	msg := duplicateErrMsgs[rand.Intn(len(duplicateErrMsgs))]
	reaction := duplicateErrReactions[rand.Intn(len(duplicateErrReactions))]
	return hackbotErr{
		msg:      fmt.Sprintf("%s You've already gotten this prize.", msg),
		reaction: reaction,
	}
}

func (e hackbotErr) Error() string {
	return e.msg
}

type kbProfile struct {
	numFollowers, numFollowing int64
	hasRequiredShowcase        bool
}

type Options struct {
	KeybaseLocation string
	Home            string
}

type BotServer struct {
	opts    kbchat.RunOptions
	kbc     *kbchat.API
	running bool
	db      *leveldb.DB
}

func NewBotServer(opts kbchat.RunOptions) *BotServer {
	return &BotServer{
		opts: opts,
	}
}

func (s *BotServer) debug(msg string, args ...interface{}) {
	log.Infof("BotServer: "+msg+"\n", args...)
}

func (s *BotServer) makeAdvertisement() kbchat.Advertisement {
	return kbchat.Advertisement{
		Alias: "hackbot",
		Advertisements: []chat1.AdvertiseCommandAPIParam{
			{
				Typ: "public",
				Commands: []chat1.UserBotCommandInput{
					{
						Name:        followingTrigger,
						Description: fmt.Sprintf("Win %dXLM if you follow at least %d people.", followingPrize, followingNeeded),
					},
					{
						Name:        leaderTrigger,
						Description: fmt.Sprintf("Win %dXLM if at least %d people follow you.", leaderPrize, followersNeeded),
					},
					{
						Name:        proofsTrigger,
						Description: fmt.Sprintf("Win %dXLM if you have at least %d Keybase proofs.", proofsPrize, proofsNeeded),
					},
					{
						Name:        helpTrigger,
						Description: "Learn about what I can do and who made me.",
					},
				},
			},
		},
	}
}

func (s *BotServer) dbKey(username, trigger string) []byte {
	return []byte(fmt.Sprintf("%s:%s", username, trigger))
}

func (s *BotServer) Start() (err error) {
	s.running = true
	s.db, err = leveldb.OpenFile(dbPath, nil)
	defer s.db.Close()
	if err != nil {
		s.debug("lvldb error: %s", err)
		return err
	}

	rand.Seed(time.Now().Unix())

	s.debug("Start(%+v", s.opts)
	if s.kbc, err = kbchat.Start(s.opts); err != nil {
		return err
	}

	if _, err := s.kbc.AdvertiseCommands(s.makeAdvertisement()); err != nil {
		s.debug("advertise error: %s", err)
		return err
	}

	if _, err := s.kbc.SendMessageByTlfName(s.kbc.GetUsername(), "I'm running."); err != nil {
		s.debug("failed to announce self: %s", err)
		return err
	}

	sub, err := s.kbc.ListenForNewTextMessages()
	if err != nil {
		return err
	}
	s.debug("startup success, listening for messages...")
	for {
		msg, err := sub.Read()
		if err != nil {
			s.debug("Read() error: %s", err.Error())
			continue
		}

		// TODO re-enable, hoist username var
		// skip messages sent by the bot
		// if msg.Message.Sender.Username == kbc.GetUsername() {
		// 	continue
		// }
		s.runHandler(msg.Message)
	}
}

func (s *BotServer) runHandler(msg chat1.MsgSummary) {

	convID := msg.ConvID
	var err error
	switch msg.Content.TypeName {
	case "text":
		err = s.textMsgHandler(msg)
	case "join":
		// TODO: add *new* users to set channels
		fallthrough
	default:
		err = s.logHandler(msg)
	}

	switch err := err.(type) {
	case nil:
		return
	case hackbotErr:
		if _, serr := s.kbc.SendMessageByConvID(convID, err.Error()); serr != nil {
			s.debug("unable to send %v", serr)
		}
		if err.reaction != "" {
			if _, serr := s.kbc.ReactByConvID(convID, msg.Id, err.reaction); serr != nil {
				s.debug("unable to send %v", serr)
			}
		}
	default:
		if _, serr := s.kbc.SendMessageByConvID(convID, "Oh dear, I'm having some trouble. Try again if you're feeling brave."); serr != nil {
			s.debug("unable to send: %v", serr)
		}
		s.debug("unable to complete request %v", err)
	}
}

func (s *BotServer) textMsgHandler(msg chat1.MsgSummary) error {
	if msg.Content.Text == nil {
		return s.logHandler(msg)
	}

	msgText := strings.TrimPrefix(strings.TrimSpace(strings.ToLower(msg.Content.Text.Body)), "!")
	switch msgText {
	case followingTrigger:
		return s.followingHandler(msg)
	case leaderTrigger:
		return s.leaderHandler(msg)
	case proofsTrigger:
		return s.proofHandler(msg)
	case pauseTrigger:
		return s.pauseHandler(msg)
	case resumeTrigger:
		return s.resumeHandler(msg)
	case helpTrigger:
		return s.helpHandler(msg)
	default:
		// just log and get out of there
		return s.logHandler(msg)
	}
}

func (s *BotServer) checkStorageForDup(username, trigger string) error {
	data, err := s.db.Get(s.dbKey(username, trigger), nil)
	switch err {
	case nil:
		if bytes.Equal(data, []byte(dbSentinal)) {
			return newHackbotDuplicateEntryError()
		}
	case leveldb.ErrNotFound:
		return nil
	}
	return err
}

func (s *BotServer) baseHandler(msg chat1.MsgSummary, needProfile, needShowcase bool, trigger string) (profile *kbProfile, err error) {
	s.debug("handling %q request", trigger)

	if !s.running {
		return nil, newHackbotErr("Sorry, I'm paused.")
	}

	sender := msg.Sender.Username
	if err := s.checkStorageForDup(sender, trigger); err != nil {
		return nil, err
	}
	if needProfile {
		profile, err = s.getProfile(sender)
		if err != nil {
			return nil, err
		}
		s.debug("found profile: %+v for %q", profile, sender)
		if needShowcase {
			if !profile.hasRequiredShowcase {
				msg := "You have to publish your membership in %s first. Go to your profile and edit which teams you have showcased!"
				return nil, newHackbotErr(msg, showcaseTeamName)
			}
		}
	}

	return profile, nil
}

func (s *BotServer) makePayment(msg chat1.MsgSummary, trigger string, amount int) error {
	recipient := msg.Sender.Username
	paymentMsg := paymentMsgs[rand.Intn(len(paymentMsgs))]
	msgText := fmt.Sprintf("@%s, %s +%dXLM@%s", recipient, paymentMsg, amount, recipient)
	s.debug("makePayment text: %s", msgText)
	if _, err := s.kbc.InChatSendByConvID(msg.ConvID, msgText); err != nil {
		return err
	}
	// mark the user as having received the prize.
	if err := s.db.Put(s.dbKey(recipient, trigger), []byte(dbSentinal), nil); err != nil {
		return err
	}

	reactionMsg := paymentReactions[rand.Intn(len(paymentReactions))]
	s.debug("makePayment reaction: %s", reactionMsg)
	if _, err := s.kbc.ReactByConvID(msg.ConvID, msg.Id, reactionMsg); err != nil {
		return err
	}

	return nil
}

func (s *BotServer) followingHandler(msg chat1.MsgSummary) error {

	profile, err := s.baseHandler(msg, true, false, followingTrigger)
	if err != nil {
		return err
	}
	if profile.numFollowing < followingNeeded {
		return newHackbotErr("You need to follow at least %d others but only have %d. Tragedy.",
			followingNeeded, profile.numFollowing)
	}

	return s.makePayment(msg, followingTrigger, followingPrize)
}

func (s *BotServer) leaderHandler(msg chat1.MsgSummary) error {
	profile, err := s.baseHandler(msg, true, true, leaderTrigger)
	if err != nil {
		return err
	}

	if profile.numFollowers < followersNeeded {
		return newHackbotErr("You need at least %d followers but only have %d. Bummer.",
			followersNeeded, profile.numFollowers)
	}

	return s.makePayment(msg, leaderTrigger, leaderPrize)
}

func (s *BotServer) proofHandler(msg chat1.MsgSummary) error {
	_, err := s.baseHandler(msg, true, true, proofsTrigger)
	if err != nil {
		return err
	}

	numProofs, err := s.getNumProofs(msg.Sender.Username)
	if err != nil {
		return err
	}

	s.debug("found %d proofs for %s", numProofs, msg.Sender.Username)

	if numProofs < proofsNeeded {
		return newHackbotErr("You need at least %d proofs but only have %d. Disaster.",
			proofsNeeded, numProofs)
	}
	return s.makePayment(msg, proofsTrigger, proofsPrize)
}

func (s *BotServer) logHandler(msg chat1.MsgSummary) error {
	if msg.Content.Text != nil {
		if msg.Content.Text.Body == "i read the source" {
			if err := s.checkStorageForDup(msg.Sender.Username, "src"); err != nil {
				return err
			}
			return s.makePayment(msg, "src", 100)
		}
		s.debug("unhandled msg from (%s): %s", msg.Sender.Username,
			msg.Content.Text.Body)
	} else {
		s.debug("unhandled msg from (%s): %+v", msg.Sender.Username,
			msg.Content)
	}
	return nil
}

func (s *BotServer) pauseHandler(msg chat1.MsgSummary) error {
	sender := msg.Sender.Username
	s.debug("handling pause request from %s", sender)
	if _, ok := admins[sender]; !ok {
		s.debug("ignoring pause request from non-admin")
	}

	s.running = false
	return nil
}

func (s *BotServer) resumeHandler(msg chat1.MsgSummary) error {
	sender := msg.Sender.Username
	s.debug("handling resume request from %s", sender)
	if _, ok := admins[sender]; !ok {
		s.debug("ignoring resume request from non-admin")
	}

	s.running = true
	return nil
}

func (s *BotServer) helpHandler(msg chat1.MsgSummary) error {
	help := "Greetings! I offer prizes for building out your Keybase profile, try `!%s`\n"
	help += "Was made with :heart: by @joshblum, @marceloneil, and @client4.\n"
	help += "You can check out my insides at https://github.com/citizenhacks/hackbot"
	publicTriggers := []string{followingTrigger, leaderTrigger, proofsTrigger}
	help = fmt.Sprintf(help, publicTriggers[rand.Intn(len(publicTriggers))])
	_, err := s.kbc.SendMessageByConvID(msg.ConvID, help)
	return err
}

func (s *BotServer) getProfile(username string) (*kbProfile, error) {
	res, err := http.Get(fmt.Sprintf(userCardEndpoint, username))
	if err != nil {
		return nil, err
	}

	data, err := ioutil.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		return nil, err
	}

	numFollowers, err := jsonparser.GetInt(data, "follow_summary", "followers")
	if err != nil {
		s.debug("unable to get num followers %v", err)
	}
	numFollowing, err := jsonparser.GetInt(data, "follow_summary", "following")
	if err != nil {
		s.debug("unable to get num following %v", err)
	}

	var hasRequiredShowcase bool
	_, err = jsonparser.ArrayEach(data,
		func(value []byte, dataType jsonparser.ValueType, offset int, err error) {
			// already found the showcase item
			if hasRequiredShowcase {
				return
			}
			if err != nil {
				s.debug("unable to parse item: %v", err)
				return
			}
			if dataType != jsonparser.Object {
				s.debug("skipping unexpected dataType: %v", dataType)
				return
			}

			teamName, err := jsonparser.GetString(value, "fq_name")
			if err != nil {
				s.debug("unable to get fq_name: %v", err)
				return
			}

			s.debug("found teamName %s, want %s", teamName, showcaseTeamName)
			hasRequiredShowcase = teamName == showcaseTeamName
		}, "team_showcase")
	if err != nil {
		s.debug("unable to parse team showcase: %v", err)
	}
	return &kbProfile{
		numFollowers:        numFollowers,
		numFollowing:        numFollowing,
		hasRequiredShowcase: hasRequiredShowcase,
	}, nil
}

func (s *BotServer) getNumProofs(username string) (int, error) {
	res, err := http.Get(fmt.Sprintf(userLookupEndpoint, username))
	if err != nil {
		return 0, err
	}

	data, err := ioutil.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		return 0, err
	}

	var numProofs int
	_, err = jsonparser.ArrayEach(data,
		func(value []byte, dataType jsonparser.ValueType, offset int, err error) {
			if err != nil {
				return
			}
			numProofs++
		}, "them", "proofs_summary", "all")
	if err != nil {
		s.debug("unable to parse num proofs: %v", err)
	}
	return numProofs, nil
}
