package hackbot

import (
	"fmt"
	"io/ioutil"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/buger/jsonparser"
	"github.com/keybase/go-keybase-chat-bot/kbchat"
	"github.com/keybase/go-keybase-chat-bot/kbchat/types/chat1"
	"github.com/op/go-logging"
)

var log = logging.MustGetLogger("hackbot")

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
// TODO think about flips.
const (
	followingTrigger = "i am a follower!"
	leaderTrigger    = "i am a leader!"
	proofsTrigger    = "i have the proof!"
	pauseTrigger     = "pause"
	resumeTrigger    = "resume"
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
}

var paymentReactions = []string{
	"💰",
	"💲",
	"💵",
	"💸",
	"🤑",
	"🧧",
}

// hackbotErr holds a human readable error message to be returned directly.
type hackbotErr struct {
	msg string
}

func newHackbotErr(msg string, args ...interface{}) hackbotErr {
	return hackbotErr{msg: fmt.Sprintf(msg, args...)}
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
	sync.RWMutex
	opts    kbchat.RunOptions
	kbc     *kbchat.API
	running bool
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
				},
			},
		},
	}
}

func (s *BotServer) Start() (err error) {
	s.Lock()
	s.running = true
	s.Unlock()

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

		// TODO re-enable
		// skip messages sent by the bot
		// if msg.Message.Sender.Username == kbc.GetUsername() {
		// 	continue
		// }
		go s.runHandler(msg.Message)
	}
}

func (s *BotServer) runHandler(msg chat1.MsgSummary) {
	if msg.Content.TypeName != "text" || msg.Content.Text == nil {
		s.logHandler(msg)
		return
	}

	convID := msg.ConvID
	msgText := strings.TrimPrefix(strings.TrimSpace(strings.ToLower(msg.Content.Text.Body)), "!")
	var err error
	switch msgText {
	case followingTrigger:
		err = s.followingHandler(msg)
	case leaderTrigger:
		err = s.leaderHandler(msg)
	case proofsTrigger:
		err = s.proofHandler(msg)
	case pauseTrigger:
		err = s.pauseHandler(msg)
	case resumeTrigger:
		err = s.resumeHandler(msg)
	default:
		// just log and get out of there
		err = s.logHandler(msg)
	}
	switch err.(type) {
	case nil:
		return
	case hackbotErr:
		if _, serr := s.kbc.SendMessageByConvID(convID, err.Error()); serr != nil {
			s.debug("unable to send %v", serr)
		}
		// TODO handle already processed.
	default:
		if _, serr := s.kbc.SendMessageByConvID(convID, "Oh dear, I'm having some trouble. Try again if you're feeling brave."); serr != nil {
			s.debug("unable to send: %v", serr)
		}
		s.debug("unable to complete request %v", err)
	}
}

func (s *BotServer) baseHandler(msg chat1.MsgSummary, needProfile, needShowcase bool, trigger string) (profile *kbProfile, err error) {
	s.debug("handling %q request", trigger)
	if !s.running {
		return nil, newHackbotErr("Sorry, I'm paused.")
	}

	// TODO check if already processed this trigger for the sender
	if needProfile {
		sender := msg.Sender.Username
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

func (s *BotServer) makePayment(msg chat1.MsgSummary, amount int) error {
	recipient := msg.Sender.Username

	paymentMsg := paymentMsgs[rand.Intn(len(paymentMsgs))]
	msgText := fmt.Sprintf("@%s, %s +%dXLM@%s", recipient, paymentMsg, amount, recipient)
	s.debug("makePayment text: %s", msgText)
	if _, err := s.kbc.InChatSendByConvID(msg.ConvID, msgText); err != nil {
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
	s.RLock()
	defer s.RUnlock()

	profile, err := s.baseHandler(msg, true, false, followingTrigger)
	if err != nil {
		return err
	}
	if profile.numFollowing < followingNeeded {
		return newHackbotErr("You need to follow at least %d others but only have %d. Tragedy.",
			followingNeeded, profile.numFollowing)
	}

	return s.makePayment(msg, followingPrize)
}

func (s *BotServer) leaderHandler(msg chat1.MsgSummary) error {
	s.RLock()
	defer s.RUnlock()

	profile, err := s.baseHandler(msg, true, true, leaderTrigger)
	if err != nil {
		return err
	}

	if profile.numFollowers < followersNeeded {
		return newHackbotErr("You need at least %d followers but only have %d. Bummer.",
			followersNeeded, profile.numFollowers)
	}

	return s.makePayment(msg, leaderPrize)
}

func (s *BotServer) proofHandler(msg chat1.MsgSummary) error {
	s.RLock()
	defer s.RUnlock()

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
	return s.makePayment(msg, proofsPrize)
}

func (s *BotServer) logHandler(msg chat1.MsgSummary) error {
	if msg.Content.Text != nil {
		s.debug("unhandled msg from (%s): %s", msg.Sender.Username,
			msg.Content.Text.Body)
	} else {
		s.debug("unhandled msg from (%s): %+v", msg.Sender.Username,
			msg.Content)
	}
	return nil
}

func (s *BotServer) pauseHandler(msg chat1.MsgSummary) error {
	s.Lock()
	defer s.Unlock()

	sender := msg.Sender.Username
	s.debug("handling pause request from %s", sender)
	if _, ok := admins[sender]; !ok {
		s.debug("ignoring pause request from non-admin")
	}

	s.running = false
	return nil
}

func (s *BotServer) resumeHandler(msg chat1.MsgSummary) error {
	s.Lock()
	defer s.Unlock()

	sender := msg.Sender.Username
	s.debug("handling resume request from %s", sender)
	if _, ok := admins[sender]; !ok {
		s.debug("ignoring resume request from non-admin")
	}

	s.running = true
	return nil
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
