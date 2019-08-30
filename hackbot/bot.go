package hackbot

import (
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
)

var log = logging.MustGetLogger("hackbot")

const (
	userCardEndpoint   = "https://keybase.io/_/api/1.0/user/card.json?username=%s"
	userLookupEndpoint = "https://keybase.io/_/api/1.0/user/lookup.json?username=%s&fields=proofs_summary"
)

// TODO revert
const showcaseTeamName = "keybase" //"citizenhacks.2019"

// TODO add pause/resume admin commands
// TODO get unfurls working for location
// TODO think about flips.
const (
	followingTrigger = "i'm a follower!"
	leaderTrigger    = "i'm a leader!"
	proofsTrigger    = "i have the proof!"
)

const (
	followingNeeded = 25
	followersNeeded = 25
	proofsNeeded    = 100
)

// in XLM
const (
	leaderPrize    = 100
	followingPrize = 10
	proofsPrize    = 25
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

type missingRequirementError struct {
	msg string
}

func newMissingRequirementError(msg string, args ...interface{}) missingRequirementError {
	return missingRequirementError{msg: fmt.Sprintf(msg, args...)}
}

func (e missingRequirementError) Error() string {
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
	opts kbchat.RunOptions
	kbc  *kbchat.API
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
		Advertisements: []chat1.AdvertiseCommandsParam{
			{
				Typ: "public",
				Commands: []chat1.UserBotCommandInput{
					{
						Name:        followingTrigger,
						Description: fmt.Sprintf("Win an XLM prize if you follow at least %d people.", followingNeeded),
					},
					{
						Name:        leaderTrigger,
						Description: fmt.Sprintf("Win an XLM prize if at least %d people follow you.", followersNeeded),
					},
					{
						Name:        proofsTrigger,
						Description: fmt.Sprintf("Win an XLM prize if you have at least %d Keybase proofs.", proofsNeeded),
					},
				},
			},
		},
	}
}

func (s *BotServer) Start() (err error) {
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
		return
	}

	convID := msg.ConvID
	msgText := strings.TrimSpace(strings.ToLower(msg.Content.Text.Body))
	var err error
	switch msgText {
	case followingTrigger:
		err = s.followingHandler(msg)
	case leaderTrigger:
		err = s.leaderHandler(msg)
	case proofsTrigger:
		err = s.proofHandler(msg)
	default:
		// just log and get out of there
		err = s.logHandler(msg)
	}
	switch err.(type) {
	case nil:
		return
	case missingRequirementError:
		if _, serr := s.kbc.SendMessageByConvID(convID, err.Error()); serr != nil {
			s.debug("unable to send %v", serr)
		}
		// TODO handle already processed.
	default:
		if _, serr := s.kbc.SendMessageByConvID(convID, "Oh no, having some trouble. Try again if you're feeling brave."); serr != nil {
			s.debug("unable to send: %v", serr)
		}
		s.debug("unable to complete request %v", err)
	}
}

func (s *BotServer) baseHandler(msg chat1.MsgSummary, needProfile, needShowcase bool, trigger string) (profile *kbProfile, err error) {
	s.debug("handling %q request", trigger)
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
				return nil, newMissingRequirementError(msg, showcaseTeamName)
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
	profile, err := s.baseHandler(msg, true, false, followingTrigger)
	if err != nil {
		return err
	}
	if profile.numFollowing < followingNeeded {
		return newMissingRequirementError("You need to follow at least %d others but only have %d. Tragedy.",
			followingNeeded, profile.numFollowing)
	}

	return s.makePayment(msg, followingPrize)
}

func (s *BotServer) leaderHandler(msg chat1.MsgSummary) error {
	profile, err := s.baseHandler(msg, true, true, leaderTrigger)
	if err != nil {
		return err
	}

	if profile.numFollowers < followersNeeded {
		return newMissingRequirementError("You need at least %d followers but only have %d. Bummer.",
			followersNeeded, profile.numFollowers)
	}

	return s.makePayment(msg, leaderPrize)
}

func (s *BotServer) proofHandler(msg chat1.MsgSummary) error {
	s.debug("handling proof request")
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
		return newMissingRequirementError("You need at least %d proofs but only have %d. Disaster.",
			proofsNeeded, numProofs)
	}
	return s.makePayment(msg, proofsPrize)
}

func (s *BotServer) logHandler(msg chat1.MsgSummary) error {
	s.debug("unhandled msg from (%s): %s", msg.Sender.Username,
		msg.Content.Text.Body)
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
